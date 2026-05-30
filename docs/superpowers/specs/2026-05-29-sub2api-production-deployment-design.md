# Sub2API Production Deployment Design

## Purpose

This design defines the first production deployment for this forked Sub2API repository. The deployment should be manually controlled, reproducible from the repository, and optimized for a single high-spec origin server handling image-generation-heavy traffic behind multiple CN2 gateway machines.

No implementation is performed as part of this design. This document records the agreed deployment shape for a later implementation plan.

## Branch and Release Model

The repository uses three long-lived branches:

- `main` tracks the upstream project and should remain easy to sync with the official repository.
- `pre-release` is the integration branch for local changes and upstream updates.
- `release` is the production candidate branch.

Production deployment is not triggered by pushing to `release`. Deployment is manually triggered from GitHub Actions so production releases require an explicit operator action.

## Deployment Trigger

Production app deployment uses a manual GitHub Actions workflow.

The daily deployment path is:

1. Operator verifies that `release` contains the intended code.
2. Operator manually runs the production deployment workflow.
3. The workflow checks out `release`.
4. The workflow builds the Sub2API Docker image.
5. The workflow saves the image as a compressed tar archive.
6. The workflow uploads the archive to the production server over SSH/SCP.
7. The workflow runs the server-side deployment script.
8. The server loads the image, updates the running app container, and runs a health check.

The deployment must not push images to Docker Hub, GHCR, or any other registry.

## Server Topology

The production topology is:

```text
Users
  ↓ HTTPS
CN2 gateway machines running Caddy
  ↓ HTTPS over public internet
Origin server Caddy at core.we-ai.cc
  ↓ Docker network HTTP
Sub2API container
  ↓
Postgres container
Redis container
```

The origin server is expected to have at least:

```text
CPU: Dual Intel Xeon Gold 6138 @ 2.00 GHz, 40 cores / 80 threads
Memory: 256 GB RAM
Disk: 2 x 2 TB U.2 NVMe
Network: 10 Gbps, 330 TB transfer
Location: Los Angeles, CA, USA
```

The origin domain is:

```text
core.we-ai.cc
```

`core.we-ai.cc` is a source/origin domain for CN2 gateways. It is not intended as the public user-facing API domain.

## Origin Server Networking

The origin server should run Caddy even though CN2 gateways also run Caddy.

Reasons:

- CN2-to-origin traffic crosses the public internet and should use HTTPS.
- Sub2API should not expose its container port directly to the public internet.
- The origin server can enforce Host-based routing.
- The origin server can later support safer cutover patterns without changing the gateway layer.

No custom shared-secret request header should be added between gateways and origin because extra headers may affect API behavior.

The first version uses:

- HTTPS from CN2 gateways to `core.we-ai.cc`.
- Firewall allowlist for CN2 gateway IPs on port 443.
- Caddy Host matching for `core.we-ai.cc`.
- Docker internal HTTP from Caddy to Sub2API.

## Firewall Model

Firewall configuration is managed by a separate manual GitHub Actions workflow.

The workflow reads allowlists from GitHub Secrets:

- `CN2_GATEWAY_IPS`: newline-separated CN2 gateway IPs that may access origin `443/tcp`.
- `ADMIN_SSH_IPS`: newline-separated admin IPs that may access origin `22/tcp`.

The firewall policy should be:

```text
Default incoming: deny
Default outgoing: allow
22/tcp: allow only from ADMIN_SSH_IPS
443/tcp: allow only from CN2_GATEWAY_IPS
80/tcp: optional for Caddy HTTP-01 certificate issuance
3000/tcp: denied publicly
5432/tcp: denied publicly
6379/tcp: denied publicly
```

The implementation should avoid locking out SSH by requiring at least one usable `ADMIN_SSH_IPS` entry before applying rules.

## Production Directory Layout

The origin server uses local directory mounts for data and configuration.

```text
/opt/sub2api/
  compose/
    docker-compose.yml
    Caddyfile
    .env.production
    postgres/
      postgresql.conf
      pg_hba.conf
    redis/
      redis.conf

  data/
    postgres/
    redis/
    app/
    caddy_data/
    caddy_config/

  releases/
    images/
      sub2api-<sha>.tar.gz
    current

  backups/
    postgres/
    redis/
    app/

  logs/
```

The repository should contain templates and scripts under:

```text
deploy/production/
  docker-compose.yml
  Caddyfile
  env.example
  postgres/
    postgresql.conf
    pg_hba.conf.example
  redis/
    redis.conf
  scripts/
    init-server.sh
    deploy-app.sh
    healthcheck.sh
```

Real secrets and production credentials must not be committed to tracked files. Production secrets should be stored in GitHub Secrets and written into `/opt/sub2api/compose/.env.production` during initialization.

