package productworkflow

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type ResearchRunPlanningRequest struct {
	ProjectRoot       string
	ResearchOutputDir string
	Profile           string
}

type ResearchRunPlanningResult struct {
	Status ProductWorkflowStatus `json:"status"`
	Plan   ResearchRunPlan       `json:"plan"`
}

type ResearchPromptRenderRequest struct {
	ProjectRoot       string
	EffortRoot        string
	ResearchOutputDir string
	ResearchPromptDir string
	Profile           string
}

type ResearchPromptRenderResult struct {
	Status           ProductWorkflowStatus      `json:"status"`
	EffortRoot       string                     `json:"effort_root"`
	ProfileCount     int                        `json:"-"`
	PromptWriteCount int                        `json:"prompt_write_count"`
	Plan             ResearchArtifactPromptPlan `json:"plan"`
}

type ResearchEvidenceMergeRequest struct {
	ProjectRoot       string
	EffortRoot        string
	ResearchOutputDir string
	Profile           string
}

type ResearchEvidenceMergeOutcome struct {
	Status              ProductWorkflowStatus      `json:"status"`
	EffortRoot          string                     `json:"effort_root"`
	ResearchOutputDir   string                     `json:"research_output_dir"`
	Profile             string                     `json:"profile"`
	ProfileCount        int                        `json:"profile_count"`
	ResearchRunCount    int                        `json:"research_run_count"`
	MergedCardCount     int                        `json:"merged_card_count"`
	MergedCardIDs       []string                   `json:"merged_card_ids"`
	Conflicts           []ResearchEvidenceConflict `json:"conflicts"`
	Findings            []ProfileFinding           `json:"findings"`
	WroteEvidenceLedger bool                       `json:"wrote_evidence_ledger"`
}

func PlanResearchRunsFromProject(request ResearchRunPlanningRequest) (ResearchRunPlanningResult, error) {
	profiles, err := DiscoverProfiles(request.ProjectRoot)
	if err != nil {
		return ResearchRunPlanningResult{}, err
	}

	plan := PlanResearchRuns(profiles.Profiles, request.Profile, request.ResearchOutputDir)
	plan.Findings = append(append([]ProfileFinding{}, profiles.Findings...), plan.Findings...)
	status := ProductWorkflowStatusPass
	if hasErrorFinding(plan.Findings) {
		status = ProductWorkflowStatusBlocked
	}

	return ResearchRunPlanningResult{Status: status, Plan: plan}, nil
}

func RenderResearchPromptsFromProject(request ResearchPromptRenderRequest) (ResearchPromptRenderResult, error) {
	profiles, err := DiscoverProfiles(request.ProjectRoot)
	if err != nil {
		return ResearchPromptRenderResult{}, err
	}
	if strings.TrimSpace(request.EffortRoot) == "" {
		return ResearchPromptRenderResult{}, fmt.Errorf("effort root is required to render research prompts")
	}

	absRoot, err := filepath.Abs(request.EffortRoot)
	if err != nil {
		return ResearchPromptRenderResult{}, fmt.Errorf("resolve effort root: %w", err)
	}

	findings := append([]ProfileFinding{}, profiles.Findings...)
	if len(profiles.Profiles) == 0 {
		findings = append(findings, profileArtifactFinding(
			"missing_research_profile",
			"no valid research profile is available for prompt rendering",
			".idiom/research/profiles",
		))
	}

	runPlan := PlanResearchRuns(profiles.Profiles, request.Profile, request.ResearchOutputDir)
	runPlan.Findings = append(findings, runPlan.Findings...)
	promptPlan := PlanResearchArtifactPrompts(runPlan, request.ResearchPromptDir)

	promptDir := filepath.Join(absRoot, filepath.FromSlash(promptPlan.ResearchPromptDir))
	if finding, outside := ValidateResearchPromptDirBoundary(absRoot, promptDir, promptPlan.ResearchPromptDir); outside {
		promptPlan.Findings = append(promptPlan.Findings, finding)
	}
	SortProfileFindings(promptPlan.Findings)

	if !hasErrorFinding(promptPlan.Findings) {
		for _, run := range promptPlan.Runs {
			promptPath := filepath.Join(absRoot, filepath.FromSlash(run.PromptPath))
			if finding, outside := ValidateResearchPromptPathBoundary(absRoot, promptPath, run.PromptPath); outside {
				promptPlan.Findings = append(promptPlan.Findings, finding)
			}
		}
		SortProfileFindings(promptPlan.Findings)
	}

	result := ResearchPromptRenderResult{
		Status:       ProductWorkflowStatusBlocked,
		EffortRoot:   absRoot,
		ProfileCount: len(profiles.Profiles),
		Plan:         promptPlan,
	}
	if !hasErrorFinding(promptPlan.Findings) {
		for _, run := range promptPlan.Runs {
			if err := writeEffortArtifactAtomic(absRoot, run.PromptPath, []byte(RenderResearchArtifactPromptText(run)), 0o644); err != nil {
				return result, fmt.Errorf("write prompt file: %w", err)
			}
			result.PromptWriteCount++
		}
		result.Status = ProductWorkflowStatusPass
	}

	if result.Plan.Runs == nil {
		result.Plan.Runs = []ResearchArtifactPromptRunSpec{}
	}
	if result.Plan.Findings == nil {
		result.Plan.Findings = []ProfileFinding{}
	}

	return result, nil
}

