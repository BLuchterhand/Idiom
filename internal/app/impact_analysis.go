package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"qwen-bubbles/internal/frontier"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
)

type writeApprovalRisk string

const (
	writeApprovalRiskLow      writeApprovalRisk = "low"
	writeApprovalRiskMedium   writeApprovalRisk = "medium"
	writeApprovalRiskHigh     writeApprovalRisk = "high"
	writeApprovalRiskCritical writeApprovalRisk = "critical"
)

type approvalImpactProfile struct {
	Risk          writeApprovalRisk
	ChangeKinds   []string
	RiskReasons   []string
	ImpactSummary []string
	BlastRadius   []string
	DeepAnalysis  []string
	AffectedTests []string
	ApprovalNotes []string
	VerifySteps   []string
}

type approvalImpactAnalyzer struct {
	ctx      context.Context
	mgr      *workspace.Manager
	proposal *writeApprovalProposal
	req      workspace.ToolRequest
	root     string
	goIndex  *semantic.GoIndex

	profile approvalImpactProfile
}

var impactReferenceExts = map[string]struct{}{
	".go":   {},
	".json": {},
	".md":   {},
	".sh":   {},
	".txt":  {},
	".toml": {},
	".yaml": {},
	".yml":  {},
}

var impactSkippedDirNames = map[string]struct{}{
	".cache":       {},
	".git":         {},
	".hg":          {},
	".svn":         {},
	"bin":          {},
	"build":        {},
	"dist":         {},
	"logs":         {},
	"models":       {},
	"node_modules": {},
	"tmp":          {},
	"transcripts":  {},
	"vendor":       {},
}

func analyzeWriteApprovalProposal(ctx context.Context, mgr *workspace.Manager, proposal *writeApprovalProposal, req workspace.ToolRequest) approvalImpactProfile {
	analyzer := approvalImpactAnalyzer{
		ctx:      ctx,
		mgr:      mgr,
		proposal: proposal,
		req:      req,
		profile: approvalImpactProfile{
			Risk: writeApprovalRiskLow,
		},
	}
	if mgr != nil {
		analyzer.root = strings.TrimSpace(mgr.Root())
		if analyzer.root != "" {
			if idx, err := semantic.BuildGoIndex(analyzer.root); err == nil {
				analyzer.goIndex = idx
			}
		}
	}
	analyzer.analyze()
	return analyzer.finalize()
}

func (a *approvalImpactAnalyzer) analyze() {
	if a.proposal == nil {
		return
	}

	switch a.proposal.Kind {
	case writeApprovalProtectedPath:
		a.addKind("protected-path")
		a.addRisk(writeApprovalRiskMedium, "guarded mode escalated the request because the target path is protected")
	case writeApprovalBroadScope:
		a.addKind("broad-write-scope")
		a.addRisk(writeApprovalRiskHigh, "guarded mode escalated the request because the write scope expanded beyond the default small set")
	}

	if len(a.proposal.Paths) > 1 {
		a.addKind("multi-file-change")
		a.addRisk(writeApprovalRiskMedium, fmt.Sprintf("requested scope spans %d files", len(a.proposal.Paths)))
	}

	topDirs := uniqueTopLevelDirs(a.proposal.Paths)
	if len(topDirs) > 1 {
		a.addKind("cross-area-change")
		a.addRisk(writeApprovalRiskHigh, fmt.Sprintf("scope crosses %d top-level areas: %s", len(topDirs), strings.Join(topDirs, ", ")))
	}

	for _, line := range approvalPathRoleLines(a.proposal.Paths) {
		a.addImpact(line)
	}

	for _, path := range a.proposal.Paths {
		a.analyzePath(normalizeGuardedPath(path))
	}
}

func (a *approvalImpactAnalyzer) analyzePath(path string) {
	if path == "" {
		return
	}

	switch {
	case strings.HasSuffix(path, ".go"):
		a.analyzeGoPath(path)
	case strings.HasSuffix(path, "_test.go"):
		a.analyzeGoPath(path)
	case strings.HasSuffix(path, ".sh"):
		a.analyzeShellPath(path)
	case path == "config.json" || path == "config.example.json":
		a.analyzeConfigPath(path)
	case strings.HasPrefix(path, "prompts/"):
		a.analyzePromptPath(path)
	case strings.HasSuffix(path, ".md"):
		a.analyzeMarkdownPath(path)
	default:
		a.analyzeGenericPath(path)
	}
}

