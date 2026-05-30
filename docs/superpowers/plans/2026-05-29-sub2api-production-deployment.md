# Sub2API Production Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add repository-managed production deployment definitions and manual GitHub Actions for initializing, deploying, and firewalling a single-origin Sub2API production server.

**Architecture:** Production runs on one origin server under `/opt/sub2api` with Docker Compose services for Caddy, Sub2API, Postgres, and Redis. GitHub Actions are manually triggered: one initializes/syncs server deployment files, one builds a local Docker image tar and deploys it over SSH, and one applies UFW firewall allowlists from secrets.

**Tech Stack:** Docker Compose, Caddy, Postgres, Redis, Bash, GitHub Actions, SSH/SCP, UFW.

---

## File Structure

Create production deployment templates:

- `deploy/production/docker-compose.yml`
  - Defines `caddy`, `sub2api`, `postgres`, and `redis` services.
  - Uses local bind mounts under `/opt/sub2api/data`.
  - Exposes only Caddy ports from Compose.
  - Applies first-version resource settings.

- `deploy/production/Caddyfile`
  - Serves `core.we-ai.cc`.
  - Reverse proxies to `sub2api:3000`.

- `deploy/production/env.example`
  - Documents required `.env.production` keys without real secrets.
  - Includes Sub2API resource/concurrency settings.

- `deploy/production/postgres/postgresql.conf`
  - Production Postgres tuning for the first 128 GB server.

- `deploy/production/postgres/pg_hba.conf.example`
  - Example access policy for container-local Postgres usage.

- `deploy/production/redis/redis.conf`
  - Redis maxmemory and persistence settings.

- `deploy/production/scripts/init-server.sh`
  - Creates `/opt/sub2api` directories.
  - Installs Docker if missing only when the host is apt-based.
  - Copies templates into `/opt/sub2api/compose`.
  - Refuses to overwrite an existing `.env.production` unless explicitly allowed.
  - Starts the Compose stack.

- `deploy/production/scripts/deploy-app.sh`
  - Loads a pre-uploaded Docker image tar.
  - Updates `/opt/sub2api/releases/current`.
  - Restarts only the `sub2api` service.
  - Keeps recent image archives.

- `deploy/production/scripts/healthcheck.sh`
  - Verifies `http://127.0.0.1:3000/health` from the host or app container.

Create manual GitHub Actions:

- `.github/workflows/production-init.yml`
  - Uploads `deploy/production` to the server.
  - Runs `init-server.sh`.

- `.github/workflows/production-deploy.yml`
  - Checks out `release`.
  - Builds Docker image `sub2api:<sha>`.
  - Saves and compresses the image tar.
  - Uploads it to the server.
  - Runs `deploy-app.sh <sha>` and `healthcheck.sh`.

- `.github/workflows/production-firewall.yml`
  - Applies UFW rules from `CN2_GATEWAY_IPS` and `ADMIN_SSH_IPS`.

Update documentation:

- `CLAUDE.md`
  - Add a short note that this session only produced spec/plan documents and implementation should follow this plan later.

Do not add Prometheus, Grafana, exporters, cAdvisor, registry pushes, or automatic deploy-on-push behavior in this plan.

## Secrets Required

Repository secrets required by the workflows:

```text
PROD_HOST
PROD_SSH_USER
PROD_SSH_KEY
ADMIN_SSH_IPS
CN2_GATEWAY_IPS
```

Optional workflow secret:

```text
ALLOW_HTTP_80
```

Production app secrets should be provided through GitHub Secrets and written to `/opt/sub2api/compose/.env.production` during `production-init`. Do not commit real production values.

Production app secrets required by the first implementation:

```text
POSTGRES_PASSWORD
REDIS_PASSWORD
JWT_SECRET
TOTP_ENCRYPTION_KEY
```

`TOTP_ENCRYPTION_KEY` must be generated as a strong random value, such as `openssl rand -base64 32`, and kept stable after production users enable TOTP.

---

### Task 1: Create Production Deployment Directory Skeleton

**Files:**
- Create: `deploy/production/.gitkeep`
- Create: `deploy/production/postgres/.gitkeep`
- Create: `deploy/production/redis/.gitkeep`
- Create: `deploy/production/scripts/.gitkeep`

- [ ] **Step 1: Create directories and placeholder files**

Run:

```bash
mkdir -p deploy/production/postgres deploy/production/redis deploy/production/scripts
touch deploy/production/.gitkeep deploy/production/postgres/.gitkeep deploy/production/redis/.gitkeep deploy/production/scripts/.gitkeep
```

