#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="${SUB2API_ROOT:-/opt/sub2api}"
SOURCE_DIR="${1:-}"
OVERWRITE_ENV="${OVERWRITE_ENV:-false}"

if [ "$(id -u)" -ne 0 ]; then
  echo "init-server.sh must run as root" >&2
  exit 1
fi

if [ -z "${SOURCE_DIR}" ] || [ ! -d "${SOURCE_DIR}" ]; then
  echo "Usage: init-server.sh /path/to/deploy/production" >&2
  exit 1
fi

install_docker() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    return
  fi

  if ! command -v apt-get >/dev/null 2>&1; then
    echo "Docker is missing and apt-get is unavailable. Install Docker manually." >&2
    exit 1
  fi

  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  . /etc/os-release
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

install_docker

install -d -m 0755 "${ROOT_DIR}/compose"
install -d -m 0755 "${ROOT_DIR}/compose/postgres"
install -d -m 0755 "${ROOT_DIR}/compose/redis"
install -d -m 0755 "${ROOT_DIR}/compose/scripts"
install -d -m 0755 "${ROOT_DIR}/data/postgres"
install -d -m 0755 "${ROOT_DIR}/data/redis"
install -d -m 0755 "${ROOT_DIR}/data/app"
install -d -m 0755 "${ROOT_DIR}/data/caddy_data"
install -d -m 0755 "${ROOT_DIR}/data/caddy_config"
install -d -m 0755 "${ROOT_DIR}/releases/images"
install -d -m 0755 "${ROOT_DIR}/backups/postgres"
install -d -m 0755 "${ROOT_DIR}/backups/redis"
install -d -m 0755 "${ROOT_DIR}/backups/app"
install -d -m 0755 "${ROOT_DIR}/logs"

install -m 0644 "${SOURCE_DIR}/docker-compose.yml" "${ROOT_DIR}/compose/docker-compose.yml"
install -m 0644 "${SOURCE_DIR}/Caddyfile" "${ROOT_DIR}/compose/Caddyfile"
install -m 0644 "${SOURCE_DIR}/postgres/postgresql.conf" "${ROOT_DIR}/compose/postgres/postgresql.conf"
install -m 0644 "${SOURCE_DIR}/redis/redis.conf" "${ROOT_DIR}/compose/redis/redis.conf"
install -m 0755 "${SOURCE_DIR}/scripts/deploy-app.sh" "${ROOT_DIR}/compose/scripts/deploy-app.sh"
install -m 0755 "${SOURCE_DIR}/scripts/healthcheck.sh" "${ROOT_DIR}/compose/scripts/healthcheck.sh"
install -m 0755 "${SOURCE_DIR}/scripts/init-server.sh" "${ROOT_DIR}/compose/scripts/init-server.sh"

if [ ! -f "${ROOT_DIR}/compose/.env.production" ]; then
  install -m 0600 "${SOURCE_DIR}/env.example" "${ROOT_DIR}/compose/.env.production"
  echo "Created ${ROOT_DIR}/compose/.env.production from env.example. Edit it before starting production traffic."
elif [ "${OVERWRITE_ENV}" = "true" ]; then
  install -m 0600 "${SOURCE_DIR}/env.example" "${ROOT_DIR}/compose/.env.production"
  echo "Overwrote ${ROOT_DIR}/compose/.env.production from env.example."
else
  echo "Keeping existing ${ROOT_DIR}/compose/.env.production."
fi

cd "${ROOT_DIR}/compose"
docker compose pull caddy postgres redis || true
docker compose up -d --no-deps postgres redis