## Compose Services

The production Compose stack should include:

- `caddy`
- `sub2api`
- `postgres`
- `redis`

The first production version should not include Prometheus, Grafana, node_exporter, postgres_exporter, redis_exporter, or cAdvisor.

## Resource Allocation

The initial resource allocation is tuned for image-generation-heavy traffic on the 256 GB / 80-thread origin server and a target around 2000 concurrent image-generation requests.

Sub2API:

```text
Memory limit: 128 GB
GOMAXPROCS: 64
Image concurrency limiter: enabled
Image max concurrent requests: 2000
Image overflow mode: wait
Image max waiting requests: 300
Image wait timeout: 30 seconds
OpenAI WS load-balancing top K: 1000
Sticky-session max waiting: 3
Sticky-session wait timeout: 15 seconds
```

Postgres:

```text
shared_buffers: 32 GB
effective_cache_size: 160 GB
max_connections: 500
work_mem: 32 MB
maintenance_work_mem: 4 GB
max_wal_size: 64 GB
```

Redis:

```text
maxmemory: 32 GB
maxmemory-policy: allkeys-lru
appendonly: yes
appendfsync: everysec
```

System reserve:

```text
At least 50 GB for Linux page cache, Docker overhead, network buffers, deployment spikes, logs, and operational headroom.
```

Storage layout:

```text
Prefer placing Postgres data and WAL on one NVMe-backed path, while Docker images, image tar archives, app data, Redis data, logs, and backups use the other NVMe-backed path. If the server is initially provisioned as one logical volume, keep this split as a second-stage operational improvement.
```

The initial allocation should be revisited after observing real production traffic for 24 to 48 hours.

## Built-In Monitoring First

Sub2API already includes application operations monitoring. The first production deployment should rely on that built-in monitoring instead of adding external monitoring components.

The built-in monitoring is expected to cover:

- request volume
- success and error rates
- latency percentiles
- TTFT
- upstream error categories
- CPU and memory snapshots
- goroutine count
- queue depth
- DB and Redis ping status
- DB and Redis pool status
- background job heartbeats
- system and error logs
- channel/provider availability

Known gaps for a later monitoring phase:

- disk I/O and filesystem capacity trends
- host network throughput
- file descriptor pressure
- Postgres cache hit ratio, locks, slow queries, and vacuum behavior
- Redis evictions, hit rate, and fragmentation
- Docker container-level historical metrics

The first version should include lightweight operational commands in documentation or scripts, but should not deploy a monitoring stack.

## GitHub Secrets

Deployment-related workflows should use GitHub Secrets for server access and production configuration.

Expected secrets include:

```text
PROD_HOST
PROD_SSH_USER
PROD_SSH_KEY
CN2_GATEWAY_IPS
ADMIN_SSH_IPS
ALLOW_HTTP_80
POSTGRES_PASSWORD
REDIS_PASSWORD
JWT_SECRET
TOTP_ENCRYPTION_KEY
```

`TOTP_ENCRYPTION_KEY` must be a strong random value, for example from `openssl rand -base64 32`, and should remain stable after production users enable TOTP.

Production initialization should generate `/opt/sub2api/compose/.env.production` from the repository template plus GitHub Secrets. Real secrets must not be committed to tracked files.

## Deployment Workflows

The implementation should create three manual workflows:

1. `production-init.yml`
   - Prepares `/opt/sub2api`.
   - Uploads production Compose, Caddy, Postgres, Redis, and script templates.
   - Starts or updates the base production stack.
   - Must not delete existing data directories.

2. `production-deploy.yml`
   - Builds the Sub2API image from `release`.
   - Saves it as `sub2api-<sha>.tar.gz`.
   - Uploads it to `/opt/sub2api/releases/images/`.
   - Runs `deploy-app.sh <sha>` on the server.
   - Runs a health check.
   - Keeps a small number of recent image archives for rollback.

3. `production-firewall.yml`
   - Applies UFW firewall rules from `CN2_GATEWAY_IPS` and `ADMIN_SSH_IPS`.
   - Keeps database, Redis, and app ports closed to the public internet.

## Safety Constraints

The implementation must follow these safety rules:

- No destructive reset of `/opt/sub2api/data`.
- No production secrets committed to the repository.
- No image push to external registries.
- No custom origin-auth request header between CN2 gateways and origin.
- No automatic deployment on push.
- No external monitoring stack in the first version.
- Firewall changes must be manually triggered and require explicit confirmation.
- Production database and Redis ports must not be publicly exposed.

## Open Operational Follow-Ups

These are intentionally deferred until after first production deployment:

- backup and restore automation
- rollback workflow
- external monitoring stack
- alerting
- load testing procedure
- performance tuning after real traffic
