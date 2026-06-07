package mcp

import "encoding/json"

// Resource describes a metadata entry exposed by resources/list.
type Resource struct {
	URI         string          `json:"uri"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	MimeType    string          `json:"mimeType,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

// ResourceReadParams is the request shape for resources/read.
type ResourceReadParams struct {
	URI string `json:"uri"`
}

// ResourceContent is one metadata/content item returned by resources/read.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// ResourceReadResult is the decoded resources/read response.
type ResourceReadResult struct {
	Contents []ResourceContent `json:"contents"`
}
