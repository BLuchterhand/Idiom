#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="${IDIOM_BIN:-$ROOT/idiom}"
PROFILE="${IDIOM_ROUTE_CLASSIFIER_PROFILE:-route-classifier}"
CMD="${1:-start}"

case "$CMD" in
    start)
        shift || true
        exec "$APP" backend start --profile "$PROFILE" --log-dir "$ROOT/logs" "$@"
        ;;
    stop|status)
        shift || true
        exec "$APP" backend "$CMD" --profile "$PROFILE" "$@"
        ;;
    restart)
        shift || true
        "$APP" backend stop --profile "$PROFILE"
        exec "$APP" backend start --profile "$PROFILE" --log-dir "$ROOT/logs" "$@"
        ;;
    *)
        echo "usage: $0 [start|stop|restart|status] [idiom backend flags...]" >&2
        exit 2
        ;;
esac