func (a *approvalImpactAnalyzer) analyzeGoPath(path string) {
	a.addKind("go-source")
	if a.goIndex == nil {
		a.addRisk(writeApprovalRiskMedium, fmt.Sprintf("%s is Go source but semantic indexing was unavailable", path))
		a.addVerifyBaselineForRuntimeChange()
		return
	}

	file := a.goIndex.FileByPath(path)
	if file == nil {
		a.addRisk(writeApprovalRiskMedium, fmt.Sprintf("%s is outside the indexed Go workspace view", path))
		a.addVerifyBaselineForRuntimeChange()
		return
	}

	dir := file.PathDir()
	packageLabel := dir
	if packageLabel == "." {
		packageLabel = a.goIndex.Module
		if strings.TrimSpace(packageLabel) == "" {
			packageLabel = "."
		}
	}

	if file.IsTest {
		a.addKind("test-code")
		a.addRisk(writeApprovalRiskLow, fmt.Sprintf("%s is test-only Go code", path))
		a.addImpact(fmt.Sprintf("%s is a test-only Go file in package %s", path, packageLabel))
		a.addAffectedTest(fmt.Sprintf("%s (changed test file %s)", goTestCommandForDir(dir), path))
		a.addVerify(goTestCommandForDir(dir))
		return
	}

	if isCoreGoImpactPath(path) {
		a.addKind("core-go-code")
		a.addRisk(writeApprovalRiskHigh, fmt.Sprintf("%s is core application Go code", path))
	} else {
		a.addRisk(writeApprovalRiskMedium, fmt.Sprintf("%s is production Go source", path))
	}
	if isEntrypointGoPath(path, file) {
		a.addKind("entrypoint-go-code")
		a.addRisk(writeApprovalRiskHigh, fmt.Sprintf("%s participates in a command or runtime entrypoint", path))
	}

	a.addImpact(fmt.Sprintf("%s is in package %s with %d funcs/methods, %d types, and %d imports", path, file.Package, file.Funcs+file.Methods, file.Types, len(file.Imports)))

	if symbol, ok := approvalTargetSymbol(a.ctx, a.mgr, a.goIndex, path, a.req); ok {
		a.addKind("symbol-scoped-edit")
		a.addImpact(fmt.Sprintf("target symbol likely falls inside %s (%s lines %d-%d)", symbolDisplayNameForApproval(symbol), path, symbol.StartLine, symbol.EndLine))
		a.addGoToolLines("incoming call", workspace.ToolRequest{Tool: "go_calls_into", Pattern: symbol.Name, Path: path}, 3)
		a.addGoToolLines("outgoing call", workspace.ToolRequest{Tool: "go_calls_from", Pattern: symbol.Name, Path: path}, 3)
		a.addGoToolLines("reference", workspace.ToolRequest{Tool: "go_references", Pattern: symbol.Name, Path: path}, 3)
		if symbol.Kind == "type" {
			a.addGoToolLines("implementation", workspace.ToolRequest{Tool: "go_implementations", Pattern: symbol.Name, Path: path}, 3)
		}
	}

	importerFiles := goImporterFiles(a.goIndex, dir, 5)
	importerDirs := make([]string, 0, len(importerFiles))
	for _, importer := range importerFiles {
		a.addBlast("importer file: " + importer)
		importerDirs = append(importerDirs, filepath.ToSlash(filepath.Dir(importer)))
	}

	for _, related := range limitedRelatedFiles(a.goIndex, path, a.proposal.Prompt, 4) {
		if related == path {
			continue
		}
		a.addBlast("related file: " + related)
	}
	for _, peer := range packagePeerFiles(a.goIndex, dir, path, 4) {
		a.addBlast("package peer: " + peer)
	}

	if tests := goTestFilesForDir(a.goIndex, dir, 4); len(tests) > 0 {
		a.addAffectedTest(fmt.Sprintf("%s (%d local test file(s): %s)", goTestCommandForDir(dir), len(tests), strings.Join(tests, ", ")))
	} else {
		a.addAffectedTest(fmt.Sprintf("%s (package-level regression target)", goTestCommandForDir(dir)))
	}
	a.addVerify(goTestCommandForDir(dir))

	for _, importerDir := range uniqueNonEmptyStrings(importerDirs) {
		a.addAffectedTest(fmt.Sprintf("%s (imports changed package)", goTestCommandForDir(importerDir)))
		a.addVerify(goTestCommandForDir(importerDir))
	}

	a.addGoDiagnosticSummary(path)
	a.addVerifyBaselineForRuntimeChange()
}

