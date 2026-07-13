package workspace

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"idiom/internal/contracts/taskledger"
)

type TaskLedgerBlockerKind string

type TaskLedgerBlockerSubjectNamespace string

const (
	TaskLedgerBlockerApprovalRequired      TaskLedgerBlockerKind = "approval_required"
	TaskLedgerBlockerMissingInput          TaskLedgerBlockerKind = "missing_input"
	TaskLedgerBlockerExternalDependency    TaskLedgerBlockerKind = "external_dependency"
	TaskLedgerBlockerEnvironment           TaskLedgerBlockerKind = "environment"
	TaskLedgerBlockerCapabilityUnavailable TaskLedgerBlockerKind = "capability_unavailable"
	TaskLedgerBlockerPolicyConstraint      TaskLedgerBlockerKind = "policy_constraint"
)

const (
	TaskLedgerBlockerSubjectArtifact            TaskLedgerBlockerSubjectNamespace = "artifact"
	TaskLedgerBlockerSubjectCommand             TaskLedgerBlockerSubjectNamespace = "command"
	TaskLedgerBlockerSubjectConfiguration       TaskLedgerBlockerSubjectNamespace = "configuration"
	TaskLedgerBlockerSubjectEnvironmentVariable TaskLedgerBlockerSubjectNamespace = "environment_variable"
	TaskLedgerBlockerSubjectPermission          TaskLedgerBlockerSubjectNamespace = "permission"
	TaskLedgerBlockerSubjectPolicy              TaskLedgerBlockerSubjectNamespace = "policy"
	TaskLedgerBlockerSubjectService             TaskLedgerBlockerSubjectNamespace = "service"
	TaskLedgerBlockerSubjectTool                TaskLedgerBlockerSubjectNamespace = "tool"
	TaskLedgerBlockerSubjectWorkspacePath       TaskLedgerBlockerSubjectNamespace = "workspace_path"
)

const (
	taskLedgerBlockerWorkspaceSubjectPrefix     = "workspace:"
	taskLedgerBlockerExecutableSubjectPrefix    = "executable:"
	taskLedgerBlockerServiceSubjectPrefix       = "uri:"
	taskLedgerBlockerProtectedPathSubjectPrefix = "protected_path:"
	taskLedgerBlockerToolRuleSubjectPrefix      = "tool_rule:"
)

var (
	taskLedgerBlockerKinds = [...]TaskLedgerBlockerKind{
		TaskLedgerBlockerApprovalRequired,
		TaskLedgerBlockerMissingInput,
		TaskLedgerBlockerExternalDependency,
		TaskLedgerBlockerEnvironment,
		TaskLedgerBlockerCapabilityUnavailable,
		TaskLedgerBlockerPolicyConstraint,
	}
	taskLedgerBlockerSubjectNamespaces = [...]TaskLedgerBlockerSubjectNamespace{
		TaskLedgerBlockerSubjectArtifact,
		TaskLedgerBlockerSubjectCommand,
		TaskLedgerBlockerSubjectConfiguration,
		TaskLedgerBlockerSubjectEnvironmentVariable,
		TaskLedgerBlockerSubjectPermission,
		TaskLedgerBlockerSubjectPolicy,
		TaskLedgerBlockerSubjectService,
		TaskLedgerBlockerSubjectTool,
		TaskLedgerBlockerSubjectWorkspacePath,
	}
	taskLedgerBlockerEnvironmentVariablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	taskLedgerBlockerExecutablePattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
)

// TaskLedgerBlockerSubjectRef identifies the concrete thing that is blocked.
// Namespace is deliberately independent from Kind: Kind explains why work is
// blocked, while Namespace and ID identify the authority-backed subject.
type TaskLedgerBlockerSubjectRef struct {
	Namespace TaskLedgerBlockerSubjectNamespace `json:"namespace"`
	ID        string                            `json:"id"`
}

// TaskLedgerBlocker is the only authority-bearing blocked-item shape.
// Reason remains operator-facing prose; Subject is resolved by Manager so a
// model cannot turn a plausible-looking string into a trusted identity.
type TaskLedgerBlocker struct {
	Kind    TaskLedgerBlockerKind       `json:"kind"`
	Subject TaskLedgerBlockerSubjectRef `json:"subject"`
	Reason  string                      `json:"reason"`
}

func TaskLedgerBlockerKindValues() []string {
	values := make([]string, 0, len(taskLedgerBlockerKinds))
	for _, kind := range taskLedgerBlockerKinds {
		values = append(values, string(kind))
	}
	return values
}

