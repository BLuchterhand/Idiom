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
	EffortID         string `json:"effort_id,omitempty"`
	TaskID           string `json:"task_id,omitempty"`
	Digest           string `json:"digest,omitempty"`
	SupersededDigest string `json:"superseded_digest,omitempty"`
	Timestamp        string `json:"timestamp"`
	Source           string `json:"source"`
}

func AppendBehavioralCoveragePromotionEvent(effortRoot string, result BehavioralCoveragePromotionResult) error {
	if err := ValidateBehavioralCoveragePromotionEventTarget(effortRoot, result); err != nil {
		return err
	}
	event := BehavioralCoveragePromotionEvent{
		Event:            "behavioral_coverage_" + strings.TrimSpace(result.State),
		EventID:          strings.TrimSpace(result.EventID),
		State:            strings.TrimSpace(result.State),
		Digest:           strings.TrimSpace(result.Digest),
		SupersededDigest: strings.TrimSpace(result.SupersededDigest),
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		Source:           "product_tdd_promote_coverage",
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
		if strings.HasPrefix(strings.TrimSpace(event.Event), "behavioral_coverage_") {
			events = append(events, event)
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
		if strings.TrimSpace(event.Event) != "behavioral_coverage_"+BehavioralCoveragePromotionStateAccepted {
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
