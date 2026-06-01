package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/discovery"
)

const (
	dependencyGraphFileLimit = 200
	HistoryNotApplicableCode = "history_not_applicable"
)

type historyNotApplicableOutput struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
	Path   string `json:"path"`
}

var (
	tsImportRe     = regexp.MustCompile(`(?m)(?:import|export)\s+(?:[^"'` + "`" + `]*?\s+from\s+)?["']([^"']+)["']|require\(\s*["']([^"']+)["']\s*\)`)
	pythonImportRe = regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z0-9_., ]+)|^\s*from\s+([A-Za-z0-9_.]+)\s+import\s+`)
)

func (m *Manager) structuralSearch(ctx context.Context, req ToolRequest) ToolResult {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" {
		return ToolResult{Tool: "structural_search", Summary: "Structural search rejected", Error: "pattern is required"}
	}
	bin := firstAvailableBinary("ast-grep", "sg")
	if bin == "" {
		return ToolResult{Tool: "structural_search", Summary: "Structural search unavailable", Error: "ast-grep is not installed"}
	}
	target, err := m.resolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: "structural_search", Summary: "Failed to resolve path", Error: err.Error()}
	}
	args := []string{"--pattern", pattern, "--json"}
	if lang := strings.TrimSpace(req.Language); lang != "" {
		args = append(args, "--lang", lang)
	}
	args = append(args, target)
	output, execErr := m.runCommand(ctx, m.cfg.Root, bin, args...)
	result := ToolResult{
		Tool:    "structural_search",
		Summary: fmt.Sprintf("Ran structural search under %s", m.displayPath(target)),
		Output:  strings.TrimSpace(output),
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) dependencyGraph(ctx context.Context, req ToolRequest) ToolResult {
	target, err := m.resolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: "dependency_graph", Summary: "Failed to resolve path", Error: err.Error()}
	}
	lang := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.Language)), ".")
	if lang == "" {
		lang = m.inferDependencyLanguage(target)
	}
	switch lang {
	case "go", "golang":
		return m.goDependencyGraph(ctx, target)
	case "rust", "rs":
		return m.rustDependencyGraph(ctx, target)
	case "typescript", "ts", "tsx", "mts", "cts", "d.ts", "d.mts", "d.cts", "javascript", "js", "jsx", "mjs", "cjs":
		return m.tsDependencyGraph(ctx, target)
	case "python", "py":
		return m.pythonDependencyGraph(ctx, target)
	case "java":
		return m.javaDependencyGraph(ctx, target)
	default:
		return ToolResult{Tool: "dependency_graph", Summary: "Dependency graph unavailable", Error: "could not infer ecosystem; pass language as go, rust, typescript, python, or java"}
	}
}

func (m *Manager) historyRange(ctx context.Context, req ToolRequest) ToolResult {
	rel, err := m.gitRelPath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "history_range", Summary: "Failed to resolve path", Error: err.Error()}
	}
	if req.StartLine <= 0 || req.EndLine <= 0 || req.StartLine > req.EndLine {
		return ToolResult{Tool: "history_range", Summary: "History query rejected", Error: "start_line and end_line are required and start_line must be <= end_line"}
	}
	if result, handled := m.gitHistoryApplicability(ctx, "history_range", rel); handled {
		return result
	}
	spec := fmt.Sprintf("%d,%d:%s", req.StartLine, req.EndLine, rel)
	output, execErr := m.runCommand(ctx, m.cfg.Root, "git", "log", "--no-color", "--decorate=short", "-L", spec)
	result := ToolResult{Tool: "history_range", Summary: fmt.Sprintf("Read git history for %s lines %d-%d", rel, req.StartLine, req.EndLine), Output: strings.TrimSpace(output)}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) historySymbol(ctx context.Context, req ToolRequest) ToolResult {
	rel, err := m.gitRelPath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "history_symbol", Summary: "Failed to resolve path", Error: err.Error()}
	}
	symbol := strings.TrimSpace(req.Pattern)
	if symbol == "" {
		return ToolResult{Tool: "history_symbol", Summary: "History query rejected", Error: "pattern is required"}
	}
	if result, handled := m.gitHistoryApplicability(ctx, "history_symbol", rel); handled {
		return result
	}
	spec := fmt.Sprintf(":%s:%s", symbol, rel)
	output, execErr := m.runCommand(ctx, m.cfg.Root, "git", "log", "--no-color", "--decorate=short", "-L", spec)
	result := ToolResult{Tool: "history_symbol", Summary: fmt.Sprintf("Read git history for %s in %s", symbol, rel), Output: strings.TrimSpace(output)}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) blameRange(ctx context.Context, req ToolRequest) ToolResult {
	rel, err := m.gitRelPath(req.Path, req.Workdir)
	if err != nil {
		return ToolResult{Tool: "blame_range", Summary: "Failed to resolve path", Error: err.Error()}
	}
	if result, handled := m.gitHistoryApplicability(ctx, "blame_range", rel); handled {
		return result
	}
	args := []string{"blame", "--date=short"}
	if req.StartLine > 0 || req.EndLine > 0 {
		start := req.StartLine
		end := req.EndLine
		if start <= 0 {
			start = 1
		}
		if end > 0 && start > end {
			return ToolResult{Tool: "blame_range", Summary: "Blame query rejected", Error: "start_line must be <= end_line"}
		}
		if end > 0 {
			args = append(args, "-L", fmt.Sprintf("%d,%d", start, end))
		} else {
			args = append(args, "-L", fmt.Sprintf("%d,+80", start))
		}
	}
	args = append(args, "--", rel)
	output, execErr := m.runCommand(ctx, m.cfg.Root, "git", args...)
	result := ToolResult{Tool: "blame_range", Summary: fmt.Sprintf("Read git blame for %s", rel), Output: strings.TrimSpace(output)}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) gitHistoryApplicability(ctx context.Context, tool string, rel string) (ToolResult, bool) {
	abs := filepath.Join(m.cfg.Root, filepath.FromSlash(rel))
	info, err := os.Stat(abs)
	if err != nil {
		return ToolResult{Tool: tool, Summary: "Failed to stat path", Error: fmt.Sprintf("path %q does not exist in this workspace: %v", rel, err)}, true
	}
	if info.IsDir() {
		return ToolResult{Tool: tool, Summary: "History query rejected", Error: fmt.Sprintf("path %q is a directory; history tools require a file path", rel)}, true
	}
	if firstAvailableBinary("git") == "" {
		return ToolResult{Tool: tool, Summary: "Git history unavailable", Error: "git is not installed"}, true
	}
	if !hasGitMetadataAncestor(m.cfg.Root) {
		return historyNotApplicableResult(tool, rel, "non_git_root"), true
	}
	if output, err := m.runCommand(ctx, m.cfg.Root, "git", "rev-parse", "--git-dir"); err != nil {
		return gitHistoryFailureResult(tool, "Git history unavailable", output, err), true
	}
	if output, err := m.runCommand(ctx, m.cfg.Root, "git", "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		if strings.TrimSpace(output) == "" {
			return historyNotApplicableResult(tool, rel, "no_head"), true
		}
		return gitHistoryFailureResult(tool, "Git history unavailable", output, err), true
	}
	trackedOutput, err := m.runCommand(ctx, m.cfg.Root, "git", "ls-files", "--", rel)
	if err != nil {
		return gitHistoryFailureResult(tool, "Git history unavailable", trackedOutput, err), true
	}
	if !gitOutputContainsPath(trackedOutput, rel) {
		return historyNotApplicableResult(tool, rel, "untracked_path"), true
	}
	headOutput, err := m.runCommand(ctx, m.cfg.Root, "git", "ls-tree", "--name-only", "-r", "HEAD", "--", rel)
	if err != nil {
		return gitHistoryFailureResult(tool, "Git history unavailable", headOutput, err), true
	}
	if !gitOutputContainsPath(headOutput, rel) {
		return historyNotApplicableResult(tool, rel, "path_no_committed_history"), true
	}
	return ToolResult{}, false
}

func historyNotApplicableResult(tool string, rel string, reason string) ToolResult {
	payload := historyNotApplicableOutput{
		Status: "not_applicable",
		Code:   HistoryNotApplicableCode,
		Reason: reason,
		Path:   rel,
	}
	data, err := json.Marshal(payload)
	output := ""
	if err == nil {
		output = string(data)
	}
	return ToolResult{
		Tool:    tool,
		Code:    HistoryNotApplicableCode,
		Summary: fmt.Sprintf("Git history not applicable for %s", rel),
		Output:  output,
	}
}

func gitHistoryFailureResult(tool string, summary string, output string, err error) ToolResult {
	return ToolResult{
		Tool:    tool,
		Summary: summary,
		Output:  strings.TrimSpace(output),
		Error:   err.Error(),
	}
}

func gitOutputContainsPath(output string, rel string) bool {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(filepath.ToSlash(line)) == rel {
			return true
		}
	}
	return false
}

func (m *Manager) ctagsSymbols(ctx context.Context, req ToolRequest) ToolResult {
	bin := firstAvailableBinary("ctags")
	if bin == "" {
		return ToolResult{Tool: "ctags_symbols", Summary: "Ctags symbol search unavailable", Error: "ctags is not installed"}
	}
	target, err := m.resolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: "ctags_symbols", Summary: "Failed to resolve path", Error: err.Error()}
	}
	output, execErr := m.runCommand(ctx, m.cfg.Root, bin, "--output-format=json", "--fields=+nK", "-f", "-", "-R", target)
	if filter := strings.TrimSpace(req.Pattern); filter != "" {
		output = filterLinesContaining(output, filter)
	}
	result := ToolResult{Tool: "ctags_symbols", Summary: fmt.Sprintf("Indexed symbols under %s with ctags", m.displayPath(target)), Output: strings.TrimSpace(output)}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) semgrepScan(ctx context.Context, req ToolRequest) ToolResult {
	bin := firstAvailableBinary("semgrep")
	if bin == "" {
		return ToolResult{Tool: "semgrep_scan", Summary: "Semgrep scan unavailable", Error: "semgrep is not installed"}
	}
	target, err := m.resolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: "semgrep_scan", Summary: "Failed to resolve path", Error: err.Error()}
	}
	config := strings.TrimSpace(req.Pattern)
	if config == "" {
		config = "auto"
	}
	output, execErr := m.runCommand(ctx, m.cfg.Root, bin, "scan", "--json", "--config", config, target)
	result := ToolResult{Tool: "semgrep_scan", Summary: fmt.Sprintf("Ran Semgrep config %q under %s", config, m.displayPath(target)), Output: strings.TrimSpace(output)}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) joernScan(ctx context.Context, req ToolRequest) ToolResult {
	bin := firstAvailableBinary("joern-scan")
	if bin == "" {
		return ToolResult{Tool: "joern_scan", Summary: "Joern scan unavailable", Error: "joern-scan is not installed"}
	}
	target, err := m.resolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: "joern_scan", Summary: "Failed to resolve path", Error: err.Error()}
	}
	output, execErr := m.runCommand(ctx, m.cfg.Root, bin, "--src", target)
	result := ToolResult{Tool: "joern_scan", Summary: fmt.Sprintf("Ran Joern scan under %s", m.displayPath(target)), Output: strings.TrimSpace(output)}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) goDependencyGraph(ctx context.Context, target string) ToolResult {
	if !m.isGoWorkspace() {
		return ToolResult{Tool: "dependency_graph", Summary: "Go dependency graph unavailable", Error: "go.mod was not found at the workspace root"}
	}
	output, execErr := m.runCommand(ctx, m.cfg.Root, "go", "list", "-deps", "./...")
	result := ToolResult{Tool: "dependency_graph", Summary: "Collected Go dependency graph with go list -deps", Output: strings.TrimSpace(output)}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) rustDependencyGraph(ctx context.Context, target string) ToolResult {
	cargoRoot := nearestAncestorWithFile(target, "Cargo.toml", m.cfg.Root)
	if cargoRoot == "" {
		return ToolResult{Tool: "dependency_graph", Summary: "Rust dependency graph unavailable", Error: "Cargo.toml was not found"}
	}
	output, execErr := m.runCommand(ctx, cargoRoot, "cargo", "tree", "--prefix", "depth")
	result := ToolResult{Tool: "dependency_graph", Summary: fmt.Sprintf("Collected Rust dependency graph in %s", m.displayPath(cargoRoot)), Output: strings.TrimSpace(output)}
	if execErr != nil {
		result.Error = execErr.Error()
	}
	return result
}

func (m *Manager) tsDependencyGraph(ctx context.Context, target string) ToolResult {
	if bin := firstAvailableBinary("madge"); bin != "" {
		output, execErr := m.runCommand(ctx, m.cfg.Root, bin, "--json", target)
		result := ToolResult{Tool: "dependency_graph", Summary: fmt.Sprintf("Collected TypeScript/JavaScript dependency graph with madge under %s", m.displayPath(target)), Output: strings.TrimSpace(output)}
		if execErr != nil {
			result.Error = execErr.Error()
		}
		return result
	}
	return m.regexDependencyGraph(target, "typescript", typescriptDependencyGraphExtensions(), tsImportRe)
}

func typescriptDependencyGraphExtensions() []string {
	return []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}
}

func (m *Manager) pythonDependencyGraph(ctx context.Context, target string) ToolResult {
	if bin := firstAvailableBinary("pydeps"); bin != "" {
		output, execErr := m.runCommand(ctx, m.cfg.Root, bin, "--show-deps", "--no-output", target)
		result := ToolResult{Tool: "dependency_graph", Summary: fmt.Sprintf("Collected Python dependency graph with pydeps under %s", m.displayPath(target)), Output: strings.TrimSpace(output)}
		if execErr != nil {
			result.Error = execErr.Error()
		}
		return result
	}
	return m.regexDependencyGraph(target, "python", []string{".py", ".pyi"}, pythonImportRe)
}

func (m *Manager) javaDependencyGraph(ctx context.Context, target string) ToolResult {
	switch {
	case fileExists(filepath.Join(m.cfg.Root, "mvnw")) || fileExists(filepath.Join(m.cfg.Root, "pom.xml")):
		cmd := "mvn"
		if fileExists(filepath.Join(m.cfg.Root, "mvnw")) {
			cmd = "./mvnw"
		}
		output, execErr := m.runCommand(ctx, m.cfg.Root, cmd, "-q", "dependency:tree")
		result := ToolResult{Tool: "dependency_graph", Summary: "Collected Java dependency graph with Maven", Output: strings.TrimSpace(output)}
		if execErr != nil {
			result.Error = execErr.Error()
		}
		return result
	case fileExists(filepath.Join(m.cfg.Root, "gradlew")) || fileExists(filepath.Join(m.cfg.Root, "build.gradle")) || fileExists(filepath.Join(m.cfg.Root, "build.gradle.kts")):
		cmd := "gradle"
		if fileExists(filepath.Join(m.cfg.Root, "gradlew")) {
			cmd = "./gradlew"
		}
		output, execErr := m.runCommand(ctx, m.cfg.Root, cmd, "dependencies")
		result := ToolResult{Tool: "dependency_graph", Summary: "Collected Java dependency graph with Gradle", Output: strings.TrimSpace(output)}
		if execErr != nil {
			result.Error = execErr.Error()
		}
		return result
	default:
		return ToolResult{Tool: "dependency_graph", Summary: "Java dependency graph unavailable", Error: "pom.xml or Gradle build files were not found"}
	}
}

func (m *Manager) regexDependencyGraph(target string, lang string, extensions []string, re *regexp.Regexp) ToolResult {
	files := collectFilesByExtension(m.cfg.Root, target, extensions, dependencyGraphFileLimit)
	if len(files) == 0 {
		return ToolResult{Tool: "dependency_graph", Summary: fmt.Sprintf("No %s files found for dependency extraction", lang)}
	}
	lines := make([]string, 0)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(m.cfg.Root, file)
		rel = filepath.ToSlash(rel)
		deps := uniqueRegexDeps(string(data), re)
		for _, dep := range deps {
			lines = append(lines, fmt.Sprintf("%s -> %s", rel, dep))
		}
	}
	slices.Sort(lines)
	if len(lines) == 0 {
		return ToolResult{Tool: "dependency_graph", Summary: fmt.Sprintf("No %s imports found in %d file(s)", lang, len(files))}
	}
	return ToolResult{Tool: "dependency_graph", Summary: fmt.Sprintf("Extracted %d %s import edge(s) from %d file(s)", len(lines), lang, len(files)), Output: strings.Join(lines, "\n")}
}

func uniqueRegexDeps(text string, re *regexp.Regexp) []string {
	seen := make(map[string]struct{})
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		for i := 1; i < len(match); i++ {
			for _, dep := range strings.Split(match[i], ",") {
				dep = strings.TrimSpace(dep)
				if dep == "" {
					continue
				}
				if idx := strings.Index(dep, " as "); idx >= 0 {
					dep = strings.TrimSpace(dep[:idx])
				}
				seen[dep] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for dep := range seen {
		out = append(out, dep)
	}
	slices.Sort(out)
	return out
}

func (m *Manager) inferDependencyLanguage(target string) string {
	if fileExists(filepath.Join(m.cfg.Root, "go.mod")) {
		return "go"
	}
	if nearestAncestorWithFile(target, "Cargo.toml", m.cfg.Root) != "" {
		return "rust"
	}
	if fileExists(filepath.Join(m.cfg.Root, "package.json")) || hasExtensionUnder(m.cfg.Root, target, typescriptDependencyGraphExtensions()...) {
		return "typescript"
	}
	if fileExists(filepath.Join(m.cfg.Root, "pyproject.toml")) || fileExists(filepath.Join(m.cfg.Root, "requirements.txt")) || hasExtensionUnder(m.cfg.Root, target, ".py", ".pyi") {
		return "python"
	}
	if fileExists(filepath.Join(m.cfg.Root, "pom.xml")) || fileExists(filepath.Join(m.cfg.Root, "build.gradle")) || fileExists(filepath.Join(m.cfg.Root, "build.gradle.kts")) {
		return "java"
	}
	return ""
}

func collectFilesByExtension(root string, target string, extensions []string, limit int) []string {
	extSet := make(map[string]struct{}, len(extensions))
	for _, ext := range extensions {
		extSet[strings.ToLower(ext)] = struct{}{}
	}
	var files []string
	_ = filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != target && discovery.ShouldSkipPath(root, path, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if discovery.ShouldSkipPath(root, path, false) {
			return nil
		}
		if _, ok := extSet[strings.ToLower(filepath.Ext(path))]; !ok {
			return nil
		}
		files = append(files, path)
		if limit > 0 && len(files) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	slices.Sort(files)
	return files
}

func hasExtensionUnder(root string, target string, extensions ...string) bool {
	return len(collectFilesByExtension(root, target, extensions, 1)) > 0
}

func (m *Manager) gitRelPath(path string, workdir string) (string, error) {
	abs, err := m.resolvePath(path, workdir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(m.cfg.Root, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func hasGitMetadataAncestor(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	dir, err := filepath.Abs(root)
	if err != nil {
		dir = root
	}
	for {
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func firstAvailableBinary(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil && path != "" {
			return path
		}
	}
	return ""
}

func nearestAncestorWithFile(start string, name string, root string) string {
	info, err := os.Stat(start)
	if err == nil && !info.IsDir() {
		start = filepath.Dir(start)
	}
	start = filepath.Clean(start)
	root = filepath.Clean(root)
	for {
		if fileExists(filepath.Join(start, name)) {
			return start
		}
		if start == root || start == "." || start == string(filepath.Separator) {
			return ""
		}
		parent := filepath.Dir(start)
		if parent == start {
			return ""
		}
		start = parent
	}
}

// looksLikeGlob reports whether s contains glob wildcard characters
// (*, ?, [...]) that suggest the caller meant a glob pattern rather
// than a literal path or filename.
func looksLikeGlob(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	return strings.ContainsAny(s, "*?[")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func filterLinesContaining(text string, needle string) string {
	needle = strings.ToLower(needle)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(strings.ToLower(line), needle) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