Expected: command exits with status 0.

- [ ] **Step 2: Verify directory structure**

Run:

```bash
find deploy/production -maxdepth 3 -type f | sort
```

Expected output:

```text
deploy/production/.gitkeep
deploy/production/postgres/.gitkeep
deploy/production/redis/.gitkeep
deploy/production/scripts/.gitkeep
```

- [ ] **Step 3: Commit**

Run:

```bash
git add deploy/production/.gitkeep deploy/production/postgres/.gitkeep deploy/production/redis/.gitkeep deploy/production/scripts/.gitkeep
git commit -m "chore: add production deployment skeleton"
```

Expected: a new commit is created.

---

### Task 2: Add Production Environment Template

**Files:**
- Create: `deploy/production/env.example`

- [ ] **Step 1: Write environment template**

Create `deploy/production/env.example`:

```dotenv
# Copy this file to /opt/sub2api/compose/.env.production on the production server.
# Do not commit real production secrets.

COMPOSE_PROJECT_NAME=sub2api

# Origin domain served by the origin Caddy instance.
ORIGIN_DOMAIN=core.we-ai.cc

# Sub2API image tag. deployment workflow updates this value on the server.
SUB2API_IMAGE=sub2api:local

# Sub2API runtime.
SERVER_MODE=release
GIN_MODE=release
GOMAXPROCS=64

# Sub2API service port inside the Docker network.
SUB2API_PORT=3000

# Database.
POSTGRES_DB=sub2api
POSTGRES_USER=sub2api
POSTGRES_PASSWORD=change-me
DATABASE_HOST=postgres
DATABASE_PORT=5432
DATABASE_NAME=sub2api
DATABASE_USER=sub2api
DATABASE_PASSWORD=change-me
DATABASE_SSL_MODE=disable
DATABASE_MAX_OPEN_CONNS=400
DATABASE_MAX_IDLE_CONNS=200
DATABASE_CONN_MAX_LIFETIME_MINUTES=30
DATABASE_CONN_MAX_IDLE_TIME_MINUTES=5

# Redis.
REDIS_HOST=redis
REDIS_PORT=6379
REDIS_PASSWORD=change-me
REDIS_DB=0
REDIS_POOL_SIZE=8192
REDIS_MIN_IDLE_CONNS=512

# Required application secrets.
JWT_SECRET=change-me
TOTP_ENCRYPTION_KEY=change-me

# Request body protection.
SERVER_MAX_REQUEST_BODY_SIZE=268435456
GATEWAY_MAX_BODY_SIZE=268435456

# Image-generation-heavy gateway limits.
GATEWAY_IMAGE_CONCURRENCY_ENABLED=true
GATEWAY_IMAGE_CONCURRENCY_MAX_CONCURRENT_REQUESTS=2000
GATEWAY_IMAGE_CONCURRENCY_OVERFLOW_MODE=wait
GATEWAY_IMAGE_CONCURRENCY_WAIT_TIMEOUT_SECONDS=30
GATEWAY_IMAGE_CONCURRENCY_MAX_WAITING_REQUESTS=300

# OpenAI WS account distribution.
GATEWAY_OPENAI_WS_LB_TOP_K=1000

# Long-running image streams.
GATEWAY_IMAGE_STREAM_DATA_INTERVAL_TIMEOUT=900
GATEWAY_IMAGE_STREAM_KEEPALIVE_INTERVAL=10
GATEWAY_OPENAI_RESPONSE_HEADER_TIMEOUT=0

# Upstream connection pooling.
GATEWAY_MAX_CONNS_PER_HOST=4096
GATEWAY_MAX_IDLE_CONNS=16384
GATEWAY_MAX_IDLE_CONNS_PER_HOST=4096
GATEWAY_OPENAI_HTTP2_ENABLED=true
GATEWAY_OPENAI_HTTP2_ALLOW_PROXY_FALLBACK_TO_HTTP1=true

# Scheduler waiting behavior.
GATEWAY_SCHEDULING_FALLBACK_WAIT_TIMEOUT=30s
GATEWAY_SCHEDULING_FALLBACK_MAX_WAITING=300
GATEWAY_SCHEDULING_STICKY_SESSION_MAX_WAITING=3
GATEWAY_SCHEDULING_STICKY_SESSION_WAIT_TIMEOUT=15s

# Overload cooldown.
RATE_LIMIT_OVERLOAD_COOLDOWN_MINUTES=5

# Logging.
LOG_LEVEL=info
LOG_FORMAT=json
LOG_OUTPUT_TO_STDOUT=true
LOG_OUTPUT_TO_FILE=true
LOG_ROTATION_MAX_SIZE_MB=100
LOG_ROTATION_MAX_BACKUPS=10
LOG_ROTATION_MAX_AGE_DAYS=7
LOG_SAMPLING_ENABLED=false
```