func (a *approvalImpactAnalyzer) analyzeShellPath(path string) {
	a.addKind("script")
	a.addImpact(fmt.Sprintf("%s is a shell script", path))
	a.addVerify("bash -n " + path)

	if strings.HasPrefix(path, "scripts/") {
		a.addKind("launcher-entrypoint")
		a.addRisk(writeApprovalRiskHigh, fmt.Sprintf("%s affects launcher or developer workflow entrypoints", path))
	} else {
		a.addRisk(writeApprovalRiskMedium, fmt.Sprintf("%s is an executable or automation script", path))
	}

	for _, line := range repoReferenceLines(a.root, path, 4) {
		a.addBlast("repo reference: " + line)
	}
	a.addVerifyBaselineForRuntimeChange()
}

func (a *approvalImpactAnalyzer) analyzeConfigPath(path string) {
	a.addKind("runtime-defaults")
	a.addRisk(writeApprovalRiskHigh, fmt.Sprintf("%s controls runtime defaults for future sessions", path))
	a.addImpact(fmt.Sprintf("%s changes runtime defaults, launch behavior, or default profiles", path))
	for _, line := range repoReferenceLines(a.root, path, 4) {
		a.addBlast("repo reference: " + line)
	}
	a.addAffectedTest("./internal/app (config loading, prompt stack, routing, approval flow)")
	a.addVerify("go test -vet=off ./internal/app")
	a.addVerifyBaselineForRuntimeChange()
}

func (a *approvalImpactAnalyzer) analyzePromptPath(path string) {
	a.addKind("baseline-behavior")
	a.addRisk(writeApprovalRiskHigh, fmt.Sprintf("%s changes baseline hidden prompt behavior across sessions", path))
	a.addImpact(fmt.Sprintf("%s influences the hidden prompt stack and can change model behavior in future sessions", path))
	for _, line := range repoReferenceLines(a.root, path, 4) {
		a.addBlast("repo reference: " + line)
	}
	a.addAffectedTest("./internal/app (prompt stack, routing, locate/review/implement lanes)")
	a.addVerify("go test -vet=off ./internal/app")
	a.addVerifyBaselineForRuntimeChange()
}

func (a *approvalImpactAnalyzer) analyzeMarkdownPath(path string) {
	a.addKind("docs-only")
	a.addImpact(fmt.Sprintf("%s is documentation/markdown content", path))
	for _, line := range repoReferenceLines(a.root, path, 3) {
		a.addBlast("repo reference: " + line)
	}
}

func (a *approvalImpactAnalyzer) analyzeGenericPath(path string) {
	a.addKind("generic-file-change")
	a.addRisk(writeApprovalRiskMedium, fmt.Sprintf("%s is outside the typed impact analyzers; approval should rely on scoped review", path))
	a.addImpact(fmt.Sprintf("%s is not yet covered by a specialized analyzer; using generic guarded-scope reasoning", path))
	for _, line := range repoReferenceLines(a.root, path, 3) {
		a.addBlast("repo reference: " + line)
	}
	a.addVerifyBaselineForRuntimeChange()
}

func (a *approvalImpactAnalyzer) addGoDiagnosticSummary(path string) {
	if a.mgr == nil {
		return
	}
	result := a.mgr.Execute(a.ctx, workspace.ToolRequest{Tool: "go_diagnostics", Path: path})
	if strings.TrimSpace(result.Output) == "" {
		return
	}
	lines := limitedResultLines(result.Output, 2)
	if len(lines) == 0 {
		return
	}
	if strings.Contains(lines[0], "no warnings or errors") {
		a.addImpact("current Go diagnostics are clean for the touched package/file")
		return
	}
	a.addRisk(writeApprovalRiskHigh, fmt.Sprintf("%s already reports Go diagnostics; verify that the change does not deepen an existing issue", path))
	a.addImpact("current Go diagnostics already exist in the touched package/file; verification should confirm the change does not widen that failure surface")
}

