package mcp

import (
	"encoding/json"
	"testing"
)

func TestResourceReadProtocolDTOsUseMCPJSONShape(t *testing.T) {
	resource, err := json.Marshal(Resource{
		URI:         "idiom://workspace/root",
		Name:        "workspace root",
		Description: "Current workspace",
		MimeType:    "application/json",
		Meta:        json.RawMessage(`{"origin":"test"}`),
	})
	if err != nil {
		t.Fatalf("marshal Resource: %v", err)
	}

	if string(resource) != `{"uri":"idiom://workspace/root","name":"workspace root","description":"Current workspace","mimeType":"application/json","_meta":{"origin":"test"}}` {
		t.Fatalf("unexpected Resource JSON: %s", resource)
	}

	params, err := json.Marshal(ResourceReadParams{URI: "idiom://workspace/root"})
	if err != nil {
		t.Fatalf("marshal ResourceReadParams: %v", err)
	}

	if string(params) != `{"uri":"idiom://workspace/root"}` {
		t.Fatalf("unexpected ResourceReadParams JSON: %s", params)
	}

	raw := []byte(`{"contents":[{"uri":"idiom://workspace/root","mimeType":"application/json","text":"{\"metadata_only\":true}"}]}`)

	var result ResourceReadResult

	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal ResourceReadResult: %v", err)
	}

	if len(result.Contents) != 1 ||
		result.Contents[0].URI != "idiom://workspace/root" ||
		result.Contents[0].MimeType != "application/json" ||
		result.Contents[0].Text != `{"metadata_only":true}` {

		t.Fatalf("unexpected ResourceReadResult: %#v", result)
	}
}
