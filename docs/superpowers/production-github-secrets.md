# Production GitHub Secrets

This repository's production deployment workflows expect the following GitHub Actions secrets.

Configure them in:

```text
GitHub Repository → Settings → Secrets and variables → Actions → Repository secrets
```

Do not commit real secret values into git-tracked files.

## Server Access

### `PROD_HOST`

Production origin server hostname or IP address.

Example value:

```text
core.we-ai.cc
```

Used by GitHub Actions to SSH/SCP into the production server.

### `PROD_SSH_USER`

Linux user used by GitHub Actions when connecting to the production server.

Example value:

```text
deploy
```

This user must be able to run the deployment scripts through `sudo`.

### `PROD_SSH_KEY`

Private SSH key for `PROD_SSH_USER`.

Example value format:

```text
-----BEGIN OPENSSH PRIVATE KEY-----
...
-----END OPENSSH PRIVATE KEY-----
```

Used by GitHub Actions to authenticate to the production server. Store the private key only as a GitHub Secret.

## Firewall Allowlists

### `ADMIN_SSH_IPS`

Newline-separated admin IP addresses allowed to access `22/tcp` on the production server.

Example value:

```text
203.0.113.10
198.51.100.20
```

The firewall workflow requires at least one usable entry so it does not lock out SSH access.

### `CN2_GATEWAY_IPS`

Newline-separated CN2 gateway machine IP addresses allowed to access `443/tcp` on the production server.

Example value:

```text
203.0.113.30
203.0.113.31
2001:db8::30
```

Only these gateways should be able to reach the origin Caddy service at `core.we-ai.cc`.

### `ALLOW_HTTP_80`

Optional flag controlling whether the firewall workflow allows public inbound `80/tcp`.

Recommended first value:

```text
true
```

Use `true` if Caddy needs HTTP-01 certificate issuance for `core.we-ai.cc`. Set to `false` or leave unset if certificate issuance does not require port 80.

## Application Secrets

### `POSTGRES_PASSWORD`

Password for the production Postgres `sub2api` user.

Generate a strong random value, for example:

```bash
openssl rand -base64 32
```

Used by both the Postgres container and Sub2API database connection settings.

### `REDIS_PASSWORD`

Password required by the production Redis instance.

Generate a strong random value, for example:

```bash
openssl rand -base64 32
```

Used by both the Redis container and Sub2API Redis connection settings.

### `JWT_SECRET`

Secret used by Sub2API to sign and verify JWT tokens.

Generate a strong random value, for example:

```bash
openssl rand -base64 32
```

Changing this value after production use can invalidate existing sessions or tokens.

### `TOTP_ENCRYPTION_KEY`

Application key used to encrypt stored TOTP two-factor-authentication secrets.

Generate a strong random value, for example:

```bash
openssl rand -base64 32
```

Keep this value stable after production users enable TOTP. Changing it can make existing TOTP secrets impossible to decrypt.

## Current Deployment Decisions

The production origin domain is:

```text
core.we-ai.cc
```

The production deployment should:

- use manual GitHub Actions workflows;
- build a Docker image tar and upload it to the server over SSH/SCP;
- avoid Docker Hub, GHCR, or other external image registries;
- run Caddy, Sub2API, Postgres, and Redis on the origin server;
- allow CN2 gateways to reach only origin `443/tcp`;
- keep Sub2API `3000/tcp`, Postgres `5432/tcp`, and Redis `6379/tcp` closed to the public internet.
