package implementationdispatch

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
)

func ResolveScope(input ScopeInput) ScopeResolution {
	requiredPaths := appendUniqueStrings(nil, input.RequiredTargets...)
	allowedPaths := appendUniqueStrings(nil, input.AllowedTargets...)
	planDocuments, blockers := PlanDocumentsAndResolutionBlockers(input)

	for _, doc := range planDocuments {
		if PlanDocumentScopeIsRequired(doc, input.WorkspaceRoot) {
			requiredPaths = appendUniqueStrings(requiredPaths, doc.EditPaths...)
			continue
		}

		allowedPaths = appendUniqueStrings(allowedPaths, doc.EditPaths...)
	}

	return ScopeResolution{
		RequiredPaths: requiredPaths,
		AllowedPaths:  allowedPaths,
		PlanDocuments: planDocuments,
		Blockers:      blockers,
	}
}

func PlanDocumentsAndResolutionBlockers(input ScopeInput) ([]PlanDocument, []ResolutionBlocker) {
	root := strings.TrimSpace(input.WorkspaceRoot)
	if root == "" {
		return nil, nil
	}

	refs := PlanDocumentRefs(input.Prompt, input.TargetRefs)
	explicitEditPaths := appendUniqueStrings(nil, input.RequiredTargets...)
	explicitEditPaths = appendUniqueStrings(explicitEditPaths, input.AllowedTargets...)

	var (
		docs     []PlanDocument
		blockers []ResolutionBlocker
	)

	blockUnresolvedPlanDocuments := PromptRequiresResolvedPlanDocument(input, refs)

	for _, ref := range refs {
		if RefIsExplicitEditPath(ref, explicitEditPaths) {
			continue
		}

		doc, candidates, ok := ResolvePlanDocument(root, ref)
		if !ok {
			if blocker, blocked := ResolutionBlockerForRef(ref, candidates, blockUnresolvedPlanDocuments); blocked {
				blockers = append(blockers, blocker)
			}

			continue
		}

		duplicate := false
		for _, existing := range docs {
			if existing.Path == doc.Path {
				duplicate = true
				break
			}
		}

		if duplicate {
			continue
		}

		docs = append(docs, doc)
	}

	return docs, blockers
}

func RefIsExplicitEditPath(ref string, editPaths []string) bool {
	ref, ok := pathparse.NormalizeWorkspacePathToken(ref)
	if !ok {
		return false
	}

	for _, path := range editPaths {
		path, ok := pathparse.NormalizeWorkspacePathToken(path)
		if ok && path == ref {
			return true
		}
	}

	return false
}

func PlanDocumentRefs(prompt string, targetRefs []string) []string {
	refs := appendUniqueStrings(nil, targetRefs...)
	return appendUniqueStrings(refs, pathparse.MentionedWorkspacePaths(prompt)...)
}

func ResolvePlanDocument(root string, ref string) (PlanDocument, []string, bool) {
	normalized, ok := pathparse.NormalizeWorkspacePathToken(ref)
	if !ok || !LooksLikePlanDocument(normalized) {
		return PlanDocument{}, nil, false
	}

	if doc, ok := ReadPlanDocument(root, normalized); ok {
		return doc, []string{doc.Path}, true
	}

	if !CanResolvePlanDocumentBasename(normalized) {
		return PlanDocument{}, nil, false
	}

	candidates := PlanDocumentBasenameMatches(root, filepath.Base(normalized))
	if len(candidates) != 1 {
		return PlanDocument{}, candidates, false
	}

	doc, ok := ReadPlanDocument(root, candidates[0])

	return doc, candidates, ok
}

func CanResolvePlanDocumentBasename(ref string) bool {
	ref = strings.TrimSpace(filepath.ToSlash(filepath.Clean(ref)))
	if ref == "" || ref == "." || strings.Contains(ref, "/") {
		return false
	}

	return LooksLikePlanDocument(ref)
}

func PlanDocumentBasenameMatches(root string, basename string) []string {
	root = strings.TrimSpace(root)
	basename = strings.TrimSpace(basename)

	if root == "" || basename == "" || !LooksLikePlanDocument(basename) {
		return nil
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}

	var matches []string

	_ = filepath.WalkDir(rootAbs, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		rel, relErr := filepath.Rel(rootAbs, path)
		if relErr != nil {
			return nil
		}

		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel != "." && skipPlanDocumentSearchDir(entry.Name()) {
				return filepath.SkipDir
			}

			return nil
		}

		if entry.Name() != basename || !entry.Type().IsRegular() {
			return nil
		}

		if IsRefineScratchpadPath(rel) {
			return nil
		}

		if _, ok := ReadPlanDocument(root, rel); !ok {
			return nil
		}

		matches = append(matches, rel)

		return nil
	})

	sort.Strings(matches)

	return matches
}

