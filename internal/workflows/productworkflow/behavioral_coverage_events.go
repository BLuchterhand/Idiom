package productworkflow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type BehavioralCoveragePromotionEvent struct {
	Event            string `json:"event"`
	EventID          string `json:"event_id"`
	State            string `json:"state"`
	CommitStatus     string `json:"commit_status,omitempty"`
	EffortID         string `json:"effort_id,omitempty"`
	TaskID           string `json:"task_id,omitempty"`
	Digest           string `json:"digest,omitempty"`
	SupersededDigest string `json:"superseded_digest,omitempty"`
	Timestamp        string `json:"timestamp"`
	Source           string `json:"source"`
}

type BehavioralCoveragePromotionCommitError struct {
	File string
	Err  error
}

func (e BehavioralCoveragePromotionCommitError) Error() string {
	if strings.TrimSpace(e.File) == "" {
		return e.Err.Error()
	}
	return strings.TrimSpace(e.File) + ": " + e.Err.Error()
}

func (e BehavioralCoveragePromotionCommitError) Unwrap() error {
	return e.Err
}

func CommitBehavioralCoveragePromotion(effortRoot string, ledger TDDLedger, contract BehavioralCoverageContract) (BehavioralCoveragePromotionResult, error) {
	result, err := PromoteBehavioralCoverageContractWithResult(ledger, contract)
	if err != nil {
		return result, err
	}
	result.CommitStatus = BehavioralCoveragePromotionCommitStatusWritePending

	data, err := json.MarshalIndent(result.Ledger, "", "  ")
	if err != nil {
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "tdd.json", Err: err}
	}
	if err := ValidateBehavioralCoveragePromotionEventTarget(effortRoot, result); err != nil {
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "events.jsonl", Err: err}
	}

	tmp, err := os.CreateTemp(effortRoot, ".tdd-*.json")
	if err != nil {
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "tdd.json", Err: err}
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "tdd.json", Err: err}
	}
	if err := tmp.Close(); err != nil {
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "tdd.json", Err: err}
	}

	prepared := result
	prepared.State = BehavioralCoveragePromotionStatePrepared
	prepared.CommitStatus = BehavioralCoveragePromotionCommitStatusPrepared
	if err := AppendBehavioralCoveragePromotionEvent(effortRoot, prepared); err != nil {
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "events.jsonl", Err: err}
	}
	if err := os.Rename(tmpPath, filepath.Join(effortRoot, "tdd.json")); err != nil {
		return blockedBehavioralCoveragePromotion(result, false), BehavioralCoveragePromotionCommitError{File: "tdd.json", Err: err}
	}
	cleanupTmp = false

	result.CommitStatus = BehavioralCoveragePromotionCommitStatusCommitted
	result.LedgerCommitted = true
	if err := AppendBehavioralCoveragePromotionEvent(effortRoot, result); err != nil {
		return blockedBehavioralCoveragePromotion(result, true), BehavioralCoveragePromotionCommitError{File: "events.jsonl", Err: err}
	}

	return result, nil
}

func blockedBehavioralCoveragePromotion(result BehavioralCoveragePromotionResult, ledgerCommitted bool) BehavioralCoveragePromotionResult {
	result.CommitStatus = BehavioralCoveragePromotionCommitStatusBlocked
	result.LedgerCommitted = ledgerCommitted
	return result
}

func AppendBehavioralCoveragePromotionEvent(effortRoot string, result BehavioralCoveragePromotionResult) error {
	if err := ValidateBehavioralCoveragePromotionEventTarget(effortRoot, result); err != nil {
		return err
	}
	eventType, ok := ProductWorkflowBehavioralCoverageEvent(result.State)
	if !ok {
		return fmt.Errorf("behavioral coverage promotion state %q has no declared event type", strings.TrimSpace(result.State))
	}
	event := BehavioralCoveragePromotionEvent{
		Event:            string(eventType),
		EventID:          strings.TrimSpace(result.EventID),
		State:            strings.TrimSpace(result.State),
		CommitStatus:     strings.TrimSpace(result.CommitStatus),
		Digest:           strings.TrimSpace(result.Digest),
		SupersededDigest: strings.TrimSpace(result.SupersededDigest),
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		Source:           string(ProductWorkflowEventSourceTDDBriefPromotion),
	}
	if result.Ledger.BehavioralCoverage != nil {
		event.EffortID = strings.TrimSpace(result.Ledger.BehavioralCoverage.EffortID)
		event.TaskID = strings.TrimSpace(result.Ledger.BehavioralCoverage.TaskID)
	}
	if event.EffortID == "" {
		event.EffortID = strings.TrimSpace(result.Ledger.EffortID)
	}
	if event.TaskID == "" {
		event.TaskID = strings.TrimSpace(result.Ledger.TaskID)
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	eventsPath := filepath.Join(effortRoot, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}

	return nil
}

func ValidateBehavioralCoveragePromotionEventTarget(effortRoot string, result BehavioralCoveragePromotionResult) error {
	if strings.TrimSpace(effortRoot) == "" {
		return errors.New("behavioral coverage promotion event effort root is required")
	}
	if strings.TrimSpace(result.EventID) == "" {
		return errors.New("behavioral coverage promotion event_id is required")
	}
	eventsPath := filepath.Join(effortRoot, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func ParseBehavioralCoveragePromotionEventsJSONL(data []byte) ([]BehavioralCoveragePromotionEvent, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var events []BehavioralCoveragePromotionEvent
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event BehavioralCoveragePromotionEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		eventType := ProductWorkflowEventType(strings.TrimSpace(event.Event))
		if KnownProductWorkflowBehavioralCoverageEvent(eventType) {
			events = append(events, event)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(event.Event), string(ProductWorkflowEventSourceBehavioralCoverage)+"_") {
			return nil, fmt.Errorf("line %d: unknown behavioral coverage event %q", lineNo, event.Event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

func AcceptedBehavioralCoveragePromotionEvent(events []BehavioralCoveragePromotionEvent, digest string) *BehavioralCoveragePromotionEvent {
	digest = strings.TrimSpace(digest)
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		acceptedEvent, _ := ProductWorkflowBehavioralCoverageEvent(BehavioralCoveragePromotionStateAccepted)
		if strings.TrimSpace(event.Event) != string(acceptedEvent) {
			continue
		}
		if strings.TrimSpace(event.State) != BehavioralCoveragePromotionStateAccepted {
			continue
		}
		if digest != "" && strings.TrimSpace(event.Digest) != digest {
			continue
		}
		if strings.TrimSpace(event.EventID) == "" {
			continue
		}
		return &events[i]
	}

	return nil
}
