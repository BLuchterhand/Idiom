package app

import (
	"fmt"
	"strings"

	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/featureplanner"
)

type greenfieldWriteApprovalMode string

const (
	greenfieldWriteApprovalLaneConfirmation   greenfieldWriteApprovalMode = "lane_confirmation"
	greenfieldWriteApprovalScopeAuthorization greenfieldWriteApprovalMode = "scope_authorization"
)

type greenfieldWriteApprovalRequest struct {
	Mode                        greenfieldWriteApprovalMode
	Command                     string
	Lane                        string
	Question                    string
	Decision                    string
	ProposedPaths               []string
	RequiresVerificationCommand bool
	StatePath                   string
	PlanPath                    string
}

func renderGreenfieldWriteApproval(request greenfieldWriteApprovalRequest) string {
	switch request.Mode {
	case greenfieldWriteApprovalScopeAuthorization:
		return renderGreenfieldScopeAuthorization(request)
	case greenfieldWriteApprovalLaneConfirmation:
		return renderGreenfieldLaneConfirmation(request)
	default:
		return ""
	}
}

func renderGreenfieldLaneConfirmation(request greenfieldWriteApprovalRequest) string {
	command := strings.TrimSpace(request.Command)
	if command == "" {
		command = "/implement"
	}
	lane := strings.TrimSpace(request.Lane)
	if lane == "" {
		lane = "implement"
	}

	return fmt.Sprintf("This project appears to be empty, and this request would create files. To continue, reply with `%s <objective>` to switch to the write-capable %s lane. Otherwise, ask your question as-is and I will answer in read-only mode.", command, lane)
}

func renderGreenfieldScopeAuthorization(request greenfieldWriteApprovalRequest) string {
	var b strings.Builder

	b.WriteString("Feature planner needs your approval before it can continue.\n\n")

	if question := strings.TrimSpace(request.Question); question != "" {
		fmt.Fprintf(&b, "Question: %s\n", question)
	}

	if len(request.ProposedPaths) > 0 {
		b.WriteString("Proposed paths:\n")
		for _, path := range request.ProposedPaths {
			fmt.Fprintf(&b, "- %s\n", path)
		}
	} else if decision := strings.TrimSpace(request.Decision); decision != "" {
		fmt.Fprintf(&b, "Decision needed: %s\n", decision)
	}

	if request.RequiresVerificationCommand {
		b.WriteString("Also provide the exact verification command for this project.\n")
	}

	if planPath := strings.TrimSpace(request.PlanPath); planPath != "" {
		fmt.Fprintf(&b, "Plan tab updated at %s.\n", planPath)
	}

	b.WriteString("\nReply with approval, revised paths/setup, or rejection. Other questions will stay in chat.")

	if statePath := strings.TrimSpace(request.StatePath); statePath != "" {
		fmt.Fprintf(&b, "\nState saved at %s", statePath)
	}

	return strings.TrimSpace(b.String())
}

func greenfieldCreationConfirmationQuestion(plan promptExecutionPlan) string {
	command := "/implement"
	lane := "implement"

	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(plan.route.Family) {
		command = "/plan-execute"
		lane = "plan-execute"
	}

	return renderGreenfieldWriteApproval(greenfieldWriteApprovalRequest{
		Mode:    greenfieldWriteApprovalLaneConfirmation,
		Command: command,
		Lane:    lane,
	})
}

func greenfieldScopeAuthorizationFromPlannerQuestion(question featureplanner.PlannerQuestion, statePath string) (greenfieldWriteApprovalRequest, bool) {
	if !featurePlannerQuestionOpen(question) ||
		strings.TrimSpace(question.AuthorityRequired) != "operator_approval" ||
		!featurePlannerQuestionNeedsField(question, "new_file_authorization") {
		return greenfieldWriteApprovalRequest{}, false
	}

	paths := featurePlannerQuestionAffectedPaths(question)
	if len(paths) == 0 {
		return greenfieldWriteApprovalRequest{}, false
	}

	return greenfieldWriteApprovalRequest{
		Mode:                        greenfieldWriteApprovalScopeAuthorization,
		Question:                    strings.TrimSpace(question.QuestionSummary),
		Decision:                    strings.TrimSpace(question.RequestedDecision),
		ProposedPaths:               paths,
		RequiresVerificationCommand: featurePlannerQuestionNeedsField(question, "verification_command"),
		StatePath:                   statePath,
		PlanPath:                    "",
	}, true
}

func featurePlannerQuestionOpen(question featureplanner.PlannerQuestion) bool {
	status := strings.TrimSpace(question.Status)

	return status == "" || strings.EqualFold(status, "open")
}

func featurePlannerQuestionAffectedPaths(question featureplanner.PlannerQuestion) []string {
	seen := map[string]struct{}{}
	var paths []string

	for _, path := range question.AffectedPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	return paths
}

func featurePlannerQuestionNeedsField(question featureplanner.PlannerQuestion, field string) bool {
	field = strings.TrimSpace(field)
	for _, candidate := range question.AffectedFields {
		if strings.TrimSpace(candidate) == field {
			return true
		}
	}

	return false
}
