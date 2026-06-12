package agent

import "testing"

func TestNormalizeDelegatedDiscoveryEvidencePathHandlesUnicodeBeforeExtension(t *testing.T) {
	got := normalizeDelegatedDiscoveryEvidencePath("İnternal/runtime/agent/runner.go:42: func Run()")
	if got != "İnternal/runtime/agent/runner.go" {
		t.Fatalf("normalized path = %q", got)
	}
}
