#!/usr/bin/env bash
set -euo pipefail

MODEL="${OPENCODE_GLM_MODEL:-zai-coding-plan/glm-5.1}"
PROVIDER="${MODEL%%/*}"
ATTACH_URL="${OPENCODE_SERVER_URL:-${OPENCODE_ATTACH_URL:-}}"
PROJECT_DIR="${OPENCODE_PROJECT_DIR:-${PWD}}"

if ! command -v opencode >/dev/null 2>&1; then
  echo "opencode is not installed or is not on PATH" >&2
  exit 127
fi

if [[ "${MODEL}" != */* ]]; then
  echo "OPENCODE_GLM_MODEL must use provider/model form, got: ${MODEL}" >&2
  exit 2
fi

if ! model_output="$(opencode models "${PROVIDER}" 2>&1)"; then
  echo "OpenCode provider ${PROVIDER} is not available:" >&2
  echo "${model_output}" >&2
  exit 1
fi

if ! grep -Fqx "${MODEL}" <<<"${model_output}"; then
  echo "OpenCode model ${MODEL} was not found under provider ${PROVIDER}" >&2
  exit 1
fi

if [[ $# -eq 0 ]]; then
  exec opencode --model "${MODEL}"
fi

case "$1" in
  run)
    shift
    if [[ -n "${ATTACH_URL}" ]]; then
      exec opencode run --attach "${ATTACH_URL}" --dir "${PROJECT_DIR}" --model "${MODEL}" "$@"
    fi
    exec opencode run --model "${MODEL}" "$@"
    ;;
  serve)
    shift
    exec opencode serve "$@"
    ;;
  *)
    exec opencode "$@"
    ;;
esac
