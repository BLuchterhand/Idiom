# Appendix I: Vendor And Tool Evaluation Checklist

Use this when a vendor, platform team, or internal tool owner claims that an AI
system is safe, governed, grounded, or production-ready.

Vendor features can help the workflow. They do not replace the workflow. The
question is not whether the tool has a control-shaped feature. The question is
whether that feature can support the decision, source, permission, review,
effect, audit, and recovery rules your workflow requires.

## Evaluation Questions

| Area | Ask For | What It Supports | What It Does Not Establish |
| --- | --- | --- | --- |
| Model evaluation | Task-specific eval design, failure cases, regression history, and pass/fail criteria. | The model was tested against known conditions. | That your workflow can accept outputs safely. |
| Retrieval and grounding | Source display, citation behavior, freshness controls, and missing-source states. | The tool can expose candidate support for claims. | That a cited source is current, authoritative, or sufficient. |
| Permissions | Role-based access, tool scopes, data boundaries, and environment separation. | The system can restrict what the AI may see or affect. | That the granted permission matches business authority. |
| Human review | Reviewer queues, packet fields, reject/escalate actions, and rejection controls. | The tool can route work to people. | That reviewers have time, independence, expertise, or rejection authority. |
| Audit logs | Prompt, source, output, action, reviewer, acceptance state, and effect logs. | The workflow can reconstruct what happened. | That the logged decision was correct. |
| Data handling | Retention, training use, deletion, tenant isolation, and restricted-data controls. | The system can satisfy data-boundary requirements. | That the workflow should send that data to the tool. |
| Incident response | Rollback, correction, alerting, rate limits, disable switches, and escalation hooks. | The tool can support recovery. | That the organization has assigned recovery ownership. |
| Change management | Model/version changes, prompt changes, source-corpus changes, and notification policy. | The workflow can detect some drift. | That old acceptance evidence still applies after change. |
| Export and portability | Logs, source packets, eval results, configuration, and incident records. | The organization can inspect or migrate evidence. | That the vendor's interpretation of the evidence is enough. |

## Claims To Treat Carefully

| Vendor Claim | Safer Interpretation |
| --- | --- |
| "Grounded in your data." | The system can retrieve or reference selected sources; freshness and authority still need workflow rules. |
| "Human in the loop." | A person appears in the path; review is a control only if the person has evidence, time, and authority to reject or escalate. |
| "Enterprise-grade governance." | Some governance features exist; verify which workflow states they can block, route, log, throttle, or stop. |
| "Agentic automation." | The system can act through tools; every action needs an effect boundary, failure state, and owner. |
| "Proven accuracy." | The model performed under a tested condition; acceptance depends on your claims, sources, reviewers, and what the workflow permits before acceptance. |

## Minimum Procurement Packet

Ask the vendor or internal platform owner for:

- eval examples that match your workflow;
- known failure modes and mitigations;
- data-retention and training-use terms;
- source freshness and citation behavior;
- permission model and tool scopes;
- audit-log fields and export format;
- human-review workflow and reject/escalate states;
- incident-response and rollback controls;
- model, prompt, and retrieval-change notification process;
- security review artifacts required by your organization.

The packet is complete only when your team can map each feature to a workflow
control. A feature that cannot change acceptance, permission, effect, logging,
review, recovery, or escalation is helpful context, not a trust boundary.
