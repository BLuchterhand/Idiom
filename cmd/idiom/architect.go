package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/architect"
	"idiom/internal/telemetry"
)

type architectOptions struct {
	Root           string
	CWD            string
	Task           string
	TaskFile       string
	Schema         string
	BudgetTier     string
	Profile        string
	OutputPath     string
	RunCollectors  bool
	MaxRuleBytes   int
	MaxOutputBytes int

	Constraints  multiFlag
	Files        multiFlag
	Tests        multiFlag
	Anchors      multiFlag
	Risks        multiFlag
	Unknowns     multiFlag
	ContextViews multiFlag
	Metadata     multiFlag
}

type multiFlag []string

func (m *multiFlag) String() string {
	if m == nil {
		return ""
	}
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func runArchitectMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runArchitectMainWithTelemetry(args, stdout, stderr, nil)
}

func runArchitectMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printArchitectUsage(stderr)
		return 0
	}
	logCLIPhase(rec, "architect_start", map[string]any{
		"subcommand": args[0],
	})
	switch args[0] {
	case "packet":
		return runArchitectPacket(args[1:], stdout, stderr)
	case "lint":
		return runArchitectLint(args[1:], stdout, stderr)
	case "plan-audit", "audit-plan":
		return runArchitectPlanAudit(args[1:], stdout, stderr)
	case "budget":
		return runArchitectBudget(args[1:], stdout, stderr)
	case "packet-compare", "compare-packets":
		return runArchitectPacketCompare(args[1:], stdout, stderr)
	case "replay-compare", "compare-replay":
		return runArchitectReplayCompare(args[1:], stdout, stderr)
	case "prompt":
		return runArchitectPrompt(args[1:], stdout, stderr)
	case "plan":
		return runArchitectPrompt(withPromptKind("plan", args[1:]), stdout, stderr)
	case "critique":
		return runArchitectPrompt(withPromptKind("critique", args[1:]), stdout, stderr)
	case "brief":
		return runArchitectPrompt(withPromptKind("brief", args[1:]), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown architect command: %s\n", args[0])
		printArchitectUsage(stderr)
		return 2
	}
}

func withPromptKind(kind string, args []string) []string {
	out := []string{"--kind", kind}
	out = append(out, args...)
	return out
}

