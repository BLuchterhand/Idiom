package productworkflow

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const researchCardsSchemaVersion = "idiom.research_cards.v1"

func PlanResearchRuns(profiles []ResearchProfile, selector, researchOutputDir string) ResearchRunPlan {
	plan := ResearchRunPlan{
		ProfileCount:      len(profiles),
		ResearchOutputDir: researchOutputDir,
		Runs:              []ResearchRunSpec{},
		Findings:          []ProfileFinding{},
	}
	selectedProfile, findings := SelectResearchProfile(profiles, selector)

	plan.Findings = append(plan.Findings, findings...)

	if selectedProfile == nil {
		return plan
	}

	plan.SelectedProfile = strings.TrimSpace(selectedProfile.Name)
	plan.SelectedProfileID = selectedProfileID(*selectedProfile)

	cleanRelDir, ok := cleanResearchOutputDir(researchOutputDir)

	if !ok {
		plan.Findings = append(plan.Findings, ProfileFinding{
			Kind:     "invalid_research_output_dir",
			Message:  fmt.Sprintf("research output directory %q must be effort-relative and must not contain path traversal", researchOutputDir),
			File:     researchOutputDir,
			Severity: ProductWorkflowSeverityError,
		})

		return plan
	}

	plan.ResearchOutputDir = cleanRelDir
	if !isPathSafeIdentifier(plan.SelectedProfileID) {
		plan.Findings = append(plan.Findings, ProfileFinding{
			Kind:     "invalid_research_profile_id",
			Message:  fmt.Sprintf("selected profile_id %q is not path-safe", plan.SelectedProfileID),
			File:     ".idiom/research/profiles",
			Severity: ProductWorkflowSeverityError,
		})

		return plan
	}

	runs := make([]ResearchRunSpec, 0, len(selectedProfile.Researchers))
	hasRunPlanError := false

	for _, researcher := range selectedProfile.Researchers {
		researcherID := strings.TrimSpace(researcher.ID)
		if !isPathSafeIdentifier(researcherID) {
			hasRunPlanError = true

			plan.Findings = append(plan.Findings, ProfileFinding{
				Kind:     "invalid_researcher_id",
				Message:  fmt.Sprintf("researcher id %q is not path-safe", researcherID),
				File:     ".idiom/research/profiles",
				Severity: ProductWorkflowSeverityError,
			})

			continue
		}

		outputFile := fmt.Sprintf("%s--%s.json", plan.SelectedProfileID, researcherID)

		runs = append(runs, ResearchRunSpec{
			ResearcherID:        researcherID,
			Alignment:           researcher.Alignment,
			Question:            researcher.Question,
			RequiredSources:     append([]string{}, researcher.RequiredSources...),
			AllowedCapabilities: append([]string{}, researcher.AllowedCapabilities...),
			Output:              researcher.Output,
			OutputPath:          filepath.ToSlash(filepath.Join(cleanRelDir, outputFile)),
			OutputContract: ResearchRunOutputContract{
				SchemaVersion: researchCardsSchemaVersion,
			},
		})
	}

	if hasRunPlanError {
		sortProfileFindings(plan.Findings)

		return plan
	}

	sort.SliceStable(runs, func(i, j int) bool {
		return runs[i].ResearcherID < runs[j].ResearcherID
	})

	plan.Runs = runs
	plan.RunCount = len(runs)
	sortProfileFindings(plan.Findings)

	return plan
}

func SelectResearchProfile(profiles []ResearchProfile, selector string) (*ResearchProfile, []ProfileFinding) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		if len(profiles) == 1 {
			return &profiles[0], nil
		}

		if len(profiles) > 1 {
			return nil, []ProfileFinding{{
				Kind:     "ambiguous_research_profile",
				Message:  "multiple research profiles are available; pass --profile with a profile_id or name for deterministic profile selection",
				File:     ".idiom/research/profiles",
				Severity: ProductWorkflowSeverityError,
			}}
		}

		return nil, nil
	}

	matches := make([]int, 0, 1)

	for i, profile := range profiles {
		if selectedProfileID(profile) == selector || strings.TrimSpace(profile.Name) == selector {
			matches = append(matches, i)
		}
	}

	switch len(matches) {
	case 0:
		return nil, []ProfileFinding{{
			Kind:     "unknown_research_profile",
			Message:  fmt.Sprintf("research profile %q was not found by profile_id or name", selector),
			File:     ".idiom/research/profiles",
			Severity: ProductWorkflowSeverityError,
		}}
	case 1:
		return &profiles[matches[0]], nil
	default:
		return nil, []ProfileFinding{{
			Kind:     "ambiguous_research_profile",
			Message:  fmt.Sprintf("research profile selector %q matched multiple profiles", selector),
			File:     ".idiom/research/profiles",
			Severity: ProductWorkflowSeverityError,
		}}
	}
}

func selectedProfileID(profile ResearchProfile) string {
	if strings.TrimSpace(profile.ProfileID) != "" {
		return strings.TrimSpace(profile.ProfileID)
	}

	return strings.TrimSpace(profile.Name)
}

func isPathSafeIdentifier(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}

	if strings.Contains(value, "/") || strings.Contains(value, "\\") {
		return false
	}

	if strings.Contains(value, string(filepath.Separator)) {
		return false
	}

	return filepath.Clean(value) == value
}

func sortProfileFindings(findings []ProfileFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity < findings[j].Severity
		}

		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}

		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}

		return findings[i].Message < findings[j].Message
	})
}
