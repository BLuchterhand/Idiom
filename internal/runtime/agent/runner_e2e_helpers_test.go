package agent

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
)

const (
	e2eTestGoMod                            = "module idiomtest\n\ngo 1.22\n"
	readOnlyStrategicThesisPath             = "docs/idiom-strategic-thesis.md"
	readOnlyStrategicThesisReadFileToolCall = `{"tool":"read_file","path":"docs/idiom-strategic-thesis.md","start_line":1,"end_line":20}`
)

func newGoModuleTestWorkspace() string {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(writeTestFile(root, "go.mod", e2eTestGoMod)).To(gomega.Succeed())

	return root
}

func newReadOnlyStrategicThesisWorkspace() string {
	ginkgo.GinkgoHelper()

	root := newGoModuleTestWorkspace()
	gomega.Expect(writeTestFile(root, readOnlyStrategicThesisPath, strings.Join([]string{
		"# Idiom Strategic Thesis",
		"Idiom sells the missing substrate: repo memory, freshness, contracts, audit, and evals.",
		"Idiom is the local, evidence-first coding-agent harness for teams that need agents to do real work in real repositories.",
		"The model owns strategy; the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
		"The harness decides whether that action is allowed, whether evidence is fresh enough, and whether readiness is satisfied.",
		"Task-scoped repository evidence feeds the runtime brief, including tools, editable paths, evidence paths, verification, and budget.",
	}, "\n"))).To(gomega.Succeed())

	return root
}

func newScriptedChatServer(replies []string) (*ghttp.Server, func() []string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(replies).NotTo(gomega.BeEmpty())

	var mu sync.Mutex
	requests := 0
	var requestBodies []string
	var protocolErrors []string

	server := ghttp.NewServer()
	server.Writer = ginkgo.GinkgoWriter
	server.RouteToHandler(http.MethodPost, "/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		body, err := io.ReadAll(r.Body)
		mu.Lock()
		idx := requests
		requests++
		requestBodies = append(requestBodies, string(body))
		if err != nil {
			protocolErrors = append(protocolErrors, fmt.Sprintf("read chat request %d: %v", idx, err))
		}
		if idx >= len(replies) {
			protocolErrors = append(protocolErrors, fmt.Sprintf("unexpected extra chat request %d", idx))
		}
		mu.Unlock()

		if idx >= len(replies) {
			ghttp.RespondWithJSONEncoded(http.StatusInternalServerError, map[string]any{
				"error": map[string]string{"message": "script exhausted"},
			})(w, r)
			return
		}

		reply := replies[len(replies)-1]
		if idx < len(replies) {
			reply = replies[idx]
		}

		ghttp.RespondWithJSONEncoded(http.StatusOK, map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})(w, r)
	})

	ginkgo.DeferCleanup(func() {
		server.Close()
		mu.Lock()
		defer mu.Unlock()
		gomega.Expect(protocolErrors).To(gomega.BeEmpty())
	})

	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), requestBodies...)
	}
}
