package codingrules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	rulescontract "idiom/internal/contracts/codingrules"
	"idiom/internal/repository/discovery"
)

const ProjectPolicyPath = ".idiom/coding_rules.json"

type CheckOptions struct {
	ProjectRoot string
	ConfigPath  string
	Paths       []string
	Policy      *rulescontract.PolicyFile
}

type CheckResult struct {
	Report          rulescontract.Report
	ConfigFound     bool
	SuppressedCount int
}

type effectiveRule struct {
	ID          rulescontract.RuleID
	Enabled     bool
	Enforcement rulescontract.EnforcementLevel
	Severity    rulescontract.FindingSeverity
	Include     []string
	Exclude     []string
	Options     rulescontract.RuleOptions
}

func LoadPolicy(projectRoot string, configPath string) (rulescontract.PolicyFile, string, bool, error) {
	explicitConfig := strings.TrimSpace(configPath) != ""
	if strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(projectRoot, ProjectPolicyPath)
	}

	body, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			if explicitConfig {
				return rulescontract.PolicyFile{}, "", false, fmt.Errorf("coding rules: policy %q does not exist", configPath)
			}

			return rulescontract.DefaultPolicy(), "", false, nil
		}

		return rulescontract.PolicyFile{}, "", false, fmt.Errorf("coding rules: read policy %q: %w", configPath, err)
	}
	defer body.Close()

	policy, err := rulescontract.DecodePolicy(body)
	if err != nil {
		return rulescontract.PolicyFile{}, "", false, fmt.Errorf("coding rules: parse policy %q: %w", configPath, err)
	}

	return policy, configPath, true, nil
}

func CheckWorkspace(ctx context.Context, opts CheckOptions) (CheckResult, error) {
	root := strings.TrimSpace(opts.ProjectRoot)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return CheckResult{}, err
		}
		root = cwd
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return CheckResult{}, err
	}

	var (
		policy      rulescontract.PolicyFile
		configPath  string
		configFound bool
	)

	if opts.Policy != nil {
		policy = rulescontract.NormalizePolicy(*opts.Policy)
		if err := rulescontract.ValidatePolicy(policy); err != nil {
			return CheckResult{}, err
		}
	} else {
		policy, configPath, configFound, err = LoadPolicy(root, opts.ConfigPath)
		if err != nil {
			return CheckResult{}, err
		}
	}

	files, err := goFilesForOptions(root, opts.Paths)
	if err != nil {
		return CheckResult{}, err
	}

	var findings []rulescontract.Finding
	suppressed := 0

	for _, rule := range effectiveRules(policy) {
		if !rule.Enabled || rule.Enforcement == rulescontract.EnforcementDisabled {
			continue
		}

		switch rule.ID {
		case rulescontract.RuleNoPassThroughWrapper:
			ruleFindings, ruleSuppressed, err := checkGoPassThroughWrappers(ctx, root, files, rule, policy.Suppressions)
			if err != nil {
				return CheckResult{}, err
			}
			findings = append(findings, ruleFindings...)
			suppressed += ruleSuppressed
		}
	}

	sortFindings(findings)
	rulescontract.AddFindingHashes(findings)

	report := rulescontract.Report{
		SchemaVersion: rulescontract.ReportSchemaVersion,
		WorkspaceRoot: filepath.ToSlash(root),
		ConfigPath:    filepath.ToSlash(configPath),
		Status:        rulescontract.EffectiveStatus(findings),
		Findings:      findings,
		Summary: rulescontract.Summary{
			FilesScanned: len(files),
			Findings:     len(findings),
			Suppressed:   suppressed,
		},
	}

	for _, finding := range findings {
		switch finding.Severity {
		case rulescontract.SeverityError:
			report.Summary.Errors++
		case rulescontract.SeverityWarning:
			report.Summary.Warnings++
		case rulescontract.SeverityInfo:
			report.Summary.Infos++
		}

		if finding.Enforcement == rulescontract.EnforcementBlocking {
			report.Summary.Blocking++
		}
	}

	return CheckResult{Report: report, ConfigFound: configFound, SuppressedCount: suppressed}, nil
}

func PolicyExists(projectRoot string, configPath string) bool {
	if strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(projectRoot, ProjectPolicyPath)
	}

	info, err := os.Stat(configPath)
	return err == nil && !info.IsDir()
}

