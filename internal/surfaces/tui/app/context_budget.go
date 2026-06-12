// Package app: ContextBudget derives compaction thresholds, conversation
// milestones, and context limits from a single model context-window size.
// Previously three separate functions duplicated the same derivation logic
// with slightly different proportions. This type consolidates them so
// changing the token estimate or proportion affects all consumers at once.

package app

const estTokensPerMsg = 125

// ContextBudget holds precomputed thresholds derived from the model's
// context window size (in tokens).
type ContextBudget struct {
	CtxSize int

	// Compaction thresholds (from compactionParams)
	CompactionThreshold  int // message count to trigger compaction
	CompactionRecentKeep int // recent messages to keep verbatim

	// Conversation lane milestones (from conversationMilestones)
	ConvFirstMilestone int // first call-count awareness nudge
	ConvNudgeInterval  int // interval between subsequent nudges

	// Context limits for conversation lanes (from runnerContextLimitsForConversation)
	ConvMaxMessages int
	ConvMaxChars    int
}

// NewContextBudget computes all derived thresholds from a context window
// size. Pass ctxSize <= 0 to use the default 65536.
func NewContextBudget(ctxSize int) ContextBudget {
	if ctxSize <= 0 {
		ctxSize = 65536
	}

	b := ContextBudget{CtxSize: ctxSize}

	// --- Compaction thresholds (from compactionParams) ---
	// Fire at ~40% of window, keep ~20% as recent verbatim.
	thresholdTokens := ctxSize * 40 / 100
	recentTokens := ctxSize * 20 / 100

	b.CompactionThreshold = clampBudget(thresholdTokens/estTokensPerMsg, 10, 200)
	b.CompactionRecentKeep = clampBudget(recentTokens/estTokensPerMsg, 3, 40)

	if b.CompactionRecentKeep > b.CompactionThreshold/2 {
		b.CompactionRecentKeep = b.CompactionThreshold / 2
	}

	// --- Conversation milestones (from conversationMilestones) ---
	thresholdMsgs := ctxSize * 40 / 100 / estTokensPerMsg
	if thresholdMsgs > 200 {
		thresholdMsgs = 200
	}

	maxToolCalls := thresholdMsgs / 2

	b.ConvFirstMilestone = maxInt(15, maxToolCalls*25/100)
	b.ConvNudgeInterval = maxInt(20, maxToolCalls*35/100)

	// --- Context limits (from runnerContextLimitsForConversation) ---
	b.ConvMaxMessages = (ctxSize * 80 / 100) / estTokensPerMsg
	b.ConvMaxChars = ctxSize * 4 * 80 / 100

	return b
}

func clampBudget(v, min, max int) int {
	if v < min {
		return min
	}

	if v > max {
		return max
	}

	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}