func skipPlanDocumentSearchDir(name string) bool {
	switch strings.TrimSpace(name) {
	case ".git", ".idiom", "node_modules", "vendor", "dist", "build", ".cache", ".direnv":
		return true
	default:
		return false
	}
}

func PromptRequiresResolvedPlanDocument(input ScopeInput, refs []string) bool {
	if !input.MutationCapable {
		return false
	}

	hasPlanDocumentRef := false
	for _, ref := range refs {
		normalized, ok := pathparse.NormalizeWorkspacePathToken(ref)
		if ok && LooksLikePlanDocument(normalized) {
			hasPlanDocumentRef = true
			break
		}
	}

	if !hasPlanDocumentRef {
		return false
	}

	if strings.TrimSpace(input.RouteReason) == "typed_plan_execute_command" {
		return true
	}

	words := wordText(input.Prompt)
	for _, phrase := range []string{
		" start working on ",
		" start woring on ",
		" start implementing ",
		" begin working on ",
		" begin implementing ",
		" implement ",
		" implementing ",
		" implement from ",
		" implement the plan ",
		" implement this plan ",
		" work from ",
		" design doc ",
		" design document ",
		" plan doc ",
		" plan document ",
		" implementation handoff ",
	} {
		if strings.Contains(words, phrase) {
			return true
		}
	}

	return false
}

func ResolutionBlockerForRef(ref string, candidates []string, blockUnresolved bool) (ResolutionBlocker, bool) {
	normalized, ok := pathparse.NormalizeWorkspacePathToken(ref)
	if !ok || !LooksLikePlanDocument(normalized) || !blockUnresolved {
		return ResolutionBlocker{}, false
	}

	if len(candidates) > 1 {
		return ResolutionBlocker{
			Ref:        normalized,
			Reason:     "ambiguous",
			Candidates: candidates,
		}, true
	}

	return ResolutionBlocker{
		Ref:    normalized,
		Reason: "missing",
	}, true
}

func ReadPlanDocument(root string, ref string) (PlanDocument, bool) {
	ref, ok := pathparse.NormalizeWorkspacePathToken(ref)
	if !ok || !LooksLikePlanDocument(ref) {
		return PlanDocument{}, false
	}

	if IsRefineScratchpadPath(ref) {
		return PlanDocument{}, false
	}

	abs, ok := WorkspaceFile(root, ref)
	if !ok {
		return PlanDocument{}, false
	}

	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() > 512*1024 {
		return PlanDocument{}, false
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return PlanDocument{}, false
	}

	content := string(body)
	obligationContent := content

	if IsAtomizedPlanDocumentPath(ref) {
		obligationContent = TextBeforeHeading(content, "source design document")
	}

	editPaths := PlanDocumentPathsFromText(content, ref)

	return PlanDocument{
		Path:               ref,
		Content:            content,
		EditPaths:          editPaths,
		AuthorizedNewFiles: plancontract.AuthorizedNewFilePathsFromPrompt(content),
		TestObligations:    DesignDocTestObligations(obligationContent),
		ReferenceOnly:      len(editPaths) == 0 && !IsAtomizedPlanDocumentPath(ref),
	}, true
}

func PlanDocumentScopeIsRequired(doc PlanDocument, root string) bool {
	return IsAtomizedPlanDocumentPath(doc.Path) ||
		IsRefinedAcceptedPlanDocumentPath(doc.Path) ||
		planDocumentScopeIsAllMissing(root, doc.EditPaths)
}

func IsRefinedAcceptedPlanDocumentPath(path string) bool {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if normalized == "" || normalized == "." {
		return false
	}

	if !strings.HasPrefix(normalized, ".idiom/refine/") {
		return false
	}

	base := filepath.Base(normalized)
	return base == "accepted.md" || strings.HasSuffix(base, ".dispatch.md")
}

func planDocumentScopeIsAllMissing(root string, paths []string) bool {
	if strings.TrimSpace(root) == "" || len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		abs, ok := WorkspaceFile(root, path)
		if !ok {
			return false
		}

		if _, err := os.Stat(abs); err == nil || !os.IsNotExist(err) {
			return false
		}
	}

	return true
}

func PlanDocumentPathsFromText(text string, sourceRef string) []string {
	found := pathparse.MentionedWorkspacePathsUnderHeadings(text, pathparse.PlanFileScopeHeadings()...)

	var paths []string
	for _, candidate := range found {
		path, ok := pathparse.NormalizeWorkspacePathToken(candidate)
		if !ok || path == sourceRef || !LooksLikeConcreteFile(path) {
			continue
		}

		paths = appendUniqueStrings(paths, path)
	}

	return paths
}

