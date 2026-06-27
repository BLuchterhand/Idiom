package workerruntime

import (
	"slices"
	"strings"

	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
)

// RenderActiveProtocolCard renders the model-visible active protocol card from
// the compiled protocol projection that owns the active contract authority.
func RenderActiveProtocolCard(protocol contractcompile.ModelProtocolProjection) string {
	if strings.TrimSpace(protocol.ProtocolHash) == "" {
		return ""
	}

	terminalOutcomes := protocolCardDisplayOrder(protocol.TerminalResponseShapes)
	if protocolCardContains(protocol.InvalidActionClasses, string(autonomy.ActionClassSubmitPlan)) {
		terminalOutcomes = protocolCardFilter(terminalOutcomes, func(value string) bool {
			return strings.TrimSpace(value) != string(autonomy.ActionClassSubmitPlan)
		})
	}

	invalid := protocolCardDisplayOrder(protocol.InvalidActionClasses)

	var b strings.Builder

	b.WriteString("[active contract protocol]\n")
	b.WriteString("Active contract: ")
	b.WriteString(strings.TrimSpace(protocol.ContractID))

	if strings.TrimSpace(protocol.ProtocolHash) != "" {
		b.WriteString(" / protocol_hash=")
		b.WriteString(strings.TrimSpace(protocol.ProtocolHash))
	}

	if strings.TrimSpace(protocol.PhaseID) != "" {
		b.WriteString("\nPhase: ")
		b.WriteString(strings.TrimSpace(protocol.PhaseID))
	}

	b.WriteString("\nVisible tools: ")
	b.WriteString(protocolCardList(protocolCardDisplayOrder(protocol.VisibleTools)))
	b.WriteString("\nCallable tools now: ")
	b.WriteString(protocolCardList(protocolCardDisplayOrder(protocol.CallableTools)))

	if len(terminalOutcomes) > 0 {
		b.WriteString("\nValid terminal outcomes: ")
		b.WriteString(strings.Join(terminalOutcomes, "; "))
	}

	if len(invalid) > 0 {
		b.WriteString("\nInvalid now: ")
		b.WriteString(strings.Join(invalid, ", "))
	}

	if len(protocol.RequiredEditCoverage) > 0 || len(protocol.SourceBodyObligations) > 0 || len(protocol.PathRoleSummary) > 0 {
		b.WriteString("\nPath roles: required_edit_paths=")
		b.WriteString(protocolCardList(protocol.RequiredEditCoverage))
		b.WriteString("; source_body_obligations=")
		b.WriteString(protocolCardList(protocol.SourceBodyObligations))
		b.WriteString("; roles=")
		b.WriteString(protocolCardList(protocol.PathRoleSummary))
	}

	if verification := strings.Join(protocol.VerificationObligations, "; "); verification != "" {
		b.WriteString("\nVerification: ")
		b.WriteString(verification)
	}

	if alternatives := strings.Join(protocolCardDisplayOrder(protocol.SafeAlternatives), "; "); alternatives != "" {
		b.WriteString("\nSafe alternatives: ")
		b.WriteString(alternatives)
	}

	return b.String()
}

func protocolCardList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}

	return "[" + strings.Join(values, ", ") + "]"
}

func protocolCardDisplayOrder(values []string) []string {
	out := protocolCardUniqueStrings(values)
	order := map[string]int{
		string(autonomy.ActionClassPerEditWrite):    10,
		string(autonomy.ActionClassSearch):          20,
		string(autonomy.ActionClassSubmitPlan):      30,
		"Decision: source-satisfied":                40,
		"<<QUESTION>>:":                             50,
		string(autonomy.ActionClassBlocker):         60,
		string(autonomy.ActionClassReviewPass):      70,
		"Decision: pass":                            80,
		string(autonomy.ActionClassFinalAnswer):     90,
		string(autonomy.ActionClassProseFinal):      100,
		string(autonomy.ActionClassGenericProbe):    110,
		string(autonomy.ActionClassRead):            120,
		string(autonomy.ActionClassVerify):          130,
		string(autonomy.ActionClassSourceSatisfied): 140,
	}

	slices.SortFunc(out, func(a, b string) int {
		ao, aok := order[a]
		bo, bok := order[b]

		if aok && bok && ao != bo {
			return ao - bo
		}

		if aok != bok {
			if aok {
				return -1
			}

			return 1
		}

		return strings.Compare(a, b)
	})

	return out
}

func protocolCardUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	return out
}

func protocolCardContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}

	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}

	return false
}

func protocolCardFilter(values []string, keep func(string) bool) []string {
	var out []string

	for _, value := range values {
		if keep == nil || keep(value) {
			out = append(out, value)
		}
	}

	return out
}
