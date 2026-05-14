package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"idiom/internal/agent"
	"idiom/internal/app"
	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/workspace"
)

func TestRunPlanExecuteMainForwardsThinkingAndMaxTokens(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts runOptions

	code := runPlanExecuteMain(
		[]string{"-config", configPath, "-prompt", "plan this", "-no-thinking", "-max-tokens", "2048"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts runOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	if !gotOpts.DisableThinking {
		t.Fatal("--no-thinking should reach the plan-execute planner run")
	}
	if gotOpts.MaxTokensOverride != 2048 {
		t.Fatalf("MaxTokensOverride = %d, want 2048", gotOpts.MaxTokensOverride)
	}
}

func TestRunPlanExecuteMainUsesDefaultPlannerTokenBudget(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts runOptions

	code := runPlanExecuteMain(
		[]string{"-config", configPath, "-prompt", "plan this"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts runOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	if gotOpts.MaxTokensOverride != 16384 {
		t.Fatalf("MaxTokensOverride = %d, want plan-execute default 16384", gotOpts.MaxTokensOverride)
	}
}

func TestPlanExecutePlannerAddendumAvoidsHardReadCaps(t *testing.T) {
	addendum := planExecutePlannerSystemAddendum()
	for _, forbidden := range []string{
		"At most one bounded source-read",
		"Never re-read the same file",
	} {
		if strings.Contains(addendum, forbidden) {
			t.Fatalf("planner addendum still contains hard read-cap wording %q:\n%s", forbidden, addendum)
		}
	}
	for _, want := range []string{
		"new named edit-safety fact",
		"Do not re-read for confidence",
		"`Decision: source-satisfied`",
	} {
		if !strings.Contains(addendum, want) {
			t.Fatalf("planner addendum missing readiness-oriented wording %q:\n%s", want, addendum)
		}
	}
}

func TestRunPlanExecuteMainTreatsSourceSatisfiedPlannerAnswerAsCleanNoop(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n| `b.go` | update existing test |\n"
	var stdout, stderr bytes.Buffer

	code := runPlanExecuteMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			return "Decision: source-satisfied\n- a.go: already implemented; no changes needed.\n- b.go: already implemented; no changes needed.", nil
		},
	)

	if code != 0 {
		t.Fatalf("source-satisfied planner answer should exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if strings.Contains(stderr.String(), "plan artifact") {
		t.Fatalf("source-satisfied closeout should not report missing plan artifact, stderr=%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "source-satisfied: executor skipped") {
		t.Fatalf("source-satisfied closeout should be visible, stdout=%s", stdout.String())
	}
}

func TestRunPlanExecuteMainStillFailsMissingArtifactWithoutSourceSatisfiedEvidence(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n| `b.go` | update existing test |\n"
	var stdout, stderr bytes.Buffer

	code := runPlanExecuteMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			return "Decision: source-satisfied\n- a.go: already implemented; no changes needed.", nil
		},
	)

	if code != 1 {
		t.Fatalf("missing source-satisfied path evidence should still fail missing artifact, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "planner phase did not produce a plan artifact") {
		t.Fatalf("expected normal missing-artifact failure, stderr=%s", stderr.String())
	}
}

func TestRunPlanExecuteMainFailsMissingArtifactDespiteExplicitFullBodyNewFileBrief(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|------|--------|\n" +
		"| `docs/generated-note.txt` | Create the new note. |\n\n" +
		"## SubmitPlan Shortcut\n\n" +
		"This is a new-file atom. Use this full file body for the `replace_all:true` / empty `old_text` / `new_text` submit_plan shape:\n\n" +
		"```text\n" +
		"alpha\n" +
		"beta\n" +
		"```\n"
	var stdout, stderr bytes.Buffer

	code := runPlanExecuteMain(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			return "I can create docs/generated-note.txt, but I did not call submit_plan.", nil
		},
	)

	if code != 1 {
		t.Fatalf("missing planner artifact must fail even with inline full body, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "planner phase did not produce a plan artifact") {
		t.Fatalf("expected missing-artifact failure, stderr=%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "generated-note.txt")); !os.IsNotExist(err) {
		t.Fatalf("host fallback must not create a file from prompt body, stat err=%v", err)
	}
	if strings.Contains(stdout.String(), "idiom.plan_execute.auto_submit_full_body_plan") {
		t.Fatalf("host fallback telemetry must not be emitted, stdout=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "idiom.plan_execute.plan_artifact_missing") {
		t.Fatalf("expected missing-artifact telemetry event, stdout=%s", stdout.String())
	}
}

func TestRunPlanExecuteMainDoesNotAutoSubmitRealQuestionBlocker(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|------|--------|\n" +
		"| `docs/generated-note.txt` | Create the new note. |\n\n" +
		"## SubmitPlan Shortcut\n\n" +
		"Use this full file body for the `replace_all:true` / empty `old_text` / `new_text` submit_plan shape:\n\n" +
		"```text\n" +
		"alpha\n" +
		"```\n"
	var stdout, stderr bytes.Buffer

	code := runPlanExecuteMain(
		[]string{"-config", configPath, "-prompt", prompt, "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			return "<<QUESTION>>: docs/generated-note.txt: current API evidence contradicts the requested body; do not create the file until the brief is rebased.", nil
		},
	)

	if code != 1 {
		t.Fatalf("real question blockers must not be auto-submitted, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "generated-note.txt")); !os.IsNotExist(err) {
		t.Fatalf("auto-submit created a file despite a real blocker, stat err=%v", err)
	}
}

func TestRunPlanExecuteMainDoesNotTreatQuestionBlockerMentioningSourceSatisfiedAsCleanNoop(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n"
	var stdout, stderr bytes.Buffer

	code := runPlanExecuteMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			return "<<QUESTION>>: Completion gate rejected before submit_plan.\n- a.go: no successful edit was recorded; provide submit_plan, Decision: source-satisfied evidence, or a path-specific blocker.", nil
		},
	)

	if code != 1 {
		t.Fatalf("blocker that merely mentions source-satisfied must fail missing artifact, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "planner phase did not produce a plan artifact") {
		t.Fatalf("expected missing-artifact failure for blocker, stderr=%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "source-satisfied: executor skipped") {
		t.Fatalf("blocker must not be reported as source-satisfied, stdout=%s", stdout.String())
	}
}

func TestPlanExecuteInheritsPromptContextPackID(t *testing.T) {
	prompt := "## Context Pack\n\n- Summary: context view artifact\n- Pack: `feedfacecafebeef`\n"
	plan := workspace.PlanArtifact{SchemaVersion: workspace.PlanArtifactSchemaVersion}
	got := inheritPlanExecutePromptPackID(&plan, prompt)
	if got != "feedfacecafebeef" || plan.PackID != "feedfacecafebeef" {
		t.Fatalf("expected inherited pack_id feedfacecafebeef, got %q plan=%#v", got, plan)
	}
	if got := extractPlanExecutePromptPackID("context_pack://0123456789abcdef"); got != "0123456789abcdef" {
		t.Fatalf("expected context_pack URI extraction, got %q", got)
	}
}

func TestTruncatePlanExecuteTextPreservesUTF8(t *testing.T) {
	got := truncatePlanExecuteText("alpha βeta", 8)
	if !strings.Contains(got, "[truncated") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncatePlanExecuteText must not split a UTF-8 rune, got %q", got)
	}
}

func TestRunPlanExecuteMainInheritsPackBeforeExecutor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "inherit pack", Mode: "plan"}
	packID := key.ID()
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         packID,
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "inherit pack",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	configPath := filepath.Join(root, "config.json")
	body := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q,"enable_writes":true}}`, root, root)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	prompt := "## Context Pack\n\n- Pack: `" + packID + "`\n\nChange a.go from alpha to beta."
	var stdout, stderr bytes.Buffer
	code := runPlanExecuteMain(
		[]string{"-config", configPath, "-json", "-skip-review-repair", "-prompt", prompt},
		&stdout,
		&stderr,
		func(cfg config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "change marker",
				Edits: []workspace.PlanEdit{
					{Path: "a.go", OldText: "alpha", NewText: "beta", Purpose: "change marker"},
				},
			})
			return "submitted", nil
		},
	)
	if code != 0 {
		t.Fatalf("runPlanExecuteMain exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.plan_artifact_pack_inherited`) {
		t.Fatalf("expected inherited pack event, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.executor_inherits_pack`) {
		t.Fatalf("expected executor to inherit pack after prompt inheritance, got:\n%s", stdout.String())
	}
}

func writePlanExecuteConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	body := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func mustTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func slicesContainString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func planExecuteJSONEvents(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode JSON event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func planExecuteEventByType(events []map[string]any, eventType string) (map[string]any, bool) {
	for _, event := range events {
		if event["type"] == eventType {
			return event, true
		}
	}
	return nil, false
}

func TestRunExecutorPhaseEmitsExecutorInheritsPackEvent(t *testing.T) {
	// When the planner stamps a pack_id and the cache row exists, the
	// executor phase emits an executor_inherits_pack JSON event so
	// downstream tooling can attach the planner's ranked context.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "exec inherits", Mode: "plan"}
	cached := comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "exec inherits",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
		CandidateSymbols: []comprehension.PackSymbol{
			{ID: "symbol://a.go#A", Kind: "function", Path: "a.go"},
		},
		Tests: []comprehension.PackTest{{Command: "go test ./...", Path: "a.go"}},
	}
	if err := comprehension.SaveView(root, key, cached); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        key.ID(),
		Edits: []workspace.PlanEdit{
			{Path: "a.go", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.executor_inherits_pack`) {
		t.Fatalf("expected executor_inherits_pack JSON event in stdout, got: %s", stdout.String())
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got: %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateInherited || handoff["reason"] != "fresh_graph_view" {
		t.Fatalf("expected inherited handoff packet, got %#v", handoff)
	}
	if !strings.Contains(stdout.String(), `"pack_id":"`+key.ID()+`"`) {
		t.Fatalf("expected pack_id %s in event, got: %s", key.ID(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"candidate_files":1`) {
		t.Fatalf("expected candidate_files:1 in event, got: %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsExecutorPackMissOnUnknownPackID(t *testing.T) {
	// pack_id that doesn't match any graph_views row → executor_pack_miss
	// event so downstream consumers know the planner's context wasn't
	// available (rather than silently degrading).
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        "deadbeefcafef00d",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.executor_pack_miss`) {
		t.Fatalf("expected executor_pack_miss JSON event when pack_id is unknown, got: %s", stdout.String())
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok || handoff["packet_state"] != executorHandoffStateMissing || handoff["reason"] != "cache_miss" {
		t.Fatalf("expected missing handoff packet, got %#v in %s", handoff, stdout.String())
	}
}

func TestRunExecutorPhaseEmitsMissingPacketHandoffWithoutPlanPacket(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan-missing-packet",
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateMissing || handoff["reason"] != "missing_packet_id" {
		t.Fatalf("expected missing packet state, got %#v", handoff)
	}
}

func TestRunExecutorPhaseBlocksPartialApprovalScopeDrift(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan raw_prompt customer-acme token=/Users/ben/private/client",
		Task: workspace.PlanArtifactTask{
			Summary:               "Block pending partial approval scope",
			ClientClassifications: []string{"finance"},
		},
		Paths: workspace.PlanArtifactPaths{Editable: []string{"a.txt", "b.txt"}, Changed: []string{"b.txt"}},
		Approval: workspace.PlanArtifactApproval{
			Required:            true,
			Status:              "partial",
			ScopeID:             "scope raw_prompt customer-acme token=/Users/ben/private/client",
			ApprovedPaths:       []string{"a.txt"},
			PendingPaths:        []string{"b.txt"},
			ExecutionPermission: "execute_approved_scope",
		},
		Handoff: workspace.PlanArtifactHandoff{ID: "handoff raw_prompt customer-acme", Status: "ready", Ready: true},
		Edits:   []workspace.PlanEdit{{Path: "b.txt", OldText: "beta", NewText: "BETA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err == nil {
		t.Fatalf("executor should block pending partial approval scope")
	}
	events := planExecuteJSONEvents(t, stdout.String())
	drift, ok := planExecuteEventByType(events, "idiom.plan_execute.plan_scope_drift")
	if !ok {
		t.Fatalf("expected plan_scope_drift event, got %s", stdout.String())
	}
	if drift["reason"] != "plan edit path is still pending approval" || drift["path"] != "b.txt" {
		t.Fatalf("unexpected drift event: %#v", drift)
	}
	if drift["handoff_id"] == "" || drift["approval_scope_id"] == "" {
		t.Fatalf("drift event should carry stable governance ids: %#v", drift)
	}
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["approval_execution_permission"] != "execute_approved_scope" || handoff["approved_path_count"] != float64(1) || handoff["pending_path_count"] != float64(1) {
		t.Fatalf("handoff event should carry partial approval counters, got %#v", handoff)
	}
	body, err := os.ReadFile(filepath.Join(root, "b.txt"))
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if string(body) != "beta\n" {
		t.Fatalf("blocked drift should not mutate pending file, got %q", body)
	}
	for _, leaked := range []string{"raw_prompt", "customer-acme", "customer_acme", "token=", "/Users/ben/private"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Fatalf("drift event leaked sensitive marker %q: %s", leaked, stdout.String())
		}
	}
}

func TestRunExecutorPhaseEmitsStalePacketHandoff(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	packID := "deadc0decafef00d"
	writePlanExecutePackArtifact(t, root, comprehension.PackResult{
		PackID:         packID,
		IndexID:        "idx-old",
		Root:           root,
		Task:           "stale packet",
		Mode:           "plan",
		Fresh:          false,
		CandidateFiles: []comprehension.PackFile{{Path: "a.txt", Role: "editable"}},
	})
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan-stale-packet",
		PackID:        packID,
		Packet:        workspace.PlanArtifactPacket{PackID: packID, Status: "available", Freshness: "fresh"},
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateStale || handoff["reason"] != "legacy_artifact_fallback" || handoff["fresh"] != false {
		t.Fatalf("expected stale packet state, got %#v", handoff)
	}
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_packet_stale"); !ok {
		t.Fatalf("expected executor_packet_stale compatibility event, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsStaleGraphViewPacketWithoutLegacyArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/stalehandoff\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package stalehandoff\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "stale graph packet", Mode: "plan", Query: comprehension.ViewQueryInputs{Path: "a.go"}}
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "stale graph packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	if n, err := comprehension.MarkAllViewsStale(root); err != nil || n != 1 {
		t.Fatalf("MarkAllViewsStale n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension", "views", key.ID()+".json")); err == nil {
		t.Fatalf("legacy view artifact should not exist for graph-row stale test")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat legacy view artifact: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan-stale-graph-row",
		PackID:        key.ID(),
		Packet:        workspace.PlanArtifactPacket{PackID: key.ID(), Status: "available", Freshness: "fresh"},
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.go", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateStale || handoff["reason"] != "stale_graph_view" || handoff["fresh"] != false {
		t.Fatalf("expected stale graph-view packet state, got %#v", handoff)
	}
	if handoff["candidate_files"] != float64(1) {
		t.Fatalf("expected stale graph row to preserve candidate count, got %#v", handoff)
	}
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_packet_stale"); !ok {
		t.Fatalf("expected executor_packet_stale compatibility event, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsStaleForNonCurrentGraphViewPacketWithoutLegacyArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/noncurrenthandoff\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package noncurrenthandoff\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	if _, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: "old-index", Task: "non-current graph packet", Mode: "plan", Query: comprehension.ViewQueryInputs{Path: "a.go"}}
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        "old-index",
		Root:           root,
		Task:           "non-current graph packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension", "views", key.ID()+".json")); err == nil {
		t.Fatalf("legacy view artifact should not exist for non-current graph-row test")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat legacy view artifact: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan-non-current-graph-row",
		PackID:        key.ID(),
		Packet:        workspace.PlanArtifactPacket{PackID: key.ID(), Status: "available", Freshness: "fresh"},
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.go", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateStale || handoff["reason"] != "stale_graph_view" || handoff["fresh"] != false {
		t.Fatalf("expected stale non-current graph-view packet state, got %#v", handoff)
	}
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_pack_miss"); ok {
		t.Fatalf("non-current graph row should not collapse to executor_pack_miss, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsOverriddenPacketHandoffAndRedactsSensitiveIDs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/override\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package override\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "override packet", Mode: "plan"}
	packID := key.ID()
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         packID,
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "override packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.txt", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan raw_prompt customer-acme token=/Users/ben/private/client",
		PackID:        packID,
		Packet:        workspace.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"},
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff raw_prompt customer-acme token", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateOverridden || handoff["reason"] != "packet_id_override" {
		t.Fatalf("expected overridden packet state, got %#v", handoff)
	}
	for _, leaked := range []string{"raw_prompt", "customer-acme", "customer_acme", "token=", "/Users/ben/private"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Fatalf("handoff event leaked sensitive marker %q: %s", leaked, stdout.String())
		}
	}
}

func TestCollectPlanExecutePhaseEvidenceLoadsPacketAndExecutorChecks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/evidence\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "phase evidence", Mode: "plan"}
	cached := comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "phase evidence",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable", Reason: "matched task"}},
		CandidateSymbols: []comprehension.PackSymbol{
			{ID: "symbol://a.go#A", QualifiedName: "A", Kind: "function", Path: "a.go", Reason: "matched task"},
		},
		Tests:          []comprehension.PackTest{{Command: "go test ./...", Path: "a.go", Reason: "packet suggested test"}},
		UnresolvedGaps: []string{"reason=stale_runtime_trace trace_id=t1"},
	}
	if err := comprehension.SaveView(root, key, cached); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	writePlanExecutePackArtifact(t, root, cached)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"beta\" }\n"), 0o644); err != nil {
		t.Fatalf("seed post-execute a.go: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        key.ID(),
		Edits: []workspace.PlanEdit{
			{Path: "a.go", OldText: "alpha", NewText: "beta", Purpose: "change return marker"},
		},
	}
	evidence := collectPlanExecutePhaseEvidence(
		root,
		"review_repair",
		plan,
		[]string{"a.go"},
		[]string{"a.go"},
		[]string{"a.go"},
		reviewRepairEvidence{DiffCheckExit: 0},
		executorPhaseReport{SourceSanity: []phaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "ok", Summary: "Source sanity passed"}}},
	)

	if evidence.SchemaVersion != phaseEvidenceSchemaVersion {
		t.Fatalf("schema version = %q", evidence.SchemaVersion)
	}
	if evidence.Packet.Status != "available_stale" || evidence.Packet.PackID != key.ID() {
		t.Fatalf("expected stale packet artifact %s, got %#v", key.ID(), evidence.Packet)
	}
	if evidence.ExecutorHandoff.PacketState != executorHandoffStateStale || evidence.ExecutorHandoff.Reason == "" {
		t.Fatalf("expected stale executor handoff evidence, got %#v", evidence.ExecutorHandoff)
	}
	if len(evidence.Packet.CandidateFiles) != 1 || evidence.Packet.CandidateFiles[0].Path != "a.go" {
		t.Fatalf("candidate files missing from phase evidence: %#v", evidence.Packet.CandidateFiles)
	}
	if len(evidence.SuggestedTests) != 1 || evidence.SuggestedTests[0].Command != "go test ./..." {
		t.Fatalf("suggested tests missing from phase evidence: %#v", evidence.SuggestedTests)
	}
	if len(evidence.SourceSanity) != 1 || evidence.SourceSanity[0].Status != "ok" {
		t.Fatalf("source sanity missing from phase evidence: %#v", evidence.SourceSanity)
	}
	if len(evidence.SubmittedPlan) != 1 || evidence.SubmittedPlan[0].Status != "applied" {
		t.Fatalf("submitted plan state should show applied edit, got %#v", evidence.SubmittedPlan)
	}
	if !slicesContainString(evidence.UnresolvedGaps, "reason=stale_runtime_trace trace_id=t1") {
		t.Fatalf("packet gap should remain visible in unresolved gaps, got %#v", evidence.UnresolvedGaps)
	}
	if slicesContainString(evidence.OpenObligations, "packet_gap:reason=stale_runtime_trace trace_id=t1") {
		t.Fatalf("informational packet gaps must not become repair obligations, got %#v", evidence.OpenObligations)
	}
	telemetry := phaseEvidenceTelemetry(evidence)
	candidatePaths, ok := telemetry["candidate_file_paths"].([]string)
	if !ok || !slicesContainString(candidatePaths, "a.go") {
		t.Fatalf("telemetry should include bounded candidate paths, got %#v", telemetry["candidate_file_paths"])
	}
	testCommands, ok := telemetry["suggested_test_commands"].([]string)
	if !ok || !slicesContainString(testCommands, "go test ./...") {
		t.Fatalf("telemetry should include suggested test commands, got %#v", telemetry["suggested_test_commands"])
	}
}

func TestCollectPlanExecutePhaseEvidenceReportsEvidenceGaps(t *testing.T) {
	evidence := collectPlanExecutePhaseEvidence(
		t.TempDir(),
		"review_repair",
		workspace.PlanArtifact{SchemaVersion: workspace.PlanArtifactSchemaVersion, PackID: "deadbeefdeadbeef"},
		[]string{"a.go", "b.go"},
		[]string{"a.go", "b.go"},
		[]string{"a.go", "b.go"},
		reviewRepairEvidence{DiffCheckExit: 128, DiffCheck: "fatal: not a git repository"},
		executorPhaseReport{SourceSanity: []phaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "ok"}}},
	)

	for _, want := range []string{
		"context_packet_unavailable:miss",
		"patch_hygiene_unavailable",
		"source_sanity_missing:b.go",
	} {
		if !slicesContainString(evidence.OpenObligations, want) {
			t.Fatalf("expected obligation %q, got %#v", want, evidence.OpenObligations)
		}
	}
	if slicesContainString(evidence.OpenObligations, "source_sanity_missing:a.go") {
		t.Fatalf("source_sanity evidence should satisfy a.go, got %#v", evidence.OpenObligations)
	}
}

func TestLoadPlanExecutePacketEvidenceRejectsInvalidPackID(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom", "comprehension", "views"), 0o755); err != nil {
		t.Fatalf("mkdir views: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idiom", "comprehension", "secret.json"), []byte(`{"pack_id":"secret"}`), 0o644); err != nil {
		t.Fatalf("seed escaped-looking artifact: %v", err)
	}
	got := loadPlanExecutePacketEvidence(root, "../secret")
	if got.Status != "invalid_pack_id" {
		t.Fatalf("invalid pack ids must be rejected before cache-path fallback, got %#v", got)
	}
}

func TestPlanExecuteReviewRepairPromptRequiresBehavioralChecklist(t *testing.T) {
	prompt := planExecuteReviewRepairPrompt(
		"Implement the required behavior.",
		workspace.PlanArtifact{
			Rationale: "change behavior",
			Edits: []workspace.PlanEdit{
				{Path: "a.go", Purpose: "update behavior"},
			},
		},
		[]string{"a.go"},
		[]string{"a.go"},
		reviewRepairEvidence{
			Diff:          "diff --git a/a.go b/a.go\n",
			DiffCheckExit: 0,
			PhaseEvidence: phaseEvidenceBundle{
				SchemaVersion: phaseEvidenceSchemaVersion,
				Phase:         "review_repair",
				ChangedPaths:  []string{"a.go"},
				Packet: phaseEvidencePacket{
					Status:         "available",
					PackID:         "abc123",
					CandidateFiles: []phaseEvidenceFile{{Path: "a.go", Role: "editable", Reason: "matched task"}},
				},
				SuggestedTests: []phaseEvidenceTest{{Command: "go test ./...", Path: "a.go", Reason: "packet suggested test"}},
				SubmittedPlan:  []phaseEvidenceCheck{{Kind: "submitted_edit", Path: "a.go", Status: "applied", Summary: "edit 1/1: submitted new_text is present"}},
				SourceSanity:   []phaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "ok", Summary: "Source sanity passed"}},
			},
		},
	)
	for _, want := range []string{
		"Verification Checklist",
		"behavior, not just literal code correctness",
		"Default/non-regression behavior",
		"Data, lifecycle, state, persistence, concurrency/threading, and performance",
		"Allowed Repair Paths",
		"Phase Evidence Bundle",
		"context_packet: status=available pack_id=abc123",
		"executor_handoff_packet",
		"submitted_plan_state",
		"do not re-submit",
		"`go test ./...`",
		"source_sanity",
		"tautology",
		"_assert_true(true)",
		"Decision: pass",
		"submit_plan.rationale",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteReviewRepairPromptQuotesOriginalSpecAllowlist(t *testing.T) {
	original := `# Atom

## Files To Modify

| File | Change |
|------|--------|
| ` + "`src/a.go`" + ` | MODIFY: original implementation target |
`
	prompt := planExecuteReviewRepairPrompt(
		original,
		workspace.PlanArtifact{
			Rationale: "change behavior",
			Edits: []workspace.PlanEdit{
				{Path: "src/a.go", Purpose: "update behavior"},
			},
		},
		[]string{"src/a.go"},
		[]string{"src/a.go"},
		reviewRepairEvidence{Diff: "diff --git a/src/a.go b/src/a.go\n", DiffCheckExit: 0},
	)
	if paths := app.RequiredEditPathsFromPrompt(prompt); len(paths) != 0 {
		t.Fatalf("original spec allowlist should be quoted reference text, got active required paths %#v\nprompt:\n%s", paths, prompt)
	}
	if !strings.Contains(prompt, "> ## Files To Modify") {
		t.Fatalf("original spec should remain visible as quoted reference text:\n%s", prompt)
	}
}

func TestCollectReviewRepairEvidenceUsesExecutorBaselineNotGitHead(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "a.txt", "head\n", "initial")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("dirty-before-lane\n"), 0o644); err != nil {
		t.Fatalf("seed dirty worktree: %v", err)
	}
	baseline := captureReviewRepairBaseline(root, []string{"a.txt"})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after-executor\n"), 0o644); err != nil {
		t.Fatalf("seed executor result: %v", err)
	}
	evidence := collectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)
	if !strings.Contains(evidence.Diff, "-dirty-before-lane") || !strings.Contains(evidence.Diff, "+after-executor") {
		t.Fatalf("current-run diff should compare against executor baseline, got:\n%s", evidence.Diff)
	}
	if strings.Contains(evidence.Diff, "-head") {
		t.Fatalf("current-run diff must not compare against git HEAD, got:\n%s", evidence.Diff)
	}
}

func TestCollectReviewRepairEvidenceChecksCurrentRunWhitespace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	baseline := captureReviewRepairBaseline(root, []string{"a.txt"})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\nbad   \n"), 0o644); err != nil {
		t.Fatalf("seed executor result: %v", err)
	}
	evidence := collectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)
	if evidence.DiffCheckExit != 1 {
		t.Fatalf("trailing whitespace should fail current-run hygiene, got exit %d diff=%s", evidence.DiffCheckExit, evidence.Diff)
	}
	if !strings.Contains(evidence.DiffCheck, "a.txt:2") {
		t.Fatalf("whitespace finding should include current-run path and line, got:\n%s", evidence.DiffCheck)
	}
}

func TestAutoRepairReviewPatchHygieneTrimsOnlyCurrentRunLines(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("preexisting   \nclean-before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	baseline := captureReviewRepairBaseline(root, []string{"a.txt"})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("preexisting   \nclean-before\nbad   \n"), 0o644); err != nil {
		t.Fatalf("seed executor result: %v", err)
	}
	evidence := collectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)
	report, err := autoRepairReviewPatchHygiene(root, []string{"a.txt"}, evidence)
	if err != nil {
		t.Fatalf("auto repair patch hygiene: %v", err)
	}
	if !report.Repaired || report.Lines != 1 {
		t.Fatalf("expected one repaired line, got %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read repaired file: %v", err)
	}
	if string(got) != "preexisting   \nclean-before\nbad\n" {
		t.Fatalf("auto repair should trim only current-run trailing whitespace, got %q", string(got))
	}
}

func TestRunReviewRepairPhaseAutoRepairsPatchHygieneBeforePrompt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	baseline := captureReviewRepairBaseline(root, []string{"a.txt"})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\nbad   \n"), 0o644); err != nil {
		t.Fatalf("seed executor result: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "clean-before\n", NewText: "clean-before\nbad   \n", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	var gotPrompt string
	report, err := runReviewRepairPhase(
		cfg,
		"Add bad line.",
		plan,
		nil,
		&baseline,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(_ config.Config, prompt string, _ runOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: bad line is present.\n- Patch hygiene: clean after mechanical repair.", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair should accept auto-repaired hygiene: %v", err)
	}
	if report.AutoRepairs != 1 {
		t.Fatalf("expected one mechanical repair, got %#v", report)
	}
	if !strings.Contains(gotPrompt, "patch_hygiene: status=passed") {
		t.Fatalf("review prompt should receive repaired patch hygiene evidence, got:\n%s", gotPrompt)
	}
	if strings.Contains(gotPrompt, "submitted_edit_pending") || strings.Contains(gotPrompt, "submitted_edit_unknown") {
		t.Fatalf("mechanical whitespace repair should not make submitted edit look pending:\n%s", gotPrompt)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read repaired file: %v", err)
	}
	if string(got) != "clean-before\nbad\n" {
		t.Fatalf("expected repaired file, got %q", string(got))
	}
}

func TestRunReviewRepairPhaseRejectsPassWithoutChecklist(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(_ config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			return "Decision: pass\nNo issues.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected inconclusive review without checklist")
	}
	if !strings.Contains(err.Error(), "Verification Checklist") {
		t.Fatalf("error should require checklist, got %v", err)
	}
}

func TestRunReviewRepairPhaseUsesReviewRepairTaskFamilyForPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	var gotTaskFamily string
	var gotPrompt string
	report, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(_ config.Config, prompt string, opts runOptions, _ io.Writer) (string, error) {
			gotTaskFamily = opts.TaskFamily
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: good is present.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair pass should be accepted: %v", err)
	}
	if report.Status != "pass (no corrective edits)" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if gotTaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
		t.Fatalf("review-repair should use review-repair task family, got %q", gotTaskFamily)
	}
	if strings.Contains(gotPrompt, "The plan-submission capability must cover every brief-required edit once") {
		t.Fatalf("review-repair prompt must not include planner-phase submit_plan closeout addendum:\n%s", gotPrompt)
	}
}

func TestRunReviewRepairPhaseAppliesChecklistRepairPlanWithinScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	report, err := runReviewRepairPhase(
		cfg,
		"Make a.txt say good.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(cfg config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Required behavior: a.txt must say good; current diff says bad.\n",
				Edits: []workspace.PlanEdit{
					{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "repair reviewed behavior"},
				},
			})
			return "submitted repair", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair should apply scoped repair: %v", err)
	}
	if report.Status != "repaired" || report.Repairs != 1 {
		t.Fatalf("unexpected review report: %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(got) != "good\n" {
		t.Fatalf("repair should update a.txt, got %q", string(got))
	}
}

func TestRunReviewRepairPhaseRejectsOutOfScopeRepairBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "surprise.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("seed surprise.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(cfg config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Scope safety failed.\n",
				Edits: []workspace.PlanEdit{
					{Path: "surprise.txt", OldText: "keep", NewText: "mutated", Purpose: "out-of-scope repair"},
				},
			})
			return "submitted repair", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected out-of-scope review repair rejection")
	}
	if !strings.Contains(err.Error(), "allowed_edit_paths") {
		t.Fatalf("expected allowed_edit_paths rejection, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "surprise.txt"))
	if err != nil {
		t.Fatalf("read surprise.txt: %v", err)
	}
	if string(got) != "keep\n" {
		t.Fatalf("out-of-scope repair must not write surprise.txt, got %q", string(got))
	}
}

func TestRunReviewRepairPhaseRejectsNoOpAssertionRepairBeforeWrites(t *testing.T) {
	root := t.TempDir()
	original := "extends Node\n\nfunc test_case():\n\tpass\n"
	if err := os.WriteFile(filepath.Join(root, "suite.gd"), []byte(original), 0o644); err != nil {
		t.Fatalf("seed suite.gd: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "suite.gd", OldText: "\tpass\n", NewText: "\t# missing behavioral assertion\n\tpass\n", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(cfg config.Config, _ string, _ runOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Required behavior still needs a concrete assertion.\n",
				Edits: []workspace.PlanEdit{
					{Path: "suite.gd", OldText: "\tpass\n", NewText: "\t_assert_true(true, \"contract validated\")\n", Purpose: "fake behavioral proof"},
				},
			})
			return "submitted repair", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected no-op assertion repair rejection")
	}
	if !strings.Contains(err.Error(), "no-op assertion") {
		t.Fatalf("expected no-op assertion rejection, got %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(root, "suite.gd"))
	if readErr != nil {
		t.Fatalf("read suite.gd: %v", readErr)
	}
	if string(got) != original {
		t.Fatalf("review-repair preflight must reject before writes, got %q", string(got))
	}
}

func writePlanExecuteTestArtifact(t *testing.T, root string, sessionID string, artifact workspace.PlanArtifact) string {
	t.Helper()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if artifact.Created == "" {
		artifact.Created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if artifact.SchemaVersion == "" {
		artifact.SchemaVersion = workspace.PlanArtifactSchemaVersion
	}
	if artifact.SchemaVersion == workspace.PlanArtifactSchemaVersion {
		if artifact.PlanID == "" {
			artifact.PlanID = "test-plan"
		}
		if artifact.Task.Summary == "" && artifact.Task.ID == "" {
			artifact.Task.Summary = "test plan-execute artifact"
		}
		if artifact.Contract.Kind == "" {
			artifact.Contract.Kind = "plan_then_execute"
		}
		if artifact.Contract.StrategyOwner == "" {
			artifact.Contract.StrategyOwner = "model"
		}
		if artifact.Contract.ContractOwner == "" {
			artifact.Contract.ContractOwner = "harness"
		}
		if artifact.Packet.Status == "" {
			if artifact.PackID != "" {
				artifact.Packet.Status = "provided"
				artifact.Packet.PackID = artifact.PackID
			} else {
				artifact.Packet.Status = "not_provided"
			}
		}
		if len(artifact.Paths.Editable) == 0 {
			artifact.Paths.Editable = planExecuteTestEditPaths(artifact.Edits)
		}
		if len(artifact.Paths.Changed) == 0 {
			artifact.Paths.Changed = planExecuteTestEditPaths(artifact.Edits)
		}
		if len(artifact.Evidence) == 0 {
			artifact.Evidence = []workspace.PlanArtifactEvidence{{Kind: "test_fixture", Summary: "bounded plan-execute test artifact"}}
		}
		if len(artifact.VerificationObligations) == 0 {
			artifact.VerificationObligations = []workspace.PlanArtifactVerificationObligation{{Kind: "test_lane_sanity", Status: "open", Required: true}}
		}
		if artifact.Approval.Status == "" {
			artifact.Approval.Status = "implicit"
		}
		if artifact.Handoff.Status == "" {
			artifact.Handoff.Status = "ready"
			artifact.Handoff.Ready = true
		}
	}
	artifact.SessionID = sessionID
	body, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan artifact: %v", err)
	}
	path := filepath.Join(dir, time.Now().UTC().Format("20060102-150405000")+".plan.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write plan artifact: %v", err)
	}
	return path
}

func planExecuteTestEditPaths(edits []workspace.PlanEdit) []string {
	seen := map[string]struct{}{}
	var paths []string
	for _, edit := range edits {
		path := strings.TrimSpace(edit.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func writePlanExecutePackArtifact(t *testing.T, root string, pack comprehension.PackResult) string {
	t.Helper()
	path := filepath.Join(root, ".idiom", "comprehension", "views", pack.PackID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir pack artifact: %v", err)
	}
	body, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		t.Fatalf("marshal pack artifact: %v", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write pack artifact: %v", err)
	}
	return path
}

func TestRunExecutorPhaseDispatchesEditsInOrder(t *testing.T) {
	// Seed two files, run the executor phase against a plan that
	// mutates each, and verify both writes landed.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Rationale:     "rename markers",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA", Purpose: "uppercase a"},
			{Path: "b.txt", OldText: "beta", NewText: "BETA", Purpose: "uppercase b"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, nil, false, &stdout)
	if err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if report.Total != 2 || report.Succeeded != 2 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if !strings.Contains(string(got), "ALPHA") {
		t.Fatalf("a.txt should contain ALPHA after edit, got %q", string(got))
	}
	got, err = os.ReadFile(filepath.Join(root, "b.txt"))
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if !strings.Contains(string(got), "BETA") {
		t.Fatalf("b.txt should contain BETA after edit, got %q", string(got))
	}
}

func TestRunExecutorPhaseRunsSourceSanityAfterSourceEdits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "suite.gd"), []byte("extends Node\n\nfunc helper():\n\tpass\n"), 0o644); err != nil {
		t.Fatalf("seed suite.gd: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "suite.gd", OldText: "\tpass\n", NewText: "", Purpose: "remove helper body"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, nil, true, &stdout)
	if err == nil {
		t.Fatalf("expected source_sanity failure after source edit")
	}
	if report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("source_sanity runs after successful dispatch; unexpected report %#v", report)
	}
	if len(report.SourceSanity) != 1 || report.SourceSanity[0].Path != "suite.gd" || report.SourceSanity[0].Status != "failed" {
		t.Fatalf("executor report should retain failed source_sanity evidence, got %#v", report.SourceSanity)
	}
	if !strings.Contains(err.Error(), "source_sanity failed") {
		t.Fatalf("expected source_sanity error, got %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.source_sanity`) {
		t.Fatalf("expected source_sanity event, got %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"source_sanity_status":"failed"`) {
		t.Fatalf("expected failed source_sanity status in executor_complete, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseAbortsAtPreflightOnMissingFile(t *testing.T) {
	// Pre-execute plan validator catches edit 2's missing file before
	// any edit dispatches. This is by-design atomic-preflight: rather
	// than landing edit 1 then failing edit 2 (the old half-applied
	// state), the whole plan is rejected with a single error pointing
	// at the bad edit. Counts remain at 0 because no dispatch ran.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
			{Path: "missing.txt", OldText: "x", NewText: "y"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, nil, false, &stdout)
	if err == nil {
		t.Fatalf("expected preflight abort on missing file, got nil")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("preflight aborts before any dispatch — report should be untouched, got %#v", report)
	}
	if !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("error should name the failing path, got %v", err)
	}
	if !strings.Contains(err.Error(), "preflight") {
		t.Fatalf("error should identify the abort layer as preflight, got %v", err)
	}
	// a.txt must NOT have been modified — atomic preflight means
	// nothing landed when one edit was bad.
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "alpha\n" {
		t.Fatalf("preflight abort must leave files untouched, got %q", string(got))
	}
}

func TestRunExecutorPhaseRejectsPlanMissingRequiredEditPathBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, []string{"a.txt", "b.txt"}, true, &stdout)
	if err == nil {
		t.Fatalf("expected required_edit_paths preflight failure")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("required path preflight must abort before dispatch, got %#v", report)
	}
	if !strings.Contains(err.Error(), "b.txt") {
		t.Fatalf("error should name the missing required path, got %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.preflight_failed`) {
		t.Fatalf("expected preflight_failed event, got %s", stdout.String())
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "alpha\n" {
		t.Fatalf("preflight abort must leave files untouched, got %q", string(got))
	}
}

func TestRunExecutorPhaseRejectsPlanOutsideRequiredEditPathsBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "surprise.txt"), []byte("surprise\n"), 0o644); err != nil {
		t.Fatalf("seed surprise.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
			{Path: "surprise.txt", OldText: "surprise", NewText: "SURPRISE"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, []string{"a.txt"}, true, &stdout)
	if err == nil {
		t.Fatalf("expected outside required_edit_paths preflight failure")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("scope preflight must abort before dispatch, got %#v", report)
	}
	if !strings.Contains(err.Error(), "surprise.txt") {
		t.Fatalf("error should name the outside path, got %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "surprise.txt"))
	if string(got) != "surprise\n" {
		t.Fatalf("preflight abort must leave outside file untouched, got %q", string(got))
	}
}

func TestPreflightPlanEditsCatchesAnchorMismatch(t *testing.T) {
	// The donkey-atom failure mode: plan's old_text spans content
	// that doesn't appear in the file in that exact form. Preflight
	// must catch it and name the edit's index + path.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("line one\nline two\nline three\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "line one\nline three", NewText: "X"},
		},
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must catch anchor mismatch (combined non-adjacent lines)")
	}
	if got.Index != 0 {
		t.Fatalf("mismatch should point at edit 0, got index %d", got.Index)
	}
	if got.Path != "x.txt" {
		t.Fatalf("mismatch should name the path, got %q", got.Path)
	}
	if !strings.Contains(strings.ToLower(got.Reason), "old_text") {
		t.Fatalf("mismatch reason should mention old_text, got %q", got.Reason)
	}
}

func TestPreflightPlanEditsAcceptsValidPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "hello", NewText: "HELLO"},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("preflight must accept a clean plan, got mismatch %#v", got)
	}
}

func TestPreflightPlanEditsRejectsAmbiguousOldTextBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("set_floor(30.0f);\nother();\nset_floor(30.0f);\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;"},
		},
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must catch ambiguous old_text before executor dispatch")
	}
	if got.Index != 0 || got.Path != "x.txt" {
		t.Fatalf("mismatch should point at edit 0 in x.txt, got %#v", got)
	}
	if !strings.Contains(got.Reason, "matched 2 locations") {
		t.Fatalf("mismatch reason should report duplicate matches, got %q", got.Reason)
	}
}

