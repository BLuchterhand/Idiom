package recovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DecompositionPacketSchemaVersion = "idiom.recovery.decomposition_packet.v1"

const (
	GateBriefLint      = "brief_lint"
	GateAtomIntake     = "atom_intake"
	GateScope          = "scope_gate"
	GateWorkspaceState = "workspace_state"
	GateRequiredTests  = "required_tests"
)

type DecompositionOptions struct {
	Now          time.Time
	PartialState PartialState
}

type DecompositionPacket struct {
	SchemaVersion  string            `json:"schema_version"`
	PacketID       string            `json:"packet_id"`
	FailureID      string            `json:"failure_id"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
	SourceRun      SourceRun         `json:"source_run"`
	LockedContract *ContractSnapshot `json:"locked_contract,omitempty"`
	Failure        Failure           `json:"failure"`
	PartialState   PartialState      `json:"partial_state"`
	ProductLinks   ProductLinks      `json:"product_links,omitempty"`
	ResidualWork   []ResidualWork    `json:"residual_work,omitempty"`
	CandidateAtoms []CandidateAtom   `json:"candidate_atoms,omitempty"`
	EvidenceRefs   []EvidenceRef     `json:"evidence_refs,omitempty"`
	NextAction     string            `json:"next_valid_action"`
}

type ProductLinks struct {
	PRDPaths    []string `json:"prd_paths,omitempty"`
	ADRPaths    []string `json:"adr_paths,omitempty"`
	TDDPaths    []string `json:"tdd_paths,omitempty"`
	LedgerPaths []string `json:"ledger_paths,omitempty"`
}

type ResidualWork struct {
	ID                string             `json:"id"`
	Title             string             `json:"title"`
	Reason            string             `json:"reason"`
	Paths             []string           `json:"paths,omitempty"`
	EvidenceRefs      []EvidenceRef      `json:"evidence_refs,omitempty"`
	InvariantFailures []InvariantFailure `json:"invariant_failures,omitempty"`
}

type CandidateAtom struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Complexity      string   `json:"complexity"`
	RequiredPaths   []string `json:"required_paths,omitempty"`
	ForbiddenPaths  []string `json:"forbidden_paths,omitempty"`
	RequiredTests   []string `json:"required_tests,omitempty"`
	Gates           []string `json:"gates,omitempty"`
	ResidualWorkIDs []string `json:"residual_work_ids,omitempty"`
	InvariantIDs    []string `json:"invariant_ids,omitempty"`
}

func BuildDecompositionPacket(record FailureRecord, opts DecompositionOptions) (DecompositionPacket, error) {
	if !validRecoveryComponent(record.FailureID) {
		return DecompositionPacket{}, fmt.Errorf("invalid recovery failure id %q", record.FailureID)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	partial := opts.PartialState
	if partial.DirtyStatus == "" && partial.ScopeStatus == "" {
		partial = record.PartialState
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	action := NextActionForPartialState(record, partial)
	packet := DecompositionPacket{
		SchemaVersion:  DecompositionPacketSchemaVersion,
		PacketID:       "packet-" + record.FailureID,
		FailureID:      record.FailureID,
		CreatedAt:      stamp,
		UpdatedAt:      stamp,
		SourceRun:      record.SourceRun,
		LockedContract: cleanContractSnapshot(record.LockedContract, record.SourceRun),
		Failure:        record.Failure,
		PartialState:   partial,
		ProductLinks:   productLinksForRecord(record, partial),
		EvidenceRefs:   record.Failure.EvidenceRefs,
		NextAction:     action,
	}
	if action != NextActionReviewDecomposition {
		return packet, nil
	}
	invariantFailures := cleanInvariantFailures(record.Failure.InvariantFailures)
	packet.Failure.InvariantFailures = invariantFailures
	paths := residualCandidatePathsForInvariantFailures(invariantFailures)
	if len(paths) == 0 {
		paths = residualCandidatePaths(record, partial)
	}
	if len(paths) == 0 {
		packet.NextAction = NextActionInspectFailure
		return packet, nil
	}
	groups := groupResidualPaths(paths)
	for i, group := range groups {
		residualID := fmt.Sprintf("residual-%03d", i+1)
		atomID := fmt.Sprintf("recovery-%03d", i+1)
		groupInvariantFailures := invariantFailuresForResidualGroup(invariantFailures, group)
		title := residualTitle(record, group, groupInvariantFailures)
		requiredTests := requiredTestsForPaths(group)
		packet.ResidualWork = append(packet.ResidualWork, ResidualWork{
			ID:                residualID,
			Title:             title,
			Reason:            residualReason(record, groupInvariantFailures),
			Paths:             group,
			EvidenceRefs:      residualEvidenceRefs(record, partial, group, requiredTests, groupInvariantFailures),
			InvariantFailures: groupInvariantFailures,
		})
		packet.CandidateAtoms = append(packet.CandidateAtoms, CandidateAtom{
			ID:              atomID,
			Title:           title,
			Complexity:      complexityForPaths(group),
			RequiredPaths:   group,
			RequiredTests:   requiredTests,
			Gates:           []string{GateWorkspaceState, GateBriefLint, GateAtomIntake, GateScope, GateRequiredTests},
			ResidualWorkIDs: []string{residualID},
			InvariantIDs:    invariantIDsForFailures(groupInvariantFailures),
		})
	}
	return packet, nil
}

func WriteDecompositionPacketForFailureRecord(root string, record FailureRecord, now time.Time) (DecompositionPacket, string, error) {
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          now,
		PartialState: record.PartialState,
	})
	if err != nil {
		return DecompositionPacket{}, "", err
	}
	_, relPath, err := WriteDecompositionPacket(root, packet, "")
	if err != nil {
		return DecompositionPacket{}, "", err
	}
	return packet, relPath, nil
}

func WriteDecompositionPacket(root string, packet DecompositionPacket, outputPath string) (string, string, error) {
	fullPath, relPath, err := DecompositionPacketPath(root, packet.FailureID, outputPath)
	if err != nil {
		return "", "", err
	}
	if err := ensureRecoveryWritePath(root, fullPath); err != nil {
		return "", "", fmt.Errorf("recovery decomposition packet path rejected: %w", err)
	}
	if existing, _, found, err := TryLoadDecompositionPacket(fullPath); err == nil && found && strings.TrimSpace(existing.CreatedAt) != "" {
		packet.CreatedAt = existing.CreatedAt
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir recovery packet dir: %w", err)
	}
	body, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal recovery decomposition packet: %w", err)
	}
	tmpPath := fullPath + ".tmp"
	if err := writeRecoveryTempFileExclusive(tmpPath, append(body, '\n')); err != nil {
		return "", "", fmt.Errorf("write recovery decomposition packet: %w", err)
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("replace recovery decomposition packet: %w", err)
	}
	return fullPath, relPath, nil
}

func TryLoadDecompositionPacket(path string) (DecompositionPacket, string, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return DecompositionPacket{}, "", false, fmt.Errorf("recovery packet path is empty")
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DecompositionPacket{}, filepath.ToSlash(path), false, nil
	}
	if err != nil {
		return DecompositionPacket{}, filepath.ToSlash(path), false, fmt.Errorf("read recovery decomposition packet %s: %w", path, err)
	}
	var packet DecompositionPacket
	if err := json.Unmarshal(body, &packet); err != nil {
		return DecompositionPacket{}, filepath.ToSlash(path), false, fmt.Errorf("decode recovery decomposition packet %s: %w", path, err)
	}
	if packet.SchemaVersion != DecompositionPacketSchemaVersion {
		return DecompositionPacket{}, filepath.ToSlash(path), false, fmt.Errorf("unsupported recovery decomposition packet schema %q in %s", packet.SchemaVersion, path)
	}
	return packet, filepath.ToSlash(path), true, nil
}

func DecompositionPacketPath(root string, failureID string, outputPath string) (string, string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", fmt.Errorf("workspace root is empty")
	}
	if !validRecoveryComponent(failureID) {
		return "", "", fmt.Errorf("invalid recovery failure id %q", failureID)
	}
	if strings.TrimSpace(outputPath) == "" {
		relPath := filepath.ToSlash(filepath.Join(".idiom", "recovery", failureID, "packet.json"))
		return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
	}
	fullPath := outputPath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(root, fullPath)
	}
	relPath, err := filepath.Rel(root, fullPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		relPath = fullPath
	}
	return fullPath, filepath.ToSlash(relPath), nil
}

func FindCandidateAtom(packet DecompositionPacket, atomID string) (CandidateAtom, bool) {
	atomID = strings.TrimSpace(atomID)
	for _, atom := range packet.CandidateAtoms {
		if atom.ID == atomID {
			return atom, true
		}
	}
	return CandidateAtom{}, false
}

func RenderRecoveryBrief(root string, packet DecompositionPacket, atom CandidateAtom) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Brief: %s\n\n", atom.Title)
	fmt.Fprintf(&b, "Status: ready-for-dispatch\n")
	fmt.Fprintf(&b, "Recovery Packet: `%s`\n", packet.PacketID)
	fmt.Fprintf(&b, "Failure ID: `%s`\n\n", packet.FailureID)
	if packet.LockedContract != nil {
		fmt.Fprintf(&b, "## Locked Contract\n\n")
		writeBriefContractSnapshot(&b, packet.LockedContract)
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Files to Modify (EXHAUSTIVE allowlist)\n\n")
	fmt.Fprintf(&b, "| File | Change |\n")
	fmt.Fprintf(&b, "| --- | --- |\n")
	for _, path := range atom.RequiredPaths {
		change := recoveryBriefChangeForPath(packet, atom, path)
		if fileExists(root, path) {
			fmt.Fprintf(&b, "| `%s` | MODIFY `%s:1` to repair %s. |\n", path, path, change)
		} else {
			fmt.Fprintf(&b, "| `%s` | CREATE the recovery artifact required to repair %s. |\n", path, change)
		}
	}
	fmt.Fprintf(&b, "\n## Verified Current State\n\n")
	for _, path := range atom.RequiredPaths {
		if fileExists(root, path) {
			fmt.Fprintf(&b, "- `%s`: line 1 exists in the current workspace and bounds this recovery atom.\n", path)
		} else {
			fmt.Fprintf(&b, "- `%s`: file is absent in the current workspace and is treated as a create target.\n", path)
		}
	}
	if failures := invariantFailuresForAtom(packet, atom); len(failures) > 0 {
		fmt.Fprintf(&b, "\n## Invariant Failures\n\n")
		fmt.Fprintf(&b, "| Invariant | Field/Path | Expected | Actual | Required Repair Evidence |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
		for _, failure := range failures {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s |\n",
				failure.InvariantID,
				briefInvariantFieldPath(failure),
				briefTableCell(failure.Expected),
				briefTableCell(failure.Actual),
				briefRepairEvidenceCell(failure.RequiredRepairEvidence),
			)
		}
	}
	fmt.Fprintf(&b, "\n## Required Edits\n\n")
	fmt.Fprintf(&b, "Apply only the residual recovery repair for candidate `%s`; preserve the locked contract and do not broaden scope without a typed authority delta.\n", atom.ID)
	if packet.Failure.Summary != "" {
		fmt.Fprintf(&b, "\nFailure summary: %s\n", packet.Failure.Summary)
	}
	if hasProductLinks(packet.ProductLinks) {
		fmt.Fprintf(&b, "\n## Product Links\n\n")
		writeBriefProductLinks(&b, "PRD", packet.ProductLinks.PRDPaths)
		writeBriefProductLinks(&b, "ADR", packet.ProductLinks.ADRPaths)
		writeBriefProductLinks(&b, "TDD", packet.ProductLinks.TDDPaths)
		writeBriefProductLinks(&b, "Ledger", packet.ProductLinks.LedgerPaths)
	}
	fmt.Fprintf(&b, "\n## Acceptance\n\n")
	for _, test := range atom.RequiredTests {
		fmt.Fprintf(&b, "- `%s` succeeds.\n", test)
	}
	if len(atom.RequiredTests) == 0 {
		fmt.Fprintf(&b, "- `go test ./...` succeeds.\n")
	}
	return b.String()
}

func hasProductLinks(links ProductLinks) bool {
	return len(links.PRDPaths)+len(links.ADRPaths)+len(links.TDDPaths)+len(links.LedgerPaths) > 0
}

func writeBriefProductLinks(b *strings.Builder, label string, paths []string) {
	for _, path := range paths {
		fmt.Fprintf(b, "- %s: `%s`\n", label, path)
	}
}

func writeBriefContractSnapshot(b *strings.Builder, snapshot *ContractSnapshot) {
	if snapshot == nil {
		return
	}
	for _, item := range []struct {
		label string
		value string
	}{
		{label: "Contract ID", value: snapshot.ContractID},
		{label: "Contract Hash", value: snapshot.ContractHash},
		{label: "Snapshot ID", value: snapshot.SnapshotID},
		{label: "Snapshot Path", value: snapshot.SnapshotPath},
		{label: "Runtime Contract", value: snapshot.RuntimeContractID},
		{label: "Protocol Hash", value: snapshot.ProtocolHash},
	} {
		if item.value != "" {
			fmt.Fprintf(b, "- %s: `%s`\n", item.label, item.value)
		}
	}
	if len(snapshot.RequiredEditPaths) > 0 {
		fmt.Fprintf(b, "- Required Edit Paths: `%s`\n", strings.Join(snapshot.RequiredEditPaths, "`, `"))
	}
	if len(snapshot.AllowedEditPaths) > 0 {
		fmt.Fprintf(b, "- Allowed Edit Paths: `%s`\n", strings.Join(snapshot.AllowedEditPaths, "`, `"))
	}
	if len(snapshot.WriteAllowedPaths) > 0 {
		fmt.Fprintf(b, "- Write Allowed Paths: `%s`\n", strings.Join(snapshot.WriteAllowedPaths, "`, `"))
	}
	if snapshot.WriteAllowedExplicit {
		b.WriteString("- Write Allowed Explicit: `true`\n")
	}
}

func residualCandidatePaths(record FailureRecord, partial PartialState) []string {
	return cleanPathList(append(append([]string{}, record.SourceRun.RequiredEditPaths...), partial.ChangedPaths...))
}

func residualCandidatePathsForInvariantFailures(failures []InvariantFailure) []string {
	var paths []string
	for _, failure := range failures {
		if path := cleanRelPath(failure.Path); path != "" {
			paths = append(paths, path)
		}
	}
	return cleanPathList(paths)
}

func residualEvidenceRefs(record FailureRecord, partial PartialState, paths []string, requiredTests []string, failures []InvariantFailure) []EvidenceRef {
	requiredPaths := pathSet(record.SourceRun.RequiredEditPaths)
	changedPaths := pathSet(partial.ChangedPaths)
	groupPaths := pathSet(paths)
	var refs []EvidenceRef
	for _, path := range cleanPathList(paths) {
		if requiredPaths[path] {
			refs = appendUniqueEvidenceRef(refs, EvidenceRef{Kind: "required_path", Path: path})
		}
		if changedPaths[path] {
			refs = appendUniqueEvidenceRef(refs, EvidenceRef{Kind: "changed_path", Path: path})
		}
	}
	for _, ref := range record.Failure.EvidenceRefs {
		if evidenceRefAppliesToResidual(ref, groupPaths) {
			refs = appendUniqueEvidenceRef(refs, ref)
		}
	}
	for _, test := range requiredTests {
		if test = strings.TrimSpace(test); test != "" {
			refs = appendUniqueEvidenceRef(refs, EvidenceRef{Kind: "required_test", ID: test})
		}
	}
	for _, failure := range failures {
		refs = appendUniqueEvidenceRef(refs, EvidenceRef{Kind: "invariant_failure", ID: failure.InvariantID, Path: failure.Path})
	}
	return refs
}

func pathSet(paths []string) map[string]bool {
	set := map[string]bool{}
	for _, path := range cleanPathList(paths) {
		set[path] = true
	}
	return set
}

func evidenceRefAppliesToResidual(ref EvidenceRef, paths map[string]bool) bool {
	if strings.TrimSpace(ref.Kind) == "" {
		return false
	}
	path := cleanRelPath(ref.Path)
	if path == "" {
		return true
	}
	if paths[path] {
		return true
	}
	kind := strings.ToLower(strings.TrimSpace(ref.Kind))
	switch kind {
	case "progress_state", "plan_artifact", "executor_failure", "review_repair_status", "review_repair_reason":
		return true
	}
	return strings.Contains(kind, "test") || strings.Contains(kind, "verification")
}

func appendUniqueEvidenceRef(refs []EvidenceRef, ref EvidenceRef) []EvidenceRef {
	ref = normalizeEvidenceRef(ref)
	if strings.TrimSpace(ref.Kind) == "" {
		return refs
	}
	for _, existing := range refs {
		if normalizeEvidenceRef(existing) == ref {
			return refs
		}
	}
	return append(refs, ref)
}

func normalizeEvidenceRef(ref EvidenceRef) EvidenceRef {
	return EvidenceRef{
		Kind:    strings.TrimSpace(ref.Kind),
		Path:    cleanRelPath(ref.Path),
		ID:      strings.TrimSpace(ref.ID),
		Summary: "",
	}
}

func groupResidualPaths(paths []string) [][]string {
	paths = cleanPathList(paths)
	if len(paths) <= 4 {
		return [][]string{paths}
	}
	groupsByDir := map[string][]string{}
	var dirs []string
	for _, path := range paths {
		dir := filepath.ToSlash(filepath.Dir(path))
		if dir == "." {
			dir = ""
		}
		if _, ok := groupsByDir[dir]; !ok {
			dirs = append(dirs, dir)
		}
		groupsByDir[dir] = append(groupsByDir[dir], path)
	}
	sort.Strings(dirs)
	var groups [][]string
	for _, dir := range dirs {
		groups = append(groups, groupsByDir[dir])
	}
	return groups
}

func residualTitle(record FailureRecord, paths []string, failures []InvariantFailure) string {
	if len(failures) == 1 {
		if path := cleanRelPath(failures[0].Path); path != "" {
			return fmt.Sprintf("Repair %s invariant on %s", failures[0].InvariantID, filepath.Base(path))
		}
		return fmt.Sprintf("Repair %s invariant", failures[0].InvariantID)
	}
	if len(paths) == 1 {
		return fmt.Sprintf("Recover %s after %s", filepath.Base(paths[0]), record.Failure.Class)
	}
	dir := commonDisplayDir(paths)
	if dir != "" {
		return fmt.Sprintf("Recover %s paths after %s", dir, record.Failure.Class)
	}
	return fmt.Sprintf("Recover residual paths after %s", record.Failure.Class)
}

func residualReason(record FailureRecord, failures []InvariantFailure) string {
	ids := invariantIDsForFailures(failures)
	if len(ids) > 0 {
		return strings.Join(ids, ", ")
	}
	return record.Failure.Class
}

func invariantFailuresForResidualGroup(failures []InvariantFailure, paths []string) []InvariantFailure {
	groupPaths := pathSet(paths)
	var out []InvariantFailure
	for _, failure := range failures {
		if path := cleanRelPath(failure.Path); path != "" && groupPaths[path] {
			out = append(out, failure)
		}
	}
	return out
}

func invariantIDsForFailures(failures []InvariantFailure) []string {
	seen := map[string]bool{}
	var ids []string
	for _, failure := range failures {
		id := strings.TrimSpace(failure.InvariantID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func invariantFailuresForAtom(packet DecompositionPacket, atom CandidateAtom) []InvariantFailure {
	neededResiduals := map[string]bool{}
	for _, id := range atom.ResidualWorkIDs {
		if id = strings.TrimSpace(id); id != "" {
			neededResiduals[id] = true
		}
	}
	var failures []InvariantFailure
	for _, residual := range packet.ResidualWork {
		if neededResiduals[residual.ID] {
			failures = append(failures, residual.InvariantFailures...)
		}
	}
	if len(failures) > 0 {
		return cleanInvariantFailures(failures)
	}
	return cleanInvariantFailures(packet.Failure.InvariantFailures)
}

func recoveryBriefChangeForPath(packet DecompositionPacket, atom CandidateAtom, path string) string {
	failures := invariantFailuresForAtom(packet, atom)
	var ids []string
	for _, failure := range failures {
		if cleanRelPath(failure.Path) == cleanRelPath(path) {
			ids = append(ids, failure.InvariantID)
		}
	}
	ids = cleanRecoveryTextList(ids, 120)
	if len(ids) > 0 {
		return "invariant " + strings.Join(ids, ", ")
	}
	return "`" + packet.Failure.Class + "`"
}

func briefInvariantFieldPath(failure InvariantFailure) string {
	if failure.FieldPath != "" && failure.Path != "" {
		return failure.FieldPath + " / " + failure.Path
	}
	if failure.FieldPath != "" {
		return failure.FieldPath
	}
	return failure.Path
}

func briefTableCell(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func briefRepairEvidenceCell(values []string) string {
	var cleaned []string
	for _, value := range cleanRecoveryTextList(values, 200) {
		cleaned = append(cleaned, briefTableCell(value))
	}
	return strings.Join(cleaned, "; ")
}

func complexityForPaths(paths []string) string {
	switch {
	case len(paths) <= 2:
		return "C1"
	case len(paths) <= 6:
		return "C2"
	default:
		return "C3"
	}
}

func requiredTestsForPaths(paths []string) []string {
	seen := map[string]bool{}
	var tests []string
	for _, path := range cleanPathList(paths) {
		test := "go test ./..."
		switch {
		case strings.HasSuffix(path, ".go"):
			dir := filepath.ToSlash(filepath.Dir(path))
			if dir != "." && dir != "" {
				test = "go test ./" + dir
			}
		}
		if !seen[test] {
			seen[test] = true
			tests = append(tests, test)
		}
	}
	if len(tests) == 0 {
		return []string{"go test ./..."}
	}
	return tests
}

func productLinksForRecord(record FailureRecord, partial PartialState) ProductLinks {
	var links ProductLinks
	paths := cleanPathList(append([]string{
		record.SourceRun.PromptFile,
		record.SourceRun.PlanPath,
		record.SourceRun.ProgressStatePath,
	}, append(append(record.SourceRun.RequiredEditPaths, record.SourceRun.AllowedEditPaths...), partial.ChangedPaths...)...))
	for _, ref := range record.Failure.EvidenceRefs {
		paths = cleanPathList(append(paths, ref.Path))
	}
	for _, path := range paths {
		lower := strings.ToLower(path)
		base := strings.ToLower(filepath.Base(path))
		switch {
		case strings.Contains(lower, "/prd") || strings.HasPrefix(base, "prd"):
			links.PRDPaths = appendUnique(links.PRDPaths, path)
		case strings.Contains(lower, "/adr") || strings.HasPrefix(base, "adr"):
			links.ADRPaths = appendUnique(links.ADRPaths, path)
		case strings.Contains(lower, "/tdd") || strings.HasPrefix(base, "tdd"):
			links.TDDPaths = appendUnique(links.TDDPaths, path)
		case strings.Contains(lower, "ledger") || strings.Contains(lower, "/implementation"):
			links.LedgerPaths = appendUnique(links.LedgerPaths, path)
		}
	}
	return links
}

func commonDisplayDir(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(paths[0]))
	for _, path := range paths[1:] {
		next := filepath.ToSlash(filepath.Dir(path))
		for dir != "" && dir != "." && !strings.HasPrefix(next+"/", dir+"/") {
			dir = filepath.ToSlash(filepath.Dir(dir))
		}
	}
	if dir == "." {
		return ""
	}
	return dir
}

func fileExists(root string, path string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
