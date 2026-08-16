package session

import (
	"fmt"
	"strings"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/backends"
	"idiom/internal/runtime/llama"
)

// BackendResolutionWarning records a role-specific consolidated backend
// profile failure.
// Resolution falls back to the supplied config defaults, so callers can
// surface the warning without treating it as a fatal session error.
type BackendResolutionWarning struct {
	Role string
	Err  error
}

// BackendResources is the resolved, UI-independent backend composition for a
// runtime session. Delivery surfaces may manage controller start/stop events,
// but role selection and resource construction have one owner here.
type BackendResources struct {
	PlannerSpec       config.LlamaServer
	PlannerClient     *llama.Client
	PlannerController *llama.ServerController

	ExecutorSpec       config.LlamaServer
	ExecutorClient     *llama.Client
	ExecutorController *llama.ServerController

	ClassifierSpec       config.LlamaServer
	ClassifierClient     llama.LLMClient
	ClassifierController *llama.ServerController

	Warnings []BackendResolutionWarning
}

// ResolveBackendResources resolves planner, executor, and classifier roles and
// constructs their clients and launchable controllers. Missing or malformed
// consolidated backend data falls back to config defaults and is reported in
// Warnings. Standalone backends.json files are not implicitly loaded here.
func ResolveBackendResources(cfg config.Config) BackendResources {
	backendSnapshot := loadBackendConfigSnapshot(cfg)
	callTimeout := time.Duration(cfg.Chat.RequestTimeoutSeconds) * time.Second
	plannerSpec, _, plannerErr := resolveRoleSpecFromSnapshot(backendSnapshot, backends.RolePlanner, cfg.LlamaServer)
	plannerClient := llama.NewClientForServer(plannerSpec, callTimeout)

	resources := BackendResources{
		PlannerSpec:   plannerSpec,
		PlannerClient: plannerClient,
	}
	resources.addWarning(backends.RolePlanner, plannerErr)

	if plannerSpec.IsLaunchable() {
		resources.PlannerController = llama.NewServerController(plannerSpec, plannerClient)
	}

	implementerEnabled := cfg.Implementer != nil
	executorDefaults := cfg.LlamaServer
	if implementerEnabled {
		executorDefaults = *cfg.Implementer
	}

	executorSpec, executorFromBackends, executorErr := resolveRoleSpecFromSnapshot(backendSnapshot, backends.RoleExecutor, executorDefaults)
	resources.addWarning(backends.RoleExecutor, executorErr)

	if executorFromBackends {
		if !SameLlamaTarget(executorSpec, plannerSpec) {
			implementerEnabled = true
		} else {
			executorSpec = plannerSpec
		}
	}
	resources.ExecutorSpec = executorSpec

	if implementerEnabled {
		resources.ExecutorClient = llama.NewClientForServer(executorSpec, callTimeout)
		if executorSpec.IsLaunchable() {
			resources.ExecutorController = llama.NewServerController(executorSpec, resources.ExecutorClient)
		}
	}

	classifierDefaults := cfg.RouteClassifier.LlamaServerSpec()
	classifierSpec, classifierFromBackends, classifierErr := resolveRoleSpecFromSnapshot(backendSnapshot, backends.RoleClassifier, classifierDefaults)
	resources.addWarning(backends.RoleClassifier, classifierErr)

	classifierBaseURL := strings.TrimSpace(cfg.RouteClassifier.BaseURL)
	if classifierFromBackends {
		classifierBaseURL = classifierSpec.EndpointBaseURL()
	}

	if classifierFromBackends && SameLlamaTarget(classifierSpec, plannerSpec) {
		resources.ClassifierClient = plannerClient
	} else if classifierBaseURL != "" {
		classifierSpec.BaseURL = classifierBaseURL
		classifierTimeout := time.Duration(cfg.RouteClassifier.TimeoutSeconds) * time.Second
		classifierClient := llama.NewClientForServer(classifierSpec, classifierTimeout)

		resources.ClassifierClient = classifierClient
		if cfg.RouteClassifier.AutoStartServer && classifierSpec.IsLaunchable() && classifierSpec.Binary != "" {
			resources.ClassifierController = llama.NewServerController(classifierSpec, classifierClient)
		}
	}
	resources.ClassifierSpec = classifierSpec

	return resources
}

type backendConfigSnapshot struct {
	file         backends.File
	profile      backends.Profile
	fileFound    bool
	profileFound bool
	err          error
}

