# Loop Protocol

Each turn ends with EXACTLY one of: a single tool call, a `<<QUESTION>>:`
blocker, or a final answer. The runner dispatches one tool per turn;
follow-up writes wait for the result of the current call.

A lane turn has three phases:

1. Read the latest state — the brief, the previous tool result, any
   role events emitted by the runner. Do not re-read evidence already
   present in the conversation.
2. Decide one action — the next semantic discovery step or narrow read
   needed to close a named edit-safety fact, the next allowed write, or
   stop with a concrete blocker. If the next action is unclear, prefer stopping with
   `<<QUESTION>>:` over speculative reads.
3. If continuing, take that action via exactly one tool call. If the
   contract is already satisfied or blocked, stop with the final answer
   or `<<QUESTION>>:` instead of forcing a tool call.

The lane preamble owns per-mode stop conditions (when `submit_plan` is
the close, when an attempted edit + sanity check is the close, what
counts as "done" for the current lane). This file owns only the
generic per-turn discipline above; it does not restate per-mode rules
the preamble already authoritatively describes.

Do not retry an action that just failed without changing the inputs.
A failing edit anchor with the same `old_text` will fail the same way
— refresh the anchor with semantic discovery or one narrow read, or report the blocker.
