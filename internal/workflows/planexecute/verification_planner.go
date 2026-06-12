package planexecute

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	VerificationCommandSourcePrompt   = "prompt"
	VerificationCommandSourceExplicit = "explicit"
	VerificationCommandSourceInferred = "touched_path"

	VerificationSkipEmptyPath        = "empty_path"
	VerificationSkipAbsolutePath     = "absolute_path"
	VerificationSkipParentTraversal  = "parent_traversal"
	VerificationSkipEmptySegment     = "empty_path_segment"
	VerificationSkipVendorPath       = "vendor_path"
	VerificationSkipGeneratedPath    = "generated_path"
	VerificationSkipGeneratedFile    = "generated_file"
	VerificationSkipNonGoPath        = "non_go_path"
	VerificationSkipNoGoModuleRoot   = "no_go_module_root"
	VerificationRejectUnsupportedCmd = "unsupported_verification_command"
)

type VerificationPlannerInput struct {
	WorkspaceRoot    string
	Prompt           string
	ExplicitCommands []string
	ChangedPaths     []string
}

type VerificationPlanner struct {
	root string
}

type VerificationPlan struct {
	Commands []VerificationCommand `json:"commands,omitempty"`
	Skips    []VerificationSkip    `json:"skips,omitempty"`
	Rejects  []VerificationReject  `json:"rejects,omitempty"`
}