- [ ] **Step 2: Check no real secrets were added**

Run:

```bash
grep -n 'change-me' deploy/production/env.example
```

Expected: every sensitive value remains `change-me`.

- [ ] **Step 3: Commit**

Run:

```bash
git add deploy/production/env.example
git commit -m "docs: add production environment template"
```

Expected: a new commit is created.

---

### Task 3: Add Postgres and Redis Production Configs

**Files:**
- Create: `deploy/production/postgres/postgresql.conf`
- Create: `deploy/production/postgres/pg_hba.conf.example`
- Create: `deploy/production/redis/redis.conf`

- [ ] **Step 1: Write Postgres config**

Create `deploy/production/postgres/postgresql.conf`:

```conf
listen_addresses = '*'
port = 5432
max_connections = 500

shared_buffers = 32GB
effective_cache_size = 160GB
maintenance_work_mem = 4GB
work_mem = 32MB

checkpoint_timeout = 15min
max_wal_size = 64GB
min_wal_size = 4GB
wal_compression = on

random_page_cost = 1.1
effective_io_concurrency = 256

log_min_duration_statement = 1000
log_checkpoints = on
log_connections = off
log_disconnections = off
log_lock_waits = on

autovacuum = on
autovacuum_max_workers = 6
autovacuum_naptime = 10s
```

- [ ] **Step 2: Write Postgres HBA example**

Create `deploy/production/postgres/pg_hba.conf.example`:

```conf
# Example only. Review before using in production.
local   all             all                                     trust
host    all             all             127.0.0.1/32            scram-sha-256
host    all             all             ::1/128                 scram-sha-256
host    all             all             172.16.0.0/12           scram-sha-256
host    all             all             10.0.0.0/8              scram-sha-256
host    all             all             192.168.0.0/16          scram-sha-256
```

- [ ] **Step 3: Write Redis config**

Create `deploy/production/redis/redis.conf`:

```conf
bind 0.0.0.0
port 6379
protected-mode yes
requirepass ${REDIS_PASSWORD}

maxmemory 32gb
maxmemory-policy allkeys-lru

appendonly yes
appendfsync everysec
save 900 1
save 300 10
save 60 10000

tcp-keepalive 300
timeout 0
```

- [ ] **Step 4: Verify files exist**

Run:

```bash
test -f deploy/production/postgres/postgresql.conf && test -f deploy/production/postgres/pg_hba.conf.example && test -f deploy/production/redis/redis.conf
```

Expected: command exits with status 0.

- [ ] **Step 5: Commit**

Run:

```bash
git add deploy/production/postgres/postgresql.conf deploy/production/postgres/pg_hba.conf.example deploy/production/redis/redis.conf
git commit -m "ops: add production datastore configs"
```

Expected: a new commit is created.

---

### Task 4: Add Caddy Origin Config

**Files:**
- Create: `deploy/production/Caddyfile`

- [ ] **Step 1: Write Caddyfile**

Create `deploy/production/Caddyfile`:

```caddyfile
{$ORIGIN_DOMAIN} {
    encode zstd gzip

    reverse_proxy sub2api:3000 {
        transport http {
            read_timeout 600s
            write_timeout 600s
            dial_timeout 10s
        }
    }
}
```

- [ ] **Step 2: Verify domain placeholder is used**

Run:

```bash
grep -n '\{$ORIGIN_DOMAIN\}' deploy/production/Caddyfile
```

Expected output includes:

```text
1:{$ORIGIN_DOMAIN} {
```

- [ ] **Step 3: Commit**

Run:

```bash
git add deploy/production/Caddyfile
git commit -m "ops: add production origin caddy config"
```

Expected: a new commit is created.

---

### Task 5: Add Production Docker Compose

**Files:**
- Create: `deploy/production/docker-compose.yml`

- [ ] **Step 1: Write Compose file**

Create `deploy/production/docker-compose.yml`:

