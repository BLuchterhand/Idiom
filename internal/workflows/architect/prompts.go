package architect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type PromptKind string

const (
	PromptPlan     PromptKind = "plan"
	PromptCritique PromptKind = "critique"
	PromptBrief    PromptKind = "brief"
)

func RenderPrompt(kind PromptKind, packet Packet, prior string) (string, error) {
	packetJSON, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode packet: %w", err)
	}
	var b bytes.Buffer
	switch kind {
	case PromptPlan:
		renderPlanPrompt(&b, packetJSON)
	case PromptCritique:
		renderCritiquePrompt(&b, packetJSON, prior)
	case PromptBrief:
		renderBriefPrompt(&b, packetJSON, prior)
	default:
		return "", fmt.Errorf("unknown architect prompt kind: %s", kind)
	}
	return strings.TrimSpace(b.String()) + "\n", nil
}

func renderPlanPrompt(b *bytes.Buffer, packetJSON []byte) {
	b.WriteString(`# Architect Plan Task

You are the planner. Use only the packet below. Do not inspect the repository.

Produce a concise implementation plan with:
- task summary
- load-bearing evidence
- atom breakdown
- file ownership and boundaries
- validation commands
- risks and unresolved questions

The plan must not invent symbols, files, tests, or rules that are not in the packet. If a required fact is missing, write it under "Unresolved Questions" instead of filling it in.

## Context Packet

`)
	b.WriteString("```json\n")
	b.Write(packetJSON)
	b.WriteString("\n```\n")
}

func renderCritiquePrompt(b *bytes.Buffer, packetJSON []byte, prior string) {
	b.WriteString(`# Architect Critique Task

You are the critic. Use only the packet and candidate plan below. Do not inspect the repository.

Find concrete defects in the plan:
- missing evidence
- unbounded files or scope
- missing tests
- contradictions with constraints or gates
- risky handoffs to weaker executor lanes
- invented symbols, APIs, or assumptions

Return findings ordered by severity. If the plan is acceptable, say so and list residual risks.

## Context Packet

`)
	b.WriteString("```json\n")
	b.Write(packetJSON)
	b.WriteString("\n```\n\n")
	b.WriteString("## Candidate Plan\n\n")
	b.WriteString(fencedText(prior))
}

func renderBriefPrompt(b *bytes.Buffer, packetJSON []byte, prior string) {
	b.WriteString(`# Architect Brief Task

You are turning a planner-approved packet into an executor brief. Use only the packet and approved plan below. Do not inspect the repository.

Produce one dispatch-ready brief with:
- one-sentence task
- hard prohibitions, capped at 8 distinct bullets
- exhaustive files-to-modify table
- verified anchors
- exact acceptance tests
- required stop rule for missing or contradicted facts
- concise continuity notes

The brief must be actionable for a bounded executor lane and must not ask the executor to rediscover facts already present in the packet.
Do not generate repetitive prohibition templates. Each prohibition must name a concrete scope, file, command, or behavior from the packet.
If the packet lacks enough detail for a concrete implementation instruction, put that gap under <<QUESTION>> instead of padding the brief.

## Context Packet

`)
	b.WriteString("```json\n")
	b.Write(packetJSON)
	b.WriteString("\n```\n\n")
	b.WriteString("## Approved Plan\n\n")
	b.WriteString(fencedText(prior))
}

func fencedText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "(none provided)"
	}
	return "```text\n" + text + "\n```\n"
}