func MergeResearchEvidenceFromProject(request ResearchEvidenceMergeRequest) (ResearchEvidenceMergeOutcome, error) {
	profiles, err := DiscoverProfiles(request.ProjectRoot)
	if err != nil {
		return ResearchEvidenceMergeOutcome{}, err
	}
	if strings.TrimSpace(request.EffortRoot) == "" {
		return ResearchEvidenceMergeOutcome{}, fmt.Errorf("effort root is required to merge research evidence")
	}

	absRoot, err := filepath.Abs(request.EffortRoot)
	if err != nil {
		return ResearchEvidenceMergeOutcome{}, fmt.Errorf("resolve effort root: %w", err)
	}

	findings := append([]ProfileFinding{}, profiles.Findings...)
	if len(profiles.Profiles) == 0 {
		findings = append(findings, profileArtifactFinding(
			"missing_research_profile",
			"no valid research profile is available for evidence merge",
			".idiom/research/profiles",
		))
	}

	selectedProfile, profileFindings := SelectResearchProfile(profiles.Profiles, request.Profile)
	findings = append(findings, profileFindings...)

	existing, evidenceFindings := loadEffortEvidenceCards(absRoot)
	findings = append(findings, evidenceFindings...)

	documents, documentFindings, err := DiscoverResearchCardDocuments(absRoot, request.ResearchOutputDir)
	if err != nil {
		return ResearchEvidenceMergeOutcome{}, err
	}
	findings = append(findings, documentFindings...)

	var policy MergePolicy
	if selectedProfile != nil {
		policy = selectedProfile.MergePolicy
	}

	merge := MergeResearchCardDocuments(existing, documents, policy)
	merge.Findings = append(findings, merge.Findings...)
	blocked := hasErrorFinding(merge.Findings) || len(merge.Conflicts) > 0
	wrote := false
	if !blocked {
		if err := writeEvidenceCardsJSONL(absRoot, "evidence/evidence.jsonl", merge.Cards); err != nil {
			return ResearchEvidenceMergeOutcome{}, fmt.Errorf("write evidence ledger: %w", err)
		}
		wrote = true
	}

	status := ProductWorkflowStatusPass
	if blocked {
		status = ProductWorkflowStatusBlocked
	}
	outcome := ResearchEvidenceMergeOutcome{
		Status:              status,
		EffortRoot:          absRoot,
		ResearchOutputDir:   request.ResearchOutputDir,
		Profile:             researchProfileIdentifier(selectedProfile),
		ProfileCount:        len(profiles.Profiles),
		ResearchRunCount:    len(documents),
		MergedCardCount:     len(merge.Cards),
		MergedCardIDs:       merge.MergedCardIDs,
		Conflicts:           merge.Conflicts,
		Findings:            merge.Findings,
		WroteEvidenceLedger: wrote,
	}
	if outcome.MergedCardIDs == nil {
		outcome.MergedCardIDs = []string{}
	}
	if outcome.Conflicts == nil {
		outcome.Conflicts = []ResearchEvidenceConflict{}
	}
	if outcome.Findings == nil {
		outcome.Findings = []ProfileFinding{}
	}

	return outcome, nil
}

func researchProfileIdentifier(profile *ResearchProfile) string {
	if profile == nil {
		return ""
	}
	if strings.TrimSpace(profile.ProfileID) != "" {
		return profile.ProfileID
	}

	return profile.Name
}

func writeEvidenceCardsJSONL(effortRoot string, relPath string, cards []EvidenceCard) error {
	var body strings.Builder
	for _, card := range cards {
		data, err := json.Marshal(card)
		if err != nil {
			return err
		}
		body.Write(data)
		body.WriteByte('\n')
	}

	return writeEffortArtifactAtomic(effortRoot, relPath, []byte(body.String()), 0o644)
}
