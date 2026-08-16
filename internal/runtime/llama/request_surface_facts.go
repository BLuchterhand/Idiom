package llama

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

const requestSurfaceFactHeader = "X-Idiom-Request-Surface"

type requestSurfaceFact struct {
	SchemaVersion    string   `json:"schema_version"`
	Transport        string   `json:"transport"`
	AllowedToolCount int      `json:"allowed_tool_count"`
	AllowedToolNames []string `json:"allowed_tool_names,omitempty"`
}

func (c *Client) requestSurfaceFact(opts ChatOptions) requestSurfaceFact {
	names := make([]string, 0, len(opts.Tools))
	seen := make(map[string]struct{}, len(opts.Tools))
	for _, tool := range opts.Tools {
		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)

	transport := "textual"
	if !opts.ResponseFormatJSON && (opts.ForceNativeTools || c.CapabilityProfile().StructuredOutput.NativeTools) {
		transport = "native"
	}

	return requestSurfaceFact{
		SchemaVersion:    "idiom.model_request_surface.v1",
		Transport:        transport,
		AllowedToolCount: len(names),
		AllowedToolNames: names,
	}
}

func (c *Client) applyRequestSurfaceFactHeader(req *http.Request, fact requestSurfaceFact) {
	if c == nil || req == nil || !c.emitRequestSurfaceFacts {
		return
	}

	data, err := json.Marshal(fact)
	if err != nil {
		return
	}
	req.Header.Set(requestSurfaceFactHeader, string(data))
}
