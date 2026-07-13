package projectartifacts

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/platform/gitstate"
	"idiom/internal/repository/discovery"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

const (
	SchemaVersion = 1
	hiddenRoot    = ".idiom"
	discoveryDir  = ".idiom/discovery"
	contextRel    = ".idiom/discovery/context.md"
)

var (
	projectPromptCandidates = []string{".qwen.md", "QWEN.md", "IDIOM.md", "AGENTS.md", "CLAUDE.md", "README.md"}
	headingPattern          = regexp.MustCompile(`(?m)^#{1,3}\s+(.+?)\s*$`)
	acronymPattern          = regexp.MustCompile(`\b[A-Z]{2,}(?:/[A-Z]{2,})?\b`)
	camelPattern            = regexp.MustCompile(`\b[A-Z][a-z0-9]+(?:[A-Z][a-z0-9]+)+\b`)
	decisionPattern         = regexp.MustCompile(`(?i)\b(must|never|do not|always|prefer|required|append-only)\b`)
)

type Manifest struct {
	SchemaVersion int      `json:"schema_version"`
	Root          string   `json:"root"`
	Fingerprint   string   `json:"fingerprint"`
	GitHead       string   `json:"git_head,omitempty"`
	GitDirty      bool     `json:"git_dirty"`
	GeneratedAt   string   `json:"generated_at"`
	Languages     []string `json:"languages,omitempty"`
	PromptFiles   []string `json:"prompt_files,omitempty"`
}

type ModulePacket struct {
	Path        string   `json:"path"`
	Kind        string   `json:"kind"`
	Summary     string   `json:"summary"`
	SampleFiles []string `json:"sample_files,omitempty"`
	Languages   []string `json:"languages,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type GlossaryEntry struct {
	Term   string `json:"term"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

type WorkflowRecipe struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	When     string   `json:"when"`
	Commands []string `json:"commands,omitempty"`
	Steps    []string `json:"steps,omitempty"`
}

