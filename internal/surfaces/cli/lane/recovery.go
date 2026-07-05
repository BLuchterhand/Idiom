package lane

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/config"
	"idiom/internal/platform/sessionid"
	"idiom/internal/workflows/recovery"
)

type lanePromptSessionCapture struct {
	sessionID string
}

func (c *lanePromptSessionCapture) wrap(next promptRunner) promptRunner {
	return func(cfg config.Config, prompt string, opts runOptions, stdout io.Writer) (string, error) {
		if sessionID := strings.TrimSpace(cfg.Workspace.SessionID); sessionID != "" {
			c.sessionID = sessionID
		}

		return next(cfg, prompt, opts, stdout)
	}
}

func (c *lanePromptSessionCapture) SessionID() string {
	if c == nil {
		return ""
	}

	return strings.TrimSpace(c.sessionID)
}

func writeLaneImplementRecovery(opts laneOptions, history laneHistoryEvent, subcommandExit int, stderr io.Writer) {
	sessionID := strings.TrimSpace(history.SessionID)
	if sessionID == "" {
		sessionID = sessionid.New()
	}

	now := time.Now().UTC()
	summary := laneImplementFailureSummary(history, subcommandExit)
	failureClass := recovery.ClassifyImplementFailure(summary)
	input := recovery.FailureRecordInput{
		FailureID: recovery.ImplementFailureID(sessionID),
		SourceRun: recoverycontract.SourceRun{
			Lane:              string(recoverycontract.SourceLaneImplement),
			SessionID:         sessionID,
			PromptFile:        laneWorkspaceRelPath(opts.ProjectRoot, opts.BriefPath),
			RequiredEditPaths: laneRecoveryRequiredEditPaths(opts, stderr),
		},
		FailureClass:   failureClass,
		FailureKind:    "implement_process_failed",
		FailurePhase:   "implement",
		FailureSummary: summary,
		EvidenceRefs:   laneImplementEvidenceRefs(opts, history),
		Implementation: history.Implementation,
	}

	record, recordPath, err := recovery.WriteFailureRecord(opts.ProjectRoot, input, now)
	if err != nil {
		fmt.Fprintf(stderr, "lane: recovery failure record failed: %v\n", err)

		return
	}

	packet, packetPath, err := recovery.WriteDecompositionPacketForFailureRecord(opts.ProjectRoot, record, now)
	if err != nil {
		fmt.Fprintf(stderr, "lane: recovery decomposition packet failed: %v\n", err)

		return
	}

	fmt.Fprintf(stderr, "lane: recovery failure_record=%s packet=%s next_valid_action=%s\n", recordPath, packetPath, packet.NextAction)
}

func laneImplementFailureSummary(history laneHistoryEvent, subcommandExit int) string {
	parts := []string{
		fmt.Sprintf("implement process failed: exit_code=%d", subcommandExit),
	}
	if finalState := strings.TrimSpace(history.FinalState); finalState != "" {
		parts = append(parts, "final_state="+finalState)
	}

	if stopReason := strings.TrimSpace(history.StopReason); stopReason != "" {
		parts = append(parts, "stop_reason="+stopReason)
	}

	if len(history.Degradations) > 0 {
		parts = append(parts, "degradations="+strings.Join(history.Degradations, ","))
	}

	return strings.Join(parts, " ")
}

func laneImplementEvidenceRefs(opts laneOptions, history laneHistoryEvent) []recoverycontract.EvidenceRef {
	refs := []recoverycontract.EvidenceRef{
		{Kind: "lane_history", Path: laneHistoryRelPath},
	}
	if brief := laneWorkspaceRelPath(opts.ProjectRoot, opts.BriefPath); brief != "" {
		refs = append(refs, recoverycontract.EvidenceRef{Kind: "original_brief", Path: brief})
	}

	if sessionID := strings.TrimSpace(history.SessionID); sessionID != "" {
		ref := recoverycontract.EvidenceRef{Kind: "session_summary", ID: sessionID}
		if stopReason := strings.TrimSpace(history.StopReason); stopReason != "" {
			ref.Summary = "stop_reason=" + stopReason
		}

		refs = append(refs, ref)
		if failures := lanePlanningFailuresPath(opts.ProjectRoot, sessionID); failures != "" {
			refs = append(refs, recoverycontract.EvidenceRef{Kind: "planning_failures", Path: failures})
		}
	}

	for _, kind := range history.Degradations {
		if kind = strings.TrimSpace(kind); kind != "" {
			refs = append(refs, recoverycontract.EvidenceRef{Kind: "lane_degradation", ID: kind})
		}
	}

	return refs
}

func laneRecoveryRequiredEditPaths(opts laneOptions, stderr io.Writer) []string {
	body, err := os.ReadFile(opts.BriefPath)
	if err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "lane: recovery required_edit_paths skipped: read brief: %v\n", err)
		}

		return nil
	}

	return plancontract.UniqueScopePaths(plancontract.RequiredEditPathsFromPrompt(string(body)))
}

func lanePlanningFailuresPath(projectRoot, sessionID string) string {
	if strings.TrimSpace(projectRoot) == "" || strings.TrimSpace(sessionID) == "" {
		return ""
	}

	rel := filepath.ToSlash(filepath.Join(".planning", sessionID+".failures.jsonl"))
	if _, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(rel))); err != nil {
		return ""
	}

	return rel
}

func laneWorkspaceRelPath(projectRoot, path string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	path = strings.TrimSpace(path)

	if projectRoot == "" || path == "" {
		return ""
	}

	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return ""
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return ""
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ""
	}

	return filepath.ToSlash(rel)
}
