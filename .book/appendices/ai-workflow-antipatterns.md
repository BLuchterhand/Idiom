# Appendix J: AI Workflow Antipatterns

Use this table when an AI pilot looks successful but the workflow has not
earned the authority it is being given.

| Antipattern | What It Looks Like | Why It Fails | Repair |
| --- | --- | --- | --- |
| Prompt as policy | The prompt says to follow policy, be accurate, cite sources, or escalate uncertainty. | The prompt improves behavior but does not decide which source is authoritative, current, or sufficient. | Add source ownership, freshness rules, missing-source states, and acceptance criteria. |
| Human-in-the-loop decoration | A person clicks approve after the output is already shaped, routed, or expected. | Presence is treated as review even when the reviewer lacks time, evidence, independence, or rejection authority. | Give reviewers packets, queue capacity, reject/escalate actions, and rejection authority. |
| Evals without acceptance rules | The model passes test cases, but live work still reaches authority without required evidence. | The eval measures model behavior, not workflow acceptance. | Connect eval failures to source requirements, permissions, review, throttling, and incident changes. |
| RAG as freshness | The answer cites retrieved material, so the team treats it as current. | Retrieval can find stale, superseded, incomplete, or non-authoritative material. | Add source-of-truth ownership, version rules, freshness checks, and stale-source blocks. |
| Generated-volume dashboard | Leadership counts drafts, recommendations, summaries, or tickets touched by AI. | Generated work is not accepted work. The bottleneck may have moved to review. | Track accepted verified output, incomplete packets, review debt, escaped errors, and reviewer-hours. |
| Named owner without control | A person is accountable for the AI workflow but cannot access sources, fund review, block effects, or reject pressure. | Responsibility becomes a label rather than authority. | Add budget, evidence access, rejection authority, escalation cover, and owner after acceptance. |
| Pilot success without incident learning | The pilot reports usage and satisfaction, but near misses do not change controls. | The workflow cannot improve from failure. | Review accepted and rejected work, update controls, retire unused checks, and record stop/go decisions. |
| Tool permission before action boundary | The AI receives access to send, update, apply, route, or trigger before the workflow defines what the output may do before acceptance. | A useful draft can become an action too early. | Define permitted and prohibited actions by mode: discovery only, draft assistance, reviewed proposal, bounded action. |
| Governance document without runtime hook | The policy says what should happen, but the workflow cannot enforce or observe it. | The rule depends on memory and goodwill under pressure. | Convert the policy into packet fields, permission checks, logs, throttles, escalation paths, and stop conditions. |
| Sanctioned path too slow | The official workflow is safe but unusable, so people move to private tools or side channels. | Bypass becomes the real operating model. | Create a lower-authority sanctioned path, narrow scope, fund review, or reduce required effects. |

The signal is not that a workflow has an antipattern once. Most pilots will.
The signal is whether the team can name the pattern and change the operating
state before more authority is granted.
