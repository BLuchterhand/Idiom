# Troubleshooting

## Backend Is Unavailable

Check the selected profile and required environment variables:

```bash
idiom backend status --profile deepseek-v4-flash
```

For hosted profiles, confirm that the API key environment variable is set. For
local profiles, confirm that the model server is running and the configured
model path exists outside the repository.

## Discovery Looks Stale

Refresh repository comprehension state:

```bash
idiom tool context_status --input '{"refresh":true,"limit":50}'
```

If the index is still stale, rebuild it:

```bash
idiom tool context_index --input '{"path":".","language":"go","mode":"standard"}'
```

## A Lane Hangs Or Loops

Inspect the newest conversation and telemetry logs before changing prompts or
runtime policy. A valid fix should identify the failed contract boundary, not
only the symptom.

## A Release Scan Finds Secrets

Treat findings in source, docs, config, logs, transcripts, or generated
artifacts as true positives until reviewed. Remove real secrets and rotate them
outside the repository. For deliberate test fixtures, use short fake values or
an explicit scanner allowlist with a narrow path and rationale.
