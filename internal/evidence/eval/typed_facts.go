package eval

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/textutil"
)

func requiredTypedFactsFindings(records []Record, expected *TypedFactsExpectation) []string {
	if expected == nil {
		return nil
	}
	if findings := validateTypedFactsExpectation(expected); len(findings) > 0 {
		return findings
	}

	var findings []string
	for index, rejection := range expected.ToolRejections {
		count := countMatchingToolRejections(records, rejection)
		if finding := typedFactCountFinding("tool rejection", index, count, rejection.ExactCount); finding != "" {
			findings = append(findings, finding)
		}
	}
	for index, window := range expected.SourceWindows {
		count := countMatchingSourceWindows(records, window)
		if finding := typedFactCountFinding("source window", index, count, window.ExactCount); finding != "" {
			findings = append(findings, finding)
		}
	}
	for index, read := range expected.DiagnosticLogReads {
		count := countMatchingDiagnosticLogReads(records, read)
		if finding := typedFactCountFinding("diagnostic log read", index, count, read.ExactCount); finding != "" {
			findings = append(findings, finding)
		}
	}
	for index, commit := range expected.MutationCommits {
		count := countMatchingMutationCommits(records, commit)
		if finding := typedFactCountFinding("mutation commit", index, count, commit.ExactCount); finding != "" {
			findings = append(findings, finding)
		}
	}
	for index, recovery := range expected.CompletionRecoveries {
		count := countMatchingCompletionRecoveries(records, recovery)
		if finding := typedFactCountFinding("completion recovery", index, count, recovery.ExactCount); finding != "" {
			findings = append(findings, finding)
		}
	}
	for index, execution := range expected.ToolExecutions {
		count := countMatchingToolExecutions(records, execution)
		if execution.ExactCount == nil {
			findings = append(findings, fmt.Sprintf("required typed tool execution %d is missing exact_count", index+1))
			continue
		}
		if count != *execution.ExactCount {
			findings = append(findings, fmt.Sprintf(
				"required typed tool execution %d count = %d, want exactly %d (tool=%q outcome=%q)",
				index+1,
				count,
				*execution.ExactCount,
				strings.TrimSpace(execution.Tool),
				strings.TrimSpace(execution.Outcome),
			))
		}
	}

	return findings
}

func typedFactCountFinding(kind string, index int, count int, exact *int) string {
	if exact == nil {
		if count == 0 {
			return fmt.Sprintf("required typed %s %d was not observed", kind, index+1)
		}
		return ""
	}
	if count == *exact {
		return ""
	}
	return fmt.Sprintf("required typed %s %d count = %d, want exactly %d", kind, index+1, count, *exact)
}

func countMatchingToolRejections(records []Record, expected ToolRejectionFactsExpectation) int {
	count := 0
	for _, record := range uniqueToolResultRecords(records) {
		if !strings.EqualFold(strings.TrimSpace(record.Type), "tool_result") ||
			!strings.EqualFold(strings.TrimSpace(stringField(record.Data, "tool")), strings.TrimSpace(expected.Tool)) ||
			!strings.EqualFold(strings.TrimSpace(stringField(record.Data, "rejection_disposition")), strings.TrimSpace(expected.Disposition)) {
			continue
		}
		if expected.PendingWritePaths != nil && !equalCanonicalPathSets(stringSliceField(record.Data, "rejected_write_paths"), expected.PendingWritePaths) {
			continue
		}
		if expected.PendingSourcePaths != nil && !equalCanonicalPathSets(stringSliceField(record.Data, "pending_source_body_paths"), expected.PendingSourcePaths) {
			continue
		}
		if expected.PendingHistoryPaths != nil && !equalCanonicalPathSets(stringSliceField(record.Data, "pending_history_paths"), expected.PendingHistoryPaths) {
			continue
		}
		count++
	}
	return count
}

func countMatchingSourceWindows(records []Record, expected SourceWindowFactsExpectation) int {
	count := 0
	for _, record := range uniqueToolResultRecords(records) {
		if tool := strings.TrimSpace(expected.Tool); tool != "" && !strings.EqualFold(strings.TrimSpace(stringField(record.Data, "tool")), tool) {
			continue
		}
		for _, window := range objectSliceField(record.Data, "source_windows") {
			if sourceWindowFactsMatch(window, expected) {
				count++
			}
		}
	}
	return count
}

