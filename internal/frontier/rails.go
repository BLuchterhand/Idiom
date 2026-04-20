package frontier

type ControlRailsSnapshot struct {
	Lane                 string   `json:"lane,omitempty"`
	FollowupLane         string   `json:"followup_lane,omitempty"`
	Contract             string   `json:"contract,omitempty"`
	AgentMode            string   `json:"agent_mode,omitempty"`
	WorkflowKind         string   `json:"workflow_kind,omitempty"`
	WorkflowPhase        string   `json:"workflow_phase,omitempty"`
	Intent               string   `json:"intent,omitempty"`
	IntentConfidence     string   `json:"intent_confidence,omitempty"`
	Risk                 string   `json:"risk,omitempty"`
	TaskFamily           string   `json:"task_family,omitempty"`
	PermissionProfile    string   `json:"permission_profile,omitempty"`
	UserBoundaries       []string `json:"user_boundaries,omitempty"`
	TargetRefs           []string `json:"target_refs,omitempty"`
	RequiredEvidence     []string `json:"required_evidence,omitempty"`
	RequiredTools        []string `json:"required_tools,omitempty"`
	AllowedTools         []string `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string `json:"forbidden_tools,omitempty"`
	ReadinessGate        []string `json:"readiness_gate,omitempty"`
	SearchPolicy         []string `json:"search_policy,omitempty"`
	VerificationContract string   `json:"verification_contract,omitempty"`
	StopConditions       []string `json:"stop_conditions,omitempty"`
	FinalCritic          []string `json:"final_critic,omitempty"`
	FailureToEval        bool     `json:"failure_to_eval"`
}

type ControlRailsEnforcementSnapshot struct {
	Lane       string   `json:"lane,omitempty"`
	TaskFamily string   `json:"task_family,omitempty"`
	Passed     bool     `json:"passed"`
	Action     string   `json:"action,omitempty"`
	Violations []string `json:"violations,omitempty"`
}

type DynamicToolPolicySnapshot struct {
	Lane           string   `json:"lane,omitempty"`
	TaskFamily     string   `json:"task_family,omitempty"`
	AgentMode      string   `json:"agent_mode,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	ForbiddenTools []string `json:"forbidden_tools,omitempty"`
}
