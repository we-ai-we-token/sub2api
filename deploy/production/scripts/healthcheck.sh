#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="${SUB2API_ROOT:-/opt/sub2api}"
URL="${HEALTHCHECK_URL:-http://127.0.0.1:3000/health}"
ATTEMPTS="${HEALTHCHECK_ATTEMPTS:-30}"
SLEEP_SECONDS="${HEALTHCHECK_SLEEP_SECONDS:-2}"

for attempt in $(seq 1 "${ATTEMPTS}"); do
  if curl -fsS "${URL}" >/tmp/sub2api-healthcheck-response.txt; then
    cat /tmp/sub2api-healthcheck-response.txt
    echo
    echo "Healthcheck passed on attempt ${attempt}."
    exit 0
  fi
  sleep "${SLEEP_SECONDS}"
done

echo "Healthcheck failed after ${ATTEMPTS} attempts: ${URL}" >&2
cd "${ROOT_DIR}/compose" 2>/dev/null && docker compose ps >&2 || true
exit 1