func runArchitectPacket(args []string, stdout io.Writer, stderr io.Writer) int {
	opts := architectOptions{MaxRuleBytes: 24 * 1024, MaxOutputBytes: 24 * 1024}
	fs := flag.NewFlagSet("idiom architect packet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addArchitectPacketFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))
		return 2
	}
	task, err := resolveTask(opts.Task, opts.TaskFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	packet, err := architect.BuildPacket(ctx, architect.BuildOptions{
		Root:           opts.Root,
		CWD:            opts.CWD,
		Task:           task,
		Schema:         opts.Schema,
		BudgetTier:     opts.BudgetTier,
		ProfilePath:    opts.Profile,
		RunCollectors:  opts.RunCollectors,
		MaxRuleBytes:   opts.MaxRuleBytes,
		MaxOutputBytes: opts.MaxOutputBytes,
		Constraints:    []string(opts.Constraints),
		Files:          parseFileRefs(opts.Files),
		Tests:          parseCommandRefs(opts.Tests),
		Anchors:        parseAnchors(opts.Anchors),
		Risks:          parseRisks(opts.Risks),
		Unknowns:       []string(opts.Unknowns),
		ContextViews:   []string(opts.ContextViews),
		Metadata:       parseMetadata(opts.Metadata),
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	data, err := architect.EncodePacket(packet)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writeArchitectOutput(opts.OutputPath, data, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runArchitectPlanAudit(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom architect plan-audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	packetPath := fs.String("packet", "", "path to architect packet JSON; reads stdin when omitted")
	planText := fs.String("plan", "", "candidate plan text")
	planFile := fs.String("plan-file", "", "read candidate plan text from file")
	planArtifact := fs.String("plan-artifact", "", "read submitted idiom.plan artifact JSON from file")
	outputPath := fs.String("output", "", "write audit JSON to path; default stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))
		return 2
	}
	planInputs := 0
	for _, value := range []string{*planText, *planFile, *planArtifact} {
		if strings.TrimSpace(value) != "" {
			planInputs++
		}
	}
	if planInputs > 1 {
		fmt.Fprintln(stderr, "--plan, --plan-file, and --plan-artifact are mutually exclusive")
		return 2
	}
	packet, err := readPacket(*packetPath, os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var result architect.PlanAuditResult
	if strings.TrimSpace(*planArtifact) != "" {
		data, err := os.ReadFile(filepath.Clean(*planArtifact))
		if err != nil {
			fmt.Fprintf(stderr, "read plan artifact: %v\n", err)
			return 1
		}
		var artifact architect.PlanArtifactAuditSubject
		if err := json.Unmarshal(data, &artifact); err != nil {
			fmt.Fprintf(stderr, "parse plan artifact: %v\n", err)
			return 1
		}
		result = architect.AuditPlanArtifact(packet, artifact)
	} else {
		plan := strings.TrimSpace(*planText)
		if strings.TrimSpace(*planFile) != "" {
			data, err := os.ReadFile(filepath.Clean(*planFile))
			if err != nil {
				fmt.Fprintf(stderr, "read plan file: %v\n", err)
				return 1
			}
			plan = string(data)
		}
		result = architect.AuditPlan(packet, plan)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	data = append(data, '\n')
	if err := writeArchitectOutput(*outputPath, data, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !result.OK {
		return 1
	}
	return 0
}

func runArchitectBudget(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom architect budget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	packetPath := fs.String("packet", "", "path to architect packet JSON; reads stdin when omitted")
	tiersValue := fs.String("tiers", "brief,standard,deep,max-local", "comma-separated budget tiers to evaluate")
	outputPath := fs.String("output", "", "write budget experiment JSON to path; default stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))
		return 2
	}
	packet, err := readPacket(*packetPath, os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := architect.BuildBudgetExperiment(packet, splitCSV(*tiersValue))
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	data = append(data, '\n')
	if err := writeArchitectOutput(*outputPath, data, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runArchitectPacketCompare(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom architect packet-compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	basePath := fs.String("base", "", "baseline/current architect packet JSON")
	candidatePath := fs.String("candidate", "", "candidate architect packet JSON, usually --schema planning_v2")
	outputPath := fs.String("output", "", "write comparison JSON to path; default stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))
		return 2
	}
	if strings.TrimSpace(*basePath) == "" || strings.TrimSpace(*candidatePath) == "" {
		fmt.Fprintln(stderr, "--base and --candidate are required")
		return 2
	}
	base, err := readPacket(*basePath, os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	candidate, err := readPacket(*candidatePath, os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := architect.ComparePacketVariants(base, candidate)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	data = append(data, '\n')
	if err := writeArchitectOutput(*outputPath, data, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runArchitectReplayCompare(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom architect replay-compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	basePath := fs.String("base", "", "baseline/current architect packet JSON")
	candidatePath := fs.String("candidate", "", "candidate architect packet JSON, usually --schema planning_v2")
	basePlanPath := fs.String("base-plan", "", "plan produced from the base packet by the replay model")
	candidatePlanPath := fs.String("candidate-plan", "", "plan produced from the candidate packet by the same replay model")
	outputPath := fs.String("output", "", "write replay comparison JSON to path; default stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))
		return 2
	}
	if strings.TrimSpace(*basePath) == "" || strings.TrimSpace(*candidatePath) == "" || strings.TrimSpace(*basePlanPath) == "" || strings.TrimSpace(*candidatePlanPath) == "" {
		fmt.Fprintln(stderr, "--base, --candidate, --base-plan, and --candidate-plan are required")
		return 2
	}
	base, err := readPacket(*basePath, os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	candidate, err := readPacket(*candidatePath, os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	basePlan, err := readTextFile(*basePlanPath, "base plan")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	candidatePlan, err := readTextFile(*candidatePlanPath, "candidate plan")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := architect.ComparePacketReplay(base, candidate, basePlan, candidatePlan)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	data = append(data, '\n')
	if err := writeArchitectOutput(*outputPath, data, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runArchitectLint(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom architect lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	packetPath := fs.String("packet", "", "path to architect packet JSON; reads stdin when omitted")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))
		return 2
	}
	packet, err := readPacket(*packetPath, os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := architect.LintPacket(packet)
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !result.OK {
		return 1
	}
	return 0
}

func runArchitectPrompt(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom architect prompt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	packetPath := fs.String("packet", "", "path to architect packet JSON; reads stdin when omitted")
	kind := fs.String("kind", "plan", "prompt kind: plan, critique, or brief")
	prior := fs.String("prior", "", "candidate plan or approved plan text")
	priorFile := fs.String("prior-file", "", "read candidate/approved plan text from file")
	outputPath := fs.String("output", "", "write prompt to path; default stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))
		return 2
	}
	if strings.TrimSpace(*prior) != "" && strings.TrimSpace(*priorFile) != "" {
		fmt.Fprintln(stderr, "--prior and --prior-file are mutually exclusive")
		return 2
	}
	packet, err := readPacket(*packetPath, os.Stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	priorText := strings.TrimSpace(*prior)
	if strings.TrimSpace(*priorFile) != "" {
		data, err := os.ReadFile(filepath.Clean(*priorFile))
		if err != nil {
			fmt.Fprintf(stderr, "read prior file: %v\n", err)
			return 1
		}
		priorText = string(data)
	}
	prompt, err := architect.RenderPrompt(architect.PromptKind(*kind), packet, priorText)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writeArchitectOutput(*outputPath, []byte(prompt), stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func addArchitectPacketFlags(fs *flag.FlagSet, opts *architectOptions) {
	fs.StringVar(&opts.Root, "root", "", "workspace root; default current directory")
	fs.StringVar(&opts.CWD, "cwd", "", "logical current working directory for the packet")
	fs.StringVar(&opts.Task, "task", "", "task statement")
	fs.StringVar(&opts.TaskFile, "task-file", "", "read task statement from file")
	fs.StringVar(&opts.Schema, "schema", "", "packet schema: v1 or planning_v2")
	fs.StringVar(&opts.BudgetTier, "budget-tier", "standard", "planning_v2 context budget tier: brief, standard, deep, or max-local")
	fs.StringVar(&opts.Profile, "profile", "", "profile path or name under .idiom/architect/profiles")
	fs.StringVar(&opts.OutputPath, "output", "", "write packet JSON to path; default stdout")
	fs.BoolVar(&opts.RunCollectors, "run-collectors", false, "execute profile collectors and include output evidence")
	fs.IntVar(&opts.MaxRuleBytes, "max-rule-bytes", opts.MaxRuleBytes, "maximum bytes to include from each rule file")
	fs.IntVar(&opts.MaxOutputBytes, "max-output-bytes", opts.MaxOutputBytes, "maximum bytes to include from each collector output")
	fs.Var(&opts.Constraints, "constraint", "add a hard planning constraint; repeatable")
	fs.Var(&opts.Files, "file", "add file allowlist entry path[:role[:reason]]; repeatable")
	fs.Var(&opts.Tests, "test", "add validation command command[:reason]; repeatable")
	fs.Var(&opts.Anchors, "anchor", "add verified anchor path[:symbol[:lines[:why]]]; repeatable")
	fs.Var(&opts.Risks, "risk", "add risk name[:mitigation]; repeatable")
	fs.Var(&opts.Unknowns, "unknown", "add unresolved question; repeatable")
	fs.Var(&opts.ContextViews, "context-view", "add context graph view JSON evidence; repeatable")
	fs.Var(&opts.Metadata, "meta", "add metadata key=value; repeatable")
}

func resolveTask(task string, taskFile string) (string, error) {
	if strings.TrimSpace(task) != "" && strings.TrimSpace(taskFile) != "" {
		return "", fmt.Errorf("--task and --task-file are mutually exclusive")
	}
	if strings.TrimSpace(taskFile) == "" {
		return strings.TrimSpace(task), nil
	}
	data, err := os.ReadFile(filepath.Clean(taskFile))
	if err != nil {
		return "", fmt.Errorf("read task file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func parseFileRefs(values []string) []architect.FileRef {
	out := make([]architect.FileRef, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value), "{") {
			var ref architect.FileRef
			if json.Unmarshal([]byte(value), &ref) == nil {
				out = append(out, ref)
				continue
			}
		}
		parts := splitColon(value, 3)
		out = append(out, architect.FileRef{Path: part(parts, 0), Role: part(parts, 1), Reason: part(parts, 2)})
	}
	return out
}

func parseCommandRefs(values []string) []architect.CommandRef {
	out := make([]architect.CommandRef, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value), "{") {
			var ref architect.CommandRef
			if json.Unmarshal([]byte(value), &ref) == nil {
				out = append(out, ref)
				continue
			}
		}
		parts := splitColon(value, 2)
		out = append(out, architect.CommandRef{Command: part(parts, 0), Reason: part(parts, 1)})
	}
	return out
}

func parseAnchors(values []string) []architect.Anchor {
	out := make([]architect.Anchor, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value), "{") {
			var anchor architect.Anchor
			if json.Unmarshal([]byte(value), &anchor) == nil {
				out = append(out, anchor)
				continue
			}
		}
		parts := splitColon(value, 4)
		out = append(out, architect.Anchor{Path: part(parts, 0), Symbol: part(parts, 1), Lines: part(parts, 2), Why: part(parts, 3)})
	}
	return out
}

func parseRisks(values []string) []architect.Risk {
	out := make([]architect.Risk, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value), "{") {
			var risk architect.Risk
			if json.Unmarshal([]byte(value), &risk) == nil {
				out = append(out, risk)
				continue
			}
		}
		parts := splitColon(value, 2)
		out = append(out, architect.Risk{Name: part(parts, 0), Mitigation: part(parts, 1)})
	}
	return out
}

func parseMetadata(values []string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key != "" && val != "" {
			out[key] = val
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitColon(value string, limit int) []string {
	parts := strings.SplitN(value, ":", limit)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func part(parts []string, idx int) string {
	if idx >= len(parts) {
		return ""
	}
	return parts[idx]
}

func readPacket(path string, stdin io.Reader) (architect.Packet, error) {
	var data []byte
	var err error
	if strings.TrimSpace(path) != "" {
		data, err = os.ReadFile(filepath.Clean(path))
		if err != nil {
			return architect.Packet{}, fmt.Errorf("read packet: %w", err)
		}
	} else {
		data, err = io.ReadAll(stdin)
		if err != nil {
			return architect.Packet{}, fmt.Errorf("read packet from stdin: %w", err)
		}
	}
	var packet architect.Packet
	if err := json.Unmarshal(data, &packet); err != nil {
		return architect.Packet{}, fmt.Errorf("parse packet: %w", err)
	}
	return packet, nil
}

func readTextFile(path string, label string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	return string(data), nil
}

func writeArchitectOutput(path string, data []byte, stdout io.Writer) error {
	if strings.TrimSpace(path) == "" || path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", clean, err)
	}
	if err := os.WriteFile(clean, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", clean, err)
	}
	return nil
}

func printArchitectUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom architect packet --task <text> [--profile <path-or-name>] [--output <packet.json>]")
	fmt.Fprintln(stderr, "  idiom architect lint --packet <packet.json>")
	fmt.Fprintln(stderr, "  idiom architect plan-audit --packet <packet.json> --plan-file <plan.md>")
	fmt.Fprintln(stderr, "  idiom architect plan-audit --packet <packet.json> --plan-artifact <last.plan.json>")
	fmt.Fprintln(stderr, "  idiom architect budget --packet <packet.json>")
	fmt.Fprintln(stderr, "  idiom architect packet-compare --base <v1.json> --candidate <v2.json>")
	fmt.Fprintln(stderr, "  idiom architect replay-compare --base <v1.json> --candidate <v2.json> --base-plan <plan.md> --candidate-plan <plan.md>")
	fmt.Fprintln(stderr, "  idiom architect prompt --kind plan|critique|brief --packet <packet.json> [--prior-file <plan.md>]")
	fmt.Fprintln(stderr, "  idiom architect plan|critique|brief --packet <packet.json> [--prior-file <plan.md>]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Architect externalizes planning context into auditable packets and prompts.")
}
