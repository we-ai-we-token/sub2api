#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="${SUB2API_ROOT:-/opt/sub2api}"
URL="${HEALTHCHECK_URL:-http://127.0.0.1:8080/health}"
# 默认预算 150×2s≈5min。平时启动 <1s，但带重迁移的版本会久得多：
# v0.1.179 的 226 在 usage_logs(2754 万行) 上 CREATE INDEX CONCURRENTLY 建了
# 两个 1.3GB 索引，启动耗时 124s，旧的 30×2s≈68s 预算会误判发布失败。
ATTEMPTS="${HEALTHCHECK_ATTEMPTS:-150}"
SLEEP_SECONDS="${HEALTHCHECK_SLEEP_SECONDS:-2}"

cd "${ROOT_DIR}/compose"

for attempt in $(seq 1 "${ATTEMPTS}"); do
  if docker compose --env-file .env.production exec -T sub2api curl -fsS "${URL}" >/tmp/sub2api-healthcheck-response.txt; then
    cat /tmp/sub2api-healthcheck-response.txt
    echo
    echo "Healthcheck passed on attempt ${attempt}."
    exit 0
  fi
  if [ $((attempt % 15)) -eq 0 ]; then
    echo "Still waiting for ${URL} (attempt ${attempt}/${ATTEMPTS}); app may be running migrations."
  fi
  sleep "${SLEEP_SECONDS}"
done

echo "Healthcheck failed after ${ATTEMPTS} attempts: ${URL}" >&2
docker compose --env-file .env.production ps >&2 || true
exit 1
