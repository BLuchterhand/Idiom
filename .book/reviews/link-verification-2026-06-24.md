# Link Verification Follow-Up - 2026-06-24

Supersession note: this is a historical review record. Use
`publication-readiness-tracker.md`, `source-ledger.md`, and
`package-manifest.md` for current beta/proof readiness, citation status, and
package status.

Purpose: follow up on the 10 URLs that failed command-line link checking while
validating the Kindle export.

## Result

No bibliography URL changes were required. The failed command-line checks were
access issues from automated requests, not confirmed broken links.

Current export status: after regenerating the EPUB and KPF on 2026-06-24,
Kindle Previewer 3.104.0 reports zero conversion errors and zero quality
issues. The earlier 26 beta external-link quality issues were historical
Previewer output from bare bibliography URLs and are superseded by the current
export, which renders bibliography URLs as explicit XHTML links.

## Follow-Up Checks

| URL | Command-line result | Follow-up result | Decision |
| --- | --- | --- | --- |
| `https://www.canlii.org/en/bc/bccrt/doc/2024/2024bccrt149/2024bccrt149.html` | 403 | Exact URL is cited by legal commentary and search results for `Moffatt v. Air Canada, 2024 BCCRT 149`. | Keep. Bot-blocked, not broken. |
| `https://www.dewr.gov.au/assuring-integrity-targeted-compliance-framework/announcements/statement-secretary-progress-under-targeted-compliance-framework-integrity-assurance-program` | Timeout | Browser/web verification resolved the DEWR page. | Keep. |
| `https://www.dewr.gov.au/assuring-integrity-targeted-compliance-framework/resources/targeted-compliance-framework-assurance-review-final-report` | Timeout | Browser/web verification resolved the DEWR final-report page. | Keep. |
| `https://www.dewr.gov.au/download/17418/correspondence-relating-targeted-compliance-framework-assurance-review/41834/correspondence-relating-targeted-compliance-framework-assurance-review/pdf` | Timeout | Browser/web verification resolved the PDF. | Keep. |
| `https://www.gao.gov/greenbook` | 403 | Browser/web verification resolved the GAO Green Book page. | Keep. Bot-blocked, not broken. |
| `https://www.gao.gov/products/gao-21-519sp` | 403 | Browser/web verification resolved the GAO product page. | Keep. Bot-blocked, not broken. |
| `https://www.nhtsa.gov/document/consent-order-cruise-standing-general-order-reporting` | 403 | Browser/web verification resolved the NHTSA PDF. | Keep. Bot-blocked, not broken. |
| `https://www.sec.gov/newsroom/press-releases/2013-222` | 403 | Browser/web verification resolved the SEC press release. | Keep. Bot-blocked, not broken. |
| `https://www.sec.gov/files/litigation/admin/2013/34-70694.pdf` | 403 | Browser/web verification resolved the SEC administrative order PDF. | Keep. Bot-blocked, not broken. |
| `https://www.washingtonpost.com/wellness/2023/06/01/eating-disorder-chatbot-ai-weight-loss/` | Timeout | Browser/web verification resolved the Washington Post article page. | Keep. |

## Production Note

Before public launch, complete final source/archive verification for
command-line-blocked, paywalled, fast-moving, or previously failed links,
whether or not KDP flags them during preview or upload.
