package root

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type responsesE2EStep struct {
	status  int
	payload map[string]any
}

type developmentRequestMilestone struct {
	name    string
	matches func(map[string]any) bool
}

func expectDevelopmentRequestMilestonesInOrder(requests []map[string]any, milestones ...developmentRequestMilestone) {
	ginkgo.GinkgoHelper()

	nextRequest := 0
	for _, milestone := range milestones {
		matchedAt := -1
		for index := nextRequest; index < len(requests); index++ {
			if milestone.matches != nil && milestone.matches(requests[index]) {
				matchedAt = index
				break
			}
		}
		gomega.Expect(matchedAt).To(
			gomega.BeNumerically(">=", 0),
			"missing request milestone %q after request %d; requests=%#v",
			milestone.name,
			nextRequest,
			requests,
		)
		nextRequest = matchedAt + 1
	}
}

func expectDevelopmentRequestMilestonesPresent(requests []map[string]any, milestones ...developmentRequestMilestone) {
	ginkgo.GinkgoHelper()

	for _, milestone := range milestones {
		found := false
		for _, request := range requests {
			if milestone.matches != nil && milestone.matches(request) {
				found = true
				break
			}
		}
		gomega.Expect(found).To(gomega.BeTrue(), "missing request milestone %q; requests=%#v", milestone.name, requests)
	}
}

// test-double: scripted-protocol-peer
type sequentialResponsesPeer struct {
	mu         sync.Mutex
	steps      []responsesE2EStep
	requests   []map[string]any
	auth       []string
	peerErrors []error
}

func newDevelopmentRepairPeer() *sequentialResponsesPeer {
	return newSequentialResponsesPeer(
		responsesE2EToolCall("resp-read-source", "call-read-source", "read_file", `{"path":"calculator.go"}`),
		responsesE2EToolCall("resp-read-test", "call-read-test", "read_file", `{"path":"calculator_test.go"}`),
		responsesE2EToolCall("resp-read-history", "call-read-history", "history_range", `{"path":"calculator.go","start_line":1,"end_line":6,"named_gap":"why the existing Add behavior was introduced"}`),
		responsesE2EToolCall("resp-edit-wrong", "call-edit-wrong", "edit_file", `{"path":"calculator.go","old_text":"return left - right","new_text":"return left * right","description":"repair Add arithmetic"}`),
		responsesE2EToolCall("resp-test-fail", "call-test-fail", "shell", `{"command":"go test ./...","workdir":"."}`),
		responsesE2EToolCall("resp-read-failed", "call-read-failed", "read_file", `{"path":"calculator.go"}`),
		responsesE2EToolCall("resp-edit-fix", "call-edit-fix", "edit_file", `{"path":"calculator.go","old_text":"return left * right","new_text":"return left + right","description":"correct Add to use addition"}`),
		responsesE2EToolCall("resp-test-pass", "call-test-pass", "shell", `{"command":"go test ./...","workdir":"."}`),
		responsesE2EToolCall("resp-read-final", "call-read-final", "read_file", `{"path":"calculator.go"}`),
		responsesE2EFinal("resp-final", "Corrected Add to return the sum at `calculator.go:5`. The first repair failed verification, then the corrected implementation passed.\n\nChanged paths:\n- calculator.go\n\nVerification: go test ./... passed"),
	)
}

func newSequentialResponsesPeer(steps ...responsesE2EStep) *sequentialResponsesPeer {
	return &sequentialResponsesPeer{steps: append([]responsesE2EStep(nil), steps...)}
}

func responsesE2EToolCall(responseID, callID, name, arguments string) responsesE2EStep {
	return responsesE2EStep{status: http.StatusOK, payload: map[string]any{
		"id":     responseID,
		"status": "completed",
		"output": []map[string]any{{
			"type":      "function_call",
			"call_id":   callID,
			"name":      name,
			"arguments": arguments,
		}},
		"usage": map[string]int{"input_tokens": 20, "output_tokens": 8, "total_tokens": 28},
	}}
}

func responsesE2EFinal(responseID, answer string) responsesE2EStep {
	return responsesE2EStep{status: http.StatusOK, payload: map[string]any{
		"id":     responseID,
		"status": "completed",
		"output": []map[string]any{{
			"type": "message",
			"role": "assistant",
			"content": []map[string]string{{
				"type": "output_text",
				"text": answer,
			}},
		}},
		"usage": map[string]int{"input_tokens": 30, "output_tokens": 15, "total_tokens": 45},
	}}
}

func (p *sequentialResponsesPeer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		p.recordError(fmt.Errorf("decode Responses request: %w", err))
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, request)
	p.auth = append(p.auth, r.Header.Get("Authorization"))
	if index >= len(p.steps) {
		p.peerErrors = append(p.peerErrors, fmt.Errorf("unexpected Responses request %d; input tail: %s", index+1, responsesRequestInputTail(request, 4)))
		p.mu.Unlock()
		http.Error(w, "script exhausted", http.StatusTooManyRequests)
		return
	}
	step := p.steps[index]
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(step.status)
	if err := json.NewEncoder(w).Encode(step.payload); err != nil {
		p.recordError(fmt.Errorf("encode Responses response %d: %w", index+1, err))
	}
}

func responsesRequestInputTail(request map[string]any, max int) string {
	input, _ := request["input"].([]any)
	if max > 0 && len(input) > max {
		input = input[len(input)-max:]
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "<unavailable>"
	}
	return string(encoded)
}

func (p *sequentialResponsesPeer) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peerErrors = append(p.peerErrors, err)
}

func (p *sequentialResponsesPeer) snapshot() ([]map[string]any, []string, []error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.requests...), append([]string(nil), p.auth...), append([]error(nil), p.peerErrors...)
}

// test-double: scripted-protocol-peer
type concurrentAnalysisPeer struct {
	mu         sync.Mutex
	requests   []map[string]any
	peerErrors []error
}

func (p *concurrentAnalysisPeer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		p.recordError(fmt.Errorf("decode concurrent Responses request: %w", err))
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()

	var step responsesE2EStep
	switch {
	case requestContainsFunctionOutput(request, "call-alpha", "ALPHA-CONTRACT"):
		step = responsesE2EFinal("resp-alpha-final", "ALPHA-CONTRACT requires public API compatibility (`ALPHA.md:1`).")
	case requestContainsFunctionOutput(request, "call-beta", "BETA-CONTRACT"):
		step = responsesE2EFinal("resp-beta-final", "BETA-CONTRACT requires transaction atomicity (`BETA.md:1`).")
	case developmentE2ERequestContains(request, "ALPHA.md"):
		step = responsesE2EToolCall("resp-alpha-read", "call-alpha", "read_file", `{"path":"ALPHA.md"}`)
	case developmentE2ERequestContains(request, "BETA.md"):
		step = responsesE2EToolCall("resp-beta-read", "call-beta", "read_file", `{"path":"BETA.md"}`)
	default:
		p.recordError(fmt.Errorf("could not classify concurrent Responses request"))
		http.Error(w, "unclassified request", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(step.payload); err != nil {
		p.recordError(fmt.Errorf("encode concurrent Responses response: %w", err))
	}
}

func (p *concurrentAnalysisPeer) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peerErrors = append(p.peerErrors, err)
}

func (p *concurrentAnalysisPeer) snapshot() ([]map[string]any, []error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.requests...), append([]error(nil), p.peerErrors...)
}

func developmentE2ERequestContains(request map[string]any, marker string) bool {
	encoded, err := json.Marshal(request)
	return err == nil && strings.Contains(string(encoded), marker)
}