```yaml
services:
  caddy:
    image: caddy:2.8-alpine
    restart: unless-stopped
    env_file:
      - .env.production
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - /opt/sub2api/data/caddy_data:/data
      - /opt/sub2api/data/caddy_config:/config
    depends_on:
      - sub2api
    networks:
      - sub2api

  sub2api:
    image: ${SUB2API_IMAGE:-sub2api:local}
    restart: unless-stopped
    env_file:
      - .env.production
    environment:
      - SERVER_MODE=${SERVER_MODE:-release}
      - GIN_MODE=${GIN_MODE:-release}
      - GOMAXPROCS=${GOMAXPROCS:-64}
      - DATABASE_HOST=${DATABASE_HOST:-postgres}
      - DATABASE_PORT=${DATABASE_PORT:-5432}
      - DATABASE_NAME=${DATABASE_NAME:-sub2api}
      - DATABASE_USER=${DATABASE_USER:-sub2api}
      - DATABASE_PASSWORD=${DATABASE_PASSWORD}
      - DATABASE_SSL_MODE=${DATABASE_SSL_MODE:-disable}
      - REDIS_HOST=${REDIS_HOST:-redis}
      - REDIS_PORT=${REDIS_PORT:-6379}
      - REDIS_PASSWORD=${REDIS_PASSWORD}
      - REDIS_DB=${REDIS_DB:-0}
    expose:
      - "3000"
    volumes:
      - /opt/sub2api/data/app:/app/data
      - /opt/sub2api/logs:/app/logs
    mem_limit: 128g
    ulimits:
      nofile:
        soft: 1048576
        hard: 1048576
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "-", "http://127.0.0.1:3000/health"]
      interval: 30s
      timeout: 5s
      retries: 5
      start_period: 60s
    networks:
      - sub2api

  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    env_file:
      - .env.production
    environment:
      - POSTGRES_DB=${POSTGRES_DB:-sub2api}
      - POSTGRES_USER=${POSTGRES_USER:-sub2api}
      - POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
      - POSTGRES_INITDB_ARGS=--encoding=UTF8 --locale=C
    command: ["postgres", "-c", "config_file=/etc/postgresql/postgresql.conf"]
    volumes:
      - /opt/sub2api/data/postgres:/var/lib/postgresql/data
      - ./postgres/postgresql.conf:/etc/postgresql/postgresql.conf:ro
    shm_size: 2g
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER:-sub2api} -d ${POSTGRES_DB:-sub2api}"]
      interval: 10s
      timeout: 5s
      retries: 5
    networks:
      - sub2api

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    env_file:
      - .env.production
    command:
      - sh
      - -c
      - redis-server /usr/local/etc/redis/redis.conf --requirepass "$${REDIS_PASSWORD}"
    volumes:
      - /opt/sub2api/data/redis:/data
      - ./redis/redis.conf:/usr/local/etc/redis/redis.conf:ro
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a "$${REDIS_PASSWORD}" ping | grep PONG"]
      interval: 10s
      timeout: 5s
      retries: 5
    networks:
      - sub2api

networks:
  sub2api:
    driver: bridge
```

- [ ] **Step 2: Validate Compose file syntax**

Run:

```bash
docker compose -f deploy/production/docker-compose.yml config >/tmp/sub2api-production-compose.txt
```

Expected: command exits with status 0. If Docker is unavailable locally, run:

```bash
ruby -e "require 'yaml'; YAML.load_file('deploy/production/docker-compose.yml'); puts 'valid yaml'"
```

Expected output:

```text
valid yaml
```

- [ ] **Step 3: Commit**

Run:

```bash
git add deploy/production/docker-compose.yml
git commit -m "ops: add production compose stack"
```

Expected: a new commit is created.

---

### Task 6: Add Server Initialization Script

**Files:**
- Create: `deploy/production/scripts/init-server.sh`

- [ ] **Step 1: Write init script**

Create `deploy/production/scripts/init-server.sh`:

```bash
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
docker compose up -d postgres redis caddy
```

- [ ] **Step 2: Make script executable**

Run:

```bash
chmod +x deploy/production/scripts/init-server.sh
```

Expected: command exits with status 0.

- [ ] **Step 3: Run shell syntax check**

Run:

```bash
bash -n deploy/production/scripts/init-server.sh
```

Expected: command exits with status 0.

- [ ] **Step 4: Commit**

Run:

```bash
git add deploy/production/scripts/init-server.sh
git commit -m "ops: add production init script"
```

Expected: a new commit is created.

---

### Task 7: Add App Deployment Script

**Files:**
- Create: `deploy/production/scripts/deploy-app.sh`

- [ ] **Step 1: Write deploy script**

