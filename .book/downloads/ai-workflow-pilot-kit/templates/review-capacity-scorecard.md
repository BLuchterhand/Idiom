# Review-Capacity Scorecard

Use this before increasing generated volume. The matching CSV in
`../spreadsheets/review-capacity-scorecard.csv` contains spreadsheet formulas in
the `Value` column for calculated rows.

## Inputs

| Measure | Value | Notes |
| --- | ---: | --- |
| Expected proposals per week |  |  |
| Complete-packet rate |  | Decimal fraction of generated packets that arrive reviewable, such as `0.75`. |
| Funded reviewer-hours per week |  | Time actually allocated, not hoped for. |
| Average review time per complete packet |  | Include source inspection and audit note. |
| Escaped unsupported decisions |  | Threshold is zero. |

## Calculations

| Measure | Formula |
| --- | --- |
| Complete packets per week | expected proposals * complete-packet rate |
| Maximum well-reviewed packets per week | funded reviewer-hours / average review time |
| Review debt | complete packets - maximum well-reviewed packets |

## Actions

| Condition | Action |
| --- | --- |
| Review debt is above zero | Throttle volume, narrow scope, or add funded reviewer time. |
| Complete-packet rate is low | Return incomplete packets and fix source collection. |
| Escaped unsupported decisions are above zero | Stop or roll back authority before optimizing throughput. |
| Review time is rising | Inspect packet quality, source access, and reviewer calibration. |

Do not increase authority unless review debt, escaped unsupported decisions,
and bypass incidents are zero for the chosen review window and the
authority-pace checklist is complete.
