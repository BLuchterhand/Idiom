package architect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PlanningPacketV2Options struct {
	BudgetTier string
	Now        time.Time
}

type PlanningContracts struct {
	Constraints            []string     `json:"constraints,omitempty"`
	ProtectedPaths         []string     `json:"protected_paths,omitempty"`
	ReadinessGates         []string     `json:"readiness_gates,omitempty"`
	ValidationRequirements []CommandRef `json:"validation_requirements,omitempty"`
}

type RepositoryViewItem struct {
	Kind       string `json:"kind"`
	Handle     string `json:"handle,omitempty"`
	Path       string `json:"path,omitempty"`
	Summary    string `json:"summary,omitempty"`
	IndexID    string `json:"index_id,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Fresh      *bool  `json:"fresh,omitempty"`
}

type RankedScopeItem struct {
	Handle      string        `json:"handle,omitempty"`
	Path        string        `json:"path,omitempty"`
	Symbol      string        `json:"symbol,omitempty"`
	Kind        string        `json:"kind"`
	Rank        float64       `json:"rank"`
	Signals     []ScopeSignal `json:"signals,omitempty"`
	Confidence  string        `json:"confidence,omitempty"`
	Limitations []string      `json:"limitations,omitempty"`
}

type ScopeSignal struct {
	Kind   string  `json:"kind"`
	Weight float64 `json:"weight"`
	Detail string  `json:"detail,omitempty"`
}

type FreshnessReport struct {
	IndexID          string                   `json:"index_id,omitempty"`
	GitHead          string                   `json:"git_head,omitempty"`
	Dirty            *bool                    `json:"dirty,omitempty"`
	DirtyFingerprint string                   `json:"dirty_fingerprint,omitempty"`
	Fresh            *bool                    `json:"fresh,omitempty"`
	ContextViews     []ContextFreshnessSource `json:"context_views,omitempty"`
	StaleNodes       []string                 `json:"stale_nodes,omitempty"`
	RefreshActions   []string                 `json:"refresh_actions,omitempty"`
}

type ContextFreshnessSource struct {
	Label          string   `json:"label,omitempty"`
	Path           string   `json:"path,omitempty"`
	PackID         string   `json:"pack_id,omitempty"`
	IndexID        string   `json:"index_id,omitempty"`
	GeneratedAt    string   `json:"generated_at,omitempty"`
	Fresh          *bool    `json:"fresh,omitempty"`
	UnresolvedGaps []string `json:"unresolved_gaps,omitempty"`
}

type LanguageCapabilityReport struct {
	Language      string   `json:"language"`
	PrecisionTier string   `json:"precision_tier"`
	Confidence    string   `json:"confidence,omitempty"`
	Limitations   []string `json:"limitations,omitempty"`
}

type PlanningHandoff struct {
	ImplementationReady bool     `json:"implementation_ready"`
	ExpectedPlanShape   []string `json:"expected_plan_shape,omitempty"`
	ReadinessFields     []string `json:"readiness_fields,omitempty"`
	Blockers            []string `json:"blockers,omitempty"`
}

type contextViewSummary struct {
	Label            string
	Path             string
	PackID           string
	IndexID          string
	GeneratedAt      string
	Fresh            *bool
	CandidateFiles   []contextCandidateFile
	CandidateSymbols []contextCandidateSymbol
	Tests            []contextCandidateTest
	Risks            []string
	Unknowns         []string
	UnresolvedGaps   []string
	StaleNodes       []string
	ParseError       string
}

type contextCandidateFile struct {
	Path       string
	Role       string
	Reason     string
	Language   string
	Confidence string
	Stale      bool
	Handles    []string
}

type contextCandidateSymbol struct {
	ID            string
	Path          string
	Kind          string
	Language      string
	QualifiedName string
	Confidence    string
	Stale         bool
	Reason        string
}

type contextCandidateTest struct {
	Command  string
	Path     string
	Symbol   string
	Reason   string
	Required bool
}

func enrichPlanningPacketV2(packet *Packet, opts PlanningPacketV2Options) {
	views := summarizeContextViews(packet.Evidence)
	packet.Tests = mergeCommands(packet.Tests, testsFromContextViews(views))
	packet.Unknowns = mergeStrings(packet.Unknowns, unknownsFromContextViews(views))
	packet.Risks = cleanRisks(append(packet.Risks, risksFromContextViews(views)...))
	packet.Contracts = buildPlanningContracts(*packet)
	packet.RepositoryView = buildRepositoryView(views)
	packet.CandidateScope = buildCandidateScope(*packet, views)
	packet.Freshness = buildFreshnessReport(*packet, views)
	packet.LanguageCapabilities = buildLanguageCapabilities(*packet, views)
	packet.Handoff = buildPlanningHandoff(*packet)
	packet.ContextBudget = BuildContextBudget(*packet, ContextBudgetOptions{Tier: opts.BudgetTier})
}

func buildPlanningContracts(packet Packet) *PlanningContracts {
	gates := make([]string, 0, len(packet.Gates))
	for _, gate := range packet.Gates {
		if gate.Name != "" {
			if gate.Required {
				gates = append(gates, gate.Name+":required")
			} else {
				gates = append(gates, gate.Name)
			}
		}
	}
	protected := make([]string, 0)
	for _, file := range packet.Files {
		role := strings.ToLower(file.Role)
		if strings.Contains(role, "protect") || strings.Contains(role, "forbid") || strings.Contains(role, "exclude") {
			protected = append(protected, file.Path)
		}
	}
	return &PlanningContracts{
		Constraints:            append([]string(nil), packet.Constraints...),
		ProtectedPaths:         cleanStrings(protected),
		ReadinessGates:         cleanStrings(gates),
		ValidationRequirements: append([]CommandRef(nil), packet.Tests...),
	}
}

func buildRepositoryView(views []contextViewSummary) []RepositoryViewItem {
	out := make([]RepositoryViewItem, 0, len(views))
	for _, view := range views {
		item := RepositoryViewItem{
			Kind:    "context_view",
			Handle:  contextViewHandle(view),
			Path:    view.Path,
			IndexID: view.IndexID,
			Fresh:   view.Fresh,
		}
		parts := []string{}
		if view.PackID != "" {
			parts = append(parts, "pack "+view.PackID)
		}
		if len(view.CandidateFiles) > 0 {
			parts = append(parts, pluralCount(len(view.CandidateFiles), "candidate file"))
		}
		if len(view.CandidateSymbols) > 0 {
			parts = append(parts, pluralCount(len(view.CandidateSymbols), "candidate symbol"))
		}
		if len(view.Tests) > 0 {
			parts = append(parts, pluralCount(len(view.Tests), "test"))
		}
		if view.ParseError != "" {
			parts = append(parts, "parse warning: "+view.ParseError)
			item.Confidence = "heuristic"
		} else {
			item.Confidence = "exact_tool_output"
		}
		item.Summary = strings.Join(parts, "; ")
		out = append(out, item)
	}
	return out
}

func buildCandidateScope(packet Packet, views []contextViewSummary) []RankedScopeItem {
	items := make(map[string]RankedScopeItem)
	add := func(item RankedScopeItem) {
		key := firstNonEmpty(item.Handle, item.Kind+"\x00"+item.Path+"\x00"+item.Symbol)
		if key == "" {
			return
		}
		existing, ok := items[key]
		if !ok || item.Rank > existing.Rank {
			items[key] = item
			return
		}
		existing.Signals = append(existing.Signals, item.Signals...)
		existing.Limitations = cleanStrings(append(existing.Limitations, item.Limitations...))
		items[key] = existing
	}
	for _, file := range packet.Files {
		role := strings.ToLower(file.Role)
		rank := 0.82
		if strings.Contains(role, "modify") || strings.Contains(role, "edit") {
			rank = 0.95
		}
		if strings.Contains(role, "read") {
			rank = 0.72
		}
		if strings.Contains(role, "protect") || strings.Contains(role, "forbid") || strings.Contains(role, "exclude") {
			rank = 0.99
		}
		add(RankedScopeItem{
			Handle:     "file://" + file.Path,
			Path:       file.Path,
			Kind:       "file",
			Rank:       rank,
			Confidence: "exact_input",
			Signals: []ScopeSignal{{
				Kind:   "explicit_file_ref",
				Weight: 0.50,
				Detail: firstNonEmpty(file.Reason, file.Role),
			}},
		})
	}
	for _, test := range packet.Tests {
		add(RankedScopeItem{
			Handle:     "test://" + stableHandlePart(test.Command),
			Kind:       "test_command",
			Rank:       0.86,
			Confidence: "exact_input",
			Signals: []ScopeSignal{{
				Kind:   "validation_requirement",
				Weight: 0.42,
				Detail: firstNonEmpty(test.Reason, test.Command),
			}},
		})
	}
	for _, view := range views {
		for _, file := range view.CandidateFiles {
			if file.Path == "" {
				continue
			}
			limitations := []string{}
			if file.Stale {
				limitations = append(limitations, "candidate was marked stale by context view")
			}
			handle := "file://" + file.Path
			if len(file.Handles) > 0 {
				handle = file.Handles[0]
			}
			add(RankedScopeItem{
				Handle:      handle,
				Path:        file.Path,
				Kind:        "file",
				Rank:        0.74,
				Confidence:  firstNonEmpty(file.Confidence, "heuristic"),
				Limitations: limitations,
				Signals: []ScopeSignal{{
					Kind:   "context_view_candidate",
					Weight: 0.32,
					Detail: firstNonEmpty(file.Reason, file.Role),
				}},
			})
		}
		for _, symbol := range view.CandidateSymbols {
			if symbol.ID == "" && symbol.QualifiedName == "" {
				continue
			}
			limitations := []string{}
			if symbol.Stale {
				limitations = append(limitations, "symbol was marked stale by context view")
			}
			add(RankedScopeItem{
				Handle:      firstNonEmpty(symbol.ID, "symbol://"+symbol.Path+"#"+symbol.QualifiedName),
				Path:        symbol.Path,
				Symbol:      symbol.QualifiedName,
				Kind:        firstNonEmpty(symbol.Kind, "symbol"),
				Rank:        0.68,
				Confidence:  firstNonEmpty(symbol.Confidence, "heuristic"),
				Limitations: limitations,
				Signals: []ScopeSignal{{
					Kind:   "context_view_symbol",
					Weight: 0.30,
					Detail: symbol.Reason,
				}},
			})
		}
		for _, test := range view.Tests {
			if test.Command == "" && test.Path == "" {
				continue
			}
			handle := ""
			if test.Command != "" {
				handle = "test://" + stableHandlePart(test.Command)
			} else if test.Path != "" {
				handle = "file://" + test.Path
			}
			add(RankedScopeItem{
				Handle:     handle,
				Path:       test.Path,
				Symbol:     test.Symbol,
				Kind:       "test_command",
				Rank:       0.62,
				Confidence: "exact_tool_output",
				Signals: []ScopeSignal{{
					Kind:   "context_view_test",
					Weight: 0.25,
					Detail: firstNonEmpty(test.Reason, test.Command),
				}},
			})
		}
	}
	out := make([]RankedScopeItem, 0, len(items))
	for _, item := range items {
		item.Limitations = cleanStrings(item.Limitations)
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank == out[j].Rank {
			return out[i].Handle < out[j].Handle
		}
		return out[i].Rank > out[j].Rank
	})
	return out
}

func buildFreshnessReport(packet Packet, views []contextViewSummary) *FreshnessReport {
	report := &FreshnessReport{}
	if packet.Workspace.Root != "" {
		report.GitHead = gitOutput(packet.Workspace.Root, "rev-parse", "--verify", "HEAD")
		status := gitOutput(packet.Workspace.Root, "status", "--porcelain")
		if status != "" {
			dirty := true
			report.Dirty = &dirty
			sum := sha256.Sum256([]byte(status))
			report.DirtyFingerprint = hex.EncodeToString(sum[:8])
		} else if report.GitHead != "" {
			dirty := false
			report.Dirty = &dirty
		}
	}
	var freshValues []bool
	for _, view := range views {
		source := ContextFreshnessSource{
			Label:          view.Label,
			Path:           view.Path,
			PackID:         view.PackID,
			IndexID:        view.IndexID,
			GeneratedAt:    view.GeneratedAt,
			Fresh:          view.Fresh,
			UnresolvedGaps: append([]string(nil), view.UnresolvedGaps...),
		}
		if report.IndexID == "" {
			report.IndexID = view.IndexID
		}
		if view.Fresh != nil {
			freshValues = append(freshValues, *view.Fresh)
		}
		report.StaleNodes = append(report.StaleNodes, view.StaleNodes...)
		report.ContextViews = append(report.ContextViews, source)
		if len(view.UnresolvedGaps) > 0 {
			report.RefreshActions = append(report.RefreshActions, view.UnresolvedGaps...)
		}
	}
	if len(freshValues) > 0 {
		fresh := true
		for _, value := range freshValues {
			fresh = fresh && value
		}
		report.Fresh = &fresh
	}
	report.StaleNodes = cleanStrings(report.StaleNodes)
	report.RefreshActions = cleanStrings(report.RefreshActions)
	if report.IndexID == "" && report.GitHead == "" && report.Dirty == nil && len(report.ContextViews) == 0 {
		return nil
	}
	return report
}

func buildLanguageCapabilities(packet Packet, views []contextViewSummary) []LanguageCapabilityReport {
	byLanguage := map[string]LanguageCapabilityReport{}
	add := func(language string, confidence string, stale bool) {
		language = strings.ToLower(strings.TrimSpace(language))
		if language == "" {
			return
		}
		row := byLanguage[language]
		if row.Language == "" {
			row.Language = language
			row.Confidence = confidence
			row.PrecisionTier = precisionTier(confidence)
		}
		if precisionRank(confidence) > precisionRank(row.Confidence) {
			row.Confidence = confidence
			row.PrecisionTier = precisionTier(confidence)
		}
		if stale {
			row.Limitations = append(row.Limitations, "some facts for this language were marked stale")
		}
		byLanguage[language] = row
	}
	for _, view := range views {
		for _, file := range view.CandidateFiles {
			add(file.Language, file.Confidence, file.Stale)
		}
		for _, symbol := range view.CandidateSymbols {
			add(symbol.Language, symbol.Confidence, symbol.Stale)
		}
	}
	out := make([]LanguageCapabilityReport, 0, len(byLanguage))
	for _, row := range byLanguage {
		row.Limitations = cleanStrings(row.Limitations)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Language < out[j].Language })
	return out
}

func buildPlanningHandoff(packet Packet) *PlanningHandoff {
	handoff := &PlanningHandoff{
		ExpectedPlanShape: []string{
			"task summary",
			"load-bearing evidence",
			"bounded atoms",
			"file ownership",
			"validation commands",
			"risks and unresolved questions",
		},
	}
	if len(packet.CandidateScope) > 0 {
		handoff.ReadinessFields = append(handoff.ReadinessFields, "candidate_scope")
	}
	if len(packet.Evidence) > 0 {
		handoff.ReadinessFields = append(handoff.ReadinessFields, "evidence")
	}
	if len(packet.Tests) > 0 {
		handoff.ReadinessFields = append(handoff.ReadinessFields, "validation_requirements")
	} else {
		handoff.Blockers = append(handoff.Blockers, "no validation command is available in the packet")
	}
	if len(packet.Unknowns) > 0 {
		handoff.Blockers = append(handoff.Blockers, "packet contains unresolved unknowns")
	}
	handoff.ImplementationReady = len(handoff.Blockers) == 0
	return handoff
}

func summarizeContextViews(evidence []Evidence) []contextViewSummary {
	out := make([]contextViewSummary, 0)
	for _, ev := range evidence {
		if ev.Kind != "context_view" || strings.TrimSpace(ev.Content) == "" || strings.TrimSpace(ev.Error) != "" {
			continue
		}
		view := contextViewSummary{Label: evidenceName(ev), Path: ev.Path}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.Content), &payload); err != nil {
			view.ParseError = err.Error()
			out = append(out, view)
			continue
		}
		view.PackID = stringField(payload, "pack_id")
		view.IndexID = stringField(payload, "index_id")
		view.GeneratedAt = stringField(payload, "generated_at")
		view.Fresh = boolField(payload, "fresh")
		view.CandidateFiles = parseContextCandidateFiles(payload["candidate_files"])
		view.CandidateSymbols = parseContextCandidateSymbols(payload["candidate_symbols"])
		view.Tests = parseContextCandidateTests(payload["tests"])
		view.Risks = stringSliceField(payload, "risks")
		view.Unknowns = stringSliceField(payload, "unknowns")
		view.UnresolvedGaps = stringSliceField(payload, "unresolved_gaps")
		view.StaleNodes = append(view.StaleNodes, stringSliceField(payload, "stale_files")...)
		view.StaleNodes = append(view.StaleNodes, stringSliceField(payload, "missing_files")...)
		for _, file := range view.CandidateFiles {
			if file.Stale {
				view.StaleNodes = append(view.StaleNodes, file.Path)
			}
		}
		for _, symbol := range view.CandidateSymbols {
			if symbol.Stale {
				view.StaleNodes = append(view.StaleNodes, firstNonEmpty(symbol.ID, symbol.Path))
			}
		}
		out = append(out, view)
	}
	return out
}

func testsFromContextViews(views []contextViewSummary) []CommandRef {
	out := make([]CommandRef, 0)
	for _, view := range views {
		for _, test := range view.Tests {
			if test.Command != "" {
				out = append(out, CommandRef{Command: test.Command, Reason: firstNonEmpty(test.Reason, "context view validation candidate")})
			}
		}
	}
	return out
}

func unknownsFromContextViews(views []contextViewSummary) []string {
	out := make([]string, 0)
	for _, view := range views {
		out = append(out, view.Unknowns...)
		out = append(out, view.UnresolvedGaps...)
		if view.ParseError != "" {
			out = append(out, "context view "+view.Label+" could not be parsed: "+view.ParseError)
		}
	}
	return cleanStrings(out)
}

func risksFromContextViews(views []contextViewSummary) []Risk {
	out := make([]Risk, 0)
	for _, view := range views {
		for _, risk := range view.Risks {
			out = append(out, Risk{Name: risk})
		}
	}
	return out
}

func parseContextCandidateFiles(value any) []contextCandidateFile {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]contextCandidateFile, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, contextCandidateFile{
			Path:       stringField(m, "path"),
			Role:       stringField(m, "role"),
			Reason:     stringField(m, "reason"),
			Language:   stringField(m, "language"),
			Confidence: stringField(m, "confidence"),
			Stale:      boolFieldValue(m, "stale"),
			Handles:    stringSliceField(m, "handles"),
		})
	}
	return out
}

func parseContextCandidateSymbols(value any) []contextCandidateSymbol {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]contextCandidateSymbol, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, contextCandidateSymbol{
			ID:            stringField(m, "id"),
			Path:          stringField(m, "path"),
			Kind:          stringField(m, "kind"),
			Language:      stringField(m, "language"),
			QualifiedName: stringField(m, "qualified_name"),
			Confidence:    stringField(m, "confidence"),
			Stale:         boolFieldValue(m, "stale"),
			Reason:        stringField(m, "reason"),
		})
	}
	return out
}

func parseContextCandidateTests(value any) []contextCandidateTest {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]contextCandidateTest, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, contextCandidateTest{
			Command:  stringField(m, "command"),
			Path:     stringField(m, "path"),
			Symbol:   stringField(m, "symbol"),
			Reason:   stringField(m, "reason"),
			Required: boolFieldValue(m, "required"),
		})
	}
	return out
}

func stringField(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return strings.TrimSpace(value)
}

func boolField(m map[string]any, key string) *bool {
	value, ok := m[key].(bool)
	if !ok {
		return nil
	}
	return &value
}

func boolFieldValue(m map[string]any, key string) bool {
	value, ok := m[key].(bool)
	return ok && value
}

func stringSliceField(m map[string]any, key string) []string {
	values, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if ok && strings.TrimSpace(text) != "" {
			out = append(out, strings.TrimSpace(text))
		}
	}
	return out
}

func contextViewHandle(view contextViewSummary) string {
	if view.PackID != "" {
		return "context_pack://" + view.PackID
	}
	if view.Path != "" {
		return "file://" + view.Path
	}
	return ""
}

func stableHandlePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unspecified"
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func gitOutput(root string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Clean(root)
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func precisionTier(confidence string) string {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "exact_source", "exact_tool_output", "exact_input":
		return "exact_source"
	case "precise_backend":
		return "precise_backend"
	case "syntax_backend":
		return "syntax_backend"
	case "model_summary":
		return "model_summary"
	case "stale":
		return "stale"
	default:
		if strings.Contains(strings.ToLower(confidence), "precise") {
			return "precise_backend"
		}
		if strings.Contains(strings.ToLower(confidence), "syntax") {
			return "syntax_backend"
		}
		return "heuristic"
	}
}

func precisionRank(confidence string) int {
	switch precisionTier(confidence) {
	case "exact_source":
		return 5
	case "precise_backend":
		return 4
	case "syntax_backend":
		return 3
	case "heuristic":
		return 2
	case "model_summary":
		return 1
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func pluralCount(count int, label string) string {
	if count == 1 {
		return "1 " + label
	}
	return strconv.Itoa(count) + " " + label + "s"
}