Create `deploy/production/scripts/deploy-app.sh`:

```bash
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
docker compose up -d sub2api

find "${ROOT_DIR}/releases/images" -maxdepth 1 -name 'sub2api-*.tar.gz' -type f -print0 \
  | xargs -0 ls -t \
  | awk "NR>${KEEP_RELEASES}" \
  | xargs -r rm -f
```

- [ ] **Step 2: Make script executable**

Run:

```bash
chmod +x deploy/production/scripts/deploy-app.sh
```

Expected: command exits with status 0.

- [ ] **Step 3: Run shell syntax check**

Run:

```bash
bash -n deploy/production/scripts/deploy-app.sh
```

Expected: command exits with status 0.

- [ ] **Step 4: Commit**

Run:

```bash
git add deploy/production/scripts/deploy-app.sh
git commit -m "ops: add production app deploy script"
```

Expected: a new commit is created.

---

### Task 8: Add Health Check Script

**Files:**
- Create: `deploy/production/scripts/healthcheck.sh`

- [ ] **Step 1: Write healthcheck script**

Create `deploy/production/scripts/healthcheck.sh`:

```bash
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
```

- [ ] **Step 2: Make script executable**

Run:

```bash
chmod +x deploy/production/scripts/healthcheck.sh
```

Expected: command exits with status 0.

- [ ] **Step 3: Run shell syntax check**

Run:

```bash
bash -n deploy/production/scripts/healthcheck.sh
```

Expected: command exits with status 0.

- [ ] **Step 4: Commit**

Run:

```bash
git add deploy/production/scripts/healthcheck.sh
git commit -m "ops: add production healthcheck script"
```

Expected: a new commit is created.

---

### Task 9: Add Manual Production Init Workflow

**Files:**
- Create: `.github/workflows/production-init.yml`

- [ ] **Step 1: Write init workflow**

Create `.github/workflows/production-init.yml`:

```yaml
name: Production Init

on:
  workflow_dispatch:
    inputs:
      confirm:
        description: 'Type INIT to sync production deployment files'
        required: true
        type: string

permissions:
  contents: read

concurrency:
  group: production-init
  cancel-in-progress: false

jobs:
  init:
    name: Initialize production server
    runs-on: ubuntu-latest
    if: ${{ github.event.inputs.confirm == 'INIT' }}
    timeout-minutes: 20
    steps:
      - name: Checkout release branch
        uses: actions/checkout@v4
        with:
          ref: release

      - name: Validate required secrets
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
          PROD_SSH_KEY: ${{ secrets.PROD_SSH_KEY }}
        run: |
          set -euo pipefail
          for name in PROD_HOST PROD_SSH_USER PROD_SSH_KEY; do
            if [ -z "${!name}" ]; then
              echo "Missing required secret: ${name}" >&2
              exit 1
            fi
          done

      - name: Prepare SSH
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_KEY: ${{ secrets.PROD_SSH_KEY }}
        run: |
          set -euo pipefail
          install -m 700 -d ~/.ssh
          printf '%s\n' "${PROD_SSH_KEY}" > ~/.ssh/prod_key
          chmod 600 ~/.ssh/prod_key
          ssh-keyscan -H "${PROD_HOST}" >> ~/.ssh/known_hosts

      - name: Upload production deployment files
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
        run: |
          set -euo pipefail
          tar czf /tmp/sub2api-production-deploy.tgz -C deploy production
          scp -i ~/.ssh/prod_key /tmp/sub2api-production-deploy.tgz "${PROD_SSH_USER}@${PROD_HOST}:/tmp/sub2api-production-deploy.tgz"
          ssh -i ~/.ssh/prod_key "${PROD_SSH_USER}@${PROD_HOST}" 'rm -rf /tmp/sub2api-production && mkdir -p /tmp/sub2api-production && tar xzf /tmp/sub2api-production-deploy.tgz -C /tmp/sub2api-production'

      - name: Run production init script
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
        run: |
          set -euo pipefail
          ssh -i ~/.ssh/prod_key "${PROD_SSH_USER}@${PROD_HOST}" 'sudo /tmp/sub2api-production/production/scripts/init-server.sh /tmp/sub2api-production/production'

      - name: Confirm skipped input
        if: ${{ github.event.inputs.confirm != 'INIT' }}
        run: |
          echo "Production init was not run because confirm was not INIT."
          exit 1
```

- [ ] **Step 2: Validate YAML**

Run:

```bash
ruby -e "require 'yaml'; YAML.load_file('.github/workflows/production-init.yml'); puts 'valid yaml'"
```