func TestPreflightPlanEditsAllowsReplaceAllRepeatedOldText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("set_floor(30.0f);\nset_floor(30.0f);\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;", ReplaceAll: true},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("replace_all repeated old_text should pass preflight, got %#v", got)
	}
}

func TestPreflightPlanUnsupportedProofClaimsRejectsNoOpAssertion(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{
				Path:    "tests/panel_contract.gd",
				NewText: "func test_contract():\n\t_assert_true(true, \"contract validated\")\n",
			},
		},
	}
	got := preflightPlanUnsupportedProofClaims(plan)
	if got == nil {
		t.Fatalf("expected unsupported proof preflight to reject no-op assertion")
	}
	if got.Path != "tests/panel_contract.gd" || !strings.Contains(got.Reason, "no-op assertion") {
		t.Fatalf("unexpected mismatch: %#v", got)
	}
}

func TestPreflightPlanUnsupportedProofClaimsRejectsSelfDisqualifyingProofComment(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{
				Path:    "tests/panel_contract.gd",
				NewText: "func test_contract():\n\t# simplified check: for now, we just verify that it exists and can be called\n\tpass\n",
			},
		},
	}
	got := preflightPlanUnsupportedProofClaims(plan)
	if got == nil {
		t.Fatalf("expected unsupported proof preflight to reject self-disqualifying proof comment")
	}
	if got.Path != "tests/panel_contract.gd" || !strings.Contains(got.Reason, "self-disqualifying") {
		t.Fatalf("unexpected mismatch: %#v", got)
	}
}