func (a *approvalImpactAnalyzer) addGoToolLines(prefix string, req workspace.ToolRequest, limit int) {
	if a.mgr == nil {
		return
	}
	result := a.mgr.Execute(a.ctx, req)
	if strings.TrimSpace(result.Output) == "" {
		return
	}
	for _, line := range limitedResultLines(result.Output, limit) {
		a.addBlast(prefix + ": " + line)
	}
}

func (a *approvalImpactAnalyzer) addVerifyBaselineForRuntimeChange() {
	if a.mgr == nil {
		return
	}
	for _, step := range implementationVerifyCommands(a.mgr) {
		a.addVerify(step)
	}
}

func (a *approvalImpactAnalyzer) finalize() approvalImpactProfile {
	if a.profile.Risk == "" {
		a.profile.Risk = writeApprovalRiskLow
	}

	if hasAnyKind(a.profile.ChangeKinds, "baseline-behavior", "runtime-defaults") && hasAnyKind(a.profile.ChangeKinds, "core-go-code", "entrypoint-go-code") {
		a.addRisk(writeApprovalRiskCritical, "scope combines baseline/runtime behavior changes with core application code changes")
	}
	if hasAnyKind(a.profile.ChangeKinds, "multi-file-change", "cross-area-change") && hasAnyKind(a.profile.ChangeKinds, "core-go-code", "launcher-entrypoint") {
		a.addRisk(writeApprovalRiskHigh, "scope couples broad edits with core runtime or launcher surfaces")
	}
	if a.profile.Risk.rank() >= writeApprovalRiskHigh.rank() {
		a.runDeepAnalysis()
	}
	if a.profile.Risk.rank() >= writeApprovalRiskHigh.rank() {
		a.addVerifyBaselineForRuntimeChange()
	}

	a.profile.ChangeKinds = uniqueSortedStrings(a.profile.ChangeKinds)
	a.profile.RiskReasons = uniqueNonEmptyStrings(a.profile.RiskReasons)
	a.profile.ImpactSummary = uniqueNonEmptyStrings(a.profile.ImpactSummary)
	a.profile.BlastRadius = uniqueNonEmptyStrings(a.profile.BlastRadius)
	a.profile.DeepAnalysis = uniqueNonEmptyStrings(a.profile.DeepAnalysis)
	a.profile.AffectedTests = uniqueNonEmptyStrings(a.profile.AffectedTests)
	a.profile.ApprovalNotes = uniqueNonEmptyStrings(a.profile.ApprovalNotes)
	a.profile.VerifySteps = uniqueNonEmptyStrings(a.profile.VerifySteps)
	return a.profile
}

func (a *approvalImpactAnalyzer) runDeepAnalysis() {
	a.addApprovalNote("this request crossed the high-risk threshold, so the approval packet includes deeper semantic traces")
	a.addApprovalNote("approve the exact scoped paths only; prefer another scoped proposal over switching to /writes auto")

	if len(a.proposal.Paths) > 1 {
		a.addApprovalNote(fmt.Sprintf("recheck the %d-file scope before approving; broad approvals are harder to review and revert", len(a.proposal.Paths)))
	}

	for _, path := range a.proposal.Paths {
		path = normalizeGuardedPath(path)
		switch {
		case strings.HasSuffix(path, ".go"), strings.HasSuffix(path, "_test.go"):
			a.runDeepGoAnalysis(path)
		case strings.HasPrefix(path, "scripts/"):
			a.runDeepScriptAnalysis(path)
		case path == "config.json" || path == "config.example.json":
			a.runDeepConfigAnalysis(path)
		case strings.HasPrefix(path, "prompts/"):
			a.runDeepPromptAnalysis(path)
		}
	}
}

