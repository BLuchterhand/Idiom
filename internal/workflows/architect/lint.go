package architect

import (
	"encoding/json"
	"fmt"
	"strings"
)

type LintResult struct {
	OK       bool          `json:"ok"`
	Findings []LintFinding `json:"findings,omitempty"`
}

type LintFinding struct {
	Gate    string `json:"gate"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

func LintPacket(packet Packet) LintResult {
	findings := make([]LintFinding, 0)
	if strings.TrimSpace(packet.Task) == "" {
		findings = append(findings, LintFinding{Gate: "task_required", Level: "error", Message: "packet task is empty"})
	}

	if !knownPacketSchema(packet.SchemaVersion) {
		findings = append(findings, LintFinding{Gate: "schema_version", Level: "error", Message: fmt.Sprintf("schema_version = %q, want %q or %q", packet.SchemaVersion, SchemaVersion, PlanningPacketV2Version)})
	}

	if packet.SchemaVersion == PlanningPacketV2Version {
		findings = append(findings, lintPlanningPacketV2(packet)...)
	}

	for _, ev := range packet.Evidence {
		if ev.Error != "" && isRequiredEvidence(packet, ev) {
			findings = append(findings, LintFinding{Gate: "required_evidence", Level: "error", Message: fmt.Sprintf("%s %q failed: %s", ev.Kind, evidenceName(ev), ev.Error)})
		}

		if ev.Error == "" && (ev.Kind == "collector" || ev.Kind == "rule" || ev.Kind == "context_view") && strings.TrimSpace(ev.Content) == "" {
			findings = append(findings, LintFinding{Gate: "empty_evidence", Level: "warning", Message: fmt.Sprintf("%s %q produced no content", ev.Kind, evidenceName(ev))})
		}

		if ev.Kind == "context_view" {
			findings = append(findings, lintContextViewEvidence(ev)...)
		}
	}

	for _, gate := range packet.Gates {
		switch gate.Name {
		case "exact_tests_required":
			if len(packet.Tests) == 0 {
				findings = append(findings, gateFinding(gate, "no exact validation command is listed"))
			}
		case "file_allowlist_required":
			if len(packet.Files) == 0 {
				findings = append(findings, gateFinding(gate, "no file allowlist is listed"))
			}
		case "question_stop_required", "qwen_question_stop_required":
			if !packetMentionsQuestionStop(packet) {
				findings = append(findings, gateFinding(gate, "packet constraints do not include a <<QUESTION>> stop rule"))
			}
		case "evidence_required":
			if !hasMaterialEvidence(packet) {
				findings = append(findings, gateFinding(gate, "packet has no rule, collector, or context-view evidence"))
			}
		case "anchors_required":
			if len(packet.Anchors) == 0 {
				findings = append(findings, gateFinding(gate, "packet has no verified anchors"))
			}
		case "context_view_required":
			if !hasContextViewEvidence(packet) {
				findings = append(findings, gateFinding(gate, "packet has no context-view evidence"))
			}
		case "fresh_context_required", "context_freshness_required":
			findings = append(findings, lintFreshContextGate(packet, gate)...)
		default:
			if gate.Required {
				findings = append(findings, LintFinding{Gate: gate.Name, Level: "warning", Message: "unknown required gate; no built-in check exists"})
			}
		}
	}

	ok := true

	for _, finding := range findings {
		if finding.Level == "error" {
			ok = false
			break
		}
	}

	return LintResult{OK: ok, Findings: findings}
}

func knownPacketSchema(schema string) bool {
	return schema == SchemaVersion || schema == PlanningPacketV2Version
}

func lintPlanningPacketV2(packet Packet) []LintFinding {
	var findings []LintFinding

	if packet.Contracts == nil {
		findings = append(findings, LintFinding{Gate: "planning_v2_contracts", Level: "error", Message: "planning_v2 packet is missing contracts"})
	}

	if packet.ContextBudget == nil {
		findings = append(findings, LintFinding{Gate: "context_budget", Level: "error", Message: "planning_v2 packet is missing context_budget"})
	}

	if len(packet.Tests) > 0 && (packet.Contracts == nil || len(packet.Contracts.ValidationRequirements) == 0) {
		findings = append(findings, LintFinding{Gate: "planning_v2_validation", Level: "error", Message: "planning_v2 packet did not preserve validation requirements in contracts"})
	}

	if packet.Freshness != nil && packet.Freshness.Fresh != nil && !*packet.Freshness.Fresh {
		findings = append(findings, LintFinding{Gate: "planning_v2_freshness", Level: "warning", Message: "planning_v2 packet includes stale context under freshness"})
	}

	for _, item := range packet.CandidateScope {
		if item.Path == "" && item.Symbol == "" && item.Handle == "" {
			findings = append(findings, LintFinding{Gate: "planning_v2_candidate_scope", Level: "warning", Message: "candidate_scope contains an item without path, symbol, or handle"})
		}
	}

	return findings
}

type contextViewLintPayload struct {
	PackID         string   `json:"pack_id"`
	IndexID        string   `json:"index_id"`
	Fresh          *bool    `json:"fresh"`
	UnresolvedGaps []string `json:"unresolved_gaps"`
}

type contextViewLintInfo struct {
	Evidence Evidence
	Payload  contextViewLintPayload
	ParseErr error
}

func lintContextViewEvidence(ev Evidence) []LintFinding {
	if strings.TrimSpace(ev.Error) != "" || strings.TrimSpace(ev.Content) == "" {
		return nil
	}

	info := parseContextViewEvidence(ev)
	if info.ParseErr != nil {
		return []LintFinding{{Gate: "context_view_parse", Level: "warning", Message: fmt.Sprintf("context_view %q is not valid JSON: %v", evidenceName(ev), info.ParseErr)}}
	}

	var findings []LintFinding

	if info.Payload.Fresh == nil {
		findings = append(findings, LintFinding{Gate: "context_freshness", Level: "warning", Message: fmt.Sprintf("context_view %q does not report freshness", evidenceName(ev))})
	} else if !*info.Payload.Fresh {
		findings = append(findings, LintFinding{Gate: "stale_context_view", Level: "warning", Message: fmt.Sprintf("context_view %q was generated from stale context", evidenceName(ev))})
	}

	if len(info.Payload.UnresolvedGaps) > 0 {
		findings = append(findings, LintFinding{Gate: "context_view_gaps", Level: "warning", Message: fmt.Sprintf("context_view %q has %d unresolved gap(s)", evidenceName(ev), len(info.Payload.UnresolvedGaps))})
	}

	return findings
}

func lintFreshContextGate(packet Packet, gate Gate) []LintFinding {
	infos := contextViewLintInfos(packet)
	if len(infos) == 0 {
		return []LintFinding{gateFinding(gate, "packet has no context-view evidence to check for freshness")}
	}

	var findings []LintFinding

	for _, info := range infos {
		name := evidenceName(info.Evidence)
		switch {
		case strings.TrimSpace(info.Evidence.Error) != "":
			findings = append(findings, gateFinding(gate, fmt.Sprintf("context_view %q failed: %s", name, info.Evidence.Error)))
		case strings.TrimSpace(info.Evidence.Content) == "":
			findings = append(findings, gateFinding(gate, fmt.Sprintf("context_view %q produced no content", name)))
		case info.ParseErr != nil:
			findings = append(findings, gateFinding(gate, fmt.Sprintf("context_view %q is not valid JSON: %v", name, info.ParseErr)))
		case info.Payload.Fresh == nil:
			findings = append(findings, gateFinding(gate, fmt.Sprintf("context_view %q does not report freshness", name)))
		case !*info.Payload.Fresh:
			findings = append(findings, gateFinding(gate, fmt.Sprintf("context_view %q was generated from stale context", name)))
		}
	}

	return findings
}

func parseContextViewEvidence(ev Evidence) contextViewLintInfo {
	info := contextViewLintInfo{Evidence: ev}

	err := json.Unmarshal([]byte(ev.Content), &info.Payload)
	if err != nil {
		info.ParseErr = err
	}

	return info
}

func contextViewLintInfos(packet Packet) []contextViewLintInfo {
	out := make([]contextViewLintInfo, 0)

	for _, ev := range packet.Evidence {
		if ev.Kind != "context_view" {
			continue
		}

		if strings.TrimSpace(ev.Error) != "" || strings.TrimSpace(ev.Content) == "" {
			out = append(out, contextViewLintInfo{Evidence: ev})
			continue
		}

		out = append(out, parseContextViewEvidence(ev))
	}

	return out
}

func gateFinding(gate Gate, message string) LintFinding {
	level := "warning"
	if gate.Required {
		level = "error"
	}

	return LintFinding{Gate: gate.Name, Level: level, Message: message}
}

func packetMentionsQuestionStop(packet Packet) bool {
	for _, value := range packet.Constraints {
		if strings.Contains(value, "<<QUESTION>>") {
			return true
		}
	}

	for _, ev := range packet.Evidence {
		if strings.Contains(ev.Content, "<<QUESTION>>") {
			return true
		}
	}

	return false
}

func isRequiredEvidence(packet Packet, ev Evidence) bool {
	if ev.Kind != "rule" && ev.Kind != "collector" && ev.Kind != "context_view" {
		return false
	}

	if ev.Required {
		return true
	}

	for _, gate := range packet.Gates {
		if (gate.Name == "evidence_required" || gate.Name == "context_view_required" || gate.Name == "fresh_context_required" || gate.Name == "context_freshness_required") && gate.Required {
			return true
		}
	}

	return false
}

func hasMaterialEvidence(packet Packet) bool {
	for _, ev := range packet.Evidence {
		if ev.Kind == "rule" || ev.Kind == "collector" || ev.Kind == "context_view" {
			return true
		}
	}

	return false
}

func hasContextViewEvidence(packet Packet) bool {
	for _, ev := range packet.Evidence {
		if ev.Kind == "context_view" {
			return true
		}
	}

	return false
}

func evidenceName(ev Evidence) string {
	if ev.Label != "" {
		return ev.Label
	}

	if ev.Path != "" {
		return ev.Path
	}

	return ev.Command
}
