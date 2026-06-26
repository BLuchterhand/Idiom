# Effect-Boundary Specification

Use this before the workflow can use tools, update records, send messages, route
work, apply changes, or trigger downstream systems.

## Boundary Summary

| Field | Answer |
| --- | --- |
| Workflow name |  |
| Initial authority mode |  |
| Highest consequence surface |  |
| Boundary owner |  |
| Permission reviewer |  |

Allowed authority mode values: discovery only / draft assistance / reviewed proposal / bounded action.

Allowed consequence surface values: customer / money / record / production / legal / internal-only.

## Allowed And Prohibited Effects

| Surface | Allowed Before Acceptance | Allowed After Acceptance | Prohibited | Enforcement |
| --- | --- | --- | --- | --- |
| Source access |  |  |  |  |
| Communication |  |  |  |  |
| Record mutation |  |  |  |  |
| Money / credit / entitlement |  |  |  |  |
| Production / code / deployment |  |  |  |  |
| Routing / assignment |  |  |  |  |
| External tools / vendors |  |  |  |  |

## Tool And Permission Scope

| Tool Or System | Permission | Why Needed | Failure Mode | Log Required |
| --- | --- | --- | --- | --- |
|  |  |  |  |  |

Allowed permission values: read / draft / route / write / execute.

Allowed failure mode values: deny / sandbox / rollback / incident.

## Stop, Rollback, And Incident Path

| Condition | Immediate Behavior | Owner | Evidence To Preserve |
| --- | --- | --- | --- |
| Unsupported action attempted |  |  | prompt, source, output, attempted action |
| Accepted output lacks required evidence |  |  | packet, reviewer note, source versions |
| Permission exceeds contract |  |  | permission config, logs |
| Stale source passes as current |  |  | source version, packet, checker finding |

Allowed immediate behavior values include stop / deny / incident / rollback / correction / disable tool / narrow scope / block workflow / repair freshness rule.

## Boundary Test

Before launch, run one test for each prohibited effect and record whether the
workflow denied, blocked, routed, logged, or stopped.

| Test | Expected Result | Actual Result | Pass? |
| --- | --- | --- | --- |
|  |  |  |  |

Allowed pass values: yes / no.