func sourceWindowFactsMatch(actual map[string]any, expected SourceWindowFactsExpectation) bool {
	if strings.TrimSpace(stringField(actual, "canonical_id")) != strings.TrimSpace(expected.CanonicalID) ||
		intField(actual, "start_line") != expected.StartLine ||
		intField(actual, "end_line") != expected.EndLine {
		return false
	}

	epoch := uint64Field(actual, "mutation_epoch")
	if expected.MutationEpoch != nil && epoch != *expected.MutationEpoch {
		return false
	}
	if expected.MinMutationEpoch != nil && epoch < *expected.MinMutationEpoch {
		return false
	}

	revisionDigest := strings.ToLower(strings.TrimSpace(stringField(actual, "revision_sha256")))
	windowDigest := strings.ToLower(strings.TrimSpace(stringField(actual, "window_sha256")))
	if expected.RequireDigests && (!validCanonicalSHA256(revisionDigest) || !validCanonicalSHA256(windowDigest)) {
		return false
	}
	if digest := strings.ToLower(strings.TrimSpace(expected.RevisionSHA256)); digest != "" && revisionDigest != digest {
		return false
	}
	if digest := strings.ToLower(strings.TrimSpace(expected.WindowSHA256)); digest != "" && windowDigest != digest {
		return false
	}
	if expected.RequireDurableBodyAbsent && containsSourceWindowBody(actual) {
		return false
	}

	return true
}

func containsSourceWindowBody(window map[string]any) bool {
	for _, key := range []string{"body", "content", "source_body", "window_body"} {
		if _, ok := window[key]; ok {
			return true
		}
	}
	return false
}

func countMatchingDiagnosticLogReads(records []Record, expected DiagnosticLogReadFactsExpectation) int {
	count := 0
	for _, record := range uniqueToolResultRecords(records) {
		if stringField(record.Data, "tool") != expected.Tool || !orderedEventOutcomeMatches(record, "success") {
			continue
		}
		for _, read := range objectSliceField(record.Data, "diagnostic_log_reads") {
			if diagnosticLogReadFactsMatch(record.Data, read, expected) {
				count++
			}
		}
	}
	return count
}

func diagnosticLogReadFactsMatch(record map[string]any, actual map[string]any, expected DiagnosticLogReadFactsExpectation) bool {
	decision, decisionPresent := actual["policy_decision"].(string)
	if !decisionPresent ||
		stringField(actual, "canonical_id") != expected.CanonicalID ||
		stringField(actual, "policy_subject") != expected.PolicySubject ||
		decision != expected.PolicyDecision ||
		!exactIntegerField(actual, "start_line", expected.StartLine) ||
		!exactIntegerField(actual, "end_line", expected.EndLine) ||
		!exactIntegerField(actual, "total_lines", expected.TotalLines) ||
		expected.Truncated == nil || !exactOptionalBoolField(actual, "truncated", *expected.Truncated) {
		return false
	}
	if expected.RequirePrivateFieldsAbsent && diagnosticLogReadHasPrivateFields(actual) {
		return false
	}
	if expected.RequireSourceWindowAbsent && len(objectSliceField(record, "source_windows")) > 0 {
		return false
	}
	return true
}

func exactOptionalBoolField(data map[string]any, key string, expected bool) bool {
	value, present := data[key]
	if !present {
		return !expected
	}
	actual, ok := value.(bool)
	return ok && actual == expected
}

func exactIntegerField(data map[string]any, key string, expected int) bool {
	value, present := data[key]
	if !present {
		return false
	}
	switch typed := value.(type) {
	case int:
		return typed == expected
	case int64:
		return typed == int64(expected)
	case float64:
		return typed == float64(expected)
	default:
		return false
	}
}

