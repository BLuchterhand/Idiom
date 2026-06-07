package agent

import "idiom/internal/runtime/agent/instructions"

type InstructionAttachment = instructions.Attachment
type InstructionAttacher = instructions.Attacher

// NewInstructionAttacher constructs an attacher rooted at the given workspace
// root. Candidates default to AGENTS.md.
func NewInstructionAttacher(root string, candidates ...string) *InstructionAttacher {
	return instructions.NewAttacher(root, candidates...)
}
