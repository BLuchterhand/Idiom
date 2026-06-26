# Appendix D: Review-Capacity Scorecard

Use this before increasing AI-generated proposal volume.

| Measure | Current Value | Unit / Notes | Threshold | Action When Threshold Is Crossed |
| --- | ---: | --- | ---: | --- |
| Expected proposals per week |  | count |  |  |
| Complete-packet rate |  | decimal fraction, e.g. `0.75` |  |  |
| Funded reviewer-hours per week |  | hours |  |  |
| Average review time per complete packet |  | hours |  |  |
| Maximum well-reviewed packets per week |  | count |  |  |
| Review debt |  | count |  |  |
| Incomplete packets |  | count |  |  |
| Escaped unsupported decisions |  | count | 0 |  |

Allowed action values include throttle / narrow scope / add capacity / auto-reject / return to source collection / stop / rollback.

Formula:

```text
Maximum well-reviewed packets =
  funded reviewer-hours / average review time per complete packet
```

Accepted verified output counts only work that passed the required checks and
was accepted for use. Generated volume is activity, not accepted work.
