package architect

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"idiom/internal/platform/util"
)

const (
	SchemaVersion           = "idiom.architect.packet.v1"
	PlanningPacketV2Version = "idiom.architect.planning_packet.v2"
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

type Profile struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	Rules              []RuleSource      `json:"rules,omitempty"`
	Collectors         []Collector       `json:"collectors,omitempty"`
	Gates              []Gate            `json:"gates,omitempty"`
	Roles              []Role            `json:"roles,omitempty"`
	Constraints        []string          `json:"constraints,omitempty"`
	Files              []FileRef         `json:"files,omitempty"`
	Tests              []CommandRef      `json:"tests,omitempty"`
	PromptInstructions []string          `json:"prompt_instructions,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

type RuleSource struct {
	Label    string `json:"label,omitempty"`
	Path     string `json:"path"`
	Required bool   `json:"required,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

type Collector struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Required       bool     `json:"required,omitempty"`
	MaxBytes       int      `json:"max_bytes,omitempty"`
}

type Gate struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type Role struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Model       string `json:"model,omitempty"`
}

type FileRef struct {
	Path   string `json:"path"`
	Role   string `json:"role,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type CommandRef struct {
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type Anchor struct {
	Path   string `json:"path,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	Lines  string `json:"lines,omitempty"`
	Why    string `json:"why,omitempty"`
}

