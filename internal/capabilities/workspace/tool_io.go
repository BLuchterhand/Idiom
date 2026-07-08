package workspace

import (
	"encoding/json"

	planartifact "idiom/internal/contracts/planartifact"
)

// ToolRequest is the normalized, typed form of a workspace tool call.
type ToolRequest struct {
	Tool              string              `json:"tool"`
	ID                string              `json:"id,omitempty"`
	Key               string              `json:"key,omitempty"`
	Handle            string              `json:"handle,omitempty"`
	Path              string              `json:"path,omitempty"`
	Root              string              `json:"root,omitempty"`
	ConfigPath        string              `json:"config_path,omitempty"`
	EffortRoot        string              `json:"effort_root,omitempty"`
	StartLine         int                 `json:"start_line,omitempty"`
	EndLine           int                 `json:"end_line,omitempty"`
	Pattern           string              `json:"pattern,omitempty"`
	Query             string              `json:"query,omitempty"`
	Status            string              `json:"status,omitempty"`
	Task              string              `json:"task,omitempty"`
	Purpose           string              `json:"purpose,omitempty"`
	Capability        string              `json:"capability,omitempty"`
	Decision          string              `json:"decision,omitempty"`
	Language          string              `json:"language,omitempty"`
	Glob              string              `json:"glob,omitempty"`
	Workdir           string              `json:"workdir,omitempty"`
	Command           string              `json:"command,omitempty"`
	Prompt            string              `json:"prompt,omitempty"`
	Agent             string              `json:"agent,omitempty"`
	Mode              string              `json:"mode,omitempty"`
	Content           string              `json:"content,omitempty"`
	OldText           string              `json:"old_text,omitempty"`
	NewText           string              `json:"new_text,omitempty"`
	ReplaceAll        bool                `json:"replace_all,omitempty"`
	Recursive         bool                `json:"recursive,omitempty"`
	FixedStrings      bool                `json:"fixed_strings,omitempty"`
	MaxDepth          int                 `json:"max_depth,omitempty"`
	Limit             int                 `json:"limit,omitempty"`
	BudgetTokens      int                 `json:"budget_tokens,omitempty"`
	Resolution        string              `json:"resolution,omitempty"`
	IncludeEvidence   bool                `json:"include_evidence,omitempty"`
	IncludeSource     bool                `json:"include_source,omitempty"`
	IncludeFiles      bool                `json:"include_files,omitempty"`
	Refresh           bool                `json:"refresh,omitempty"`
	OperatorView      bool                `json:"operator_view,omitempty"`
	FreshnessPolicy   string              `json:"freshness_policy,omitempty"`
	FreshnessMaxFiles int                 `json:"freshness_max_files,omitempty"`
	Category          string              `json:"category,omitempty"`
	Component         string              `json:"component,omitempty"`
	Expected          string              `json:"expected,omitempty"`
	Observed          string              `json:"observed,omitempty"`
	Workaround        string              `json:"workaround,omitempty"`
	Impact            string              `json:"impact,omitempty"`
	Evidence          []string            `json:"evidence,omitempty"`
	Confidence        string              `json:"confidence,omitempty"`
	Severity          string              `json:"severity,omitempty"`
	SuggestedFix      string              `json:"suggested_fix,omitempty"`
	SessionID         string              `json:"session_id,omitempty"`
	FailureID         string              `json:"failure_id,omitempty"`
	PacketPath        string              `json:"packet_path,omitempty"`
	OutputPath        string              `json:"output_path,omitempty"`
	RequiredTests     []string            `json:"required_tests,omitempty"`
	RequiredEditPaths []string            `json:"required_edit_paths,omitempty"`
	ForbiddenPaths    []string            `json:"forbidden_paths,omitempty"`
	Destination       string              `json:"destination,omitempty"`
	Declarations      []string            `json:"declarations,omitempty"`
	Moves             []GoDeclarationMove `json:"moves,omitempty"`
	Todos             []TaskLedgerItem    `json:"todos,omitempty"`
	Items             []TaskLedgerItem    `json:"items,omitempty"`
	// NamedGap explains one exact unresolved claim a post-readiness
	// follow-up tool call is trying to verify or falsify. It should be
	// specific enough that the next discovery move is obvious (for
	// example, inspect one function body, one caller branch, or one
	// implementation boundary). Optional in general, but required by the
	// runner once answer-readiness or child-packet sufficiency has
	// already been reached and the model still wants another read/search
	// step.
	NamedGap string `json:"named_gap,omitempty"`
	// WhyNotAnswering is a short explanation for what would be materially
	// wrong or missing if the model answered now despite already reaching
	// a closure-biased state. Optional, and usually paired with NamedGap
	// on post-readiness reads.
	WhyNotAnswering string `json:"why_not_answering,omitempty"`
	// Description is the tactical intent for a write-tool call: why this
	// edit, in one short sentence. Required for edit_file, write_file,
	// append_file, and apply_patch. Used by the plan-panel autodoc to
	// label each write with its purpose.
	Description string `json:"description,omitempty"`
	// TaskID, when set on a write-tool call, declares "this write
	// fulfills planned task <id>". The plan-panel autodoc uses it to
	// attach the write to that ledger item even when the original
	// task_ledger entry didn't list paths. Optional; falls back to
	// path-matching and a single-pending heuristic when empty.
	TaskID string `json:"task_id,omitempty"`
	// Args carries the raw, model-emitted argument JSON for
	// `mcp:<server>/<tool>` calls. MCP-discovered tools have arbitrary
	// JSON-Schema parameters that do not map onto this struct's typed
	// fields, so the agent layer routes the raw bytes through unchanged
	// and Manager.Execute hands them to mcp.Registry.Call. Empty for
	// every built-in tool: they keep using the typed fields above.
	Args json.RawMessage `json:"-"`
	// Edits carries the structured edit list for a submit_plan call
	// (the planner phase of plan-then-execute mode). Each entry is
	// one edit the executor phase should dispatch in order.
	Edits []planartifact.PlanEdit `json:"edits,omitempty"`
	// SourceSatisfiedPaths declares required edit paths whose requested final
	// state is already present. These paths satisfy submit_plan coverage but
	// are not executable edits.
	SourceSatisfiedPaths []string `json:"source_satisfied_paths,omitempty"`
	// Rationale is the planner's free-text explanation of why the plan looks
	// the way it does. Captured alongside the plan so the executor and any
	// reviewer can read the plan as a single document.
	Rationale string `json:"rationale,omitempty"`
	// PackID is the content-addressed handle of the comprehension graph_views
	// cache row the planner used when drafting the plan. Optional; when present,
	// the executor can rehydrate the planner's packet via comprehension.LoadView
	// instead of rediscovering the same files.
	PackID string `json:"pack_id,omitempty"`
	// PlanArtifact carries the durable structured submit_plan schema. It is
	// optional so legacy callers can keep sending rationale, pack_id, and edits
	// at the top level; submit_plan normalizes that legacy shape into the same
	// persisted artifact contract.
	PlanArtifact *planartifact.PlanArtifact `json:"plan_artifact,omitempty"`
}

