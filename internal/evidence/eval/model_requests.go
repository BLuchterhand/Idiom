package eval

import (
	"fmt"
	"slices"
	"strings"

	"idiom/internal/platform/textutil"
)

func requiredModelRequestFindings(records []Record, expectations []ModelRequestExpectation) []string {
	if len(expectations) == 0 {
		return nil
	}

	facts := scriptedModelRequestFacts(records)
	if len(facts) == 0 {
		return []string{"required scripted-model request facts were not observed"}
	}

	var findings []string
	for _, expected := range expectations {
		fact, ok := facts[expected.Index]
		if !ok {
			findings = append(findings, fmt.Sprintf("required scripted-model request index %d was not observed", expected.Index))
			continue
		}

		if kind := strings.TrimSpace(expected.Kind); kind != "" && !strings.EqualFold(strings.TrimSpace(stringField(fact, "kind")), kind) {
			findings = append(findings, fmt.Sprintf("scripted-model request %d kind = %q, want %q", expected.Index, stringField(fact, "kind"), kind))
		}
		if transport := strings.TrimSpace(expected.Transport); transport != "" && !strings.EqualFold(strings.TrimSpace(stringField(fact, "transport")), transport) {
			findings = append(findings, fmt.Sprintf("scripted-model request %d transport = %q, want %q", expected.Index, stringField(fact, "transport"), transport))
		}

		tools := textutil.UniqueNonEmpty(stringSliceField(fact, "effective_tool_names"))
		if len(tools) == 0 {
			tools = textutil.UniqueNonEmpty(stringSliceField(fact, "exposed_tool_names"))
		}
		for _, required := range textutil.UniqueNonEmpty(expected.RequireTools) {
			if !slices.Contains(tools, required) {
				findings = append(findings, fmt.Sprintf("scripted-model request %d did not expose required tool %q (got %v)", expected.Index, required, tools))
			}
		}
		for _, forbidden := range textutil.UniqueNonEmpty(expected.ForbidTools) {
			if slices.Contains(tools, forbidden) {
				findings = append(findings, fmt.Sprintf("scripted-model request %d exposed forbidden tool %q", expected.Index, forbidden))
			}
		}
	}

	return findings
}

func scriptedModelRequestFacts(records []Record) map[int]map[string]any {
	facts := make(map[int]map[string]any)
	for _, record := range records {
		if strings.TrimSpace(record.Type) != "idiom.scripted_model.requests" {
			continue
		}

		items, ok := record.Data["request_facts"].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			fact, ok := item.(map[string]any)
			if !ok {
				continue
			}
			facts[intField(fact, "index")] = fact
		}
	}

	return facts
}
