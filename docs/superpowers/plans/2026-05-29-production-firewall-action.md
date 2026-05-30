# Production Firewall Action Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manually triggered GitHub Actions workflow that configures the production server UFW firewall from repository secrets.

**Architecture:** The workflow SSHes into the production server, uploads a generated shell script, and applies an allowlist-based UFW policy. CN2 gateway IPs and admin SSH IPs come from multiline GitHub Secrets so firewall changes can be managed without code changes.

**Tech Stack:** GitHub Actions, Bash, SSH, UFW.

---

## File Structure

- Create: `.github/workflows/production-firewall.yml`
  - Manual `workflow_dispatch` action.
  - Validates required secrets.
  - Connects to the production server over SSH.
  - Applies UFW defaults and allow rules from multiline secrets.
  - Prints final firewall status.

No production credentials or real IP addresses go in the repository. The workflow expects these repository secrets:

- `PROD_HOST`: production server hostname or IP.
- `PROD_SSH_USER`: SSH user.
- `PROD_SSH_KEY`: private SSH key for that user.
- `ADMIN_SSH_IPS`: newline-separated admin IPs allowed to access port 22.
- `CN2_GATEWAY_IPS`: newline-separated CN2 gateway IPs allowed to access port 443.

Optional secret:

- `ALLOW_HTTP_80`: set to `true` to allow public inbound `80/tcp` for Caddy HTTP-01 certificate issuance. Any other value keeps port 80 closed.

### Task 1: Add Manual Firewall Workflow

**Files:**
- Create: `.github/workflows/production-firewall.yml`

- [ ] **Step 1: Create the workflow file**

Write `.github/workflows/production-firewall.yml` with this content:

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
          PROD_SSH_KEY: ${{ secrets.PROD_SSH_KEY }}
        run: |
          set -euo pipefail
          install -m 700 -d ~/.ssh
          printf '%s\n' "${PROD_SSH_KEY}" > ~/.ssh/prod_key
          chmod 600 ~/.ssh/prod_key

      - name: Add production host key
        env:
          PROD_HOST: ${{ secrets.PROD_HOST }}
        run: |
          set -euo pipefail
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

- [ ] **Step 2: Run YAML syntax check locally**

Run:

```bash
ruby -e "require 'yaml'; YAML.load_file('.github/workflows/production-firewall.yml'); puts 'valid yaml'"
```

Expected output:

```text
valid yaml
```

- [ ] **Step 3: Inspect the workflow for secret leakage**

Run:

```bash
grep -nE 'PROD_SSH_KEY|ADMIN_SSH_IPS|CN2_GATEWAY_IPS|PROD_HOST|PROD_SSH_USER' .github/workflows/production-firewall.yml
```

Expected: references only use `${{ secrets.* }}` or environment variables. No real secret values or real IP addresses should appear.

- [ ] **Step 4: Verify git diff**

Run:

```bash
git diff -- .github/workflows/production-firewall.yml
```

Expected: only the new workflow file is shown.

- [ ] **Step 5: Commit**

Run:

```bash
git add .github/workflows/production-firewall.yml
git commit -m "ci: add production firewall workflow"
```

Expected: a new commit is created.

## Self-Review

- Spec coverage: The plan adds a manual Action, reads CN2 gateway IPs from a multiline secret, reads admin SSH IPs from a multiline secret to avoid lockout, applies UFW allowlist rules, keeps database/Redis/app ports closed, and supports optional HTTP 80 for Caddy.
- Placeholder scan: No TBD/TODO placeholders remain.
- Type consistency: Secret names and workflow environment variable names are consistent throughout the plan.