type DecisionEntry struct {
	Rule   string `json:"rule"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

type ImpactPacket struct {
	Target             string   `json:"target"`
	Kind               string   `json:"kind"`
	PromptFiles        []string `json:"prompt_files,omitempty"`
	ActiveLanguages    []string `json:"active_languages,omitempty"`
	Hotspots           []string `json:"hotspots,omitempty"`
	VerificationRecipe []string `json:"verification_recipe,omitempty"`
}

// Ensure regenerates the hidden project-local discovery artifacts when the
// project fingerprint changes. Returns the context path even when generation
// is a no-op because the existing artifacts are still current.
func Ensure(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("stat project root: %w", err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("project root %q is not a directory", root)
	}

	dir := filepath.Join(root, filepath.FromSlash(discoveryDir))
	if err := os.MkdirAll(filepath.Join(dir, "impact"), 0o755); err != nil {
		return "", fmt.Errorf("mkdir discovery dir: %w", err)
	}

	fp, gitHead, gitDirty := currentFingerprint(root)
	contextPath := filepath.Join(root, filepath.FromSlash(contextRel))

	if manifest, ok := loadManifest(root); ok && manifest.SchemaVersion == SchemaVersion && manifest.Fingerprint == fp && artifactsPresent(root) {
		return contextPath, nil
	}

	if err := generate(root, dir, contextPath, fp, gitHead, gitDirty); err != nil {
		return "", err
	}

	return contextPath, nil
}

func generate(root string, dir string, contextPath string, fingerprint string, gitHead string, gitDirty bool) error {
	promptFiles := discoverPromptFiles(root)
	activeLanguages := detectLanguages(root)
	modules := discoverModules(root)
	glossary := buildGlossary(root, promptFiles)
	workflows := buildWorkflows(root, activeLanguages, promptFiles)
	decisions := buildDecisions(root, promptFiles)
	impact := buildRootImpact(promptFiles, activeLanguages, workflows, modules)

	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		Root:          root,
		Fingerprint:   fingerprint,
		GitHead:       gitHead,
		GitDirty:      gitDirty,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Languages:     activeLanguages,
		PromptFiles:   promptFiles,
	}
	if err := writeJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if err := writeJSONL(filepath.Join(dir, "modules.jsonl"), modules); err != nil {
		return fmt.Errorf("write modules: %w", err)
	}

	if err := writeJSON(filepath.Join(dir, "glossary.json"), glossary); err != nil {
		return fmt.Errorf("write glossary: %w", err)
	}

	if err := writeJSON(filepath.Join(dir, "workflows.json"), workflows); err != nil {
		return fmt.Errorf("write workflows: %w", err)
	}

	if err := writeJSONL(filepath.Join(dir, "decisions.jsonl"), decisions); err != nil {
		return fmt.Errorf("write decisions: %w", err)
	}

	if err := writeJSON(filepath.Join(dir, "impact", "root.json"), impact); err != nil {
		return fmt.Errorf("write impact packet: %w", err)
	}

	if err := os.WriteFile(contextPath, []byte(renderContext(promptFiles, activeLanguages, modules, glossary, workflows, decisions)), 0o644); err != nil {
		return fmt.Errorf("write context: %w", err)
	}

	return nil
}

func loadManifest(root string) (Manifest, bool) {
	path := filepath.Join(root, filepath.FromSlash(discoveryDir), "manifest.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, false
	}

	var manifest Manifest

	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, false
	}

	return manifest, true
}

func artifactsPresent(root string) bool {
	paths := []string{
		filepath.Join(root, filepath.FromSlash(discoveryDir), "manifest.json"),
		filepath.Join(root, filepath.FromSlash(discoveryDir), "modules.jsonl"),
		filepath.Join(root, filepath.FromSlash(discoveryDir), "glossary.json"),
		filepath.Join(root, filepath.FromSlash(discoveryDir), "workflows.json"),
		filepath.Join(root, filepath.FromSlash(discoveryDir), "decisions.jsonl"),
		filepath.Join(root, filepath.FromSlash(discoveryDir), "impact", "root.json"),
		filepath.Join(root, filepath.FromSlash(contextRel)),
	}
	for _, path := range paths {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return false
		}
	}

	return true
}

func currentFingerprint(root string) (fingerprint string, gitHead string, gitDirty bool) {
	if snapshot, err := gitstate.Current(context.Background(), root); err == nil {
		gitHead = snapshot.Head

		lines := filterStatusLines(snapshot.StatusLines)

		gitDirty = len(lines) > 0

		h := sha256.New()

		_, _ = h.Write([]byte(gitHead))
		_, _ = h.Write([]byte{'\n'})

		for _, line := range lines {
			_, _ = h.Write([]byte(line))
			_, _ = h.Write([]byte{'\n'})

			path := gitstate.PathFromPorcelainLine(line)

			if path == "" {
				continue
			}

			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil {
				_, _ = h.Write([]byte(path))
				_, _ = h.Write([]byte(fmt.Sprintf("|%d|%d\n", info.ModTime().UnixNano(), info.Size())))
			}
		}

		return hex.EncodeToString(h.Sum(nil)), gitHead, gitDirty
	}

	h := sha256.New()

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if discovery.ShouldSkipPath(root, path, true) && path != root {
				return filepath.SkipDir
			}

			return nil
		}

		if discovery.ShouldSkipPath(root, path, false) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		rel = filepath.ToSlash(rel)
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte(fmt.Sprintf("|%d|%d\n", info.ModTime().UnixNano(), info.Size())))

		return nil
	})

	return hex.EncodeToString(h.Sum(nil)), "", false
}

func filterStatusLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}

		path := gitstate.PathFromPorcelainLine(line)
		if path == "" {
			continue
		}

		if strings.HasPrefix(path, hiddenRoot+"/") || path == hiddenRoot {
			continue
		}

		out = append(out, line)
	}

	slices.Sort(out)

	return out
}

func discoverPromptFiles(root string) []string {
	var paths []string

	for _, candidate := range projectPromptCandidates {
		path := filepath.Join(root, candidate)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			paths = append(paths, candidate)
		}
	}

	return paths
}

func detectLanguages(root string) []string {
	langs := semanticlanguage.ActiveLanguages(config.CanonicalizeExistingPath(root))
	names := make([]string, 0, len(langs))

	for _, lang := range langs {
		names = append(names, lang.Name)
	}

	slices.Sort(names)

	return names
}

func discoverModules(root string) []ModulePacket {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	modules := make([]ModulePacket, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if discovery.ShouldSkipPath(root, path, entry.IsDir()) {
			continue
		}

		if strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".qwen.md" {
			continue
		}

		rel := filepath.ToSlash(entry.Name())
		if entry.IsDir() {
			count, sample, langs := summarizeDirModule(root, path)

			modules = append(modules, ModulePacket{
				Path:        rel,
				Kind:        "directory",
				Summary:     fmt.Sprintf("Top-level directory with %d visible file(s).", count),
				SampleFiles: sample,
				Languages:   langs,
				Tags:        moduleTags(rel, true),
			})

			continue
		}

		langs := languagesForFiles([]string{rel})

		modules = append(modules, ModulePacket{
			Path:      rel,
			Kind:      "file",
			Summary:   summarizeFileModule(rel),
			Languages: langs,
			Tags:      moduleTags(rel, false),
		})
	}

	slices.SortFunc(modules, func(a, b ModulePacket) int { return cmp.Compare(a.Path, b.Path) })

	if len(modules) > 24 {
		modules = modules[:24]
	}

	return modules
}

func summarizeDirModule(root string, dir string) (count int, sample []string, langs []string) {
	languageSet := make(map[string]struct{})

	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if path != dir && discovery.ShouldSkipPath(root, path, true) {
				return filepath.SkipDir
			}

			return nil
		}

		if discovery.ShouldSkipPath(root, path, false) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		rel = filepath.ToSlash(rel)
		count++

		if len(sample) < 6 {
			sample = append(sample, rel)
		}

		if lang, ok := semanticlanguage.LanguageForFile(rel); ok {
			languageSet[lang.Name] = struct{}{}
		}

		return nil
	})
	langs = setToSortedSlice(languageSet)

	return count, sample, langs
}

func summarizeFileModule(rel string) string {
	switch strings.ToLower(filepath.Base(rel)) {
	case "readme.md":
		return "Top-level repository overview and contributor entrypoint."
	case "makefile":
		return "Top-level command surface for common build and validation flows."
	case "go.mod":
		return "Go module boundary and dependency anchor."
	case "package.json":
		return "JavaScript/TypeScript workspace metadata and script entrypoint."
	case "pyproject.toml":
		return "Python project metadata and tooling configuration."
	}

	return "Top-level file that likely anchors configuration, docs, or entrypoints."
}

func moduleTags(rel string, isDir bool) []string {
	name := strings.ToLower(filepath.Base(rel))

	var tags []string

	if isDir {
		switch name {
		case "cmd":
			tags = append(tags, "entrypoints")
		case "docs":
			tags = append(tags, "documentation")
		case "internal", "src", "lib":
			tags = append(tags, "implementation")
		case "scripts":
			tags = append(tags, "tooling")
		case "test", "tests", "testdata":
			tags = append(tags, "tests")
		}
	} else {
		switch name {
		case ".qwen.md", "qwen.md", "agents.md", "claude.md", "context.md":
			tags = append(tags, "prompt")
		case "readme.md":
			tags = append(tags, "documentation")
		case "makefile", "justfile":
			tags = append(tags, "tooling")
		}
	}

	return tags
}

func buildGlossary(root string, promptFiles []string) []GlossaryEntry {
	type key struct {
		term string
		kind string
	}

	seen := make(map[key]struct{})

	var out []GlossaryEntry

	add := func(term string, kind string, source string) {
		term = strings.TrimSpace(term)
		if term == "" || len(term) < 3 {
			return
		}

		k := key{term: term, kind: kind}
		if _, ok := seen[k]; ok {
			return
		}

		seen[k] = struct{}{}

		out = append(out, GlossaryEntry{Term: term, Kind: kind, Source: source})
	}

	for _, rel := range promptFiles {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}

		text := string(data)
		for _, match := range headingPattern.FindAllStringSubmatch(text, 8) {
			add(strings.TrimSpace(match[1]), "heading", rel)
		}

		for _, term := range acronymPattern.FindAllString(text, 12) {
			add(term, "acronym", rel)
		}

		for _, term := range camelPattern.FindAllString(text, 12) {
			add(term, "symbol", rel)
		}
	}

	slices.SortFunc(out, func(a, b GlossaryEntry) int {
		if c := cmp.Compare(a.Source, b.Source); c != 0 {
			return c
		}

		return cmp.Compare(a.Term, b.Term)
	})

	if len(out) > 20 {
		out = out[:20]
	}

	return out
}

func buildWorkflows(root string, activeLanguages []string, promptFiles []string) []WorkflowRecipe {
	workflows := []WorkflowRecipe{
		{
			ID:    "discovery_first",
			Title: "Discovery First",
			When:  "Use before broad repo reading or edits.",
			Steps: []string{
				"Start with workspace overview, search_content, or a structured overview route.",
				"Use symbol/navigation routes before broad file reads when the question is symbol-shaped.",
				"Only fall back to raw reads once the target file or declaration is known.",
			},
		},
	}

	for _, lang := range activeLanguages {
		switch lang {
		case "go":
			workflows = append(workflows, WorkflowRecipe{
				ID:       "verify_go_package",
				Title:    "Verify Go Package",
				When:     "After changing Go behavior in one package.",
				Commands: []string{"go test ./path/to/package"},
				Steps:    []string{"Run the smallest affected package test first.", "Use source_sanity for source edits and structured symbol/navigation routes to narrow failures before broad test runs."},
			})
		case "python":
			workflows = append(workflows, WorkflowRecipe{
				ID:       "verify_python_slice",
				Title:    "Verify Python Slice",
				When:     "After changing Python code with a known affected test path.",
				Commands: []string{"pytest path/to/test_file.py"},
				Steps:    []string{"Use python_workspace_symbol or python_read_symbol to narrow the target first.", "Prefer targeted pytest invocations over suite-wide reruns."},
			})
		case "typescript":
			workflows = append(workflows, WorkflowRecipe{
				ID:       "verify_ts_slice",
				Title:    "Verify TS/JS Slice",
				When:     "After changing TypeScript or JavaScript with a known test entrypoint.",
				Commands: []string{jsTestCommand(root)},
				Steps:    []string{"Use ts_workspace_symbol and ts_calls_into/out_of before broad text search.", "Run the smallest matching test or diagnostics command available in the repo."},
			})
		case "rust":
			workflows = append(workflows, WorkflowRecipe{
				ID:       "verify_rust_crate",
				Title:    "Verify Rust Crate",
				When:     "After changing a bounded Rust crate or package.",
				Commands: []string{"cargo test -p <crate>"},
				Steps:    []string{"Use rust_workspace_symbol and rust_read_symbol first.", "Prefer crate-scoped tests before workspace-wide cargo test."},
			})
		case "java":
			workflows = append(workflows, WorkflowRecipe{
				ID:       "verify_java_target",
				Title:    "Verify Java Target",
				When:     "After changing Java code with a known module or test target.",
				Commands: []string{javaTestCommand(root)},
				Steps:    []string{"Use java_workspace_symbol before file-wide greps.", "Prefer test-target filters over full-suite reruns when possible."},
			})
		}
	}

	if len(promptFiles) > 0 {
		workflows = append(workflows, WorkflowRecipe{
			ID:    "refresh_project_guidance",
			Title: "Refresh Project Guidance",
			When:  "After structural workflow or architecture changes.",
			Steps: []string{
				"Update the human-authored root guidance files first.",
				"Regenerate the hidden .idiom/discovery context so the prompt-facing digest stays aligned.",
			},
		})
	}

	return workflows
}

func buildDecisions(root string, promptFiles []string) []DecisionEntry {
	seen := make(map[string]struct{})

	var out []DecisionEntry

	for _, rel := range promptFiles {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}

		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
			if line == "" || len(line) < 12 {
				continue
			}

			if !decisionPattern.MatchString(line) {
				continue
			}

			line = strings.Join(strings.Fields(line), " ")
			if _, ok := seen[line]; ok {
				continue
			}

			seen[line] = struct{}{}

			kind := "constraint"

			if strings.Contains(strings.ToLower(line), "prefer") {
				kind = "preference"
			}

			out = append(out, DecisionEntry{Rule: line, Kind: kind, Source: rel})
		}
	}

	slices.SortFunc(out, func(a, b DecisionEntry) int {
		if c := cmp.Compare(a.Source, b.Source); c != 0 {
			return c
		}

		return cmp.Compare(a.Rule, b.Rule)
	})

	if len(out) > 24 {
		out = out[:24]
	}

	return out
}

func buildRootImpact(promptFiles []string, activeLanguages []string, workflows []WorkflowRecipe, modules []ModulePacket) ImpactPacket {
	var verification []string

	for _, workflow := range workflows {
		if strings.HasPrefix(workflow.ID, "verify_") {
			verification = append(verification, workflow.Commands...)
		}
	}

	var hotspots []string

	for _, module := range modules {
		if len(hotspots) >= 8 {
			break
		}

		if containsTag(module.Tags, "implementation") || containsTag(module.Tags, "entrypoints") || containsTag(module.Tags, "tooling") {
			hotspots = append(hotspots, module.Path)
		}
	}

	return ImpactPacket{
		Target:             ".",
		Kind:               "project",
		PromptFiles:        promptFiles,
		ActiveLanguages:    activeLanguages,
		Hotspots:           hotspots,
		VerificationRecipe: verification,
	}
}

func renderContext(promptFiles []string, activeLanguages []string, modules []ModulePacket, glossary []GlossaryEntry, workflows []WorkflowRecipe, decisions []DecisionEntry) string {
	var b strings.Builder

	b.WriteString("# Generated Project Context\n\n")
	b.WriteString("This digest is generated into `.idiom/discovery/`. Human-authored root guidance files remain canonical; this file exists to give the model a compact, structured project map.\n")

	if len(promptFiles) > 0 {
		b.WriteString("\n## Prompt Files\n")

		for _, file := range promptFiles {
			b.WriteString("- ")
			b.WriteString(file)
			b.WriteString("\n")
		}
	}

	if len(activeLanguages) > 0 {
		b.WriteString("\n## Active Languages\n")

		for _, lang := range activeLanguages {
			b.WriteString("- ")
			b.WriteString(lang)
			b.WriteString("\n")
		}
	}

	if len(modules) > 0 {
		b.WriteString("\n## Module Map\n")

		for _, module := range limitModules(modules, 8) {
			b.WriteString("- `")
			b.WriteString(module.Path)
			b.WriteString("`: ")
			b.WriteString(module.Summary)

			if len(module.SampleFiles) > 0 {
				b.WriteString(" Samples: ")
				b.WriteString(strings.Join(limitStrings(module.SampleFiles, 3), ", "))
				b.WriteString(".")
			}

			b.WriteString("\n")
		}
	}

	if len(workflows) > 0 {
		b.WriteString("\n## Workflow Recipes\n")

		for _, workflow := range limitWorkflows(workflows, 4) {
			b.WriteString("- `")
			b.WriteString(workflow.ID)
			b.WriteString("`: ")
			b.WriteString(workflow.When)

			if len(workflow.Commands) > 0 {
				b.WriteString(" Command: `")
				b.WriteString(workflow.Commands[0])
				b.WriteString("`.")
			}

			b.WriteString("\n")
		}
	}

	if len(decisions) > 0 {
		b.WriteString("\n## Decision Memory\n")

		for _, decision := range limitDecisions(decisions, 8) {
			b.WriteString("- [")
			b.WriteString(decision.Source)
			b.WriteString("] ")
			b.WriteString(decision.Rule)
			b.WriteString("\n")
		}
	}

	if len(glossary) > 0 {
		b.WriteString("\n## Glossary Seeds\n")

		for _, entry := range limitGlossary(glossary, 8) {
			b.WriteString("- `")
			b.WriteString(entry.Term)
			b.WriteString("` (")
			b.WriteString(entry.Kind)
			b.WriteString(" from ")
			b.WriteString(entry.Source)
			b.WriteString(")\n")
		}
	}

	return strings.TrimSpace(b.String()) + "\n"
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	data = append(data, '\n')

	return os.WriteFile(path, data, 0o644)
}