func appendUniqueStrings(values []string, candidates ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(candidates))
	for _, value := range values {
		seen[value] = struct{}{}
	}

	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}

		seen[candidate] = struct{}{}
		values = append(values, candidate)
	}

	return values
}

func DesignDocTestObligations(text string) []string {
	var obligations []string

	obligations = append(obligations, MinimumTestObligations(text)...)
	for _, heading := range []string{"design doc test obligations", "verification", "acceptance", "acceptance criteria"} {
		obligations = append(obligations, TestObligationsFromLines(MarkdownSectionLines(text, heading))...)
	}

	return appendUniqueStrings(nil, obligations...)
}

func MinimumTestObligations(text string) []string {
	var obligations []string

	active := false
	sawItem := false

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
		normalized := NormalizeMarkdownHeading(line)

		if strings.Contains(normalized, "minimum tests") {
			active = true
			sawItem = false

			continue
		}

		if !active {
			continue
		}

		if line == "" {
			continue
		}

		if sawItem && (LooksLikeMarkdownHeading(line) || LooksLikeNumberedStep(line)) {
			break
		}

		obligation := CleanObligationLine(line)
		if obligation == "" {
			continue
		}

		if LineLooksLikeTestObligation(obligation) {
			obligations = append(obligations, obligation)
			sawItem = true
		}
	}

	return obligations
}

func MarkdownSectionLines(text string, heading string) []string {
	want := NormalizeMarkdownHeading(heading)

	var lines []string

	active := false

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
		normalized := NormalizeMarkdownHeading(line)

		if active && LooksLikeMarkdownHeading(line) {
			break
		}

		if normalized == want {
			active = true
			continue
		}

		if active {
			lines = append(lines, raw)
		}
	}

	return lines
}

func TextBeforeHeading(text string, heading string) string {
	want := NormalizeMarkdownHeading(heading)

	var kept []string

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
		if NormalizeMarkdownHeading(line) == want {
			break
		}

		kept = append(kept, raw)
	}

	return strings.Join(kept, "\n")
}

func TestObligationsFromLines(lines []string) []string {
	var obligations []string

	inFence := false

	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}

		isListItem := strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") || LooksLikeNumberedStep(line)
		obligation := CleanObligationLine(line)

		if obligation == "" {
			continue
		}

		if inFence {
			if LineLooksLikeVerificationCommand(obligation) {
				obligations = append(obligations, obligation)
			}

			continue
		}

		if !isListItem && !LineLooksLikeVerificationCommand(obligation) {
			continue
		}

		if LineLooksLikeTestObligation(obligation) {
			obligations = append(obligations, obligation)
		}
	}

	return obligations
}

func CleanObligationLine(line string) string {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ">"))
	line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
	line = strings.TrimSpace(strings.TrimPrefix(line, "*"))

	for i, r := range line {
		if i == 0 && r >= '0' && r <= '9' {
			continue
		}

		if i > 0 && (r == '.' || r == ')') {
			prefix := line[:i]
			if strings.Trim(prefix, "0123456789") == "" {
				return strings.TrimSpace(line[i+1:])
			}
		}

		break
	}

	return strings.TrimSpace(line)
}

func LineLooksLikeTestObligation(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}

	if strings.Contains(lower, "design doc test obligations") && strings.Contains(lower, "represented in source") {
		return false
	}

	if LineLooksLikeVerificationCommand(line) {
		return true
	}

	return strings.Contains(lower, "test") ||
		strings.Contains(lower, "_test.") ||
		strings.Contains(lower, "coverage") ||
		strings.Contains(lower, "verification")
}

func LineLooksLikeVerificationCommand(line string) bool {
	lower := strings.ToLower(strings.Trim(strings.TrimSpace(line), "`"))
	for _, prefix := range []string{
		"go test",
		"go build",
		"make test",
		"make build",
		"git diff --check",
		"pytest",
		"npm test",
		"pnpm test",
		"yarn test",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
}

func LooksLikeNumberedStep(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}

	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func NormalizeMarkdownHeading(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "#*: `\"'")
	value = strings.TrimSuffix(value, ":")
	value = strings.ToLower(value)
	value = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(value)

	return strings.Join(strings.Fields(value), " ")
}

func LooksLikeMarkdownHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	return strings.HasPrefix(trimmed, "#") ||
		(strings.HasPrefix(trimmed, "**") && strings.Contains(trimmed, "**")) ||
		strings.HasSuffix(trimmed, ":")
}

func wordText(prompt string) string {
	prompt = strings.TrimSpace(strings.ToLower(prompt))
	if prompt == "" {
		return " "
	}

	var b strings.Builder
	b.Grow(len(prompt) + 2)
	b.WriteByte(' ')

	lastSpace := true
	for _, r := range prompt {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}

		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}

	if !lastSpace {
		b.WriteByte(' ')
	}

	return b.String()
}
