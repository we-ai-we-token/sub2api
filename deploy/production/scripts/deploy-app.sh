#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="${SUB2API_ROOT:-/opt/sub2api}"
SHA="${1:-}"
KEEP_RELEASES="${KEEP_RELEASES:-3}"

if [ -z "${SHA}" ]; then
  echo "Usage: deploy-app.sh <git-sha>" >&2
  exit 1
fi

IMAGE_ARCHIVE="${ROOT_DIR}/releases/images/sub2api-${SHA}.tar.gz"
COMPOSE_DIR="${ROOT_DIR}/compose"
ENV_FILE="${COMPOSE_DIR}/.env.production"

if [ ! -f "${IMAGE_ARCHIVE}" ]; then
  echo "Missing image archive: ${IMAGE_ARCHIVE}" >&2
  exit 1
fi

if [ ! -f "${ENV_FILE}" ]; then
  echo "Missing env file: ${ENV_FILE}" >&2
  exit 1
fi

gzip -dc "${IMAGE_ARCHIVE}" | docker load

if grep -q '^SUB2API_IMAGE=' "${ENV_FILE}"; then
  sed -i.bak "s|^SUB2API_IMAGE=.*|SUB2API_IMAGE=sub2api:${SHA}|" "${ENV_FILE}"
else
  printf '\nSUB2API_IMAGE=sub2api:%s\n' "${SHA}" >> "${ENV_FILE}"
fi

printf '%s\n' "${SHA}" > "${ROOT_DIR}/releases/current"

cd "${COMPOSE_DIR}"
docker compose up -d sub2api caddy

find "${ROOT_DIR}/releases/images" -maxdepth 1 -name 'sub2api-*.tar.gz' -type f -print0 \
  | xargs -0 ls -t \
  | awk "NR>${KEEP_RELEASES}" \
  | xargs -r rm -f