func (kind TaskLedgerBlockerKind) Valid() bool {
	for _, candidate := range taskLedgerBlockerKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

func TaskLedgerBlockerSubjectNamespaceValues() []string {
	values := make([]string, 0, len(taskLedgerBlockerSubjectNamespaces))
	for _, namespace := range taskLedgerBlockerSubjectNamespaces {
		values = append(values, string(namespace))
	}
	return values
}

func (namespace TaskLedgerBlockerSubjectNamespace) Valid() bool {
	for _, candidate := range taskLedgerBlockerSubjectNamespaces {
		if namespace == candidate {
			return true
		}
	}
	return false
}

// NormalizeTaskLedgerBlocker validates a blocker against the Manager's real
// workspace, tool-catalog, permission, and policy authorities. Blocked items
// require the typed shape; non-blocked items must not carry one.
func (m *Manager) NormalizeTaskLedgerBlocker(status taskledger.Status, blocker *TaskLedgerBlocker) (*TaskLedgerBlocker, error) {
	if status != taskledger.StatusBlocked {
		if blocker != nil {
			return nil, fmt.Errorf("task ledger blocker is only valid when status is blocked")
		}
		return nil, nil
	}
	if blocker == nil {
		return nil, fmt.Errorf("blocked task ledger item requires blocker.kind, blocker.subject, and blocker.reason")
	}
	if m == nil {
		return nil, fmt.Errorf("blocked task ledger item requires workspace subject authority")
	}

	normalized := *blocker
	normalized.Kind = TaskLedgerBlockerKind(strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(string(normalized.Kind)))))
	normalized.Subject.Namespace = TaskLedgerBlockerSubjectNamespace(strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(string(normalized.Subject.Namespace)))))
	normalized.Subject.ID = strings.TrimSpace(normalized.Subject.ID)
	normalized.Reason = strings.TrimSpace(normalized.Reason)
	if !normalized.Kind.Valid() {
		return nil, fmt.Errorf("unsupported task ledger blocker kind %q", normalized.Kind)
	}
	if !normalized.Subject.Namespace.Valid() {
		return nil, fmt.Errorf("unsupported task ledger blocker subject namespace %q", normalized.Subject.Namespace)
	}

	subject, err := m.normalizeTaskLedgerBlockerSubject(normalized.Subject)
	if err != nil {
		return nil, err
	}
	normalized.Subject = subject
	if normalized.Reason == "" {
		return nil, fmt.Errorf("task ledger blocker reason is required")
	}

	return &normalized, nil
}

func (m *Manager) normalizeTaskLedgerBlockerSubject(subject TaskLedgerBlockerSubjectRef) (TaskLedgerBlockerSubjectRef, error) {
	switch subject.Namespace {
	case TaskLedgerBlockerSubjectEnvironmentVariable:
		if !taskLedgerBlockerEnvironmentVariablePattern.MatchString(subject.ID) {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("task ledger environment_variable subject id must be a case-preserving POSIX variable name")
		}
	case TaskLedgerBlockerSubjectWorkspacePath:
		identity, err := m.CanonicalizeWorkspacePath(subject.ID, "")
		if err != nil {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("resolve task ledger workspace_path subject: %w", err)
		}
		subject.ID = identity.CanonicalID
	case TaskLedgerBlockerSubjectTool:
		if !m.taskLedgerBlockerKnownTool(subject.ID) {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("task ledger tool subject %q is not in the workspace tool catalog", subject.ID)
		}
	case TaskLedgerBlockerSubjectCommand:
		executable, ok := strings.CutPrefix(subject.ID, taskLedgerBlockerExecutableSubjectPrefix)
		if !ok || !taskLedgerBlockerExecutablePattern.MatchString(executable) {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("task ledger command subject must use executable:<exact-name>")
		}
	case TaskLedgerBlockerSubjectService:
		rawURI, ok := strings.CutPrefix(subject.ID, taskLedgerBlockerServiceSubjectPrefix)
		parsed, err := url.ParseRequestURI(rawURI)
		if !ok || err != nil || !parsed.IsAbs() || strings.TrimSpace(parsed.Host) == "" {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("task ledger service subject must use uri:<absolute-service-uri>")
		}
	case TaskLedgerBlockerSubjectArtifact, TaskLedgerBlockerSubjectConfiguration:
		path, ok := strings.CutPrefix(subject.ID, taskLedgerBlockerWorkspaceSubjectPrefix)
		if !ok {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("task ledger %s subject must use workspace:<path>", subject.Namespace)
		}
		identity, err := m.CanonicalizeWorkspacePath(path, "")
		if err != nil {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("resolve task ledger %s subject: %w", subject.Namespace, err)
		}
		subject.ID = taskLedgerBlockerWorkspaceSubjectPrefix + identity.CanonicalID
	case TaskLedgerBlockerSubjectPermission:
		path, ok := strings.CutPrefix(subject.ID, taskLedgerBlockerProtectedPathSubjectPrefix)
		if !ok || !m.taskLedgerBlockerProtectedPathKnown(path) {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("task ledger permission subject must name a configured protected_path:<path>")
		}
		identity, err := m.CanonicalizeWorkspacePath(path, "")
		if err != nil {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("resolve task ledger permission subject: %w", err)
		}
		subject.ID = taskLedgerBlockerProtectedPathSubjectPrefix + identity.CanonicalID
	case TaskLedgerBlockerSubjectPolicy:
		tool, ok := strings.CutPrefix(subject.ID, taskLedgerBlockerToolRuleSubjectPrefix)
		if !ok || !m.taskLedgerBlockerToolRuleKnown(tool) {
			return TaskLedgerBlockerSubjectRef{}, fmt.Errorf("task ledger policy subject must name a configured tool_rule:<tool>")
		}
		subject.ID = taskLedgerBlockerToolRuleSubjectPrefix + strings.TrimSpace(tool)
	}

	return subject, nil
}

func (m *Manager) taskLedgerBlockerKnownTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}
	if IsBuiltInToolName(tool) {
		return true
	}
	snapshot := m.ToolSurfaceSnapshot()
	return snapshot.HasExternalTool(tool)
}

func (m *Manager) taskLedgerBlockerProtectedPathKnown(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range m.pathAccess.ProtectedPathKeys(path) {
		if _, ok := m.protectedWritePaths[key]; ok {
			return true
		}
	}
	return false
}

func (m *Manager) taskLedgerBlockerToolRuleKnown(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}
	if _, ok := m.cfg.ToolRules[tool]; ok {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.permissionRules[tool]
	return ok
}
