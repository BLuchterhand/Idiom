package transcript

import (
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/toolfacts"
)

// PrivacySafeToolResultFactFields projects the typed rejection contract into the
// privacy-safe event vocabulary. Model messages, tool arguments, result
// content, and absolute paths are intentionally outside this projection.
func PrivacySafeToolResultFactFields(facts *toolfacts.ToolResultFacts) map[string]any {
	if facts == nil {
		return nil
	}

	fields := make(map[string]any, 7)
	if disposition := knownToolRejectionDisposition(facts.RejectionDisposition); disposition != "" {
		fields["rejection_disposition"] = disposition
	}
	addCanonicalRelativePaths(fields, "rejected_write_paths", facts.RejectedWritePaths)
	addCanonicalRelativePaths(fields, "deferred_plan_paths", facts.DeferredPlanPaths)
	addCanonicalRelativePaths(fields, "pending_source_body_paths", facts.PendingSourceBodyPaths)
	addCanonicalRelativePaths(fields, "pending_history_paths", facts.PendingHistoryPaths)
	if windows := toolfacts.NormalizeSourceWindows(facts.SourceWindows); len(windows) > 0 {
		fields["source_windows"] = windows
	}
	if reads := toolfacts.NormalizeDiagnosticLogReads(facts.DiagnosticLogReads); len(reads) > 0 {
		fields["diagnostic_log_reads"] = reads
	}

	if len(fields) == 0 {
		return nil
	}

	return fields
}

func knownToolRejectionDisposition(disposition toolfacts.ToolRejectionDisposition) string {
	switch disposition {
	case toolfacts.ToolRejectionNotRetained,
		toolfacts.ToolRejectionDeferredByPhase,
		toolfacts.ToolRejectionDeferredByPrerequisite,
		toolfacts.ToolRejectionInvalidRequest,
		toolfacts.ToolRejectionPolicyDenied:
		return string(disposition)
	default:
		return ""
	}
}

func addCanonicalRelativePaths(fields map[string]any, key string, paths []string) {
	if len(paths) == 0 {
		return
	}

	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		path := pathparse.NormalizeWorkspaceRelativePath(candidate)
		if path == "" || path != candidate {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	if len(normalized) > 0 {
		fields[key] = normalized
	}
}
