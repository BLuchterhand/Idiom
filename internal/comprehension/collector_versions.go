package comprehension

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

var currentCollectorVersions = map[string]string{
	"schema":                    fmt.Sprintf("%d", SchemaVersion),
	"file_fingerprint":          "1",
	"git_state":                 "1",
	"go_index":                  "3",
	"cpp_index":                 "3",
	"python_index":              "2",
	"typescript_index":          "2",
	"javascript_index":          "2",
	"rust_index":                "2",
	"java_index":                "2",
	"gdscript_index":            "2",
	"generic_source_index":      "2",
	"language_environment":      "2",
	"workspace_tool_enrichment": "2",
	"summary_index":             "1",
}

func currentCollectorVersion() string {
	entries := currentCollectorVersionEntries()
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(sum[:8])
}

func currentCollectorVersionMap() map[string]string {
	out := make(map[string]string, len(currentCollectorVersions))
	for key, value := range currentCollectorVersions {
		out[key] = value
	}
	return out
}

func currentCollectorVersionEntries() []string {
	entries := make([]string, 0, len(currentCollectorVersions))
	for key, value := range currentCollectorVersions {
		entries = append(entries, key+"="+value)
	}
	slices.Sort(entries)
	return entries
}