Expected output:

```text
valid yaml
```

- [ ] **Step 3: Commit**

Run:

```bash
git add .github/workflows/production-init.yml
git commit -m "ci: add production init workflow"
```

Expected: a new commit is created.

---

### Task 10: Add Manual Production Deploy Workflow

**Files:**
- Create: `.github/workflows/production-deploy.yml`

- [ ] **Step 1: Write deploy workflow**

Create `.github/workflows/production-deploy.yml`:

```yaml
name: Production Deploy

on:
  workflow_dispatch:
    inputs:
      confirm:
        description: 'Type DEPLOY to deploy the release branch'
        required: true
        type: string

permissions:
  contents: read

concurrency:
  group: production-deploy
  cancel-in-progress: false

jobs:
  deploy:
    name: Build and deploy production image
    runs-on: ubuntu-latest
    if: ${{ github.event.inputs.confirm == 'DEPLOY' }}
    timeout-minutes: 60
    steps:
      - name: Checkout release branch
        uses: actions/checkout@v4
        with:
          ref: release

      - name: Validate required secrets
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
          PROD_SSH_KEY: ${{ secrets.PROD_SSH_KEY }}
        run: |
          set -euo pipefail
          for name in PROD_HOST PROD_SSH_USER PROD_SSH_KEY; do
            if [ -z "${!name}" ]; then
              echo "Missing required secret: ${name}" >&2
              exit 1
            fi
          done

      - name: Prepare SSH
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_KEY: ${{ secrets.PROD_SSH_KEY }}
        run: |
          set -euo pipefail
          install -m 700 -d ~/.ssh
          printf '%s\n' "${PROD_SSH_KEY}" > ~/.ssh/prod_key
          chmod 600 ~/.ssh/prod_key
          ssh-keyscan -H "${PROD_HOST}" >> ~/.ssh/known_hosts

      - name: Build Docker image
        run: |
          set -euo pipefail
          SHA="${GITHUB_SHA}"
          docker build -f deploy/Dockerfile -t "sub2api:${SHA}" .
          docker save "sub2api:${SHA}" | gzip -9 > "sub2api-${SHA}.tar.gz"

      - name: Upload image archive
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
        run: |
          set -euo pipefail
          SHA="${GITHUB_SHA}"
          ssh -i ~/.ssh/prod_key "${PROD_SSH_USER}@${PROD_HOST}" 'sudo install -d -m 0755 /opt/sub2api/releases/images && sudo chown "$USER":"$USER" /opt/sub2api/releases/images'
          scp -i ~/.ssh/prod_key "sub2api-${SHA}.tar.gz" "${PROD_SSH_USER}@${PROD_HOST}:/opt/sub2api/releases/images/sub2api-${SHA}.tar.gz"

      - name: Deploy image on production server
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
        run: |
          set -euo pipefail
          SHA="${GITHUB_SHA}"
          ssh -i ~/.ssh/prod_key "${PROD_SSH_USER}@${PROD_HOST}" "sudo /opt/sub2api/compose/scripts/deploy-app.sh ${SHA}"

      - name: Run healthcheck
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
        run: |
          set -euo pipefail
          ssh -i ~/.ssh/prod_key "${PROD_SSH_USER}@${PROD_HOST}" 'sudo /opt/sub2api/compose/scripts/healthcheck.sh'

      - name: Confirm skipped input
        if: ${{ github.event.inputs.confirm != 'DEPLOY' }}
        run: |
          echo "Production deploy was not run because confirm was not DEPLOY."
          exit 1
```

- [ ] **Step 2: Validate YAML**

Run:

```bash
ruby -e "require 'yaml'; YAML.load_file('.github/workflows/production-deploy.yml'); puts 'valid yaml'"
```

Expected output:

```text
valid yaml
```

- [ ] **Step 3: Commit**

Run:

```bash
git add .github/workflows/production-deploy.yml
git commit -m "ci: add production deploy workflow"
```

Expected: a new commit is created.

---

### Task 11: Add Manual Production Firewall Workflow

**Files:**
- Create: `.github/workflows/production-firewall.yml`

- [ ] **Step 1: Write firewall workflow**

Create `.github/workflows/production-firewall.yml`:

```yaml
name: Production Firewall

on:
  workflow_dispatch:
    inputs:
      confirm:
        description: 'Type APPLY to update the production firewall'
        required: true
        type: string

permissions:
  contents: read

concurrency:
  group: production-firewall
  cancel-in-progress: false

jobs:
  apply-firewall:
    name: Apply UFW firewall rules
    runs-on: ubuntu-latest
    if: ${{ github.event.inputs.confirm == 'APPLY' }}
    timeout-minutes: 10
    steps:
      - name: Validate required secrets
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
          PROD_SSH_KEY: ${{ secrets.PROD_SSH_KEY }}
          ADMIN_SSH_IPS: ${{ secrets.ADMIN_SSH_IPS }}
          CN2_GATEWAY_IPS: ${{ secrets.CN2_GATEWAY_IPS }}
        run: |
          set -euo pipefail
          missing=0
          for name in PROD_HOST PROD_SSH_USER PROD_SSH_KEY ADMIN_SSH_IPS CN2_GATEWAY_IPS; do
            if [ -z "${!name}" ]; then
              echo "Missing required secret: ${name}" >&2
              missing=1
            fi
          done
          if [ "${missing}" -ne 0 ]; then
            exit 1
          fi

      - name: Prepare SSH key
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_KEY: ${{ secrets.PROD_SSH_KEY }}
        run: |
          set -euo pipefail
          install -m 700 -d ~/.ssh
          printf '%s\n' "${PROD_SSH_KEY}" > ~/.ssh/prod_key
          chmod 600 ~/.ssh/prod_key
          ssh-keyscan -H "${PROD_HOST}" >> ~/.ssh/known_hosts

      - name: Upload and apply firewall script
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
          PROD_SSH_USER: ${{ secrets.PROD_SSH_USER }}
          ADMIN_SSH_IPS: ${{ secrets.ADMIN_SSH_IPS }}
          CN2_GATEWAY_IPS: ${{ secrets.CN2_GATEWAY_IPS }}
          ALLOW_HTTP_80: ${{ secrets.ALLOW_HTTP_80 }}
        run: |
          set -euo pipefail

          cat > /tmp/apply-production-firewall.sh <<'SCRIPT'
          #!/usr/bin/env bash
          set -euo pipefail

          require_root() {
            if [ "$(id -u)" -ne 0 ]; then
              echo "This script must run as root." >&2
              exit 1
            fi
          }

          install_ufw() {
            if command -v ufw >/dev/null 2>&1; then
              return
            fi
            if command -v apt-get >/dev/null 2>&1; then
              apt-get update
              DEBIAN_FRONTEND=noninteractive apt-get install -y ufw
              return
            fi
            echo "ufw is not installed and apt-get is unavailable." >&2
            exit 1
          }

          normalize_ip_list() {
            sed 's/#.*$//' | tr -d '\r' | awk 'NF { print $1 }'
          }

          allow_ips() {
            local port="$1"
            local proto="$2"
            local label="$3"
            local ip
            while IFS= read -r ip; do
              [ -n "${ip}" ] || continue
              echo "Allowing ${label} ${ip} -> ${port}/${proto}"
              ufw allow from "${ip}" to any port "${port}" proto "${proto}"
            done
          }

          require_root
          install_ufw

          admin_ips_file="$(mktemp)"
          cn2_ips_file="$(mktemp)"
          trap 'rm -f "${admin_ips_file}" "${cn2_ips_file}"' EXIT

          printf '%s\n' "${ADMIN_SSH_IPS}" | normalize_ip_list > "${admin_ips_file}"
          printf '%s\n' "${CN2_GATEWAY_IPS}" | normalize_ip_list > "${cn2_ips_file}"

          if [ ! -s "${admin_ips_file}" ]; then
            echo "ADMIN_SSH_IPS did not contain any usable IPs." >&2
            exit 1
          fi
          if [ ! -s "${cn2_ips_file}" ]; then
            echo "CN2_GATEWAY_IPS did not contain any usable IPs." >&2
            exit 1
          fi

          ufw --force reset
          ufw default deny incoming
          ufw default allow outgoing

          allow_ips 22 tcp "admin SSH" < "${admin_ips_file}"
          allow_ips 443 tcp "CN2 gateway" < "${cn2_ips_file}"

          if [ "${ALLOW_HTTP_80:-false}" = "true" ]; then
            echo "Allowing public HTTP 80/tcp"
            ufw allow 80/tcp
          fi

          ufw deny 3000/tcp || true
          ufw deny 5432/tcp || true
          ufw deny 6379/tcp || true

          ufw --force enable
          ufw status verbose
          SCRIPT

          chmod +x /tmp/apply-production-firewall.sh
          scp -i ~/.ssh/prod_key /tmp/apply-production-firewall.sh "${PROD_SSH_USER}@${PROD_HOST}:/tmp/apply-production-firewall.sh"
          ssh -i ~/.ssh/prod_key "${PROD_SSH_USER}@${PROD_HOST}" \
            "sudo ADMIN_SSH_IPS=$(printf '%q' "${ADMIN_SSH_IPS}") CN2_GATEWAY_IPS=$(printf '%q' "${CN2_GATEWAY_IPS}") ALLOW_HTTP_80=$(printf '%q' "${ALLOW_HTTP_80:-false}") /tmp/apply-production-firewall.sh"

      - name: Confirm skipped input
        if: ${{ github.event.inputs.confirm != 'APPLY' }}
        run: |
          echo "Firewall was not changed because confirm was not APPLY."
          exit 1
```

