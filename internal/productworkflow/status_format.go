package productworkflow

import (
	"fmt"
	"strings"
)

// FormatProductOperatorStatusText renders the shared text presenter for CLI and TUI operator status.
func FormatProductOperatorStatusText(effortName string, view ProductOperatorStatusView) string {
	var out strings.Builder
	fmt.Fprintf(&out, "product operator status: %s %s\n", effortName, view.EffortID)
	fmt.Fprintf(&out, "  authority: %s\n", view.ContinuationAuthority)
	fmt.Fprintf(&out, "  state: %s/%s  audit: %s\n", view.CurrentPhase, view.CurrentState, view.AuditStatus)
	fmt.Fprintf(&out, "  context_pack: %s  freshness: %s\n", view.ContextPackID, view.FreshnessState)
	writeProductRecoveryStatusText(&out, view.Recovery)
	if len(view.Blockers) > 0 {
		fmt.Fprintf(&out, "  blockers (%d):\n", len(view.Blockers))
		for _, blocker := range view.Blockers {
			fmt.Fprintf(&out, "    [%s] %s (%s)\n", blocker.Code, blocker.Message, blocker.Source)
		}
	}
	if len(view.OpenObligations) > 0 {
		fmt.Fprintf(&out, "  open_obligations (%d): %s\n", len(view.OpenObligations), strings.Join(view.OpenObligations, ", "))
	}
	if len(view.NextValidActions) > 0 {
		fmt.Fprintf(&out, "  next_valid_actions (%d):\n", len(view.NextValidActions))
		for _, action := range view.NextValidActions {
			fmt.Fprintf(&out, "    - %s [%s] owner=%s dispatchable=%t runnable_now=%t", action.Label, action.ID, action.Owner, action.Dispatchable, action.RunnableNow)
			if len(action.BlockedBy) > 0 {
				fmt.Fprintf(&out, " blocked_by=%s", strings.Join(action.BlockedBy, ","))
			}
			if action.CommandHint != "" {
				fmt.Fprintf(&out, " command_hint=%s", action.CommandHint)
			}
			if action.Message != "" {
				fmt.Fprintf(&out, "  %s", action.Message)
			}
			fmt.Fprintln(&out)
		}
	}
	if view.CheckedFileCount > 0 {
		fmt.Fprintf(&out, "  files_checked: %d\n", view.CheckedFileCount)
	}
	if view.Metrics != nil {
		metrics := *view.Metrics
		fmt.Fprintf(&out, "  metrics: phase=%s state=%s revision=%d gates=%d blockers=%d obligations=%d next_actions=%d\n",
			metrics.CurrentPhase,
			metrics.CurrentState,
			metrics.StateRevision,
			len(metrics.GateStatusCounts),
			metrics.BlockerCount,
			metrics.OpenObligationCount,
			metrics.NextValidActionCount,
		)
	}
	for _, sec := range view.Sections {
		fmt.Fprintf(&out, "  %s: %s", sec.Name, sec.Status)
		if sec.FindingCount > 0 {
			fmt.Fprintf(&out, " (%d findings)", sec.FindingCount)
		}
		fmt.Fprintln(&out)
	}
	if len(view.ArtifactPaths) > 0 {
		fmt.Fprintf(&out, "  artifacts (%d):\n", len(view.ArtifactPaths))
		for _, artifact := range view.ArtifactPaths {
			fmt.Fprintf(&out, "    %s: %s\n", artifact.Name, artifact.Path)
		}
	}
	return out.String()
}

// FormatProductRecoveryStatusText renders the recovery subsection used by product status presenters.
func FormatProductRecoveryStatusText(recoveryStatus *ProductRecoveryStatus) string {
	var out strings.Builder
	writeProductRecoveryStatusText(&out, recoveryStatus)
	return out.String()
}

func writeProductRecoveryStatusText(out *strings.Builder, recoveryStatus *ProductRecoveryStatus) {
	if recoveryStatus == nil {
		return
	}
	fmt.Fprintf(out, "  recovery: %s", recoveryStatus.Status)
	if recoveryStatus.NextValidAction != "" {
		fmt.Fprintf(out, " next=%s", recoveryStatus.NextValidAction)
	}
	if recoveryStatus.CandidateAtomCount > 0 {
		fmt.Fprintf(out, " candidate_atoms=%d", recoveryStatus.CandidateAtomCount)
	}
	if recoveryStatus.CompletedAtomCount > 0 {
		fmt.Fprintf(out, " completed_atoms=%d", recoveryStatus.CompletedAtomCount)
	}
	if recoveryStatus.RemainingAtomCount > 0 && recoveryStatus.RemainingAtomCount != recoveryStatus.CandidateAtomCount {
		fmt.Fprintf(out, " remaining_atoms=%d", recoveryStatus.RemainingAtomCount)
	}
	if recoveryStatus.ReconciliationPath != "" {
		fmt.Fprintf(out, " reconciliation=%s", recoveryStatus.ReconciliationPath)
	}
	if recoveryStatus.PacketPath != "" {
		fmt.Fprintf(out, " packet=%s", recoveryStatus.PacketPath)
	}
	fmt.Fprintln(out)
	if len(recoveryStatus.Blockers) > 0 {
		fmt.Fprintf(out, "  recovery_blockers (%d):\n", len(recoveryStatus.Blockers))
		for _, blocker := range recoveryStatus.Blockers {
			fmt.Fprintf(out, "    [%s] %s\n", blocker.Code, blocker.Message)
		}
	}
}
