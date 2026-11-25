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
	"cpp_index":                 "4",
	"python_index":              "2",
	"typescript_index":          "2",
	"javascript_index":          "2",
	"rust_index":                "2",
	"java_index":                "2",
	"gdscript_index":            "2",
	"generic_source_index":      "3",
	"language_environment":      "2",
	"workspace_tool_enrichment": "2",
	"summary_index":             "1",
	"lexical_index":             "1",
}

// localizationCollectorKey names the collector-version entry that carries the
// localization config fingerprint. It exists only when the repository has a
// non-empty `localization` section, so default fingerprints are unchanged.
const localizationCollectorKey = "localization"

func currentCollectorVersion(root string) string {
	entries := currentCollectorVersionEntries(root)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))

	return hex.EncodeToString(sum[:8])
}

func currentCollectorVersionMap(root string) map[string]string {
	out := make(map[string]string, len(currentCollectorVersions)+1)
	for key, value := range currentCollectorVersions {
		out[key] = value
	}

	if fingerprint := LocalizationConfigFingerprint(root); fingerprint != "" {
		out[localizationCollectorKey] = fingerprint
	}

	return out
}

func currentCollectorVersionEntries(root string) []string {
	versions := currentCollectorVersionMap(root)
	entries := make([]string, 0, len(versions))

	for key, value := range versions {
		entries = append(entries, key+"="+value)
	}

	slices.Sort(entries)

	return entries
}
