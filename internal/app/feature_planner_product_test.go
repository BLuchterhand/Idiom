package app

import (
	"strings"
	"testing"

	"idiom/internal/productworkflow"
)

func TestSanitizeEffortSlug(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"build user auth", "build-user-auth"},
		{"Add OAuth 2.0 support!!!", "add-oauth-2-0-support"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
		{"UPPERCASE", "uppercase"},
		{"", ""}, // empty returns a timestamp-based slug, not testable here
		{"a", "a"},
		{"a very long feature description that goes on and on and on and on forever", "a-very-long-feature-description-that-goes-on-and-on-and-on"},
		{"already-slugged", "already-slugged"},
		{"-leading-and-trailing-", "leading-and-trailing"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeEffortSlug(tt.input)
			if tt.input == "" {
				if got == "" {
					t.Error("empty input should produce a timestamp-based slug")
				}
				return
			}
			if got != tt.expected {
				t.Errorf("sanitizeEffortSlug(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSplitAndParseADRs(t *testing.T) {
	prd := productworkflow.PRDDocument{
		Requirements: []productworkflow.PRDRequirement{
			{ID: "REQ-001", Priority: "P1"},
			{ID: "REQ-002", Priority: "P2"},
		},
	}

	t.Run("single ADR", func(t *testing.T) {
		output := `---
schema_version: "idiom.adr.v1"
adr_id: "ADR-001"
status: "draft"
prd_refs: ["REQ-001"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-001"]
coverage_tier: "unit"
---
## Context
This is the context for the decision.

## Decision
We chose option A.

## Alternatives Considered
Option B was considered but rejected.

## Evidence
Based on PRD requirement REQ-001.

## Consequences
This will affect the auth module.

## Compatibility and Migration
No migration needed.

## TDD Obligations
TDD-ADR-001: unit test for auth flow.

## Rollback
Revert the auth module changes.

## Open Questions
- None
`
		docs, entries, err := splitAndParseADRs(output, prd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(docs) != 1 {
			t.Fatalf("expected 1 doc, got %d", len(docs))
		}
		if docs[0].FrontMatter.ADRID != "ADR-001" {
			t.Errorf("expected ADR-001, got %s", docs[0].FrontMatter.ADRID)
		}
		if !docs[0].Sections.HasContext {
			t.Error("expected HasContext to be true")
		}
		if !docs[0].Sections.HasDecision {
			t.Error("expected HasDecision to be true")
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 index entry, got %d", len(entries))
		}
		if entries[0].Path != "ADR-001.md" {
			t.Errorf("expected ADR-001.md, got %s", entries[0].Path)
		}
	})

	t.Run("multiple ADRs with === separator", func(t *testing.T) {
		output := `---
schema_version: "idiom.adr.v1"
adr_id: "ADR-001"
status: "draft"
prd_refs: ["REQ-001"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-001"]
coverage_tier: "unit"
---
## Context
First ADR context.

## Decision
First decision.

## Alternatives Considered
Alt 1.

## Evidence
Evidence 1.

## Consequences
Consequence 1.

## Compatibility and Migration
Compat 1.

## TDD Obligations
TDD 1.

## Rollback
Rollback 1.

## Open Questions
None.
===
---
schema_version: "idiom.adr.v1"
adr_id: "ADR-002"
status: "draft"
prd_refs: ["REQ-002"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-002"]
coverage_tier: "unit"
---
## Context
Second ADR context.

## Decision
Second decision.

## Alternatives Considered
Alt 2.

## Evidence
Evidence 2.

## Consequences
Consequence 2.

## Compatibility and Migration
Compat 2.

## TDD Obligations
TDD 2.

## Rollback
Rollback 2.

## Open Questions
None.
`
		docs, entries, err := splitAndParseADRs(output, prd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(docs) != 2 {
			t.Fatalf("expected 2 docs, got %d", len(docs))
		}
		if docs[0].FrontMatter.ADRID != "ADR-001" {
			t.Errorf("expected ADR-001 first, got %s", docs[0].FrontMatter.ADRID)
		}
		if docs[1].FrontMatter.ADRID != "ADR-002" {
			t.Errorf("expected ADR-002 second, got %s", docs[1].FrontMatter.ADRID)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
	})

	t.Run("auto-assigns ADR ID when missing", func(t *testing.T) {
		output := `---
schema_version: "idiom.adr.v1"
status: "draft"
prd_refs: ["REQ-001"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-001"]
coverage_tier: "unit"
---
## Context
Test.

## Decision
Test.

## Alternatives Considered
Test.

## Evidence
Test.

## Consequences
Test.

## Compatibility and Migration
Test.

## TDD Obligations
Test.

## Rollback
Test.

## Open Questions
Test.
`
		docs, _, err := splitAndParseADRs(output, prd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if docs[0].FrontMatter.ADRID != "ADR-001" {
			t.Errorf("expected auto-assigned ADR-001, got %s", docs[0].FrontMatter.ADRID)
		}
	})

	t.Run("skips invalid ADRs", func(t *testing.T) {
		output := `---
schema_version: "idiom.adr.v1"
adr_id: "ADR-001"
status: "draft"
prd_refs: ["REQ-001"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-001"]
coverage_tier: "unit"
---
## Context
Valid ADR.

## Decision
Valid.

## Alternatives Considered
Valid.

## Evidence
Valid.

## Consequences
Valid.

## Compatibility and Migration
Valid.

## TDD Obligations
Valid.

## Rollback
Valid.

## Open Questions
Valid.
===
garbage not an ADR
===
---
schema_version: "idiom.adr.v1"
adr_id: "ADR-002"
status: "draft"
prd_refs: ["REQ-002"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-002"]
coverage_tier: "unit"
---
## Context
Second valid ADR.

## Decision
Second.

## Alternatives Considered
Second.

## Evidence
Second.

## Consequences
Second.

## Compatibility and Migration
Second.

## TDD Obligations
Second.

## Rollback
Second.

## Open Questions
Second.
`
		docs, _, err := splitAndParseADRs(output, prd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(docs) != 2 {
			t.Fatalf("expected 2 valid docs (skipping garbage), got %d", len(docs))
		}
	})
}

func TestPlanExecuteAnswerIsBlocked(t *testing.T) {
	for _, input := range []string{
		"",
		"<<QUESTION>>: Plan-execute could not produce a plan artifact.",
		"plan-execute: recovery-open\nblockers: required_edit_paths_remaining",
	} {
		if !planExecuteAnswerIsBlocked(input) {
			t.Fatalf("expected blocked plan-execute answer for %q", input)
		}
	}
	if planExecuteAnswerIsBlocked("Decision: source-satisfied\n- internal/example/example.go: already satisfied") {
		t.Fatal("source-satisfied answer should remain eligible for completion wrapper")
	}
}

func TestInferProductWorkflowFilesToModifyFromADRBody(t *testing.T) {
	body := "## Consequences\n- New package internal/greeting with greeting.go and greeting_test.go\n"
	got := inferProductWorkflowFilesToModify(body)
	for _, want := range []string{"internal/greeting/greeting.go", "internal/greeting/greeting_test.go"} {
		found := false
		for _, path := range got {
			if path == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing inferred path %q from %#v", want, got)
		}
	}
}

func TestMergeADRIndex(t *testing.T) {
	makeDoc := func(id string) productworkflow.ADRDocument {
		return productworkflow.ADRDocument{
			FrontMatter: productworkflow.ADRFrontMatter{ADRID: id, Status: "draft"},
		}
	}
	makeEntry := func(id, path string) productworkflow.ADRIndexEntry {
		return productworkflow.ADRIndexEntry{ADRID: id, Path: path, Status: "draft"}
	}

	t.Run("all new entries", func(t *testing.T) {
		existing := productworkflow.ADRIndex{}
		newDocs := []productworkflow.ADRDocument{makeDoc("ADR-001"), makeDoc("ADR-002")}
		newEntries := []productworkflow.ADRIndexEntry{makeEntry("ADR-001", "ADR-001.md"), makeEntry("ADR-002", "ADR-002.md")}

		docs, entries := mergeADRIndex(existing, newDocs, newEntries)
		if len(docs) != 2 || len(entries) != 2 {
			t.Fatalf("expected 2 each, got %d docs, %d entries", len(docs), len(entries))
		}
	})

	t.Run("replaces existing entries with same ID", func(t *testing.T) {
		existing := productworkflow.ADRIndex{
			ADRs: []productworkflow.ADRIndexEntry{
				{ADRID: "ADR-001", Path: "ADR-001.md", Status: "draft"},
			},
		}
		newDocs := []productworkflow.ADRDocument{makeDoc("ADR-001")}
		newDocs[0].FrontMatter.Status = "updated"
		newEntries := []productworkflow.ADRIndexEntry{makeEntry("ADR-001", "ADR-001.md")}
		newEntries[0].Status = "updated"

		docs, entries := mergeADRIndex(existing, newDocs, newEntries)
		if len(entries) != 1 {
			t.Fatalf("expected 1, got %d", len(entries))
		}
		if entries[0].Status != "updated" {
			t.Errorf("expected status updated, got %s", entries[0].Status)
		}
		_ = docs
	})

	t.Run("appends new entries to existing", func(t *testing.T) {
		existing := productworkflow.ADRIndex{
			ADRs: []productworkflow.ADRIndexEntry{
				{ADRID: "ADR-001", Path: "ADR-001.md", Status: "draft"},
			},
		}
		newDocs := []productworkflow.ADRDocument{makeDoc("ADR-002")}
		newEntries := []productworkflow.ADRIndexEntry{makeEntry("ADR-002", "ADR-002.md")}

		_, entries := mergeADRIndex(existing, newDocs, newEntries)
		if len(entries) != 2 {
			t.Fatalf("expected 2, got %d", len(entries))
		}
		found := map[string]bool{}
		for _, e := range entries {
			found[e.ADRID] = true
		}
		if !found["ADR-001"] || !found["ADR-002"] {
			t.Errorf("expected both ADR-001 and ADR-002, got %v", found)
		}
	})
}

func TestBuildPRDGenerationPrompt(t *testing.T) {
	prompt := buildPRDGenerationPrompt("build user authentication", "/tmp/effort")
	if !strings.Contains(prompt, "build user authentication") {
		t.Error("prompt should contain feature description")
	}
	if !strings.Contains(prompt, "idiom.prd.v1") {
		t.Error("prompt should contain schema version")
	}
	if !strings.Contains(prompt, "**id**: REQ-001") {
		t.Error("prompt should contain example requirement format")
	}
	if !strings.Contains(prompt, "open_questions") {
		t.Error("prompt should contain open_questions field")
	}
}

func TestBuildADRGenerationPrompt(t *testing.T) {
	prompt := buildADRGenerationPrompt("PRD content here", "/tmp/effort")
	if !strings.Contains(prompt, "PRD content here") {
		t.Error("prompt should contain PRD content")
	}
	if !strings.Contains(prompt, "===") {
		t.Error("prompt should mention === separator")
	}
	if !strings.Contains(prompt, "## Context") {
		t.Error("prompt should list required sections")
	}
	if !strings.Contains(prompt, "idiom.adr.v1") {
		t.Error("prompt should contain schema version")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "b", "c"); got != "b" {
		t.Errorf("expected b, got %s", got)
	}
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("expected a, got %s", got)
	}
	if got := firstNonEmpty("", "", ""); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("expected hello..., got %s", got)
	}
	if got := truncate("hello world", 11); got != "hello world" {
		t.Errorf("expected hello world, got %s", got)
	}
}