func diagnosticLogReadHasPrivateFields(read map[string]any) bool {
	privateFields := map[string]struct{}{
		"absolute_path":   {},
		"attachment":      {},
		"attachments":     {},
		"body":            {},
		"content":         {},
		"digest":          {},
		"epoch":           {},
		"mutation_epoch":  {},
		"output":          {},
		"revision_sha256": {},
		"root":            {},
		"session_id":      {},
		"sha256":          {},
		"source_body":     {},
		"window_sha256":   {},
	}
	for key := range read {
		if _, private := privateFields[strings.ToLower(strings.TrimSpace(key))]; private {
			return true
		}
	}
	return false
}

func countMatchingMutationCommits(records []Record, expected MutationCommitFactsExpectation) int {
	count := 0
	for _, record := range records {
		if role := strings.TrimSpace(expected.Role); role != "" && !strings.EqualFold(strings.TrimSpace(stringField(record.Data, "role")), role) {
			continue
		}
		content := recordJSONContent(record)
		for _, receipt := range objectSliceField(content, "commit_receipts") {
			if mutationCommitFactsMatch(receipt, expected) {
				count++
			}
		}
	}
	return count
}

func mutationCommitFactsMatch(actual map[string]any, expected MutationCommitFactsExpectation) bool {
	if expected.Effects != nil && !equalObjectSets(
		objectSliceField(actual, "effects"),
		expectedMutationEffects(expected.Effects),
		[]string{"canonical_id", "kind", "role"},
	) {
		return false
	}
	if expected.FailedTargets != nil && !equalObjectSets(
		objectSliceField(actual, "failed_targets"),
		expectedMutationTargets(expected.FailedTargets),
		[]string{"canonical_id", "role", "preimage_class"},
	) {
		return false
	}
	if expected.UnattemptedTargets != nil && !equalObjectSets(
		objectSliceField(actual, "unattempted_targets"),
		expectedMutationTargets(expected.UnattemptedTargets),
		[]string{"canonical_id", "role", "preimage_class"},
	) {
		return false
	}
	return true
}

func expectedMutationEffects(expected []MutationCommitEffectExpectation) []map[string]any {
	items := make([]map[string]any, 0, len(expected))
	for _, effect := range expected {
		items = append(items, map[string]any{
			"canonical_id": strings.TrimSpace(effect.CanonicalID),
			"kind":         strings.TrimSpace(effect.Kind),
			"role":         strings.TrimSpace(effect.Role),
		})
	}
	return items
}

func expectedMutationTargets(expected []MutationTargetFactsExpectation) []map[string]any {
	items := make([]map[string]any, 0, len(expected))
	for _, target := range expected {
		items = append(items, map[string]any{
			"canonical_id":   strings.TrimSpace(target.CanonicalID),
			"role":           strings.TrimSpace(target.Role),
			"preimage_class": strings.TrimSpace(target.PreimageClass),
		})
	}
	return items
}

func equalObjectSets(actual []map[string]any, expected []map[string]any, keys []string) bool {
	actualKeys := make([]string, 0, len(actual))
	for _, item := range actual {
		actualKeys = append(actualKeys, objectIdentity(item, keys))
	}
	expectedKeys := make([]string, 0, len(expected))
	for _, item := range expected {
		expectedKeys = append(expectedKeys, objectIdentity(item, keys))
	}
	slices.Sort(actualKeys)
	slices.Sort(expectedKeys)
	return slices.Equal(actualKeys, expectedKeys)
}

func objectIdentity(value map[string]any, keys []string) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, strings.TrimSpace(stringField(value, key)))
	}
	return strings.Join(parts, "\x00")
}

func countMatchingCompletionRecoveries(records []Record, expected CompletionRecoveryFactsExpectation) int {
	count := 0
	for _, record := range records {
		if role := strings.TrimSpace(expected.Role); role != "" && !strings.EqualFold(strings.TrimSpace(stringField(record.Data, "role")), role) {
			continue
		}
		content := recordJSONContent(record)
		for _, recovery := range completionRecoveryFacts(content) {
			if strings.EqualFold(strings.TrimSpace(stringField(recovery, "class")), strings.TrimSpace(expected.Class)) &&
				expected.AttemptsSinceProgress != nil &&
				intField(recovery, "attempts_since_progress") == *expected.AttemptsSinceProgress {
				count++
			}
		}
	}
	return count
}

