# Appendix H: Workflow Readiness Preflight

Use this before deciding whether an AI workflow is ready for draft assistance,
reviewed proposals, or bounded action.

The preflight is not a maturity score. It is a refusal tool. If the workflow
cannot answer these questions, the AI should stay at a lower authority level
until the missing owner, source, reviewer, or effect boundary exists.

## Minimum Readiness Check

| Question | Ready Answer | If Missing |
| --- | --- | --- |
| What exact decision or work state is involved? | One named decision, claim, packet, record, or action. | Keep AI in discovery only or draft assistance. |
| Which source controls the highest-risk claim? | A named current source with an owner. | Do not accept the claim. |
| What may the output do before acceptance? | A specific action limit. | Treat all actions as prohibited. |
| What effect is explicitly prohibited? | Customer commitment, record mutation, payment, filing, routing, or other bounded effect. | Do not grant tool or communication authority. |
| Who checks the controlling claim? | A reviewer, automated check, source owner, or domain owner. | Return incomplete before review. |
| How much review capacity exists? | Funded reviewer-hours and queue threshold. | Throttle proposal volume. |
| What condition requires rejection or escalation? | A named condition that blocks or escalates without informal override. | Do not call human review a control. |
| Who owns the accepted decision? | A person or role with authority after the workflow completes. | Do not promote output to accepted work. |
| What is logged? | Sources, claims, reviewer action, acceptance state, and effect. | Do not expand authority. |
| What happens when it fails? | Stop, rollback, correction, escalation, or incident path. | Keep the workflow reversible. |

## Authority Recommendation

| If The Preflight Shows... | Use This Mode |
| --- | --- |
| The decision is unclear or source ownership is missing. | No production workflow yet. Use discovery only. |
| Sources exist but acceptance authority is not assigned. | Draft assistance. |
| Sources, reviewer, and blocked states exist, but effects are high-risk. | Reviewed proposal. |
| Sources, review, permissions, rollback, logging, and owner after acceptance exist. | Bounded action. |

## The One-Sentence Output

The preflight should produce a sentence plain enough to test:

```text
This workflow may use AI to [permitted work] but may not [prohibited action]
until [source/check] supports the required claims and [owner] accepts
[decision].
```

For the support-credit workflow:

```text
This workflow may use AI to prepare a service-credit packet but may not imply
customer approval or update the credit record until finance approves the amount,
compliance/legal clears the policy or contract issue, and the workflow records
the recommendation as accepted against the current contract, policy, and outage
evidence.
```

If the sentence cannot be written without vague words such as "appropriate,"
"aligned," "reviewed," or "responsible," the workflow is not ready for more
authority.
