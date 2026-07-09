package featureplanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	featureplannercontract "idiom/internal/contracts/featureplanner"
)

func AppendEvents(root string, sessionID string, events []Event) (string, error) {
	if len(events) == 0 {
		_, relPath, err := EventLogPath(root, sessionID)
		return relPath, err
	}

	fullPath, relPath, err := EventLogPath(root, sessionID)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir feature planner event dir: %w", err)
	}

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("open feature planner event log: %w", err)
	}
	defer file.Close()

	for _, event := range events {
		if err := validateStoredEvent(event); err != nil {
			return "", err
		}

		line, err := json.Marshal(event)
		if err != nil {
			return "", fmt.Errorf("marshal feature planner event: %w", err)
		}

		if _, err := file.Write(append(line, '\n')); err != nil {
			return "", fmt.Errorf("append feature planner event: %w", err)
		}
	}

	return relPath, nil
}

func validateStoredEvent(event Event) error {
	if event.SchemaVersion != "" && event.SchemaVersion != FeatureEventSchemaVersion {
		return fmt.Errorf("unsupported feature event schema %q", event.SchemaVersion)
	}

	if strings.TrimSpace(event.Type) == "" {
		return fmt.Errorf("feature event missing type")
	}
	if !featureplannercontract.KnownDurableEventName(event.Type) {
		return fmt.Errorf("unsupported feature event type %q", event.Type)
	}

	if event.FeatureRevisionAfter > 0 && event.FeatureRevisionAfter != event.FeatureRevisionBefore+1 {
		return fmt.Errorf("feature event %q revision_after must increment by one", event.EventID)
	}

	return nil
}