func (a *approvalImpactAnalyzer) runDeepGoAnalysis(path string) {
	if a.goIndex == nil {
		return
	}
	file := a.goIndex.FileByPath(path)
	if file == nil {
		return
	}

	exported := exportedSymbolsForApproval(file, 5)
	if len(exported) > 0 {
		a.addDeep(fmt.Sprintf("%s exports or defines major symbols: %s", path, strings.Join(exported, ", ")))
	}

	if symbol, ok := approvalTargetSymbol(a.ctx, a.mgr, a.goIndex, path, a.req); ok {
		incoming := callChainPreview(a.goIndex.FindCallsInto(symbol.Name, 24), symbol, true, 4)
		outgoing := callChainPreview(a.goIndex.FindCallsFrom(symbol.Name, 24), symbol, false, 4)
		if len(incoming) > 0 {
			a.addDeep(fmt.Sprintf("incoming call chain focus for %s: %s", symbolDisplayNameForApproval(symbol), strings.Join(incoming, " | ")))
		}
		if len(outgoing) > 0 {
			a.addDeep(fmt.Sprintf("outgoing call chain focus for %s: %s", symbolDisplayNameForApproval(symbol), strings.Join(outgoing, " | ")))
		}
		a.addApprovalNote(fmt.Sprintf("inspect the target symbol body for %s before approving broad follow-up edits", symbolDisplayNameForApproval(symbol)))
	}

	importerCount := len(goImporterFiles(a.goIndex, file.PathDir(), 32))
	if importerCount > 0 {
		a.addDeep(fmt.Sprintf("%s has %d local importer file(s) in the workspace", path, importerCount))
	}

	if tests := goTestFilesForDir(a.goIndex, file.PathDir(), 8); len(tests) > 0 {
		a.addDeep(fmt.Sprintf("local test files touching %s's package: %s", path, strings.Join(tests, ", ")))
	}
}

func (a *approvalImpactAnalyzer) runDeepScriptAnalysis(path string) {
	refs := repoReferenceLines(a.root, path, 6)
	if len(refs) > 0 {
		a.addDeep(fmt.Sprintf("%s is referenced by %d tracked file(s); the first references are: %s", path, len(refs), strings.Join(refs, " | ")))
	}
	a.addApprovalNote("launcher script changes should stay narrowly scoped and always pass shell syntax plus Go smoke verification")
}

func (a *approvalImpactAnalyzer) runDeepConfigAnalysis(path string) {
	refs := repoReferenceLines(a.root, path, 6)
	if len(refs) > 0 {
		a.addDeep(fmt.Sprintf("%s is consumed in repo code/docs/tests at: %s", path, strings.Join(refs, " | ")))
	}
	a.addApprovalNote("config changes can silently alter future sessions; prefer reviewing the exact default/value diff before approval")
}

func (a *approvalImpactAnalyzer) runDeepPromptAnalysis(path string) {
	refs := repoReferenceLines(a.root, path, 6)
	if len(refs) > 0 {
		a.addDeep(fmt.Sprintf("%s is wired into the prompt/runtime stack via: %s", path, strings.Join(refs, " | ")))
	}
	a.addApprovalNote("prompt changes alter hidden agent behavior; approve only after checking the intended downstream routing/behavior effect")
}

func (a *approvalImpactAnalyzer) addKind(kind string) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return
	}
	a.profile.ChangeKinds = append(a.profile.ChangeKinds, kind)
}

func (a *approvalImpactAnalyzer) addRisk(level writeApprovalRisk, reason string) {
	if level.rank() > a.profile.Risk.rank() {
		a.profile.Risk = level
	}
	reason = strings.TrimSpace(reason)
	if reason != "" {
		a.profile.RiskReasons = append(a.profile.RiskReasons, reason)
	}
}

func (a *approvalImpactAnalyzer) addImpact(line string) {
	line = strings.TrimSpace(line)
	if line != "" {
		a.profile.ImpactSummary = append(a.profile.ImpactSummary, line)
	}
}

func (a *approvalImpactAnalyzer) addBlast(line string) {
	line = strings.TrimSpace(line)
	if line != "" {
		a.profile.BlastRadius = append(a.profile.BlastRadius, line)
	}
}