func loadBackendConfigSnapshot(cfg config.Config) backendConfigSnapshot {
	if len(cfg.Backends) > 0 {
		file, err := backends.Parse(cfg.Backends)
		profile, profileFound := file.Profile("")
		return backendConfigSnapshot{
			file:         file,
			fileFound:    true,
			profile:      profile,
			profileFound: profileFound,
			err:          err,
		}
	}

	// Standalone .idiom/backends.json is a legacy explicit-profile surface.
	// It must not implicitly override the active model from config.json.
	return backendConfigSnapshot{}
}
func (r *BackendResources) addWarning(role string, err error) {
	if r == nil || err == nil {
		return
	}

	r.Warnings = append(r.Warnings, BackendResolutionWarning{Role: role, Err: err})
}

// ServerSpecForBackendModel overlays a backends.json model on config defaults.
// Fields absent from the backend declaration retain their configured values.
func ServerSpecForBackendModel(model backends.Model, defaults config.LlamaServer) config.LlamaServer {
	out := defaults
	explicitKind := strings.TrimSpace(model.Kind)
	explicitBaseURL := strings.TrimSpace(model.BaseURL)

	if explicitKind != "" {
		if explicitBaseURL == "" && !strings.EqualFold(explicitKind, strings.TrimSpace(defaults.Kind)) {
			out.BaseURL = ""
		}
		out.Kind = explicitKind
	}

	if binary := strings.TrimSpace(model.Binary); binary != "" {
		out.Binary = binary
	}

	if host := strings.TrimSpace(model.Host); host != "" {
		out.Host = host
	} else if strings.TrimSpace(out.Host) == "" {
		out.Host = "127.0.0.1"
	}

	if model.Port > 0 {
		out.Port = model.Port
		if explicitBaseURL == "" {
			out.BaseURL = ""
		}
	}

	if modelID := strings.TrimSpace(model.Model); modelID != "" {
		out.Model = modelID
	} else if modelPath := strings.TrimSpace(model.ModelPath); modelPath != "" {
		out.Model = modelPath
	} else if alias := strings.TrimSpace(model.Alias); alias != "" {
		out.Model = alias
	}

	if alias := strings.TrimSpace(model.Alias); alias != "" {
		out.DisplayName = alias
	}

	if explicitBaseURL != "" {
		out.BaseURL = explicitBaseURL
	}
	if apiKeyEnv := strings.TrimSpace(model.APIKeyEnv); apiKeyEnv != "" {
		out.APIKeyEnv = apiKeyEnv
	}
	if effort := strings.TrimSpace(model.ReasoningEffort); effort != "" {
		out.ReasoningEffort = effort
	}
	if verbosity := strings.TrimSpace(model.Verbosity); verbosity != "" {
		out.Verbosity = verbosity
	}
	if len(model.ExtraArgs) > 0 {
		out.ExtraArgs = append([]string(nil), model.ExtraArgs...)
	}

	return out
}

func resolveRoleSpecFromSnapshot(snapshot backendConfigSnapshot, role string, defaults config.LlamaServer) (config.LlamaServer, bool, error) {
	if snapshot.err != nil {
		return defaults, false, snapshot.err
	}
	if !snapshot.fileFound || !snapshot.profileFound {
		return defaults, false, nil
	}

	if explicit := strings.TrimSpace(defaults.Model); explicit != "" {
		if model, found := snapshot.profile.ModelByAlias(explicit); found && explicitAliasCanOverrideRole(model, role) {
			return ServerSpecForBackendModel(model, defaults), true, nil
		}
		if model, found := snapshot.file.ModelByAlias(explicit); found && explicitAliasCanOverrideRole(model, role) {
			return ServerSpecForBackendModel(model, defaults), true, nil
		}
	}

	alias, ok := snapshot.profile.AliasForRole(role)
	if !ok {
		return defaults, false, nil
	}

	model, found := snapshot.profile.ModelByAlias(alias)
	if !found {
		return defaults, false, fmt.Errorf("backends role %q resolved undeclared alias %q", role, alias)
	}

	return ServerSpecForBackendModel(model, defaults), true, nil
}

func explicitAliasCanOverrideRole(model backends.Model, role string) bool {
	if role != backends.RoleExecutor || len(model.Roles) == 0 {
		return true
	}

	for _, declaredRole := range model.Roles {
		if textutil.EqualTrimFold(declaredRole, backends.RoleExecutor) {
			return true
		}
	}

	return false
}

// SameLlamaTarget reports whether two server specs route to the same backend.
func SameLlamaTarget(left, right config.LlamaServer) bool {
	return left.EndpointBaseURL() == right.EndpointBaseURL() &&
		left.Model == right.Model &&
		strings.EqualFold(left.Kind, right.Kind) &&
		left.APIKeyEnvName() == right.APIKeyEnvName()
}