func effectiveRules(policy rulescontract.PolicyFile) []effectiveRule {
	defaultEnabled := true
	if policy.Defaults.Enabled != nil {
		defaultEnabled = *policy.Defaults.Enabled
	}

	defaultEnforcement := policy.Defaults.Enforcement
	if defaultEnforcement == "" {
		defaultEnforcement = rulescontract.EnforcementAdvisory
	}

	defaultSeverity := policy.Defaults.Severity
	if defaultSeverity == "" {
		defaultSeverity = rulescontract.SeverityWarning
	}

	defaultInclude := policy.Defaults.Include
	if len(defaultInclude) == 0 {
		defaultInclude = []string{"**/*.go"}
	}

	defaultExclude := policy.Defaults.Exclude

	byID := map[rulescontract.RuleID]rulescontract.RuleConfig{}
	for _, rule := range policy.Rules {
		byID[rule.ID] = rule
	}

	ids := []rulescontract.RuleID{rulescontract.RuleNoPassThroughWrapper}
	out := make([]effectiveRule, 0, len(ids))

	for _, id := range ids {
		spec := byID[id]
		enabled := defaultEnabled
		if spec.Enabled != nil {
			enabled = *spec.Enabled
		}

		enforcement := spec.Enforcement
		if enforcement == "" {
			enforcement = defaultEnforcement
		}

		severity := spec.Severity
		if severity == "" {
			severity = defaultSeverity
		}

		include := spec.Include
		if len(include) == 0 {
			include = defaultInclude
		}

		exclude := append([]string(nil), defaultExclude...)
		exclude = append(exclude, spec.Exclude...)

		options := spec.Options
		if options.PassThroughWrapper == nil {
			options.PassThroughWrapper = rulescontract.DefaultPolicy().Rules[0].Options.PassThroughWrapper
		}

		out = append(out, effectiveRule{
			ID:          id,
			Enabled:     enabled,
			Enforcement: enforcement,
			Severity:    severity,
			Include:     include,
			Exclude:     exclude,
			Options:     options,
		})
	}

	return out
}

func goFilesForOptions(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return discoverGoFiles(root)
	}

	seen := map[string]struct{}{}
	var files []string

	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, filepath.FromSlash(p))
		}

		info, err := os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		var scoped []string
		if info.IsDir() {
			var err error
			scoped, err = discoverGoFiles(abs)
			if err != nil {
				return nil, err
			}
		} else if strings.HasSuffix(abs, ".go") && !shouldSkipGoPath(root, abs) {
			scoped = []string{abs}
		}

		for _, file := range scoped {
			file, err = filepath.Abs(file)
			if err != nil {
				return nil, err
			}
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			files = append(files, file)
		}
	}

	slices.Sort(files)

	return files, nil
}

func discoverGoFiles(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if path != root && discovery.ShouldSkipDirName(d.Name()) {
				return filepath.SkipDir
			}
			if d.Name() == "vendor" || d.Name() == ".idiom" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasSuffix(path, ".go") && !shouldSkipGoPath(root, path) {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.Sort(files)

	return files, nil
}

func shouldSkipGoPath(root string, absPath string) bool {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return true
	}

	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)

	return strings.HasSuffix(base, "_test.go") ||
		strings.HasPrefix(base, ".") ||
		strings.Contains(rel, "/vendor/") ||
		strings.Contains(rel, "/testdata/") ||
		strings.HasPrefix(rel, "vendor/") ||
		strings.HasPrefix(rel, "testdata/")
}

func sortFindings(findings []rulescontract.Finding) {
	slices.SortFunc(findings, func(a, b rulescontract.Finding) int {
		if rank := severityRank(b.Severity) - severityRank(a.Severity); rank != 0 {
			return rank
		}
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		if a.Symbol != b.Symbol {
			return strings.Compare(a.Symbol, b.Symbol)
		}

		return strings.Compare(string(a.RuleID), string(b.RuleID))
	})
}

func severityRank(severity rulescontract.FindingSeverity) int {
	switch severity {
	case rulescontract.SeverityError:
		return 3
	case rulescontract.SeverityWarning:
		return 2
	case rulescontract.SeverityInfo:
		return 1
	default:
		return 0
	}
}
