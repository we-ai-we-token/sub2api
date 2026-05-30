# Production Operations Guide Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a practical production operations guide that explains how to use the manual GitHub Actions for init, deploy, and firewall operations.

**Architecture:** Create one focused Markdown guide under `docs/superpowers/` that complements the existing production secrets document. The guide will be operator-facing: prerequisites, exact Action inputs, expected results, verification commands, and failure triage.

**Tech Stack:** Markdown, GitHub Actions, Docker Compose, SSH/SCP, Bash, UFW.

---

## File Structure

- Create: `docs/superpowers/production-operations.md`
  - Operator handbook for using `Production Init`, `Production Deploy`, and `Production Firewall` Actions.
  - Includes first-time setup, routine deploys, verification commands, safe manual recovery, and known gaps.

---

### Task 1: Add Production Actions Operations Guide

**Files:**
- Create: `docs/superpowers/production-operations.md`

- [ ] **Step 1: Write the operations guide**

Create `docs/superpowers/production-operations.md` with this content:

```markdown
# Production Operations Guide

This guide explains how to operate the repository-managed production deployment from GitHub Actions.

The production origin runs from the `release` branch and uses files under `deploy/production/`. The server-side root is `/opt/sub2api`.

Do not commit real production secrets. Do not delete `/opt/sub2api/data` during normal operations.

## Related Files

- GitHub Secrets reference: `docs/superpowers/production-github-secrets.md`
- Production Compose template: `deploy/production/docker-compose.yml`
- Production env template: `deploy/production/env.example`
- Init script: `deploy/production/scripts/init-server.sh`
- Deploy script: `deploy/production/scripts/deploy-app.sh`
- Healthcheck script: `deploy/production/scripts/healthcheck.sh`
- Init workflow: `.github/workflows/production-init.yml`
- Deploy workflow: `.github/workflows/production-deploy.yml`
- Firewall workflow: `.github/workflows/production-firewall.yml`

## GitHub Actions Overview

| Action | Confirm input | Purpose | Typical frequency |
| --- | --- | --- | --- |
| `Production Init` | `INIT` | Sync production templates, prepare `/opt/sub2api`, write secrets, start base services | First setup and template syncs |
| `Production Deploy` | `DEPLOY` | Build image from `release`, upload tar archive, restart `sub2api`, run healthcheck | Every release deploy |
| `Production Firewall` | `APPLY` | Apply UFW allowlist for SSH and CN2 gateway access | After IP allowlist changes |

All three workflows are manual. They do not run on push.

## Required GitHub Secrets

Configure repository secrets in GitHub:

```text
Settings → Secrets and variables → Actions → Repository secrets
```

Required for server access:

```text
PROD_HOST
PROD_SSH_USER
PROD_SSH_KEY
```

Required for `Production Init`:

```text
POSTGRES_PASSWORD
REDIS_PASSWORD
JWT_SECRET
TOTP_ENCRYPTION_KEY
```

Required for `Production Firewall`:

```text
ADMIN_SSH_IPS
CN2_GATEWAY_IPS
```

Optional for `Production Firewall`:

```text
ALLOW_HTTP_80
```

Use `ALLOW_HTTP_80=true` only when the origin Caddy needs public HTTP-01 certificate issuance on `80/tcp`.

## Before Running Any Action

Make sure the local `release` branch has been pushed to GitHub:

```bash
git push origin release
```

The workflows checkout `release`, so unpushed local commits are invisible to GitHub Actions.

## Action 1: Production Init

Use this Action to initialize or resync the production server templates.

### When to run

Run `Production Init` when:

- setting up the server for the first time;
- updating production Compose, Caddy, Postgres, Redis, or script templates;
- refreshing `.env.production` secret values from GitHub Secrets.

It should not delete existing data directories.

### How to run

In GitHub:

```text
Actions → Production Init → Run workflow
```

Select branch:

```text
release
```

Enter:

```text
confirm = INIT
```

Start the workflow.

### What it does

The workflow:

1. checks out `release`;
2. validates required server and app secrets;
3. prepares the SSH key;
4. uploads `deploy/production` to `/tmp/sub2api-production` on the server;
5. runs:

```bash
sudo /tmp/sub2api-production/production/scripts/init-server.sh /tmp/sub2api-production/production
```

6. writes these secret-backed values into `/opt/sub2api/compose/.env.production`:

```text
POSTGRES_PASSWORD
DATABASE_PASSWORD
REDIS_PASSWORD
JWT_SECRET
TOTP_ENCRYPTION_KEY
```

7. starts or updates the base services:

```text
postgres
redis
caddy
```

### Verify init

SSH to the production server:

```bash
ssh <PROD_SSH_USER>@<PROD_HOST>
```

Check files:

```bash
sudo ls -la /opt/sub2api
sudo ls -la /opt/sub2api/compose
sudo ls -la /opt/sub2api/compose/scripts
```

Check services:

```bash
cd /opt/sub2api/compose
sudo docker compose ps
```

Expected: `postgres`, `redis`, and `caddy` are present. The `sub2api` service may not be healthy until `Production Deploy` uploads an app image.

Check selected non-secret env values:

```bash
sudo grep -E '^(ORIGIN_DOMAIN|SUB2API_IMAGE|SERVER_MODE|GIN_MODE)=' /opt/sub2api/compose/.env.production
```

Do not print or paste full `.env.production` output because it contains secrets.

## Action 2: Production Deploy

Use this Action to deploy the current `release` branch to production.

### When to run

Run `Production Deploy` after:

- merging verified changes into `release`;
- pushing `release` to GitHub;
- confirming production secrets and init are already in place.

### How to run

In GitHub:

```text
Actions → Production Deploy → Run workflow
```

Select branch:

```text
release
```

Enter:

```text
confirm = DEPLOY
```

Start the workflow.

### What it does

The workflow:

1. checks out `release`;
2. validates SSH secrets;
3. builds the Docker image:

```text
sub2api:<GITHUB_SHA>
```

4. saves and compresses it as:

```text
sub2api-<GITHUB_SHA>.tar.gz
```

5. uploads it to:

```text
/opt/sub2api/releases/images/sub2api-<GITHUB_SHA>.tar.gz
```

6. runs:

```bash
sudo /opt/sub2api/compose/scripts/deploy-app.sh <GITHUB_SHA>
```

7. updates `/opt/sub2api/compose/.env.production` so `SUB2API_IMAGE=sub2api:<GITHUB_SHA>`;
8. writes the deployed SHA to `/opt/sub2api/releases/current`;
9. restarts only the `sub2api` service;
10. runs:

```bash
sudo /opt/sub2api/compose/scripts/healthcheck.sh
```

### Verify deploy

On the server:

```bash
cd /opt/sub2api/compose
sudo docker compose ps
```

Check health:

```bash
sudo /opt/sub2api/compose/scripts/healthcheck.sh
```

Check deployed SHA:

```bash
sudo cat /opt/sub2api/releases/current
```

Check the configured image:

```bash
sudo grep '^SUB2API_IMAGE=' /opt/sub2api/compose/.env.production
```

Check logs:

```bash
cd /opt/sub2api/compose
sudo docker compose logs --tail=100 sub2api
sudo docker compose logs --tail=100 caddy
```

Check uploaded image archives:

```bash
sudo ls -lh /opt/sub2api/releases/images
```

## Action 3: Production Firewall

Use this Action to apply the production UFW allowlist.

### When to run

Run `Production Firewall` when:

- setting firewall rules for the first time;
- changing admin SSH IPs;
- changing CN2 gateway IPs;
- intentionally changing whether public `80/tcp` is open.

### Lockout warning

Before running this Action, confirm `ADMIN_SSH_IPS` contains your current admin egress IP.

If `ADMIN_SSH_IPS` is wrong, the workflow can remove your SSH access.

### How to run

In GitHub:

```text
Actions → Production Firewall → Run workflow
```

Select branch:

```text
release
```

Enter:

```text
confirm = APPLY
```

Start the workflow.

### What it does

The workflow:

1. validates SSH and firewall secrets;
2. uploads a temporary UFW script to the production server;
3. installs `ufw` if missing on an apt-based host;
4. resets UFW rules;
5. sets default incoming policy to deny;
6. sets default outgoing policy to allow;
7. allows `ADMIN_SSH_IPS` to `22/tcp`;
8. allows `CN2_GATEWAY_IPS` to `443/tcp`;
9. allows public `80/tcp` only when `ALLOW_HTTP_80=true`;
10. denies public `3000/tcp`, `5432/tcp`, and `6379/tcp`;
11. enables UFW and prints final status.

### Verify firewall

On the server:

```bash
sudo ufw status verbose
```

Expected:

- `22/tcp` allowed only from admin IPs;
- `443/tcp` allowed only from CN2 gateway IPs;
- `80/tcp` allowed only if `ALLOW_HTTP_80=true`;
- `3000/tcp`, `5432/tcp`, and `6379/tcp` not publicly open.

Also verify SSH from an allowed admin IP before ending the maintenance window.

## Routine Operations

### Check all services

```bash
cd /opt/sub2api/compose
sudo docker compose ps
```

### Run healthcheck

```bash
sudo /opt/sub2api/compose/scripts/healthcheck.sh
```

### View recent logs

```bash
cd /opt/sub2api/compose
sudo docker compose logs --tail=100 sub2api
sudo docker compose logs --tail=100 caddy
sudo docker compose logs --tail=100 postgres
sudo docker compose logs --tail=100 redis
```

### Follow app logs

```bash
cd /opt/sub2api/compose
sudo docker compose logs -f sub2api
```

### Check disk usage

```bash
sudo df -h
sudo du -sh /opt/sub2api/data/* /opt/sub2api/releases/images /opt/sub2api/logs 2>/dev/null
```

### Check current deployment

```bash
sudo cat /opt/sub2api/releases/current
sudo grep '^SUB2API_IMAGE=' /opt/sub2api/compose/.env.production
```

## Manual Recovery Before Rollback Workflow Exists

There is no rollback workflow yet. The deploy script keeps recent image archives and Docker images, so a manual rollback can use a previously deployed SHA if it is still present on the server.

List known image archives:

```bash
sudo ls -lh /opt/sub2api/releases/images
```

List local Docker images:

```bash
sudo docker images 'sub2api'
```

Rollback to an already-loaded image tag:

```bash
cd /opt/sub2api/compose
sudo sed -i.bak 's|^SUB2API_IMAGE=.*|SUB2API_IMAGE=sub2api:<previous-sha>|' .env.production
sudo docker compose up -d sub2api
sudo /opt/sub2api/compose/scripts/healthcheck.sh
```

If the image is archived but not loaded, load it first:

```bash
sudo gzip -dc /opt/sub2api/releases/images/sub2api-<previous-sha>.tar.gz | sudo docker load
```

Then update `SUB2API_IMAGE` and restart `sub2api`.

Do not remove `/opt/sub2api/data` during rollback.

## Failure Triage

### Workflow cannot SSH

Check:

- `PROD_HOST` is reachable from GitHub-hosted runners;
- `PROD_SSH_USER` is correct;
- `PROD_SSH_KEY` is the private key for that user;
- the server accepts that key;
- firewall still allows GitHub runner access if the workflow depends on direct SSH.

### Production Init fails during Docker install

The init script installs Docker only on apt-based hosts. If the host is not Debian/Ubuntu-compatible, install Docker manually and rerun `Production Init`.

### Production Deploy fails healthcheck

Check service status and logs:

```bash
cd /opt/sub2api/compose
sudo docker compose ps
sudo docker compose logs --tail=200 sub2api
sudo docker compose logs --tail=100 postgres
sudo docker compose logs --tail=100 redis
```

Check env wiring without printing secrets:

```bash
sudo grep -E '^(SUB2API_IMAGE|DATABASE_HOST|DATABASE_PORT|DATABASE_NAME|DATABASE_USER|REDIS_HOST|REDIS_PORT|REDIS_DB)=' /opt/sub2api/compose/.env.production
```

### Firewall workflow risks lockout

Before applying firewall changes, open a second SSH session from an allowed admin IP. Keep it open until `sudo ufw status verbose` confirms the expected rules.

## Known Gaps

The first production version does not yet include:

- automated backup and restore;
- rollback workflow;
- external monitoring and alerting;
- load testing procedure;
- performance tuning after real production traffic.
```

- [ ] **Step 2: Validate the guide has all Action names and confirm inputs**

Run:

```bash
grep -nE 'Production Init|Production Deploy|Production Firewall|confirm = INIT|confirm = DEPLOY|confirm = APPLY' docs/superpowers/production-operations.md
```

Expected: output includes all three workflow names and all three confirm inputs.

- [ ] **Step 3: Validate the guide references the existing secrets document**

Run:

```bash
grep -n 'production-github-secrets.md' docs/superpowers/production-operations.md
```

Expected: output includes the related file reference.

- [ ] **Step 4: Check for placeholder text**

Run:

```bash
grep -nE 'TBD|TODO|fill in|placeholder' docs/superpowers/production-operations.md || true
```

Expected: no output.

- [ ] **Step 5: Commit**

Run:

```bash
git add docs/superpowers/production-operations.md
git commit -m "docs: add production operations guide"
```

Expected: a new commit is created.

## Self-Review

- Spec coverage: The plan creates an operator-facing guide focused on using the three manual GitHub Actions, their confirm inputs, prerequisites, verification steps, failure triage, and known gaps.
- Placeholder scan: No TBD/TODO/fill-in-later placeholders remain. Literal `<PROD_SSH_USER>`, `<PROD_HOST>`, and `<previous-sha>` examples are intentional operator substitution markers.
- Type consistency: Workflow names, confirm inputs, secret names, and file paths match the current production deployment files.