func writeJSONL[T any](path string, values []T) error {
	var b strings.Builder

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)

	for _, value := range values {
		if err := enc.Encode(value); err != nil {
			return err
		}
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func languagesForFiles(paths []string) []string {
	set := make(map[string]struct{})

	for _, path := range paths {
		if lang, ok := semanticlanguage.LanguageForFile(path); ok {
			set[lang.Name] = struct{}{}
		}
	}

	return setToSortedSlice(set)
}

func setToSortedSlice(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}

	return false
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}

	return values[:limit]
}

func limitModules(values []ModulePacket, limit int) []ModulePacket {
	if limit <= 0 || len(values) <= limit {
		return values
	}

	return values[:limit]
}

func limitWorkflows(values []WorkflowRecipe, limit int) []WorkflowRecipe {
	if limit <= 0 || len(values) <= limit {
		return values
	}

	return values[:limit]
}

func limitDecisions(values []DecisionEntry, limit int) []DecisionEntry {
	if limit <= 0 || len(values) <= limit {
		return values
	}

	return values[:limit]
}

func limitGlossary(values []GlossaryEntry, limit int) []GlossaryEntry {
	if limit <= 0 || len(values) <= limit {
		return values
	}

	return values[:limit]
}

func jsTestCommand(root string) string {
	switch {
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm test -- <pattern>"
	case fileExists(filepath.Join(root, "yarn.lock")):
		return "yarn test <pattern>"
	default:
		return "npm test -- <pattern>"
	}
}

func javaTestCommand(root string) string {
	switch {
	case fileExists(filepath.Join(root, "gradlew")), fileExists(filepath.Join(root, "build.gradle")), fileExists(filepath.Join(root, "build.gradle.kts")):
		return "./gradlew test --tests <Pattern>"
	default:
		return "mvn test -Dtest=<Pattern>"
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
