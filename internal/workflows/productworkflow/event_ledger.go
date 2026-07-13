package productworkflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// productWorkflowEventEnvelope is the ledger-wide identity projection shared
// by every product-workflow event type. Event-specific readers still own their
// full payload validation; this type owns only cross-type event_id lookup.
type productWorkflowEventEnvelope struct {
	Event      ProductWorkflowEventType
	EventID    string
	LineNumber int
	raw        json.RawMessage
}

func productWorkflowEventEnvelopesByID(body []byte, eventID string) ([]productWorkflowEventEnvelope, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, fmt.Errorf("product workflow event_id is required")
	}
	envelopes, err := productWorkflowEventEnvelopes(body)
	if err != nil {
		return nil, err
	}

	var matches []productWorkflowEventEnvelope
	for _, envelope := range envelopes {
		if envelope.EventID == eventID {
			matches = append(matches, envelope)
		}
	}

	return matches, nil
}

func productWorkflowEventEnvelopes(body []byte) ([]productWorkflowEventEnvelope, error) {
	var envelopes []productWorkflowEventEnvelope
	for lineNumber, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var identity struct {
			Event   string `json:"event"`
			EventID string `json:"event_id"`
		}
		if err := json.Unmarshal(line, &identity); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber+1, err)
		}
		event := strings.TrimSpace(identity.Event)
		eventID := strings.TrimSpace(identity.EventID)
		if event == "" || identity.Event != event {
			return nil, fmt.Errorf("line %d: product workflow event must have a normalized non-empty event", lineNumber+1)
		}
		if eventID == "" || identity.EventID != eventID {
			return nil, fmt.Errorf("line %d: product workflow event must have a normalized non-empty event_id", lineNumber+1)
		}
		envelopes = append(envelopes, productWorkflowEventEnvelope{
			Event:      ProductWorkflowEventType(event),
			EventID:    eventID,
			LineNumber: lineNumber + 1,
			raw:        append(json.RawMessage(nil), line...),
		})
	}

	return envelopes, nil
}