func TestPreflightPlanUnsupportedProofClaimsIgnoresProseFiles(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{
				Path:    "docs/review.md",
				NewText: "This retrospective says we cannot easily test the old behavior.\n",
			},
		},
	}
	if got := preflightPlanUnsupportedProofClaims(plan); got != nil {
		t.Fatalf("prose files should not trigger source proof preflight, got %#v", got)
	}
}

func TestPreflightPlanEditsSkipsRangeShapedEdits(t *testing.T) {
	// Range-shaped edits (start_line+end_line+content, no old_text)
	// don't depend on substring matching; preflight must skip them
	// and let the executor handle their validation.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", StartLine: 1, EndLine: 1, Content: "A\n"},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("preflight must skip range-shaped edits, got mismatch %#v", got)
	}
}

func TestPreflightPlanEditsRejectsAdjacentDuplicateCodeLine(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sim.cpp"), []byte("void tick() {\n    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n}\n"), 0o644); err != nil {
		t.Fatalf("seed sim.cpp: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{
				Path:    "sim.cpp",
				OldText: "    c.cultural_work_mult = 1.0f;\n",
				NewText: "    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n",
			},
		},
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must reject plans that introduce adjacent duplicate code")
	}
	if got.Index != 0 || got.Path != "sim.cpp" {
		t.Fatalf("mismatch should point at edit 0 in sim.cpp, got %#v", got)
	}
	if !strings.Contains(got.Reason, "adjacent duplicate code line") {
		t.Fatalf("mismatch reason should name adjacent duplicate code, got %q", got.Reason)
	}
}