type VerificationCommand struct {
	Command    string   `json:"command"`
	Argv       []string `json:"argv,omitempty"`
	WorkingDir string   `json:"working_dir,omitempty"`
	Source     string   `json:"source,omitempty"`
	Path       string   `json:"path,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

type VerificationSkip struct {
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

type VerificationReject struct {
	Command string `json:"command,omitempty"`
	Source  string `json:"source,omitempty"`
	Reason  string `json:"reason"`
}

func NewVerificationPlanner(root string) VerificationPlanner {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" || trimmed == "." {
		if moduleRoot, ok := nearestAncestorGoModuleRoot("."); ok {
			return VerificationPlanner{root: moduleRoot}
		}
	}

	if trimmed == "" {
		trimmed = "."
	}

	return VerificationPlanner{root: trimmed}
}

func PlanVerification(input VerificationPlannerInput) VerificationPlan {
	return NewVerificationPlanner(input.WorkspaceRoot).Plan(input)
}

func (p VerificationPlanner) Plan(input VerificationPlannerInput) VerificationPlan {
	plan := VerificationPlan{}
	seen := map[string]struct{}{}

	for _, command := range planExecuteRequestedVerificationCommands(input.Prompt) {
		plan.addExplicitCommand(command, VerificationCommandSourcePrompt, seen)
	}

	for _, command := range input.ExplicitCommands {
		plan.addExplicitCommand(command, VerificationCommandSourceExplicit, seen)
	}

	for _, rawPath := range input.ChangedPaths {
		command, skip, ok := p.inferTouchedPathCommand(rawPath)
		if !ok {
			plan.Skips = append(plan.Skips, skip)
			continue
		}

		plan.addCommand(command, seen)
	}

	return plan
}

func (p *VerificationPlan) addExplicitCommand(command string, source string, seen map[string]struct{}) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}

	args, ok := VerificationCommandArgs(command)
	if !ok {
		p.Rejects = append(p.Rejects, VerificationReject{
			Command: command,
			Source:  source,
			Reason:  VerificationRejectUnsupportedCmd,
		})

		return
	}

	p.addCommand(VerificationCommand{
		Command: command,
		Argv:    args,
		Source:  source,
		Reason:  "requested verification command",
	}, seen)
}

func (p *VerificationPlan) addCommand(command VerificationCommand, seen map[string]struct{}) {
	command.Command = strings.TrimSpace(command.Command)
	command.Argv = normalizeVerificationArgv(command.Argv)

	if len(command.Argv) == 0 {
		if args, ok := VerificationCommandArgs(command.Command); ok {
			command.Argv = args
		}
	}

	if len(command.Argv) == 0 || !VerificationArgvAllowed(command.Argv) {
		p.Rejects = append(p.Rejects, VerificationReject{
			Command: command.Command,
			Source:  command.Source,
			Reason:  VerificationRejectUnsupportedCmd,
		})

		return
	}

	if command.Command == "" {
		command.Command = strings.Join(command.Argv, " ")
	}

	workingDir, ok := cleanVerificationWorkingDir(command.WorkingDir)
	if !ok {
		p.Rejects = append(p.Rejects, VerificationReject{
			Command: command.Command,
			Source:  command.Source,
			Reason:  "unsafe_working_dir",
		})

		return
	}

	command.WorkingDir = workingDir
	command.Path = filepath.ToSlash(strings.TrimSpace(command.Path))

	if command.Command == "" {
		return
	}

	key := command.WorkingDir + "\x00" + command.Command
	if _, ok := seen[key]; ok {
		return
	}

	seen[key] = struct{}{}

	p.Commands = append(p.Commands, command)
}

func (p VerificationPlan) CommandStrings() []string {
	out := make([]string, 0, len(p.Commands))
	for _, command := range p.Commands {
		if strings.TrimSpace(command.Command) == "" {
			continue
		}

		out = append(out, strings.TrimSpace(command.Command))
	}

	return out
}

func InferredTouchedVerificationCommands(paths []string) []string {
	plan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: ".",
		ChangedPaths:  paths,
	})
	out := make([]string, 0, len(plan.Commands))

	for _, command := range plan.Commands {
		if command.Source == VerificationCommandSourceInferred {
			out = append(out, command.Command)
		}
	}

	return out
}

func (p VerificationPlanner) inferTouchedPathCommand(rawPath string) (VerificationCommand, VerificationSkip, bool) {
	cleaned, skip, ok := cleanVerificationChangedPath(rawPath)
	if !ok {
		return VerificationCommand{}, skip, false
	}

	if verificationPathGeneratedByName(cleaned) {
		return VerificationCommand{}, VerificationSkip{Path: cleaned, Reason: VerificationSkipGeneratedPath}, false
	}

	if verificationFileHasGeneratedHeader(p.root, cleaned) {
		return VerificationCommand{}, VerificationSkip{Path: cleaned, Reason: VerificationSkipGeneratedFile}, false
	}

	moduleRoot, ok := p.closestGoModuleRoot(cleaned)
	if !ok {
		return VerificationCommand{}, VerificationSkip{Path: cleaned, Reason: VerificationSkipNoGoModuleRoot}, false
	}

	rootAbs := cleanAbsPath(p.root)
	moduleRel, _ := filepath.Rel(rootAbs, moduleRoot)

	moduleRel, _ = cleanVerificationWorkingDir(moduleRel)

	pkgDirAbs := filepath.Dir(filepath.Join(rootAbs, filepath.FromSlash(cleaned)))

	pkgRel, err := filepath.Rel(moduleRoot, pkgDirAbs)
	if err != nil {
		return VerificationCommand{}, VerificationSkip{Path: cleaned, Reason: VerificationSkipNoGoModuleRoot, Detail: err.Error()}, false
	}

	packageArg := "."
	if pkgRel != "." {
		packageArg = "./" + filepath.ToSlash(pkgRel)
	}

	return VerificationCommand{
		Command:    "go test " + packageArg,
		Argv:       []string{"go", "test", packageArg},
		WorkingDir: moduleRel,
		Source:     VerificationCommandSourceInferred,
		Path:       cleaned,
		Reason:     "changed Go package",
	}, VerificationSkip{}, true
}

func cleanVerificationChangedPath(rawPath string) (string, VerificationSkip, bool) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", VerificationSkip{Reason: VerificationSkipEmptyPath}, false
	}

	if filepath.IsAbs(rawPath) {
		return "", VerificationSkip{Path: rawPath, Reason: VerificationSkipAbsolutePath}, false
	}

	slashPath := filepath.ToSlash(rawPath)
	if strings.Contains(slashPath, "//") {
		return "", VerificationSkip{Path: slashPath, Reason: VerificationSkipEmptySegment}, false
	}

	parts := strings.Split(slashPath, "/")
	for _, part := range parts {
		switch part {
		case "":
			return "", VerificationSkip{Path: slashPath, Reason: VerificationSkipEmptySegment}, false
		case "..":
			return "", VerificationSkip{Path: slashPath, Reason: VerificationSkipParentTraversal}, false
		}
	}

	cleaned := filepath.ToSlash(filepath.Clean(rawPath))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", VerificationSkip{Path: slashPath, Reason: VerificationSkipParentTraversal}, false
	}

	if !strings.HasSuffix(cleaned, ".go") {
		return "", VerificationSkip{Path: cleaned, Reason: VerificationSkipNonGoPath}, false
	}

	if verificationPathHasSegment(cleaned, "vendor") {
		return "", VerificationSkip{Path: cleaned, Reason: VerificationSkipVendorPath}, false
	}

	return cleaned, VerificationSkip{}, true
}

func (p VerificationPlanner) closestGoModuleRoot(cleanedPath string) (string, bool) {
	rootAbs := cleanAbsPath(p.root)
	dir := filepath.Dir(filepath.Join(rootAbs, filepath.FromSlash(cleanedPath)))

	for {
		info, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil && !info.IsDir() {
			return dir, true
		}

		if samePath(dir, rootAbs) {
			return "", false
		}

		parent := filepath.Dir(dir)
		if samePath(parent, dir) {
			return "", false
		}

		rel, err := filepath.Rel(rootAbs, parent)
		if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "../") || rel == ".." {
			return "", false
		}

		dir = parent
	}
}

func cleanAbsPath(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}

	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}

	return filepath.Clean(root)
}

func nearestAncestorGoModuleRoot(start string) (string, bool) {
	dir := cleanAbsPath(start)
	for {
		info, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil && !info.IsDir() {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if samePath(parent, dir) {
			return "", false
		}

		dir = parent
	}
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func cleanVerificationWorkingDir(dir string) (string, bool) {
	dir = strings.TrimSpace(filepath.ToSlash(dir))
	if dir == "" || dir == "." {
		return "", true
	}

	if filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
		return "", false
	}

	cleaned := strings.TrimPrefix(path.Clean(dir), "./")
	if cleaned == "" || cleaned == "." {
		return "", true
	}

	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}

	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}

	return cleaned, true
}

func verificationPathHasSegment(path string, segment string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == segment {
			return true
		}
	}

	return false
}

func verificationPathGeneratedByName(path string) bool {
	path = filepath.ToSlash(path)

	base := strings.ToLower(filepath.Base(path))

	if verificationPathHasSegment(path, "generated") || verificationPathHasSegment(path, "gen") {
		return true
	}

	generatedSuffixes := []string{
		"_generated.go",
		".generated.go",
		".gen.go",
		".pb.go",
	}

	return slices.ContainsFunc(generatedSuffixes, func(suffix string) bool {
		return strings.HasSuffix(base, suffix)
	})
}

func verificationFileHasGeneratedHeader(root string, cleanedPath string) bool {
	fullPath := filepath.Join(cleanAbsPath(root), filepath.FromSlash(cleanedPath))

	body, err := os.ReadFile(fullPath)
	if err != nil {
		return false
	}

	header := string(body)
	if len(header) > 512 {
		header = header[:512]
	}

	return strings.Contains(strings.ToLower(header), "code generated") &&
		strings.Contains(strings.ToLower(header), "do not edit")
}
