package featureplanner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Bundle struct {
	State  FeatureState `json:"state"`
	Plan   FeaturePlan  `json:"plan"`
	Events []Event      `json:"events,omitempty"`
}

func SaveBundle(dir string, bundle Bundle) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("feature planner bundle dir is empty")
	}
	if err := ValidateDurableState(bundle.State); err != nil {
		return err
	}
	if err := ValidateDurablePlan(bundle.Plan); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir feature planner bundle dir: %w", err)
	}
	if err := writeJSONAtomic(filepath.Join(dir, DefaultFeatureStateName), bundle.State); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(dir, DefaultFeaturePlanName), bundle.Plan); err != nil {
		return err
	}
	return writeEventsAtomic(filepath.Join(dir, DefaultEventLogName), bundle.Events)
}

func LoadBundle(dir string) (Bundle, error) {
	if strings.TrimSpace(dir) == "" {
		return Bundle{}, fmt.Errorf("feature planner bundle dir is empty")
	}
	var bundle Bundle
	if err := readJSON(filepath.Join(dir, DefaultFeatureStateName), &bundle.State); err != nil {
		return Bundle{}, err
	}
	if err := readJSON(filepath.Join(dir, DefaultFeaturePlanName), &bundle.Plan); err != nil {
		return Bundle{}, err
	}
	events, err := readEvents(filepath.Join(dir, DefaultEventLogName))
	if err != nil {
		return Bundle{}, err
	}
	bundle.Events = events
	if err := ValidateDurableState(bundle.State); err != nil {
		return Bundle{}, err
	}
	if err := ValidateDurablePlan(bundle.Plan); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

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

func LoadEvents(root string, sessionID string) ([]Event, string, error) {
	fullPath, relPath, err := EventLogPath(root, sessionID)
	if err != nil {
		return nil, "", err
	}
	events, err := readEvents(fullPath)
	return events, relPath, err
}

func writeJSONAtomic(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	return writeFileAtomic(path, append(body, '\n'))
}

func writeEventsAtomic(path string, events []Event) error {
	var body []byte
	for _, event := range events {
		if err := validateStoredEvent(event); err != nil {
			return err
		}
		line, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal feature planner event: %w", err)
		}
		body = append(body, line...)
		body = append(body, '\n')
	}
	return writeFileAtomic(path, body)
}

func writeFileAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func readJSON(path string, value any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(body, value); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func readEvents(path string) ([]Event, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open feature planner event log: %w", err)
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("decode feature planner event line %d: %w", lineNo, err)
		}
		if err := validateStoredEvent(event); err != nil {
			return nil, fmt.Errorf("invalid feature planner event line %d: %w", lineNo, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan feature planner event log: %w", err)
	}
	return events, nil
}

func validateStoredEvent(event Event) error {
	if event.SchemaVersion != "" && event.SchemaVersion != FeatureEventSchemaVersion {
		return fmt.Errorf("unsupported feature event schema %q", event.SchemaVersion)
	}
	if strings.TrimSpace(event.Type) == "" {
		return fmt.Errorf("feature event missing type")
	}
	if event.FeatureRevisionAfter > 0 && event.FeatureRevisionAfter != event.FeatureRevisionBefore+1 {
		return fmt.Errorf("feature event %q revision_after must increment by one", event.EventID)
	}
	return nil
}
