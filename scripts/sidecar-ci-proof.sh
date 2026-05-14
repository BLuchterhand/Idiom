#!/usr/bin/env bash
set -euo pipefail

idiom_bin="${IDIOM_BIN:-idiom}"
caller_id="${IDIOM_CALLER_ID:-ci-script}"
request_id="${IDIOM_REQUEST_ID:-build-123}"

status_json="$(
  "$idiom_bin" tool context_status \
    --profile discovery \
    --caller-id "$caller_id" \
    --request-id "$request_id" \
    --input '{"limit":1}'
)"

jq -e --arg caller_id "$caller_id" --arg request_id "$request_id" '
  .sidecar.schema_version == "idiom.tool.v1" and
  .sidecar.profile == "discovery" and
  .sidecar.caller.caller_id == $caller_id and
  .sidecar.caller.request_id == $request_id and
  .sidecar.error == null and
  .sidecar.contract.transport == "cli" and
  .sidecar.contract.decision_authority == "runtime_contract" and
  .sidecar.freshness.policy == "report_only" and
  (.sidecar.audit_event_ids | type) == "array"
' >/dev/null <<<"$status_json"

jq -c '{
  fresh: .sidecar.structured_output.fresh,
  index_id: .sidecar.freshness.index_id,
  stale_path_count: .sidecar.freshness.stale_path_count,
  audit_event_ids: .sidecar.audit_event_ids
}' <<<"$status_json"
