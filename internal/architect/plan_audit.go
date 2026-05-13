package architect

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type PlanAuditResult struct {
	OK       bool               `json:"ok"`
	Findings []PlanAuditFinding `json:"findings,omitempty"`
}

type PlanAuditFinding struct {
	Severity            string   `json:"severity"`
	Kind                string   `json:"kind"`
	Message             string   `json:"message"`
	Evidence            []string `json:"evidence,omitempty"`
	SuggestedResolution string   `json:"suggested_resolution,omitempty"`
}

var (
	planPathPattern        = regexp.MustCompile(`(?:^|[\s'"\(\[` + "`" + `])([A-Za-z0-9_./-]+\.(?:go|rs|pyi|py|tsx|tscn|ts|jsx|json|js|java|cxx|cpp|cc|c|hxx|hpp|hh|h|gd|tres|md|ya?ml|toml|sh|sql)(?::\d+)?)(?:$|[\s'"\)\]\},;.` + "`" + `])`)
	planHandlePattern      = regexp.MustCompile(`\b(?:file|symbol|test|trace|context_pack)://[^\s\]\),;` + "`" + `]+`)
	planCommandPattern     = regexp.MustCompile("`([^`]*(?:go test|make test|make build|go build|npm test|pnpm test|yarn test|bash |sh |python |python3 |pytest|godot|scons|\\./scripts/|scripts/)[^`]*)`")
	planPlaceholderPattern = regexp.MustCompile(`<(?:path|count|file|command|test|todo|unknown)>`)
)

func AuditPlan(packet Packet, plan string) PlanAuditResult {
	plan = strings.TrimSpace(plan)
	findings := make([]PlanAuditFinding, 0)
	if plan == "" {
		findings = append(findings, PlanAuditFinding{
			Severity:            "error",
			Kind:                "empty_plan",
			Message:             "candidate plan is empty",
			SuggestedResolution: "Provide the model-authored plan text before running plan audit.",
		})
		return PlanAuditResult{OK: false, Findings: findings}
	}
	findings = append(findings, auditPlanPaths(packet, plan)...)
	findings = append(findings, auditPlanHandles(packet, plan)...)
	findings = append(findings, auditPlanValidation(packet, plan)...)
	findings = append(findings, auditPlanCommandReferences(packet, plan)...)
	findings = append(findings, auditPlanFreshness(packet, plan)...)
	findings = append(findings, auditProtectedPaths(packet, plan)...)
	findings = append(findings, auditPlanReadiness(packet, plan)...)
	findings = append(findings, auditPlanPlaceholders(plan)...)
	ok := true
	for _, finding := range findings {
		if finding.Severity == "error" {
			ok = false
			break
		}
	}
	return PlanAuditResult{OK: ok, Findings: findings}
}

func auditPlanCommandReferences(packet Packet, plan string) []PlanAuditFinding {
	root := strings.TrimSpace(packet.Workspace.Root)
	if root == "" {
		return nil
	}
	var findings []PlanAuditFinding
	for _, command := range extractPlanCommands(plan) {
		for _, rel := range commandReferencePaths(command) {
			if pathExistsUnderRoot(root, rel) {
				continue
			}
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "unknown_command_path",
				Message:             fmt.Sprintf("plan validation command %q references missing path %q", command, rel),
				Evidence:            []string{command, rel},
				SuggestedResolution: "Correct the command path, add the path as an explicit new artifact, or mark validation blocked with the exact missing command.",
			})
		}
	}
	return findings
}

func auditPlanPlaceholders(plan string) []PlanAuditFinding {
	raw := planPlaceholderPattern.FindAllString(plan, -1)
	if len(raw) == 0 {
		return nil
	}
	placeholders := cleanStrings(raw)
	return []PlanAuditFinding{{
		Severity:            "error",
		Kind:                "unresolved_placeholder",
		Message:             "plan still contains unresolved placeholder token(s)",
		Evidence:            placeholders,
		SuggestedResolution: "Replace placeholders with packet-backed values or mark the exact unresolved fact as a blocker.",
	}}
}

func auditPlanPaths(packet Packet, plan string) []PlanAuditFinding {
	paths := extractPlanPaths(plan)
	if len(paths) == 0 {
		return nil
	}
	roles := packetFileRoles(packet)
	var findings []PlanAuditFinding
	lines := strings.Split(plan, "\n")
	for _, path := range paths {
		cleanPath := strings.TrimSuffix(path, ":")
		if idx := strings.LastIndex(cleanPath, ":"); idx > 1 {
			if allDigits(cleanPath[idx+1:]) {
				cleanPath = cleanPath[:idx]
			}
		}
		if pathAllowedAsNew(cleanPath, roles, lines) {
			continue
		}
		if fileExistsUnderRoot(packet.Workspace.Root, cleanPath) {
			continue
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            "error",
			Kind:                "missing_file",
			Message:             fmt.Sprintf("plan cites %q, but that file does not exist and is not marked as new", cleanPath),
			Evidence:            []string{cleanPath},
			SuggestedResolution: "Mark the file as new/create in the plan, correct the path, or remove the unsupported reference.",
		})
	}
	return findings
}