type GoDeclarationMove struct {
	Destination  string   `json:"destination,omitempty"`
	Pattern      string   `json:"pattern,omitempty"`
	Declarations []string `json:"declarations,omitempty"`
}

type TaskLedgerItem struct {
	ID       string `json:"id,omitempty"`
	Content  string `json:"content,omitempty"`
	Status   string `json:"status,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	// Paths lists the workspace-relative files this item plans to touch. The
	// plan autodoc uses it to attach a completed write to the planned item
	// instead of adding a duplicate entry under "Unplanned". Empty when the
	// item is a non-write task or the paths are not yet known.
	Paths []string `json:"paths,omitempty"`
}

type ToolResult struct {
	Tool          string
	Code          string `json:"code,omitempty"`
	Summary       string
	Output        string
	Error         string
	Failure       error              `json:"-"`
	Paths         []string           `json:"paths,omitempty"`
	PrecisionTier string             `json:"precision_tier,omitempty"`
	Relationships []ToolRelationship `json:"relationships,omitempty"`
	PayloadType   string             `json:"payload_type,omitempty"`
	Payload       json.RawMessage    `json:"payload,omitempty"`
}

type ToolRelationship struct {
	Kind         string `json:"kind,omitempty"`
	SourcePath   string `json:"source_path,omitempty"`
	SourceLine   int    `json:"source_line,omitempty"`
	SourceSymbol string `json:"source_symbol,omitempty"`
	TargetPath   string `json:"target_path,omitempty"`
	TargetLine   int    `json:"target_line,omitempty"`
	TargetSymbol string `json:"target_symbol,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
}
