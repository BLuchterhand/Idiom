package cliassets

import (
	"slices"
	"strings"
	"testing"
)

func TestAssetsExposeExpectedBundles(t *testing.T) {
	if !strings.Contains(string(BriefPreflightScript()), "brief-preflight.py <brief.md>") {
		t.Fatalf("brief preflight script is missing its usage anchor")
	}

	preloadKinds, err := BriefPreloadKinds()
	if err != nil {
		t.Fatalf("list brief preload kinds: %v", err)
	}

	if !slices.Contains(preloadKinds, "generic") {
		t.Fatalf("expected generic preload kind, got %#v", preloadKinds)
	}

	if body, err := LanePrompt("implement", "preamble.md"); err != nil || strings.TrimSpace(body) == "" {
		t.Fatalf("expected embedded implement preamble, body=%q err=%v", body, err)
	}

	if body, err := LaneRule("brief-skeleton.md"); err != nil || strings.TrimSpace(body) == "" {
		t.Fatalf("expected embedded brief-skeleton rule, body=%q err=%v", body, err)
	}

	skillNames, err := SkillNames()
	if err != nil {
		t.Fatalf("list skill names: %v", err)
	}

	if !slices.Contains(skillNames, "orchestrator-workflow") {
		t.Fatalf("expected orchestrator-workflow skill, got %#v", skillNames)
	}
}