func TestRunExecutorPhaseRejectsCompletedArtifactOverwriteBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	specPath := filepath.Join(root, ".planning", "atoms", "done.md")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("mkdir spec parent: %v", err)
	}
	originalSpec := "# Done\n\nStatus: complete\n\nAccepted spec.\n"
	if err := os.WriteFile(specPath, []byte(originalSpec), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after"},
			{Path: ".planning/atoms/done.md", NewText: "# Done\n\nNew draft.\n", ReplaceAll: true},
		},
	}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, nil, false, &stdout)
	if err == nil {
		t.Fatalf("expected completed artifact overwrite preflight failure")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("preflight failure should happen before dispatch, got %#v", report)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "before\n" {
		t.Fatalf("preflight abort must leave earlier files untouched, got %q", string(got))
	}
	if got, _ := os.ReadFile(specPath); string(got) != originalSpec {
		t.Fatalf("preflight abort must leave completed spec untouched, got %q", string(got))
	}
}

func TestRunExecutorPhaseRejectsEmptyPlan(t *testing.T) {
	cfg := config.Config{Workspace: config.Workspace{Root: t.TempDir()}}
	var stdout bytes.Buffer
	_, err := runExecutorPhase(cfg, workspace.PlanArtifact{SchemaVersion: workspace.PlanArtifactSchemaVersion}, nil, false, &stdout)
	if err == nil {
		t.Fatalf("empty plan should be rejected")
	}
}