func auditPlanHandles(packet Packet, plan string) []PlanAuditFinding {
	known := packetHandles(packet)
	raw := planHandlePattern.FindAllString(plan, -1)
	if len(raw) == 0 {
		return nil
	}
	var findings []PlanAuditFinding
	for _, handle := range cleanStrings(raw) {
		if _, ok := known[handle]; ok {
			continue
		}
		if lineFor(plan, handle, mentionsUnresolved) {
			continue
		}
		findings = append(findings, PlanAuditFinding{
			Severity:            "warning",
			Kind:                "unresolved_handle",
			Message:             fmt.Sprintf("plan cites handle %q that is not present in the packet evidence or candidate scope", handle),
			Evidence:            []string{handle},
			SuggestedResolution: "Use a packet handle, mark the handle unresolved, or remove the unsupported citation.",
		})
	}
	return findings
}

func auditPlanValidation(packet Packet, plan string) []PlanAuditFinding {
	var findings []PlanAuditFinding
	if len(packet.Tests) > 0 {
		mentioned := false
		for _, test := range packet.Tests {
			if strings.Contains(plan, test.Command) {
				mentioned = true
				break
			}
		}
		if !mentioned {
			findings = append(findings, PlanAuditFinding{
				Severity:            "warning",
				Kind:                "validation_gap",
				Message:             "packet lists validation commands, but the plan does not mention any of them",
				Evidence:            packetTestCommands(packet),
				SuggestedResolution: "Carry at least one packet validation command into the plan or explain why validation is blocked.",
			})
		}
	}
	for _, command := range extractPlanCommands(plan) {
		if !looksScopedCommand(command) {
			findings = append(findings, PlanAuditFinding{
				Severity:            "warning",
				Kind:                "unscoped_validation",
				Message:             fmt.Sprintf("plan lists broad or ambiguous validation command %q", command),
				Evidence:            []string{command},
				SuggestedResolution: "Prefer the narrowest package or target command that exercises the changed behavior.",
			})
		}
	}
	return findings
}

func auditPlanFreshness(packet Packet, plan string) []PlanAuditFinding {
	if packet.Freshness == nil || packet.Freshness.Fresh == nil || *packet.Freshness.Fresh {
		return nil
	}
	lower := strings.ToLower(plan)
	if strings.Contains(lower, "stale") || strings.Contains(lower, "refresh") {
		return nil
	}
	return []PlanAuditFinding{{
		Severity:            "warning",
		Kind:                "stale_fact",
		Message:             "packet contains stale context evidence, but the plan does not label or refresh it",
		Evidence:            append([]string(nil), packet.Freshness.StaleNodes...),
		SuggestedResolution: "Label stale evidence in the plan or add a refresh step before relying on it.",
	}}
}

func auditProtectedPaths(packet Packet, plan string) []PlanAuditFinding {
	if packet.Contracts == nil || len(packet.Contracts.ProtectedPaths) == 0 {
		return nil
	}
	var findings []PlanAuditFinding
	for _, protected := range packet.Contracts.ProtectedPaths {
		if protected == "" {
			continue
		}
		if lineFor(plan, protected, mentionsEditVerb) {
			findings = append(findings, PlanAuditFinding{
				Severity:            "error",
				Kind:                "protected_path",
				Message:             fmt.Sprintf("plan proposes changing protected path %q", protected),
				Evidence:            []string{protected},
				SuggestedResolution: "Remove the protected-path edit or get an explicit packet permission before implementation.",
			})
		}
	}
	return findings
}

func auditPlanReadiness(packet Packet, plan string) []PlanAuditFinding {
	lower := strings.ToLower(plan)
	var findings []PlanAuditFinding
	if taskSuggestsImplementation(packet.Task) && len(extractPlanPaths(plan)) == 0 && len(packet.Files) > 0 {
		findings = append(findings, PlanAuditFinding{
			Severity:            "warning",
			Kind:                "edit_readiness",
			Message:             "task appears to ask for edits, but the plan does not name a file to change",
			SuggestedResolution: "Add a bounded implementation atom with the file path, or state the concrete blocker.",
		})
	}
	if (strings.Contains(lower, "inspect more") || strings.Contains(lower, "look into more") || strings.Contains(lower, "need more context")) && !mentionsUnresolved(lower) {
		findings = append(findings, PlanAuditFinding{
			Severity:            "warning",
			Kind:                "missing_exact_gap",
			Message:             "plan defers to more inspection without naming an exact missing claim",
			SuggestedResolution: "Name the missing claim and the evidence category that would resolve it.",
		})
	}
	return findings
}

func extractPlanPaths(plan string) []string {
	matches := planPathPattern.FindAllStringSubmatch(plan, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.Trim(match[1], " \t\n\r'\"`()[]{}.,;")
		if path != "" {
			out = append(out, path)
		}
	}
	return cleanStrings(out)
}

