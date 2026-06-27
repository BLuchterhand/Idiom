package jsonfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteJSON marshals v as indented JSON and writes it to path with a trailing newline.
// Parent directories are created if needed.
func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write json %s: %w", filepath.Base(path), err)
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("write json %s: %w", filepath.Base(path), err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write json %s: %w", filepath.Base(path), err)
	}

	return nil
}

// ReadJSON reads path and unmarshals its JSON content into v.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read json %s: %w", filepath.Base(path), err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return fmt.Errorf("read json %s: file is empty", filepath.Base(path))
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("read json %s: %w", filepath.Base(path), err)
	}

	return nil
}