func TestFindLatestPlanArtifactReturnsMostRecent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write two stamped plans + the rolling pointer.
	old := filepath.Join(dir, "20260101-000000000.plan.json")
	newer := filepath.Join(dir, "20260101-000001000.plan.json")
	pointer := filepath.Join(dir, "last.plan.json")
	for _, p := range []string{old, newer, pointer} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	// Force the newer one to have a later mtime regardless of write
	// order.
	if err := os.Chtimes(newer, mustTime("2026-04-01T00:00:00Z"), mustTime("2026-04-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes newer: %v", err)
	}
	if err := os.Chtimes(old, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}
	got, err := findLatestPlanArtifact(root, time.Time{}, "")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact: %v", err)
	}
	if filepath.Base(got) != filepath.Base(newer) {
		t.Fatalf("expected newer artifact, got %s", got)
	}
}

func TestFindLatestPlanArtifactErrorsWhenNoneExist(t *testing.T) {
	root := t.TempDir()
	if _, err := findLatestPlanArtifact(root, time.Time{}, ""); err == nil {
		t.Fatalf("expected error when plans dir missing")
	}
}

func TestFindLatestPlanArtifactFiltersBySessionID(t *testing.T) {
	// The headline concurrency-isolation invariant: when a session ID
	// is supplied, plans from other sessions (even newer ones) must be
	// invisible. Without this filter, two concurrent plan-execute runs
	// in the same workspace can load each other's plans and dispatch
	// the wrong edits.
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mine := filepath.Join(dir, "20260510-180000000.plan.json")
	otherNewer := filepath.Join(dir, "20260510-180100000.plan.json")
	if err := os.WriteFile(mine, []byte(`{"schema_version":"idiom.plan.v1","session_id":"sid-mine","edits":[{"path":"a","old_text":"x","new_text":"y"}]}`), 0o644); err != nil {
		t.Fatalf("seed mine: %v", err)
	}
	if err := os.WriteFile(otherNewer, []byte(`{"schema_version":"idiom.plan.v1","session_id":"sid-other","edits":[{"path":"a","old_text":"x","new_text":"y"}]}`), 0o644); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	if err := os.Chtimes(mine, mustTime("2026-05-10T18:00:00Z"), mustTime("2026-05-10T18:00:00Z")); err != nil {
		t.Fatalf("chtimes mine: %v", err)
	}
	if err := os.Chtimes(otherNewer, mustTime("2026-05-10T18:01:00Z"), mustTime("2026-05-10T18:01:00Z")); err != nil {
		t.Fatalf("chtimes other: %v", err)
	}
	// With sessionID filter: mine is selected even though other is newer.
	got, err := findLatestPlanArtifact(root, time.Time{}, "sid-mine")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact: %v", err)
	}
	if filepath.Base(got) != filepath.Base(mine) {
		t.Fatalf("expected my session's plan even though other is newer, got %s", got)
	}
	// Without sessionID (legacy callers): newest wins regardless of attribution.
	got, err = findLatestPlanArtifact(root, time.Time{}, "")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact (no session): %v", err)
	}
	if filepath.Base(got) != filepath.Base(otherNewer) {
		t.Fatalf("legacy lookup should return newest, got %s", got)
	}
	// Filter for an unknown session: error, even though other plans exist.
	if _, err := findLatestPlanArtifact(root, time.Time{}, "sid-nobody"); err == nil {
		t.Fatalf("expected error when no plan matches the requested session")
	}
}