- [ ] **Step 2: Validate YAML**

Run:

```bash
ruby -e "require 'yaml'; YAML.load_file('.github/workflows/production-firewall.yml'); puts 'valid yaml'"
```

Expected output:

```text
valid yaml
```

- [ ] **Step 3: Commit**

Run:

```bash
git add .github/workflows/production-firewall.yml
git commit -m "ci: add production firewall workflow"
```

Expected: a new commit is created.

---

### Task 12: Document Production Operations Notes

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Read existing CLAUDE.md**

Run:

```bash
grep -n '^## Branch Management\|^## Production Deployment' CLAUDE.md || true
```

Expected: existing branch management section is visible, and production deployment section may be absent.

- [ ] **Step 2: Append production deployment note**

Append this section to `CLAUDE.md`:

```markdown

## Production Deployment

Production deployment is designed but should not be implemented ad hoc. Follow `docs/superpowers/specs/2026-05-29-sub2api-production-deployment-design.md` and `docs/superpowers/plans/2026-05-29-sub2api-production-deployment.md`.

Key decisions:

- Production deploys are manually triggered from GitHub Actions, not automatic on push.
- The production origin domain is `core.we-ai.cc`.
- Production images are transferred as Docker image tar archives over SSH/SCP, not pushed to Docker Hub or GHCR.
- The origin server runs Caddy, Sub2API, Postgres, and Redis with Docker Compose.
- First-version monitoring uses Sub2API built-in operations monitoring; do not add Prometheus/Grafana/exporters unless explicitly requested later.
- Do not add custom shared-secret headers between CN2 gateways and the origin server.
```

- [ ] **Step 3: Verify note was appended**

Run:

```bash
grep -n 'Production Deployment\|core.we-ai.cc\|Docker image tar' CLAUDE.md
```

Expected: output includes the new production deployment lines.

- [ ] **Step 4: Commit**

Run:

```bash
git add CLAUDE.md
git commit -m "docs: record production deployment decisions"
```

Expected: a new commit is created.

---

### Task 13: Final Verification

**Files:**
- Verify all files from Tasks 1-12.

- [ ] **Step 1: Run shell syntax checks**

Run:

```bash
bash -n deploy/production/scripts/init-server.sh
bash -n deploy/production/scripts/deploy-app.sh
bash -n deploy/production/scripts/healthcheck.sh
```

Expected: all commands exit with status 0.

- [ ] **Step 2: Run YAML syntax checks**

Run:

```bash
ruby -e "require 'yaml'; %w[deploy/production/docker-compose.yml .github/workflows/production-init.yml .github/workflows/production-deploy.yml .github/workflows/production-firewall.yml].each { |f| YAML.load_file(f) }; puts 'valid yaml'"
```

Expected output:

```text
valid yaml
```

- [ ] **Step 3: Check for accidental secret values**

Run:

```bash
grep -RInE '(password|secret|key)=' deploy/production .github/workflows CLAUDE.md | grep -v 'change-me' | grep -v 'secrets\.' || true
```

Expected: no real secret values are printed.

- [ ] **Step 4: Review git status**

Run:

```bash
git status --short --branch
```

Expected: clean working tree on the implementation branch after all commits.

## Self-Review

- Spec coverage: This plan covers manual init, manual deploy, manual firewall, image tar transfer without registry, `core.we-ai.cc` origin Caddy, Compose-managed Sub2API/Postgres/Redis, local directory mounts under `/opt/sub2api`, first-version resource allocation, no custom origin headers, and no first-version Prometheus/Grafana/exporters.
- Placeholder scan: No TBD/TODO placeholders remain.
- Type consistency: Secret names, file paths, service names, and workflow names are consistent across tasks.
