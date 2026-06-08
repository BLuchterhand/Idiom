package productworkflow

import (
	"fmt"
	"sort"
	"strings"

	"idiom/internal/platform/util"
)

func PlanTDDObligations(prd PRDDocument, adrs []ParsedADR, ledger TDDLedger, adrPathByID map[string]string) TDDObligationSynthesisPlan {
	plan := TDDObligationSynthesisPlan{
		EffortID:     ledger.EffortID,
		TaskID:       ledger.TaskID,
		CoverageTier: strings.TrimSpace(ledger.CoverageTier),
		Obligations:  []TDDObligationPlanItem{},
		Findings:     []TDDObligationPlanFinding{},
	}
	if plan.CoverageTier == "" {
		plan.CoverageTier = "unit"
	}
	obligationByID := map[string]TDDObligation{}
	for _, obligation := range ledger.Obligations {
		id := strings.TrimSpace(obligation.ID)
		if id != "" {
			obligationByID[id] = obligation
		}
	}
	if !isAcceptedArtifact(prd.FrontMatter.Status) {
		plan.Findings = append(plan.Findings, TDDObligationPlanFinding{
			Kind:     "unaccepted_prd",
			Message:  fmt.Sprintf("prd status %q is not accepted; obligation automation unavailable", prd.FrontMatter.Status),
			File:     "prd.md",
			Severity: "error",
		})
	}
	acceptedADRs := make([]ParsedADR, 0, len(adrs))
	for _, adr := range adrs {
		if isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			acceptedADRs = append(acceptedADRs, adr)
		}
	}
	if len(adrs) > 0 && len(acceptedADRs) == 0 {
		plan.Findings = append(plan.Findings, TDDObligationPlanFinding{
			Kind:     "no_accepted_adrs",
			Message:  "no accepted ADRs available; obligation automation unavailable",
			File:     "adrs/index.json",
			Severity: "error",
		})
	}

	adrByReq := map[string][]ParsedADR{}
	for _, adr := range acceptedADRs {
		for _, reqRef := range adr.Doc.FrontMatter.PRDRefs {
			ref := strings.TrimSpace(reqRef)
			if ref == "" {
				continue
			}
			adrByReq[ref] = append(adrByReq[ref], adr)
		}
	}

	for _, req := range prd.Requirements {
		if strings.TrimSpace(req.ID) == "" {
			continue
		}
		candidates := adrByReq[req.ID]
		if len(candidates) == 0 {
			plan.Obligations = append(plan.Obligations, TDDObligationPlanItem{
				PRDRequirementRef:      req.ID,
				ExistingObligationRefs: stableUniqueStrings(req.TDDObligations),
				EvidenceRefs:           stableUniqueStrings(req.EvidenceRefs),
				CoverageTier:           plan.CoverageTier,
				SuggestedType:          "acceptance",
				OwnerPath:              "",
				Command:                "",
				BlockerReason:          "no accepted ADR references this requirement",
			})
			plan.Findings = append(plan.Findings, TDDObligationPlanFinding{
				Kind:     "missing_accepted_adr_for_prd_requirement",
				Message:  fmt.Sprintf("PRD requirement %q is not referenced by an accepted ADR; obligation automation unavailable", req.ID),
				File:     "prd.md",
				Severity: "error",
			})
			continue
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Doc.FrontMatter.ADRID < candidates[j].Doc.FrontMatter.ADRID
		})
		for _, adr := range candidates {
			coverageTier := strings.TrimSpace(adr.Doc.FrontMatter.CoverageTier)
			if coverageTier == "" {
				coverageTier = plan.CoverageTier
			}
			if coverageTier == "" {
				coverageTier = "unit"
			}
			ownerPath := adrPathByID[adr.Doc.FrontMatter.ADRID]
			if ownerPath == "" {
				ownerPath = adr.RelPath
			}
			existingRefs := stableUniqueStrings(append(req.TDDObligations, adr.Doc.FrontMatter.TDDObligations...))
			suggestedType, command := suggestedTDDPlanForTier(coverageTier)
			if isContractTier(coverageTier) && len(existingRefs) == 0 {
				message := fmt.Sprintf("PRD requirement %q and ADR %q do not name any tdd_obligations; contract-tier obligations must not be silently produced without TDD obligation references", req.ID, adr.Doc.FrontMatter.ADRID)
				plan.Obligations = append(plan.Obligations, TDDObligationPlanItem{
					PRDRequirementRef:      req.ID,
					ADRRef:                 adr.Doc.FrontMatter.ADRID,
					ExistingObligationRefs: existingRefs,
					EvidenceRefs:           stableUniqueStrings(append(req.EvidenceRefs, adr.Doc.FrontMatter.EvidenceRefs...)),
					CoverageTier:           coverageTier,
					SuggestedType:          suggestedType,
					OwnerPath:              ownerPath,
					Command:                "",
					BlockerReason:          message,
				})
				plan.Findings = append(plan.Findings, TDDObligationPlanFinding{
					Kind:     "missing_tdd_obligation_reference",
					Message:  message,
					File:     "prd.md",
					Severity: "error",
				})
				continue
			}
			if existingType, existingCommand := existingTDDPlan(existingRefs, obligationByID); existingCommand != "" {
				if existingType != "" {
					suggestedType = existingType
				}
				command = existingCommand
			}
			plan.Obligations = append(plan.Obligations, TDDObligationPlanItem{
				PRDRequirementRef:      req.ID,
				ADRRef:                 adr.Doc.FrontMatter.ADRID,
				ExistingObligationRefs: existingRefs,
				EvidenceRefs:           stableUniqueStrings(append(req.EvidenceRefs, adr.Doc.FrontMatter.EvidenceRefs...)),
				CoverageTier:           coverageTier,
				SuggestedType:          suggestedType,
				OwnerPath:              ownerPath,
				Command:                command,
			})
		}
	}
	sort.Slice(plan.Obligations, func(i, j int) bool {
		if plan.Obligations[i].PRDRequirementRef == plan.Obligations[j].PRDRequirementRef {
			return plan.Obligations[i].ADRRef < plan.Obligations[j].ADRRef
		}
		return plan.Obligations[i].PRDRequirementRef < plan.Obligations[j].PRDRequirementRef
	})
	sort.Slice(plan.Findings, func(i, j int) bool {
		if plan.Findings[i].Severity == plan.Findings[j].Severity {
			if plan.Findings[i].File == plan.Findings[j].File {
				return plan.Findings[i].Kind < plan.Findings[j].Kind
			}
			return plan.Findings[i].File < plan.Findings[j].File
		}
		return plan.Findings[i].Severity > plan.Findings[j].Severity
	})
	return plan
}