func completionRecoveryFacts(content map[string]any) []map[string]any {
	if content == nil {
		return nil
	}
	if recovery, ok := objectField(content, "completion_recovery"); ok {
		if active, ok := objectField(recovery, "active_violation"); ok {
			return []map[string]any{active}
		}
		return []map[string]any{recovery}
	}
	if active, ok := objectField(content, "active_violation"); ok {
		return []map[string]any{active}
	}
	return nil
}

func countMatchingToolExecutions(records []Record, expected ToolExecutionCountExpectation) int {
	count := 0
	for _, record := range uniqueToolResultRecords(records) {
		if !strings.EqualFold(strings.TrimSpace(record.Type), "tool_result") ||
			!strings.EqualFold(strings.TrimSpace(stringField(record.Data, "tool")), strings.TrimSpace(expected.Tool)) {
			continue
		}
		if outcome := strings.TrimSpace(expected.Outcome); outcome != "" && !orderedEventOutcomeMatches(record, outcome) {
			continue
		}
		count++
	}
	return count
}

// uniqueToolResultRecords collapses the mirrored conversation and headless
// projections of one logical tool result. Both projections retain the same
// runner-owned step and tool identity; results without that identity remain
// distinct so repeated synthetic or legacy events are not guessed away.
func uniqueToolResultRecords(records []Record) []Record {
	unique := make([]Record, 0, len(records))
	seen := make(map[string]struct{})
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSpace(record.Type), "tool_result") {
			continue
		}
		if key := toolAttemptEventKey(record.Data); key != "" {
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
		}
		unique = append(unique, record)
	}
	return unique
}

func recordJSONContent(record Record) map[string]any {
	content := strings.TrimSpace(stringField(record.Data, "content"))
	if content == "" {
		return record.Data
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		return record.Data
	}
	return decoded
}

func objectField(data map[string]any, key string) (map[string]any, bool) {
	value, ok := data[key].(map[string]any)
	return value, ok
}

func objectSliceField(data map[string]any, key string) []map[string]any {
	if data == nil {
		return nil
	}
	switch typed := data[key].(type) {
	case []map[string]any:
		return typed
	case []any:
		items := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(map[string]any); ok {
				items = append(items, value)
			}
		}
		return items
	default:
		return nil
	}
}

func equalCanonicalPathSets(actual []string, expected []string) bool {
	actual = normalizedCanonicalPathSet(actual)
	expected = normalizedCanonicalPathSet(expected)
	return slices.Equal(actual, expected)
}

func normalizedCanonicalPathSet(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		path := pathparse.NormalizeWorkspaceRelativePath(candidate)
		if path != "" && path == candidate {
			normalized = append(normalized, path)
		}
	}
	normalized = textutil.UniqueNonEmpty(normalized)
	slices.Sort(normalized)
	return normalized
}

