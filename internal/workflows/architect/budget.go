package architect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type ContextBudgetOptions struct {
	Tier string
}

type ContextBudgetReport struct {
	BudgetTier           string                  `json:"budget_tier"`
	TokenLimit           int                     `json:"token_limit"`
	EstimatedTokens      int                     `json:"estimated_tokens"`
	ReservedOutputTokens int                     `json:"reserved_output_tokens"`
	Risk                 string                  `json:"risk"`
	Allocations          []ContextBudgetCategory `json:"allocations,omitempty"`
	Warnings             []string                `json:"warnings,omitempty"`
}

type ContextBudgetCategory struct {
	Category        string `json:"category"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Bytes           int    `json:"bytes"`
	Items           int    `json:"items,omitempty"`
}

type budgetTierSpec struct {
	Name           string
	TokenLimit     int
	ReservedOutput int
}

func BuildContextBudget(packet Packet, opts ContextBudgetOptions) *ContextBudgetReport {
	spec := contextBudgetTier(opts.Tier)
	builder := contextBudgetBuilder{}
	builder.add("user_task", packet.Task, 1)
	builder.addStrings("constraints", packet.Constraints)
	builder.addFiles(packet.Files)
	builder.addCommands(packet.Tests)
	builder.addAnchors(packet.Anchors)
	builder.addJSON("contracts", packet.Contracts)
	builder.addJSON("repository_view", packet.RepositoryView)
	builder.addJSON("candidate_scope", packet.CandidateScope)
	builder.addJSON("freshness", packet.Freshness)
	builder.addJSON("language_capabilities", packet.LanguageCapabilities)
	builder.addJSON("handoff", packet.Handoff)
	builder.addStrings("unknowns", packet.Unknowns)
	builder.addStrings("risks", risksToStrings(packet.Risks))

	for _, ev := range packet.Evidence {
		category := "evidence_" + strings.TrimSpace(ev.Kind)
		if category == "evidence_" {
			category = "evidence"
		}

		builder.add(category, ev.Content+ev.Error+ev.Path+ev.Command, 1)
	}

	builder.add("packet_overhead", packet.SchemaVersion+packet.Profile.Name+packet.Workspace.Root, 1)

	report := &ContextBudgetReport{
		BudgetTier:           spec.Name,
		TokenLimit:           spec.TokenLimit,
		ReservedOutputTokens: spec.ReservedOutput,
		Allocations:          builder.allocations(),
	}
	for _, allocation := range report.Allocations {
		report.EstimatedTokens += allocation.EstimatedTokens
	}

	report.Warnings = budgetWarnings(packet, report)
	report.Risk = budgetRisk(report.EstimatedTokens+report.ReservedOutputTokens, spec.TokenLimit)

	return report
}

type contextBudgetBuilder struct {
	categories map[string]*ContextBudgetCategory
	order      []string
}

func (b *contextBudgetBuilder) add(category string, content string, items int) {
	content = strings.TrimSpace(content)
	if content == "" && items == 0 {
		return
	}

	if b.categories == nil {
		b.categories = map[string]*ContextBudgetCategory{}
	}

	item := b.categories[category]
	if item == nil {
		item = &ContextBudgetCategory{Category: category}
		b.categories[category] = item
		b.order = append(b.order, category)
	}

	item.Bytes += len(content)
	item.EstimatedTokens += estimateTokens(content)
	item.Items += items
}

func (b *contextBudgetBuilder) addStrings(category string, values []string) {
	for _, value := range values {
		b.add(category, value, 1)
	}
}

func (b *contextBudgetBuilder) addFiles(files []FileRef) {
	for _, file := range files {
		b.add("files", file.Path+" "+file.Role+" "+file.Reason, 1)
	}
}

func (b *contextBudgetBuilder) addCommands(commands []CommandRef) {
	for _, command := range commands {
		b.add("validation_commands", command.Command+" "+command.Reason, 1)
	}
}

func (b *contextBudgetBuilder) addAnchors(anchors []Anchor) {
	for _, anchor := range anchors {
		b.add("anchors", anchor.Path+" "+anchor.Symbol+" "+anchor.Lines+" "+anchor.Why, 1)
	}
}

func (b *contextBudgetBuilder) addJSON(category string, value any) {
	if value == nil {
		return
	}

	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || string(data) == "null" || string(data) == "[]" || string(data) == "{}" {
		return
	}

	b.add(category, string(data), 1)
}

func (b *contextBudgetBuilder) allocations() []ContextBudgetCategory {
	out := make([]ContextBudgetCategory, 0, len(b.order))
	for _, category := range b.order {
		if item := b.categories[category]; item != nil {
			out = append(out, *item)
		}
	}

	return out
}

func contextBudgetTier(value string) budgetTierSpec {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "brief":
		return budgetTierSpec{Name: "brief", TokenLimit: 24_000, ReservedOutput: 6_000}
	case "", "standard":
		return budgetTierSpec{Name: "standard", TokenLimit: 64_000, ReservedOutput: 12_000}
	case "deep":
		return budgetTierSpec{Name: "deep", TokenLimit: 128_000, ReservedOutput: 20_000}
	case "max-local", "max_local":
		return budgetTierSpec{Name: "max-local", TokenLimit: 196_000, ReservedOutput: 32_000}
	default:
		return budgetTierSpec{Name: "standard", TokenLimit: 64_000, ReservedOutput: 12_000}
	}
}

func budgetRisk(tokens int, limit int) string {
	if limit <= 0 {
		return "unknown"
	}

	switch {
	case tokens >= limit:
		return "critical"
	case tokens >= int(float64(limit)*0.85):
		return "high"
	case tokens >= int(float64(limit)*0.60):
		return "moderate"
	default:
		return "low"
	}
}

func budgetWarnings(packet Packet, report *ContextBudgetReport) []string {
	warnings := make([]string, 0)
	seenLocations := map[string]string{}
	seenContent := map[string]string{}

	for _, ev := range packet.Evidence {
		name := evidenceName(ev)
		location := firstNonEmpty(ev.Path, ev.Command)

		if location != "" {
			if prior := seenLocations[location]; prior != "" {
				warnings = append(warnings, fmt.Sprintf("duplicate evidence location %q appears in %q and %q", location, prior, name))
			} else {
				seenLocations[location] = name
			}
		}

		content := strings.TrimSpace(ev.Content)
		if content != "" {
			sum := sha256.Sum256([]byte(content))
			key := hex.EncodeToString(sum[:8])

			if prior := seenContent[key]; prior != "" {
				warnings = append(warnings, fmt.Sprintf("duplicate evidence content appears in %q and %q", prior, name))
			} else {
				seenContent[key] = name
			}
		}

		if strings.Contains(content, "[truncated]") {
			warnings = append(warnings, fmt.Sprintf("evidence %q was truncated before budgeting", name))
		}

		if ev.Error != "" {
			warnings = append(warnings, fmt.Sprintf("evidence %q has an error: %s", name, ev.Error))
		}
	}

	if packet.Freshness != nil && packet.Freshness.Fresh != nil && !*packet.Freshness.Fresh {
		warnings = append(warnings, "one or more context views are stale")
	}

	if report.EstimatedTokens+report.ReservedOutputTokens > report.TokenLimit {
		warnings = append(warnings, "estimated packet plus reserved output exceeds selected budget tier")
	}

	return cleanStrings(warnings)
}

func estimateTokens(content string) int {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0
	}

	return (len(content) + 3) / 4
}

func risksToStrings(risks []Risk) []string {
	out := make([]string, 0, len(risks))
	for _, risk := range risks {
		out = append(out, risk.Name+" "+risk.Mitigation)
	}

	return out
}