func extractPlanCommands(plan string) []string {
	matches := planCommandPattern.FindAllStringSubmatch(plan, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) >= 2 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	return cleanStrings(out)
}

func packetFileRoles(packet Packet) map[string]string {
	out := map[string]string{}
	for _, file := range packet.Files {
		out[file.Path] = strings.ToLower(file.Role)
	}
	return out
}

func packetHandles(packet Packet) map[string]struct{} {
	out := map[string]struct{}{}
	for _, file := range packet.Files {
		if file.Path != "" {
			out["file://"+file.Path] = struct{}{}
		}
	}
	for _, ev := range packet.Evidence {
		if ev.Handle != "" {
			out[ev.Handle] = struct{}{}
		}
		if ev.Path != "" {
			out["file://"+ev.Path] = struct{}{}
		}
	}
	for _, item := range packet.CandidateScope {
		if item.Handle != "" {
			out[item.Handle] = struct{}{}
		}
		if item.Path != "" {
			out["file://"+item.Path] = struct{}{}
		}
	}
	if packet.Freshness != nil {
		for _, view := range packet.Freshness.ContextViews {
			if view.PackID != "" {
				out["context_pack://"+view.PackID] = struct{}{}
			}
			if view.Path != "" {
				out["file://"+view.Path] = struct{}{}
			}
		}
	}
	return out
}

func packetTestCommands(packet Packet) []string {
	out := make([]string, 0, len(packet.Tests))
	for _, test := range packet.Tests {
		out = append(out, test.Command)
	}
	sort.Strings(out)
	return out
}

func pathAllowedAsNew(path string, roles map[string]string, lines []string) bool {
	role := roles[path]
	if strings.Contains(role, "create") || strings.Contains(role, "new") || strings.Contains(role, "add") {
		return true
	}
	for _, line := range lines {
		if !strings.Contains(line, path) {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "create") || strings.Contains(lower, "new file") || strings.Contains(lower, "add ") {
			return true
		}
	}
	return false
}

func fileExistsUnderRoot(root string, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	full, ok := pathUnderRoot(root, path)
	if !ok {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && !info.IsDir()
}

func pathExistsUnderRoot(root string, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	full, ok := pathUnderRoot(root, path)
	if !ok {
		return false
	}
	_, err := os.Stat(full)
	return err == nil
}

func pathUnderRoot(root string, path string) (string, bool) {
	cleanRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", false
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(cleanRoot, path)
	}
	cleanFull, err := filepath.Abs(filepath.Clean(full))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(cleanRoot, cleanFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return cleanFull, true
}

func commandReferencePaths(command string) []string {
	fields := strings.Fields(command)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.Trim(field, " \t\n\r'\"`")
		if token == "" || strings.HasPrefix(token, "-") || strings.Contains(token, "...") {
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "./") && !strings.HasPrefix(token, "scripts/") {
			continue
		}
		token = strings.TrimPrefix(token, "./")
		if token == "" || token == "." || token == ".." || filepath.IsAbs(token) || strings.HasPrefix(token, "../") || strings.Contains(token, "://") {
			continue
		}
		if commandTokenIsWorkspaceReference(token) {
			out = append(out, token)
		}
	}
	return cleanStrings(out)
}

func commandTokenIsWorkspaceReference(token string) bool {
	if strings.HasPrefix(token, "scripts/") {
		return true
	}
	for _, prefix := range []string{"cmd/", "internal/", "pkg/", "native/", "test/", "tests/"} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	if !strings.Contains(token, "/") {
		return false
	}
	switch strings.ToLower(filepath.Ext(token)) {
	case ".bash", ".gd", ".go", ".js", ".json", ".md", ".py", ".rs", ".sh", ".ts", ".yaml", ".yml":
		return true
	}
	return false
}

func lineFor(plan string, needle string, predicate func(string) bool) bool {
	for _, line := range strings.Split(plan, "\n") {
		if strings.Contains(line, needle) && predicate(line) {
			return true
		}
	}
	return false
}

func mentionsEditVerb(line string) bool {
	lower := strings.ToLower(line)
	for _, verb := range []string{"edit", "modify", "change", "write", "delete", "remove", "replace", "patch"} {
		if strings.Contains(lower, verb) {
			return true
		}
	}
	return false
}

func mentionsUnresolved(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "unresolved") || strings.Contains(lower, "missing evidence") || strings.Contains(lower, "missing claim") || strings.Contains(lower, "blocked")
}

func taskSuggestsImplementation(task string) bool {
	lower := strings.ToLower(task)
	for _, word := range []string{"implement", "fix", "change", "add", "remove", "refactor", "begin on", "build"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func looksScopedCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if strings.Contains(command, "./...") {
		return false
	}
	for _, scoped := range []string{" ./", " /", " internal/", " cmd/", " pkg/", " --run ", " -run ", " TEST=", " LOG="} {
		if strings.Contains(command, scoped) {
			return true
		}
	}
	return strings.HasPrefix(command, "make ")
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
