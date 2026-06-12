package completiongate

import "idiom/internal/capabilities/workspace"

// Check inspects a model reply and accumulated results, returning a rejection
// ToolResult with shouldReject=true, or an empty ToolResult with false.
type Check func(reply string, results []workspace.ToolResult) (rejection workspace.ToolResult, shouldReject bool)

// Pipeline evaluates checks in registration order and returns the first
// rejection. If no check rejects, the pipeline passes.
type Pipeline struct {
	checks []Check
}

func New(checks ...Check) *Pipeline {
	return &Pipeline{checks: append([]Check(nil), checks...)}
}

func (p *Pipeline) Evaluate(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if p == nil {
		return workspace.ToolResult{}, false
	}

	for _, check := range p.checks {
		if rejection, rejected := check(reply, results); rejected {
			return rejection, true
		}
	}

	return workspace.ToolResult{}, false
}