func (a *approvalImpactAnalyzer) addAffectedTest(line string) {
	line = strings.TrimSpace(line)
	if line != "" {
		a.profile.AffectedTests = append(a.profile.AffectedTests, line)
	}
}

func (a *approvalImpactAnalyzer) addDeep(line string) {
	line = strings.TrimSpace(line)
	if line != "" {
		a.profile.DeepAnalysis = append(a.profile.DeepAnalysis, line)
	}
}

func (a *approvalImpactAnalyzer) addApprovalNote(line string) {
	line = strings.TrimSpace(line)
	if line != "" {
		a.profile.ApprovalNotes = append(a.profile.ApprovalNotes, line)
	}
}

func (a *approvalImpactAnalyzer) addVerify(step string) {
	step = strings.TrimSpace(step)
	if step != "" {
		a.profile.VerifySteps = append(a.profile.VerifySteps, step)
	}
}

func exportedSymbolsForApproval(file *semantic.GoFile, limit int) []string {
	if file == nil || limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for _, symbol := range file.Symbols {
		if symbol.Name == "" {
			continue
		}
		if !isExportedGoName(symbol.Name) {
			continue
		}
		out = append(out, symbolDisplayNameForApproval(symbol))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func isExportedGoName(name string) bool {
	if name == "" {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

func callChainPreview(matches []semantic.CallMatch, target semantic.GoSymbol, incoming bool, limit int) []string {
	if len(matches) == 0 || limit <= 0 {
		return nil
	}
	lines := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, match := range matches {
		var line string
		if incoming {
			line = fmt.Sprintf("%s:%d -> %s", match.Caller.Path, match.Line, symbolDisplayNameForApproval(target))
		} else {
			line = fmt.Sprintf("%s:%d -> %s", match.Caller.Path, match.Line, match.CalleeName)
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	return lines
}

func (r writeApprovalRisk) rank() int {
	switch r {
	case writeApprovalRiskCritical:
		return 4
	case writeApprovalRiskHigh:
		return 3
	case writeApprovalRiskMedium:
		return 2
	case writeApprovalRiskLow:
		return 1
	default:
		return 0
	}
}

func isCoreGoImpactPath(path string) bool {
	return frontier.IsCoreGoImpactPath(path)
}

func isEntrypointGoPath(path string, file *semantic.GoFile) bool {
	return frontier.IsEntrypointGoPath(path, file)
}

func approvalGoImportPath(pkgPath string, module string) string {
	return frontier.GoImportPath(pkgPath, module)
}

func goImporterFiles(idx *semantic.GoIndex, pkgDir string, limit int) []string {
	return frontier.GoImporterFiles(idx, pkgDir, limit)
}

func goTestFilesForDir(idx *semantic.GoIndex, dir string, limit int) []string {
	return frontier.GoTestFilesForDir(idx, dir, limit)
}

func goTestCommandForDir(dir string) string {
	return frontier.GoTestCommandForDir(dir)
}

func repoReferenceLines(root string, target string, limit int) []string {
	root = strings.TrimSpace(root)
	target = normalizeGuardedPath(target)
	if root == "" || target == "" || limit <= 0 {
		return nil
	}
	exact := target
	base := filepath.Base(target)
	results := make([]string, 0, limit)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if _, skip := impactSkippedDirNames[d.Name()]; skip && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if len(results) >= limit {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := impactReferenceExts[ext]; !ok {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if rel == exact {
			return nil
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if !strings.Contains(line, exact) && !strings.Contains(line, base) {
				continue
			}
			results = append(results, fmt.Sprintf("%s:%d:%s", rel, lineNo, trimImpactSnippet(line)))
			break
		}
		return nil
	})
	return results
}

func trimImpactSnippet(line string) string {
	line = strings.TrimSpace(line)
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 120 {
		return line[:117] + "..."
	}
	return line
}

func uniqueTopLevelDirs(paths []string) []string {
	return frontier.UniqueTopLevelDirs(paths)
}

func uniqueSortedStrings(values []string) []string {
	values = uniqueNonEmptyStrings(values)
	sort.Strings(values)
	return values
}

func hasAnyKind(values []string, kinds ...string) bool {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	for _, kind := range kinds {
		if _, ok := set[kind]; ok {
			return true
		}
	}
	return false
}