func uint64Field(data map[string]any, key string) uint64 {
	if data == nil {
		return 0
	}
	switch typed := data[key].(type) {
	case uint64:
		return typed
	case uint:
		return uint64(typed)
	case int:
		if typed >= 0 {
			return uint64(typed)
		}
	case int64:
		if typed >= 0 {
			return uint64(typed)
		}
	case float64:
		if typed >= 0 {
			return uint64(typed)
		}
	}
	return 0
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validCanonicalSHA256(value string) bool {
	const prefix = "sha256:"
	return strings.HasPrefix(value, prefix) && validSHA256(strings.TrimPrefix(value, prefix))
}

func validateTypedFactsExpectation(expected *TypedFactsExpectation) []string {
	if expected == nil {
		return nil
	}
	if len(expected.ToolRejections) == 0 &&
		len(expected.SourceWindows) == 0 &&
		len(expected.DiagnosticLogReads) == 0 &&
		len(expected.MutationCommits) == 0 &&
		len(expected.CompletionRecoveries) == 0 &&
		len(expected.ToolExecutions) == 0 {
		return []string{"require_typed_facts must declare at least one typed expectation"}
	}

	var findings []string
	for index, rejection := range expected.ToolRejections {
		field := fmt.Sprintf("require_typed_facts.tool_rejections[%d]", index)
		if strings.TrimSpace(rejection.Tool) == "" {
			findings = append(findings, field+" missing tool")
		}
		if !typedToolRejectionDisposition(rejection.Disposition) {
			findings = append(findings, field+" has invalid disposition")
		}
		findings = append(findings, validateExpectationPaths(field+".pending_write_paths", rejection.PendingWritePaths)...)
		findings = append(findings, validateExpectationPaths(field+".pending_source_paths", rejection.PendingSourcePaths)...)
		findings = append(findings, validateExpectationPaths(field+".pending_history_paths", rejection.PendingHistoryPaths)...)
		findings = append(findings, validateExactCount(field, rejection.ExactCount, false)...)
	}

	for index, window := range expected.SourceWindows {
		field := fmt.Sprintf("require_typed_facts.source_windows[%d]", index)
		findings = append(findings, validateExpectationPaths(field+".canonical_id", []string{window.CanonicalID})...)
		if window.StartLine <= 0 || window.EndLine < window.StartLine {
			findings = append(findings, field+" requires a positive, non-reversed line range")
		}
		if (window.MutationEpoch == nil) == (window.MinMutationEpoch == nil) {
			findings = append(findings, field+" must set exactly one of mutation_epoch or min_mutation_epoch")
		} else if window.MutationEpoch != nil && *window.MutationEpoch == 0 {
			findings = append(findings, field+" mutation_epoch must be positive")
		} else if window.MinMutationEpoch != nil && *window.MinMutationEpoch == 0 {
			findings = append(findings, field+" min_mutation_epoch must be positive")
		}
		if !window.RequireDigests && (strings.TrimSpace(window.RevisionSHA256) == "" || strings.TrimSpace(window.WindowSHA256) == "") {
			findings = append(findings, field+" must require digests or provide both exact digests")
		}
		for name, digest := range map[string]string{
			"revision_sha256": window.RevisionSHA256,
			"window_sha256":   window.WindowSHA256,
		} {
			if digest = strings.TrimSpace(digest); digest != "" && !validCanonicalSHA256(digest) {
				findings = append(findings, field+"."+name+" must be a sha256:-prefixed SHA-256 digest")
			}
		}
		if !window.RequireDurableBodyAbsent {
			findings = append(findings, field+" must explicitly require durable body absence")
		}
		findings = append(findings, validateExactCount(field, window.ExactCount, false)...)
	}

	for index, read := range expected.DiagnosticLogReads {
		field := fmt.Sprintf("require_typed_facts.diagnostic_log_reads[%d]", index)
		if strings.TrimSpace(read.Tool) == "" {
			findings = append(findings, field+" missing tool")
		} else if read.Tool != "read_file" {
			findings = append(findings, field+" tool must be read_file")
		}
		findings = append(findings, validateExpectationPaths(field+".canonical_id", []string{read.CanonicalID})...)
		findings = append(findings, validateExpectationPaths(field+".policy_subject", []string{read.PolicySubject})...)
		if read.PolicyDecision != strings.TrimSpace(read.PolicyDecision) {
			findings = append(findings, field+" has invalid policy_decision")
		} else {
			switch toolpolicy.Decision(read.PolicyDecision) {
			case toolpolicy.None, toolpolicy.Allow, toolpolicy.Ask, toolpolicy.Deny:
			default:
				findings = append(findings, field+" has invalid policy_decision")
			}
		}
		if !validDiagnosticLogReadRange(read.StartLine, read.EndLine, read.TotalLines) {
			findings = append(findings, field+" requires either empty 0/0/0 lines or a positive, non-reversed range within total_lines")
		}
		if read.Truncated == nil {
			findings = append(findings, field+" must explicitly assert truncated")
		} else if read.TotalLines == 0 && *read.Truncated {
			findings = append(findings, field+" cannot mark an empty log truncated")
		}
		if !read.RequirePrivateFieldsAbsent {
			findings = append(findings, field+" must explicitly require private-field absence")
		}
		if !read.RequireSourceWindowAbsent {
			findings = append(findings, field+" must explicitly require matching source-window absence")
		}
		findings = append(findings, validateExactCount(field, read.ExactCount, false)...)
	}

	for index, commit := range expected.MutationCommits {
		field := fmt.Sprintf("require_typed_facts.mutation_commits[%d]", index)
		if commit.Effects == nil && commit.FailedTargets == nil && commit.UnattemptedTargets == nil {
			findings = append(findings, field+" must assert effects, failed_targets, or unattempted_targets")
		}
		for itemIndex, effect := range commit.Effects {
			itemField := fmt.Sprintf("%s.effects[%d]", field, itemIndex)
			findings = append(findings, validateExpectationPaths(itemField+".canonical_id", []string{effect.CanonicalID})...)
			if !knownMutationCommitKind(effect.Kind) {
				findings = append(findings, itemField+" has invalid kind")
			}
			if !knownMutationTargetRole(effect.Role) {
				findings = append(findings, itemField+" has invalid role")
			}
		}
		for itemIndex, target := range commit.FailedTargets {
			findings = append(findings, validateMutationTargetExpectation(fmt.Sprintf("%s.failed_targets[%d]", field, itemIndex), target)...)
		}
		for itemIndex, target := range commit.UnattemptedTargets {
			findings = append(findings, validateMutationTargetExpectation(fmt.Sprintf("%s.unattempted_targets[%d]", field, itemIndex), target)...)
		}
		findings = append(findings, validateExactCount(field, commit.ExactCount, false)...)
	}

	for index, recovery := range expected.CompletionRecoveries {
		field := fmt.Sprintf("require_typed_facts.completion_recoveries[%d]", index)
		if strings.TrimSpace(recovery.Class) == "" {
			findings = append(findings, field+" missing class")
		}
		if recovery.AttemptsSinceProgress == nil || *recovery.AttemptsSinceProgress < 0 {
			findings = append(findings, field+" attempts_since_progress must be non-negative")
		}
		findings = append(findings, validateExactCount(field, recovery.ExactCount, false)...)
	}

	for index, execution := range expected.ToolExecutions {
		field := fmt.Sprintf("require_typed_facts.tool_executions[%d]", index)
		if strings.TrimSpace(execution.Tool) == "" {
			findings = append(findings, field+" missing tool")
		}
		if outcome := strings.TrimSpace(execution.Outcome); outcome != "" {
			switch normalizedOrderedEventOutcome(outcome) {
			case "requested", "success", "rejected", "failed":
			default:
				findings = append(findings, field+" has invalid outcome")
			}
		}
		findings = append(findings, validateExactCount(field, execution.ExactCount, true)...)
	}

	return findings
}

func validDiagnosticLogReadRange(startLine int, endLine int, totalLines int) bool {
	if totalLines == 0 {
		return startLine == 0 && endLine == 0
	}
	return startLine > 0 && endLine >= startLine && totalLines >= endLine
}

func validateExpectationPaths(field string, paths []string) []string {
	var findings []string
	seen := make(map[string]struct{}, len(paths))
	for index, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		normalized := pathparse.NormalizeWorkspaceRelativePath(candidate)
		if normalized == "" || normalized != candidate {
			findings = append(findings, fmt.Sprintf("%s[%d] must be a canonical workspace-relative path", field, index))
			continue
		}
		if _, ok := seen[normalized]; ok {
			findings = append(findings, fmt.Sprintf("%s[%d] duplicates path %q", field, index, normalized))
			continue
		}
		seen[normalized] = struct{}{}
	}
	return findings
}

func validateExactCount(field string, count *int, required bool) []string {
	if count == nil {
		if required {
			return []string{field + " missing exact_count"}
		}
		return nil
	}
	if *count < 0 {
		return []string{field + " exact_count must be non-negative"}
	}
	return nil
}

func validateMutationTargetExpectation(field string, target MutationTargetFactsExpectation) []string {
	findings := validateExpectationPaths(field+".canonical_id", []string{target.CanonicalID})
	if !knownMutationTargetRole(target.Role) {
		findings = append(findings, field+" has invalid role")
	}
	switch strings.TrimSpace(target.PreimageClass) {
	case "required", "optional", "absent", "directory":
	default:
		findings = append(findings, field+" has invalid preimage_class")
	}
	return findings
}

func knownMutationCommitKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "created_directory", "replaced_file", "appended_file", "removed_file":
		return true
	default:
		return false
	}
}

func knownMutationTargetRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "source", "destination", "file", "directory":
		return true
	default:
		return false
	}
}
