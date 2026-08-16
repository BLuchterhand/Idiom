package workspace

import (
	"fmt"
	"strings"
)

func ParseToolRequest(text string) (ToolRequest, string, bool, error) {
	payload, ok := extractToolPayload(text)
	if !ok {
		payload, ok = extractTaggedJSONObjectToolPayload(text)
	}

	if !ok {
		var embeddedErr error

		payload, ok, embeddedErr = extractLineAnchoredToolJSONObjectPayload(text)

		if ok {
			if embeddedErr != nil {
				return ToolRequest{}, payload, true, embeddedErr
			}
		}
	}

	if !ok {
		var embeddedErr error

		payload, ok, embeddedErr = extractBareToolJSONObjectPayload(text)

		if ok {
			if embeddedErr != nil {
				return ToolRequest{}, payload, true, embeddedErr
			}
		}
	}

	if !ok {
		var err error

		payload, ok, err = extractQwenXMLToolPayload(text)

		if !ok && err == nil {
			payload, ok, err = extractDeepSeekXMLToolPayload(text)
		}

		if !ok {
			if candidate := strings.TrimSpace(text); strings.HasPrefix(candidate, "{") && strings.HasSuffix(candidate, "}") && strings.Contains(candidate, `"tool"`) {
				payload, ok = candidate, true
			} else {
				return ToolRequest{}, "", false, nil
			}
		}

		if err != nil {
			return ToolRequest{}, payload, true, err
		}
	}

	req, err := DecodeToolRequestJSON([]byte(payload))
	if err != nil {
		return ToolRequest{}, payload, true, fmt.Errorf("decode tool request: %w", err)
	}

	if strings.TrimSpace(req.Tool) == "" {
		return ToolRequest{}, payload, true, fmt.Errorf("tool request is missing tool")
	}

	return req, payload, true, nil
}

func DecodeToolRequestJSON(payload []byte) (ToolRequest, error) {
	if nested, ok, err := decodeNestedToolRequestJSON(payload); ok || err != nil {
		if err != nil {
			return nested, err
		}

		return NormalizeMCPToolRequestArgs(nested, payload), nil
	}

	req, err := decodeFlatToolRequestJSON(payload)
	if err != nil {
		return ToolRequest{}, err
	}

	return NormalizeMCPToolRequestArgs(req, payload), nil
}