type Evidence struct {
	Kind     string `json:"kind"`
	Label    string `json:"label,omitempty"`
	Path     string `json:"path,omitempty"`
	Handle   string `json:"handle,omitempty"`
	Command  string `json:"command,omitempty"`
	Required bool   `json:"required,omitempty"`
	Content  string `json:"content,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Risk struct {
	Name       string `json:"name"`
	Mitigation string `json:"mitigation,omitempty"`
}

type Packet struct {
	SchemaVersion        string                     `json:"schema_version"`
	CreatedAt            time.Time                  `json:"created_at"`
	Task                 string                     `json:"task"`
	Profile              ProfileSummary             `json:"profile"`
	Workspace            WorkspaceSummary           `json:"workspace"`
	Contracts            *PlanningContracts         `json:"contracts,omitempty"`
	RepositoryView       []RepositoryViewItem       `json:"repository_view,omitempty"`
	CandidateScope       []RankedScopeItem          `json:"candidate_scope,omitempty"`
	Constraints          []string                   `json:"constraints,omitempty"`
	Files                []FileRef                  `json:"files,omitempty"`
	Tests                []CommandRef               `json:"tests,omitempty"`
	Anchors              []Anchor                   `json:"anchors,omitempty"`
	Evidence             []Evidence                 `json:"evidence,omitempty"`
	Freshness            *FreshnessReport           `json:"freshness,omitempty"`
	LanguageCapabilities []LanguageCapabilityReport `json:"language_capabilities,omitempty"`
	Handoff              *PlanningHandoff           `json:"handoff,omitempty"`
	ContextBudget        *ContextBudgetReport       `json:"context_budget,omitempty"`
	Risks                []Risk                     `json:"risks,omitempty"`
	Unknowns             []string                   `json:"unknowns,omitempty"`
	Gates                []Gate                     `json:"gates,omitempty"`
	Roles                []Role                     `json:"roles,omitempty"`
	Metadata             map[string]string          `json:"metadata,omitempty"`
}

type ProfileSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
}

type WorkspaceSummary struct {
	Root string `json:"root"`
	CWD  string `json:"cwd,omitempty"`
}

type BuildOptions struct {
	Root           string
	CWD            string
	Task           string
	Schema         string
	BudgetTier     string
	ProfilePath    string
	RunCollectors  bool
	MaxRuleBytes   int
	MaxOutputBytes int
	Constraints    []string
	Files          []FileRef
	Tests          []CommandRef
	Anchors        []Anchor
	Risks          []Risk
	Unknowns       []string
	ContextViews   []string
	Metadata       map[string]string
	Now            time.Time
}

func BuildPacket(ctx context.Context, opts BuildOptions) (Packet, error) {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return Packet{}, err
	}
	cwd := strings.TrimSpace(opts.CWD)
	if cwd == "" {
		cwd = root
	}
	profile, profilePath, err := LoadProfile(root, opts.ProfilePath)
	if err != nil {
		return Packet{}, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	schemaVersion, err := normalizePacketSchema(opts.Schema)
	if err != nil {
		return Packet{}, err
	}
	packet := Packet{
		SchemaVersion: schemaVersion,
		CreatedAt:     now,
		Task:          strings.TrimSpace(opts.Task),
		Profile: ProfileSummary{
			Name:        strings.TrimSpace(profile.Name),
			Description: strings.TrimSpace(profile.Description),
			Path:        profilePath,
		},
		Workspace:   WorkspaceSummary{Root: root, CWD: cwd},
		Constraints: mergeStrings(profile.Constraints, opts.Constraints),
		Files:       mergeFiles(profile.Files, opts.Files),
		Tests:       mergeCommands(profile.Tests, opts.Tests),
		Anchors:     cleanAnchors(opts.Anchors),
		Risks:       cleanRisks(opts.Risks),
		Unknowns:    cleanStrings(opts.Unknowns),
		Gates:       cleanGates(profile.Gates),
		Roles:       cleanRoles(profile.Roles),
		Metadata:    mergeMetadata(profile.Metadata, opts.Metadata),
	}
	packet.Evidence = append(packet.Evidence, readRuleEvidence(root, profile.Rules, opts.MaxRuleBytes)...)
	packet.Evidence = append(packet.Evidence, readContextViewEvidence(root, opts.ContextViews, opts.MaxOutputBytes)...)
	if opts.RunCollectors {
		packet.Evidence = append(packet.Evidence, runCollectorEvidence(ctx, root, profile.Collectors, opts.MaxOutputBytes)...)
	} else {
		for _, collector := range profile.Collectors {
			packet.Evidence = append(packet.Evidence, Evidence{
				Kind:     "collector_pending",
				Label:    strings.TrimSpace(collector.Name),
				Command:  renderCommand(collector.Command, collector.Args),
				Required: collector.Required,
				Content:  strings.TrimSpace(collector.Description),
			})
		}
	}
	if packet.Profile.Name == "" {
		packet.Profile.Name = "default"
	}
	if packet.Task == "" {
		return Packet{}, fmt.Errorf("task is required")
	}
	if packet.SchemaVersion == PlanningPacketV2Version {
		enrichPlanningPacketV2(&packet, PlanningPacketV2Options{BudgetTier: opts.BudgetTier, Now: now})
	}
	return packet, nil
}

func normalizePacketSchema(schema string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(schema)) {
	case "", "v1", "packet_v1", "architect_packet_v1", SchemaVersion:
		return SchemaVersion, nil
	case "v2", "planning_v2", "planning-packet-v2", "planning_packet_v2", PlanningPacketV2Version:
		return PlanningPacketV2Version, nil
	default:
		return "", fmt.Errorf("unknown architect packet schema %q", schema)
	}
}

func LoadProfile(root string, profilePath string) (Profile, string, error) {
	path := strings.TrimSpace(profilePath)
	if path == "" {
		for _, candidate := range []string{
			filepath.Join(root, ".idiom", "architect", "profile.json"),
			filepath.Join(root, ".idiom", "architecture", "profile.json"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
		if path == "" {
			return Profile{Name: "default"}, "", nil
		}
	} else if !filepath.IsAbs(path) {
		named := filepath.Join(root, ".idiom", "architect", "profiles", path+".json")
		if _, err := os.Stat(named); err == nil {
			path = named
		} else {
			path = filepath.Join(root, path)
		}
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Profile{}, "", fmt.Errorf("read architect profile: %w", err)
	}
	var profile Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return Profile{}, "", fmt.Errorf("parse architect profile %s: %w", path, err)
	}
	if profile.Name == "" {
		profile.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return profile, path, nil
}

func readRuleEvidence(root string, rules []RuleSource, defaultMaxBytes int) []Evidence {
	out := make([]Evidence, 0, len(rules))
	for _, rule := range rules {
		path := strings.TrimSpace(rule.Path)
		if path == "" {
			continue
		}
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, full)
		}
		label := strings.TrimSpace(rule.Label)
		if label == "" {
			label = path
		}
		data, err := os.ReadFile(filepath.Clean(full))
		ev := Evidence{Kind: "rule", Label: label, Path: path, Required: rule.Required}
		if err != nil {
			ev.Error = err.Error()
			out = append(out, ev)
			continue
		}
		limit := rule.MaxBytes
		if limit <= 0 {
			limit = defaultMaxBytes
		}
		ev.Content = util.TruncateBytes(string(data), limit, "\n[truncated]")
		out = append(out, ev)
	}
	return out
}

func readContextViewEvidence(root string, paths []string, defaultMaxBytes int) []Evidence {
	out := make([]Evidence, 0, len(paths))
	for _, value := range paths {
		path := strings.TrimSpace(value)
		if path == "" {
			continue
		}
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, full)
		}
		ev := Evidence{Kind: "context_view", Label: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Path: path}
		data, err := os.ReadFile(filepath.Clean(full))
		if err != nil {
			ev.Error = err.Error()
			out = append(out, ev)
			continue
		}
		ev.Content = util.TruncateBytes(string(data), defaultMaxBytes, "\n[truncated]")
		out = append(out, ev)
	}
	return out
}

func runCollectorEvidence(ctx context.Context, root string, collectors []Collector, defaultMaxBytes int) []Evidence {
	out := make([]Evidence, 0, len(collectors))
	for _, collector := range collectors {
		name := strings.TrimSpace(collector.Name)
		if name == "" {
			name = strings.TrimSpace(collector.Command)
		}
		ev := Evidence{Kind: "collector", Label: name, Command: renderCommand(collector.Command, collector.Args), Required: collector.Required}
		if strings.TrimSpace(collector.Command) == "" {
			ev.Error = "collector command is required"
			out = append(out, ev)
			continue
		}
		timeout := time.Duration(collector.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		cmd := exec.CommandContext(runCtx, collector.Command, collector.Args...)
		if collector.Cwd != "" {
			if filepath.IsAbs(collector.Cwd) {
				cmd.Dir = collector.Cwd
			} else {
				cmd.Dir = filepath.Join(root, collector.Cwd)
			}
		} else {
			cmd.Dir = root
		}
		outBytes, err := cmd.CombinedOutput()
		cancel()
		limit := collector.MaxBytes
		if limit <= 0 {
			limit = defaultMaxBytes
		}
		ev.Content = util.TruncateBytes(stripANSI(string(outBytes)), limit, "\n[truncated]")
		if err != nil {
			if runCtx.Err() == context.DeadlineExceeded {
				ev.Error = fmt.Sprintf("collector timed out after %s", timeout)
			} else {
				ev.Error = err.Error()
			}
		}
		out = append(out, ev)
	}
	return out
}

func stripANSI(s string) string {
	return ansiEscapePattern.ReplaceAllString(s, "")
}

func resolveRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get workspace root: %w", err)
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	return filepath.Clean(abs), nil
}

func renderCommand(command string, args []string) string {
	parts := append([]string{strings.TrimSpace(command)}, args...)
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, " ")
}



func mergeStrings(a []string, b []string) []string {
	return cleanStrings(append(append([]string(nil), a...), b...))
}

func cleanStrings(values []string) []string {
	seen := map[string]struct{}{}
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

func mergeFiles(a []FileRef, b []FileRef) []FileRef {
	return cleanFiles(append(append([]FileRef(nil), a...), b...))
}

func cleanFiles(values []FileRef) []FileRef {
	seen := map[string]struct{}{}
	out := make([]FileRef, 0, len(values))
	for _, value := range values {
		value.Path = strings.TrimSpace(value.Path)
		value.Role = strings.TrimSpace(value.Role)
		value.Reason = strings.TrimSpace(value.Reason)
		if value.Path == "" {
			continue
		}
		key := value.Path + "\x00" + value.Role
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mergeCommands(a []CommandRef, b []CommandRef) []CommandRef {
	return cleanCommands(append(append([]CommandRef(nil), a...), b...))
}

func cleanCommands(values []CommandRef) []CommandRef {
	seen := map[string]struct{}{}
	out := make([]CommandRef, 0, len(values))
	for _, value := range values {
		value.Command = strings.TrimSpace(value.Command)
		value.Reason = strings.TrimSpace(value.Reason)
		if value.Command == "" {
			continue
		}
		if _, ok := seen[value.Command]; ok {
			continue
		}
		seen[value.Command] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cleanAnchors(values []Anchor) []Anchor {
	out := make([]Anchor, 0, len(values))
	for _, value := range values {
		value.Path = strings.TrimSpace(value.Path)
		value.Symbol = strings.TrimSpace(value.Symbol)
		value.Lines = strings.TrimSpace(value.Lines)
		value.Why = strings.TrimSpace(value.Why)
		if value.Path == "" && value.Symbol == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func cleanRisks(values []Risk) []Risk {
	out := make([]Risk, 0, len(values))
	for _, value := range values {
		value.Name = strings.TrimSpace(value.Name)
		value.Mitigation = strings.TrimSpace(value.Mitigation)
		if value.Name == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func cleanGates(values []Gate) []Gate {
	out := make([]Gate, 0, len(values))
	for _, value := range values {
		value.Name = strings.TrimSpace(value.Name)
		value.Description = strings.TrimSpace(value.Description)
		if value.Name == "" {
			continue
		}
		out = append(out, value)
	}
	slices.SortFunc(out, func(a, b Gate) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

func cleanRoles(values []Role) []Role {
	out := make([]Role, 0, len(values))
	for _, value := range values {
		value.Name = strings.TrimSpace(value.Name)
		value.Description = strings.TrimSpace(value.Description)
		value.Model = strings.TrimSpace(value.Model)
		if value.Name == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func mergeMetadata(a map[string]string, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	for k, v := range b {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func EncodePacket(packet Packet) ([]byte, error) {
	data, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