func TestFindLatestPlanArtifactSkipsArtifactsBeforeNotBefore(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(dir, "20260101-000000000.plan.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := os.Chtimes(stale, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}
	notBefore := mustTime("2026-04-01T00:00:00Z")
	if _, err := findLatestPlanArtifact(root, notBefore, ""); err == nil {
		t.Fatalf("expected error when only stale artifacts exist before notBefore")
	}
}

func TestCleanupStalePlanArtifactsRemovesOldArtifacts(t *testing.T) {
	// cleanupStalePlanArtifacts must remove *.plan.json files older than
	// the threshold while preserving recent artifacts and last.plan.json.
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Seed a stale plan artifact (older than 7 days).
	stale := filepath.Join(dir, "20260101-000000000.plan.json")
	staleContent := `{"schema_version":"idiom.plan.v1","pack_id":"abc123","edits":[]}`
	if err := os.WriteFile(stale, []byte(staleContent), 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := os.Chtimes(stale, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}

	// Seed a non-stale plan artifact (recent mtime) to verify it is preserved.
	recent := filepath.Join(dir, "20260601-000000000.plan.json")
	recentContent := `{"schema_version":"idiom.plan.v1","pack_id":"def456","edits":[]}`
	if err := os.WriteFile(recent, []byte(recentContent), 0o644); err != nil {
		t.Fatalf("seed recent: %v", err)
	}

	// Call cleanupStalePlanArtifacts directly with 7-day threshold.
	var buf bytes.Buffer
	cleanupStalePlanArtifacts(root, 7*24*time.Hour, &buf)

	// Verify the stale plan artifact was removed.
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale plan should have been removed, but still exists")
	}

	// Verify the non-stale plan artifact was preserved.
	if _, err := os.Stat(recent); os.IsNotExist(err) {
		t.Fatalf("recent plan should have been preserved, but was removed")
	}
}
