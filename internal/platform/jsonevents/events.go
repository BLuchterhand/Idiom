package jsonevents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

func Emit(stdout io.Writer, enabled bool, eventName string, fields map[string]any) error {
	if !enabled {
		return nil
	}
	if stdout == nil {
		return errors.New("json event writer is nil")
	}

	eventName = strings.TrimSpace(eventName)
	if eventName == "" {
		return errors.New("json event name is empty")
	}

	payload := map[string]any{
		"type": eventName,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range fields {
		key = strings.TrimSpace(key)
		if key == "" || key == "type" || key == "ts" {
			continue
		}
		payload[key] = value
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(stdout, string(body)); err != nil {
		return err
	}
	return nil
}
