package readiness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/backends"
	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/runner"
)

// HostCheck is a command-level diagnostic result folded into readiness reports.
// Status accepts "pass", "warn", or "fail".
type HostCheck struct {
	Name   string
	Status string
	Detail string
	Remedy string
}

type Check struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Detail   string `json:"detail,omitempty"`
	Remedy   string `json:"remedy,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type ProfileSelectionError struct {
	Selected string
}

func (e ProfileSelectionError) Error() string {
	return fmt.Sprintf("doctor: unknown readiness profile %q (expected all, discovery, plan, implement, eval, or sidecar)", e.Selected)
}

func IsProfileSelectionError(err error) bool {
	var selectionErr ProfileSelectionError
	return errors.As(err, &selectionErr)
}

type Unavailable struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
	Remedy string `json:"remedy,omitempty"`
}

type GateDecision struct {
	Name              string `json:"name"`
	Status            string `json:"status"`
	Severity          string `json:"severity"`
	Enforcement       string `json:"enforcement"`
	RequiredForLaunch bool   `json:"required_for_launch"`
	Evidence          string `json:"evidence"`
	Reason            string `json:"reason,omitempty"`
	Remedy            string `json:"remedy,omitempty"`
}

type ProfileReport struct {
	Name                 string            `json:"name"`
	State                string            `json:"state"`
	Detail               string            `json:"detail,omitempty"`
	Enforcement          string            `json:"enforcement"`
	RequiredForLaunch    bool              `json:"required_for_launch"`
	TaskFamily           string            `json:"task_family"`
	Contract             string            `json:"contract"`
	PermissionProfile    string            `json:"permission_profile,omitempty"`
	ToolProfile          string            `json:"tool_profile,omitempty"`
	RequiredEvidence     []string          `json:"required_evidence,omitempty"`
	ReadinessGate        []string          `json:"readiness_gate,omitempty"`
	VerificationContract string            `json:"verification_contract,omitempty"`
	RequiredRoles        []string          `json:"required_roles,omitempty"`
	RoleAliases          map[string]string `json:"role_aliases,omitempty"`
	Gates                []GateDecision    `json:"gates,omitempty"`
	HardFailures         []GateDecision    `json:"hard_failures,omitempty"`
	SoftDegradations     []GateDecision    `json:"soft_degradations,omitempty"`
	Checks               []Check           `json:"checks,omitempty"`
	Unavailable          []Unavailable     `json:"unavailable,omitempty"`
	Metrics              Metrics           `json:"metrics,omitempty"`
}

type Metrics struct {
	PassChecks         int    `json:"pass_checks,omitempty"`
	DegradedChecks     int    `json:"degraded_checks,omitempty"`
	BlockedChecks      int    `json:"blocked_checks,omitempty"`
	UnavailableChecks  int    `json:"unavailable_checks,omitempty"`
	QueuePools         int    `json:"queue_pools,omitempty"`
	QueueInFlight      int    `json:"queue_in_flight,omitempty"`
	QueueWaiting       int    `json:"queue_waiting,omitempty"`
	QueueIdle          int    `json:"queue_idle,omitempty"`
	ContextTokens      int    `json:"context_tokens,omitempty"`
	MaxOutputTokens    int    `json:"max_output_tokens,omitempty"`
	ClassifierState    string `json:"classifier_state,omitempty"`
	BackendState       string `json:"backend_state,omitempty"`
	QueueState         string `json:"queue_state,omitempty"`
	ContextBudgetState string `json:"context_budget_state,omitempty"`
	HardFailures       int    `json:"hard_failures,omitempty"`
	SoftDegradations   int    `json:"soft_degradations,omitempty"`
}

type profileSpec struct {
	name          string
	taskFamily    string
	requiredRoles []string
	needsContext  bool
}

var profileSpecs = []profileSpec{
	{name: "discovery", taskFamily: "architecture", needsContext: true},
	{name: "plan", taskFamily: "plan_then_execute_planner", requiredRoles: []string{backends.RolePlanner, backends.RoleExecutor}, needsContext: true},
	{name: "implement", taskFamily: "implementation", requiredRoles: []string{backends.RoleExecutor}, needsContext: true},
	{name: "eval", taskFamily: "verification_only", requiredRoles: []string{backends.RoleExecutor}},
	{name: "sidecar", taskFamily: "debug_logs", requiredRoles: []string{backends.RoleClassifier}},
}

func ProfileCount() int {
	return len(profileSpecs)
}

type gatePolicy struct {
	name              string
	checkName         string
	hardProfiles      map[string]bool
	softProfiles      map[string]bool
	requiredForLaunch bool
}

var gatePolicies = []gatePolicy{
	{
		name:              "backend_roles_resolved",
		checkName:         "backend role config",
		hardProfiles:      profileSet("plan", "implement", "eval", "sidecar"),
		softProfiles:      profileSet("discovery"),
		requiredForLaunch: true,
	},
	{
		name:              "queue_admission_known",
		checkName:         "queue admission",
		hardProfiles:      profileSet("eval"),
		softProfiles:      profileSet("discovery", "plan", "implement", "sidecar"),
		requiredForLaunch: true,
	},
	{
		name:              "context_budget_valid",
		checkName:         "context budget",
		hardProfiles:      profileSet("plan", "implement", "eval"),
		softProfiles:      profileSet("discovery", "sidecar"),
		requiredForLaunch: true,
	},
	{
		name:              "route_classifier_available_or_fallback_recorded",
		checkName:         "route classifier",
		softProfiles:      profileSet("discovery", "plan", "implement", "eval", "sidecar"),
		requiredForLaunch: false,
	},
}

func profileSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func BuildProfiles(configPath, selected string, hostChecks []HostCheck) ([]ProfileReport, error) {
	selected = strings.ToLower(strings.TrimSpace(selected))
	if selected == "" {
		selected = "all"
	}
	specs := make([]profileSpec, 0, len(profileSpecs))
	for _, spec := range profileSpecs {
		if selected == "all" || selected == spec.name {
			specs = append(specs, spec)
		}
	}
	if len(specs) == 0 {
		return nil, ProfileSelectionError{Selected: selected}
	}

	root := WorkspaceRootFromConfig(configPath)
	if strings.TrimSpace(root) == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	backendFile, backendFound, backendErr := backends.LoadFromProjectRoot(root)
	cfg, cfgOK := loadConfig(configPath)
	contextReady, contextDetail := contextIndexReadiness(root)
	queueCheck, queueMetrics, queueUnavailable := queueReadiness()

	reports := make([]ProfileReport, 0, len(specs))
	for _, spec := range specs {
		reports = append(reports, buildProfile(spec, hostChecks, backendFile, backendFound, backendErr, cfg, cfgOK, contextReady, contextDetail, queueCheck, queueMetrics, queueUnavailable))
	}
	return reports, nil
}

func buildProfile(spec profileSpec, hostChecks []HostCheck, backendFile backends.File, backendFound bool, backendErr error, cfg config.Config, cfgOK bool, contextReady bool, contextDetail string, queueCheck Check, queueMetrics Metrics, queueUnavailable *Unavailable) ProfileReport {
	contract := workerruntime.RuntimeContractForTaskFamily(spec.taskFamily)
	report := ProfileReport{
		Name:                 spec.name,
		TaskFamily:           contract.TaskFamily,
		Contract:             contract.Contract,
		PermissionProfile:    contract.PermissionProfile,
		ToolProfile:          string(contract.ToolProfile),
		RequiredEvidence:     append([]string(nil), contract.RequiredEvidence...),
		ReadinessGate:        append([]string(nil), contract.ReadinessGate...),
		VerificationContract: contract.VerificationContract,
		RequiredRoles:        append([]string(nil), spec.requiredRoles...),
		RoleAliases:          map[string]string{},
		Enforcement:          "report_only",
		RequiredForLaunch:    true,
	}
	report.Checks = append(report.Checks, Check{
		Name:   "runtime contract",
		State:  "pass",
		Detail: fmt.Sprintf("%s / %s", contract.Contract, contract.PermissionProfile),
	})
	report.Checks = append(report.Checks, checksFromHost(hostChecks)...)
	if spec.needsContext {
		if contextReady {
			report.Checks = append(report.Checks, Check{Name: "context index", State: "pass", Detail: contextDetail, Optional: true})
		} else {
			report.Checks = append(report.Checks, Check{
				Name:     "context index",
				State:    "degraded",
				Detail:   contextDetail,
				Remedy:   "run `idiom context status --refresh` or `idiom context build` before relying on cached discovery evidence",
				Optional: true,
			})
		}
	}
	report.Checks = append(report.Checks, queueCheck)
	if queueUnavailable != nil {
		report.Unavailable = append(report.Unavailable, *queueUnavailable)
	}
	report.Checks = append(report.Checks, contextBudgetCheck(cfg, cfgOK))
	report.Checks = append(report.Checks, routeClassifierCheck(cfg, cfgOK))
	report.Unavailable = append(report.Unavailable, Unavailable{
		Check:  "route classifier live probe",
		Reason: "doctor reports route-classifier configuration state but defers live classifier calls",
		Remedy: "use a deep readiness or route-classifier-specific probe when live classification health is required",
	})
	report = applyBackendRoleReadiness(report, backendFile, backendFound, backendErr)
	report = applyGatePolicy(report)
	report.State = profileState(report.Checks, report.Gates)
	report.Detail = profileDetail(report.State)
	report.Metrics = metricsFor(report.Checks, report.Gates, queueMetrics)
	if len(report.RoleAliases) == 0 {
		report.RoleAliases = nil
	}
	return report
}

func loadConfig(configPath string) (config.Config, bool) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, false
	}
	return cfg, true
}

func queueReadiness() (Check, Metrics, *Unavailable) {
	socket := runner.DefaultSocketPath()
	snap, err := runner.Status(socket)
	if err != nil {
		check := Check{
			Name:     "queue admission",
			State:    "unavailable",
			Detail:   "queue daemon not reachable",
			Remedy:   "start the daemon with `idiom queue serve` or treat queue gating as opportunistic",
			Optional: true,
		}
		unavailable := Unavailable{
			Check:  "queue admission",
			Reason: "queue daemon not reachable",
			Remedy: "start the daemon with `idiom queue serve` for live queue capacity evidence",
		}
		return check, Metrics{QueueState: "unavailable"}, &unavailable
	}
	metrics := Metrics{QueueState: "pass", QueuePools: len(snap.Pools), QueueWaiting: len(snap.Waiting), QueueInFlight: len(snap.InFlight)}
	for _, job := range snap.InFlight {
		if job.Idle {
			metrics.QueueIdle++
		}
	}
	state := "pass"
	detail := fmt.Sprintf("reachable, pools=%d, in_flight=%d, waiting=%d", metrics.QueuePools, metrics.QueueInFlight, metrics.QueueWaiting)
	remedy := ""
	if metrics.QueueIdle > 0 {
		state = "degraded"
		metrics.QueueState = "degraded"
		detail = fmt.Sprintf("%s, idle=%d", detail, metrics.QueueIdle)
		remedy = "inspect `idiom queue status --json` and cancel or restart stale in-flight jobs if they are wedged"
	}
	return Check{Name: "queue admission", State: state, Detail: detail, Remedy: remedy, Optional: true}, metrics, nil
}

func contextBudgetCheck(cfg config.Config, cfgOK bool) Check {
	if !cfgOK {
		return Check{
			Name:     "context budget",
			State:    "unavailable",
			Detail:   "project config did not load",
			Remedy:   "fix project config so context capacity can be computed",
			Optional: true,
		}
	}
	ctxSize := cfg.LlamaServer.CtxSize
	maxOutput := cfg.Chat.MaxTokens
	switch {
	case ctxSize <= 0:
		return Check{Name: "context budget", State: "degraded", Detail: "llama_server.ctx_size is not configured", Remedy: "set llama_server.ctx_size to the backend-supported context window", Optional: true}
	case maxOutput <= 0:
		return Check{Name: "context budget", State: "degraded", Detail: fmt.Sprintf("ctx_size=%d but chat.max_tokens is not configured", ctxSize), Remedy: "set chat.max_tokens to reserve output space per lane", Optional: true}
	case maxOutput >= ctxSize:
		return Check{Name: "context budget", State: "degraded", Detail: fmt.Sprintf("ctx_size=%d max_output=%d leaves no prompt budget", ctxSize, maxOutput), Remedy: "lower chat.max_tokens or raise the configured backend context", Optional: true}
	default:
		return Check{Name: "context budget", State: "pass", Detail: fmt.Sprintf("ctx_size=%d max_output=%d", ctxSize, maxOutput), Optional: true}
	}
}

func routeClassifierCheck(cfg config.Config, cfgOK bool) Check {
	if !cfgOK {
		return Check{Name: "route classifier", State: "unavailable", Detail: "project config did not load", Remedy: "fix project config so classifier fallback state can be reported", Optional: true}
	}
	if !cfg.RouteClassifier.Enabled {
		return Check{Name: "route classifier", State: "degraded", Detail: "disabled; deterministic routing fallback active", Remedy: "enable route_classifier when classifier-backed routing or summaries are required", Optional: true}
	}
	if strings.TrimSpace(cfg.RouteClassifier.BaseURL) == "" && cfg.RouteClassifier.Port <= 0 {
		return Check{Name: "route classifier", State: "degraded", Detail: "enabled but no base_url or server port is configured", Remedy: "set route_classifier.base_url or route_classifier.port", Optional: true}
	}
	return Check{Name: "route classifier", State: "pass", Detail: "configured; live JSON probe is deferred to deep readiness", Optional: true}
}

func metricsFor(checks []Check, gates []GateDecision, queue Metrics) Metrics {
	metrics := queue
	for _, check := range checks {
		switch check.State {
		case "pass":
			metrics.PassChecks++
		case "blocked":
			metrics.BlockedChecks++
		case "degraded":
			metrics.DegradedChecks++
		case "unavailable":
			metrics.UnavailableChecks++
		}
		switch check.Name {
		case "backend role config":
			metrics.BackendState = check.State
		case "context budget":
			metrics.ContextBudgetState = check.State
			fmt.Sscanf(check.Detail, "ctx_size=%d max_output=%d", &metrics.ContextTokens, &metrics.MaxOutputTokens)
		case "route classifier":
			metrics.ClassifierState = check.State
		case "queue admission":
			if metrics.QueueState == "" {
				metrics.QueueState = check.State
			}
		}
	}
	for _, gate := range gates {
		switch gate.Status {
		case "blocked":
			metrics.HardFailures++
		case "degraded":
			metrics.SoftDegradations++
		}
	}
	return metrics
}

func applyGatePolicy(report ProfileReport) ProfileReport {
	checks := map[string]Check{}
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	for _, policy := range gatePolicies {
		check, ok := checks[policy.checkName]
		if !ok {
			continue
		}
		severity := gateSeverity(report.Name, policy)
		if severity == "" {
			continue
		}
		gate := GateDecision{
			Name:              policy.name,
			Status:            gateStatus(check, severity),
			Severity:          severity,
			Enforcement:       report.Enforcement,
			RequiredForLaunch: policy.requiredForLaunch && severity == "hard",
			Evidence:          check.Name,
			Reason:            gateReason(check),
			Remedy:            check.Remedy,
		}
		if gate.Status == "pass" {
			gate.Reason = ""
			gate.Remedy = ""
		}
		report.Gates = append(report.Gates, gate)
		switch gate.Status {
		case "blocked":
			report.HardFailures = append(report.HardFailures, gate)
		case "degraded":
			report.SoftDegradations = append(report.SoftDegradations, gate)
		}
	}
	return report
}

func gateSeverity(profile string, policy gatePolicy) string {
	profile = strings.ToLower(strings.TrimSpace(profile))
	switch {
	case policy.hardProfiles[profile]:
		return "hard"
	case policy.softProfiles[profile]:
		return "soft"
	default:
		return ""
	}
}

func gateStatus(check Check, severity string) string {
	switch check.State {
	case "pass":
		return "pass"
	case "blocked":
		return "blocked"
	case "degraded", "unavailable":
		if severity == "hard" {
			return "blocked"
		}
		return "degraded"
	default:
		if severity == "hard" {
			return "blocked"
		}
		return "degraded"
	}
}

func gateReason(check Check) string {
	if strings.TrimSpace(check.Detail) != "" {
		return check.Detail
	}
	return check.State
}

func checksFromHost(hostChecks []HostCheck) []Check {
	out := make([]Check, 0, len(hostChecks))
	for _, check := range hostChecks {
		state := "pass"
		switch check.Status {
		case "fail":
			state = "blocked"
		case "warn":
			state = "degraded"
		}
		out = append(out, Check{
			Name:   check.Name,
			State:  state,
			Detail: check.Detail,
			Remedy: check.Remedy,
		})
	}
	return out
}

func applyBackendRoleReadiness(report ProfileReport, backendFile backends.File, backendFound bool, backendErr error) ProfileReport {
	if len(report.RequiredRoles) == 0 {
		report.Checks = append(report.Checks, Check{
			Name:     "backend role config",
			State:    "pass",
			Detail:   "profile does not require a dedicated backend role",
			Optional: true,
		})
		return report
	}
	if backendErr != nil {
		report.Checks = append(report.Checks, Check{
			Name:   "backend role config",
			State:  "blocked",
			Detail: backendErr.Error(),
			Remedy: "fix .idiom/backends.json so roles can be resolved before launch",
		})
		return report
	}
	if !backendFound {
		report.Checks = append(report.Checks, Check{
			Name:   "backend role config",
			State:  "unavailable",
			Detail: ".idiom/backends.json not found",
			Remedy: "create .idiom/backends.json or run with legacy config while treating role-specific readiness as unavailable",
		})
		report.Unavailable = append(report.Unavailable, Unavailable{
			Check:  "backend role config",
			Reason: ".idiom/backends.json not found",
			Remedy: "create .idiom/backends.json to make role readiness machine-readable",
		})
		return report
	}
	profile, ok := backendFile.Profile("")
	if !ok {
		report.Checks = append(report.Checks, Check{
			Name:   "backend role config",
			State:  "blocked",
			Detail: "no default or unambiguous backend profile selected",
			Remedy: "set default_profile in .idiom/backends.json or keep only one backend profile",
		})
		return report
	}
	var missing []string
	for _, role := range report.RequiredRoles {
		alias, ok := profile.AliasForRole(role)
		if !ok {
			missing = append(missing, role)
			continue
		}
		report.RoleAliases[role] = alias
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		report.Checks = append(report.Checks, Check{
			Name:   "backend role config",
			State:  "blocked",
			Detail: "missing role aliases: " + strings.Join(missing, ", "),
			Remedy: "add role tags or proxy.default_<role> aliases in .idiom/backends.json",
		})
		return report
	}
	report.Checks = append(report.Checks, Check{
		Name:   "backend role config",
		State:  "pass",
		Detail: "resolved roles: " + strings.Join(sortedRoleAliases(report.RoleAliases), ", "),
	})
	return report
}

func sortedRoleAliases(aliases map[string]string) []string {
	keys := make([]string, 0, len(aliases))
	for key := range aliases {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+aliases[key])
	}
	return out
}

func contextIndexReadiness(root string) (bool, string) {
	if strings.TrimSpace(root) == "" {
		return false, "workspace root unavailable"
	}
	for _, rel := range []string{
		filepath.Join(".idiom", "comprehension", "graph.sqlite"),
		filepath.Join(".idiom", "comprehension", "graph.json"),
	} {
		path := filepath.Join(root, rel)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true, rel + " present"
		}
	}
	return false, "no local context graph found under .idiom/comprehension"
}

func profileState(checks []Check, gates []GateDecision) string {
	state := "pass"
	for _, gate := range gates {
		switch gate.Status {
		case "blocked":
			return "blocked"
		case "degraded":
			state = "degraded"
		}
	}
	for _, check := range checks {
		switch check.State {
		case "blocked":
			return "blocked"
		case "unavailable":
			if state == "pass" {
				state = "degraded"
			}
		case "degraded":
			if state == "pass" {
				state = "degraded"
			}
		}
	}
	return state
}

func profileDetail(state string) string {
	switch state {
	case "pass":
		return "all required static checks passed"
	case "blocked":
		return "one or more required checks block launch"
	case "degraded":
		return "required contract is visible but one or more checks are degraded or unavailable"
	default:
		return "readiness state unavailable"
	}
}

// WorkspaceRootFromConfig reads workspace.root from the config file without
// invoking the full config loader. It returns "" on any failure so callers can
// fall back to cwd or report a warning.
func WorkspaceRootFromConfig(path string) string {
	abs := path
	if !filepath.IsAbs(abs) {
		if cwd, err := os.Getwd(); err == nil {
			abs = filepath.Join(cwd, path)
		}
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	var shape struct {
		Workspace struct {
			Root string `json:"root"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(body, &shape); err != nil {
		return ""
	}
	root := strings.TrimSpace(shape.Workspace.Root)
	if root == "" || filepath.IsAbs(root) {
		return root
	}
	return filepath.Clean(filepath.Join(filepath.Dir(abs), root))
}
