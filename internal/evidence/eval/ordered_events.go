package eval

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/platform/textutil"
)

func requiredOrderedEventsFinding(records []Record, expectations []OrderedEventExpectation) string {
	nextRecord := 0
	for i, expected := range expectations {
		if !orderedEventExpectationHasCriteria(expected) {
			return fmt.Sprintf("required ordered event %d has no match criteria", i+1)
		}

		matched := false
		for nextRecord < len(records) {
			record := records[nextRecord]
			nextRecord++

			if orderedEventMatches(record, expected) {
				matched = true
				break
			}
		}

		if !matched {
			return fmt.Sprintf(
				"required ordered event %d was not observed after the prior match: %s",
				i+1,
				describeOrderedEventExpectation(expected),
			)
		}
	}

	return ""
}

func orderedEventExpectationHasCriteria(expected OrderedEventExpectation) bool {
	if strings.TrimSpace(expected.RecordType) != "" ||
		strings.TrimSpace(expected.Role) != "" ||
		strings.TrimSpace(expected.Tool) != "" ||
		strings.TrimSpace(expected.Path) != "" ||
		strings.TrimSpace(expected.Outcome) != "" {
		return true
	}

	for _, fragment := range expected.Contains {
		if strings.TrimSpace(fragment) != "" {
			return true
		}
	}

	return false
}

func orderedEventMatches(record Record, expected OrderedEventExpectation) bool {
	if recordType := strings.TrimSpace(expected.RecordType); recordType != "" && !strings.EqualFold(strings.TrimSpace(record.Type), recordType) {
		return false
	}

	if role := strings.TrimSpace(expected.Role); role != "" && !strings.EqualFold(strings.TrimSpace(stringField(record.Data, "role")), role) {
		return false
	}

	if tool := strings.TrimSpace(expected.Tool); tool != "" && !strings.EqualFold(strings.TrimSpace(stringField(record.Data, "tool")), tool) {
		return false
	}

	if expectedPath := strings.TrimSpace(expected.Path); expectedPath != "" && !orderedEventPathMatches(record, expectedPath) {
		return false
	}

	if outcome := strings.TrimSpace(expected.Outcome); outcome != "" && !orderedEventOutcomeMatches(record, outcome) {
		return false
	}

	corpus := strings.ToLower(orderedEventText(record))
	for _, fragment := range expected.Contains {
		fragment = strings.ToLower(strings.TrimSpace(fragment))
		if fragment != "" && !strings.Contains(corpus, fragment) {
			return false
		}
	}

	return true
}

func orderedEventPathMatches(record Record, expectedPath string) bool {
	expectedPath = normalizedOrderedEventPath(expectedPath)
	if expectedPath == "" {
		return false
	}

	for _, candidate := range orderedEventPaths(record) {
		candidate = normalizedOrderedEventPath(candidate)
		if strings.EqualFold(candidate, expectedPath) ||
			strings.HasSuffix(strings.ToLower(candidate), "/"+strings.ToLower(expectedPath)) {
			return true
		}
	}

	return false
}

func orderedEventPaths(record Record) []string {
	paths := []string{
		stringField(record.Data, "path"),
		stringField(record.Data, "target_path"),
		stringField(record.Data, "file_path"),
		pathFromRawToolRequest(stringField(record.Data, "raw")),
	}

	for _, key := range []string{"paths", "changed_paths", "rejected_write_paths"} {
		paths = append(paths, stringSliceField(record.Data, key)...)
	}

	if _, actionPath, ok := strings.Cut(stringField(record.Data, "action"), ":"); ok {
		paths = append(paths, actionPath)
	}

	for _, key := range []string{"raw", "summary", "error", "content", "reason"} {
		paths = append(paths, evidencePathsFromText(stringField(record.Data, key))...)
	}

	return textutil.UniqueNonEmpty(paths)
}

func normalizedOrderedEventPath(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "./")
	return strings.TrimSuffix(value, "/")
}

func orderedEventOutcomeMatches(record Record, expectedOutcome string) bool {
	expectedOutcome = normalizedOrderedEventOutcome(expectedOutcome)
	for _, outcome := range orderedEventOutcomes(record) {
		if normalizedOrderedEventOutcome(outcome) == expectedOutcome {
			return true
		}
	}

	return false
}

func orderedEventOutcomes(record Record) []string {
	outcomes := make([]string, 0, 6)
	for _, key := range []string{"outcome", "status", "state", "result", "decision"} {
		if value := strings.TrimSpace(stringField(record.Data, key)); value != "" {
			outcomes = append(outcomes, value)
		}
	}

	switch strings.ToLower(strings.TrimSpace(record.Type)) {
	case "tool_request":
		outcomes = append(outcomes, "requested")
	case "tool_result":
		switch {
		case boolField(record.Data, "runner_rejected") || toolResultCountsAsRejectedAttempt(record.Data):
			outcomes = append(outcomes, "rejected")
		case strings.TrimSpace(stringField(record.Data, "error")) != "":
			outcomes = append(outcomes, "failed")
		default:
			outcomes = append(outcomes, "success")
		}
	}

	return textutil.UniqueNonEmpty(outcomes)
}

func normalizedOrderedEventOutcome(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ok", "succeeded", "successful":
		return "success"
	case "error", "failure":
		return "failed"
	case "deny", "denied", "reject":
		return "rejected"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func orderedEventText(record Record) string {
	values := []string{record.Type}
	appendOrderedEventTextValues(&values, record.Data)
	return strings.Join(values, "\n")
}

func appendOrderedEventTextValues(values *[]string, value any) {
	switch typed := value.(type) {
	case string:
		*values = append(*values, typed)
	case []string:
		*values = append(*values, typed...)
	case []any:
		for _, item := range typed {
			appendOrderedEventTextValues(values, item)
		}
	case map[string]any:
		for _, item := range typed {
			appendOrderedEventTextValues(values, item)
		}
	}
}

func describeOrderedEventExpectation(expected OrderedEventExpectation) string {
	criteria := make([]string, 0, 6)
	if value := strings.TrimSpace(expected.RecordType); value != "" {
		criteria = append(criteria, fmt.Sprintf("type=%q", value))
	}
	if value := strings.TrimSpace(expected.Role); value != "" {
		criteria = append(criteria, fmt.Sprintf("role=%q", value))
	}
	if value := strings.TrimSpace(expected.Tool); value != "" {
		criteria = append(criteria, fmt.Sprintf("tool=%q", value))
	}
	if value := strings.TrimSpace(expected.Path); value != "" {
		criteria = append(criteria, fmt.Sprintf("path=%q", value))
	}
	if value := strings.TrimSpace(expected.Outcome); value != "" {
		criteria = append(criteria, fmt.Sprintf("outcome=%q", value))
	}
	if len(expected.Contains) > 0 {
		criteria = append(criteria, fmt.Sprintf("contains=%q", expected.Contains))
	}

	return strings.Join(criteria, " ")
}