func existingTDDPlan(refs []string, obligationByID map[string]TDDObligation) (string, string) {
	for _, ref := range refs {
		obligation, ok := obligationByID[strings.TrimSpace(ref)]
		if !ok {
			continue
		}
		command := strings.TrimSpace(obligation.Command)
		if command == "" {
			continue
		}
		return strings.TrimSpace(obligation.Type), command
	}
	return "", ""
}

func TDDObligationPlanHasBlockers(plan TDDObligationSynthesisPlan) bool {
	if len(plan.Findings) > 0 {
		return true
	}
	for _, obligation := range plan.Obligations {
		if strings.TrimSpace(obligation.BlockerReason) != "" {
			return true
		}
	}
	return false
}

func suggestedTDDPlanForTier(coverageTier string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(coverageTier)) {
	case "documentation":
		return "audit", "git diff --check"
	case "local_low":
		return "source_sanity", "go test ./..."
	case "behavior_standard":
		return "acceptance", "go test ./..."
	case "contract_high", "formal_optional":
		return "contract", "go test ./..."
	default:
		return "acceptance", "go test ./..."
	}
}

func isContractTier(coverageTier string) bool {
	switch strings.ToLower(strings.TrimSpace(coverageTier)) {
	case "contract_high", "formal_optional":
		return true
	default:
		return false
	}
}

func isAcceptedArtifact(status string) bool {
	return util.EqualTrimFold(status, "accepted")
}

func stableUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil
	}
	return result
}
