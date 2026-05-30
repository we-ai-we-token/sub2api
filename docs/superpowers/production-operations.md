# 生产运维操作指南

这份文档说明怎么通过 GitHub Actions 操作仓库内管理的生产部署。

生产 origin 使用 `release` 分支，部署模板在 `deploy/production/`。服务器上的根目录是 `/opt/sub2api`。

不要把真实生产密钥提交到仓库。正常运维时不要删除 `/opt/sub2api/data`。

## 相关文件

- GitHub Secrets 说明：`docs/superpowers/production-github-secrets.md`
- 生产 Compose 模板：`deploy/production/docker-compose.yml`
- 生产环境变量模板：`deploy/production/env.example`
- 初始化脚本：`deploy/production/scripts/init-server.sh`
- 部署脚本：`deploy/production/scripts/deploy-app.sh`
- 健康检查脚本：`deploy/production/scripts/healthcheck.sh`
- 初始化 workflow：`.github/workflows/production-init.yml`
- 部署 workflow：`.github/workflows/production-deploy.yml`
- 防火墙 workflow：`.github/workflows/production-firewall.yml`

## 三个 GitHub Actions 是干什么的

| Action | confirm 输入 | 用途 | 什么时候用 |
| --- | --- | --- | --- |
| `Production Init` | `INIT` | 同步生产模板、准备 `/opt/sub2api`、写入 secrets、启动基础服务 | 第一次初始化；模板或 secrets 变更后 |
| `Production Deploy` | `DEPLOY` | 从 `release` 构建镜像、上传 tar 包、重启 `sub2api`、跑健康检查 | 每次正式发版 |
| `Production Firewall` | `APPLY` | 按 allowlist 应用 UFW 防火墙规则 | 首次开防火墙；SSH/CN2 IP 变更后 |

这三个 workflow 都是手动触发，不会因为 push 自动部署。

## 需要先配置的 GitHub Secrets

进入 GitHub 仓库：

```text
Settings → Secrets and variables → Actions → Repository secrets
```

所有 workflow 都需要服务器登录信息：

```text
PROD_HOST
PROD_SSH_USER
PROD_SSH_KEY
```

`Production Init` 还需要应用密钥：

```text
POSTGRES_PASSWORD
REDIS_PASSWORD
JWT_SECRET
TOTP_ENCRYPTION_KEY
```

`Production Firewall` 还需要 IP 白名单：

```text
ADMIN_SSH_IPS
CN2_GATEWAY_IPS
```

`Production Firewall` 可选：

```text
ALLOW_HTTP_80
```

只有 origin Caddy 需要公网 `80/tcp` 做 HTTP-01 证书签发时，才设置：

```text
ALLOW_HTTP_80=true
```

否则不设置，或设置成 `false`。

## 跑任何 Action 前先确认

本地 `release` 分支必须先推到 GitHub：

```bash
git push origin release
```

这些 workflow 都会 checkout GitHub 上的 `release`。如果本地提交没 push，GitHub Actions 看不到。

## Action 1：Production Init

`Production Init` 用来初始化服务器，或者把生产模板重新同步到服务器。

### 什么时候跑

这些情况跑 `Production Init`：

- 第一次搭生产服务器；
- `deploy/production` 下的 Compose、Caddy、Postgres、Redis、脚本模板有变更；
- GitHub Secrets 里的生产密钥变更后，需要刷新 `/opt/sub2api/compose/.env.production`。

这个 Action 不应该删除已有数据目录。

### 怎么跑

在 GitHub 页面进入：

```text
Actions → Production Init → Run workflow
```

Branch 选：

```text
release
```

输入：

```text
confirm = INIT
```

然后点击运行。

### 它会做什么

这个 workflow 会：

1. checkout `release`；
2. 检查服务器登录 secrets 和应用 secrets 是否都配置了；
3. 准备 SSH key；
4. 把 `deploy/production` 上传到服务器的 `/tmp/sub2api-production`；
5. 在服务器执行：

```bash
sudo /tmp/sub2api-production/production/scripts/init-server.sh /tmp/sub2api-production/production
```

6. 把这些 GitHub Secrets 写入 `/opt/sub2api/compose/.env.production`：

```text
POSTGRES_PASSWORD
DATABASE_PASSWORD
REDIS_PASSWORD
JWT_SECRET
TOTP_ENCRYPTION_KEY
```

7. 启动或更新基础服务：

```text
postgres
redis
caddy
```

### 怎么确认初始化成功

登录服务器：

```bash
ssh <PROD_SSH_USER>@<PROD_HOST>
```

检查目录和脚本是否存在：

```bash
sudo ls -la /opt/sub2api
sudo ls -la /opt/sub2api/compose
sudo ls -la /opt/sub2api/compose/scripts
```

检查服务：

```bash
cd /opt/sub2api/compose
sudo docker compose ps
```

预期：能看到 `postgres`、`redis`、`caddy`。`sub2api` 可能要等 `Production Deploy` 上传应用镜像后才会正常。

只检查非敏感 env 项：

```bash
sudo grep -E '^(ORIGIN_DOMAIN|SUB2API_IMAGE|SERVER_MODE|GIN_MODE)=' /opt/sub2api/compose/.env.production
```

不要把完整 `.env.production` 打印出来或贴出来，里面有密钥。

## Action 2：Production Deploy

`Production Deploy` 用来把当前 GitHub 上的 `release` 部署到生产。

### 什么时候跑

这些情况跑 `Production Deploy`：

- 已经把验证过的变更合并到 `release`；
- 已经 `git push origin release`；
- `Production Init` 已经跑过，生产服务器上有 `/opt/sub2api/compose/.env.production`。

### 怎么跑

在 GitHub 页面进入：

```text
Actions → Production Deploy → Run workflow
```

Branch 选：

```text
release
```

输入：

```text
confirm = DEPLOY
```

然后点击运行。

### 它会做什么

这个 workflow 会：

1. checkout `release`；
2. 检查 SSH secrets；
3. 构建 Docker 镜像：

```text
sub2api:<GITHUB_SHA>
```

4. 保存并压缩成：

```text
sub2api-<GITHUB_SHA>.tar.gz
```

5. 上传到服务器：

```text
/opt/sub2api/releases/images/sub2api-<GITHUB_SHA>.tar.gz
```

6. 在服务器执行：

```bash
sudo /opt/sub2api/compose/scripts/deploy-app.sh <GITHUB_SHA>
```

7. 更新 `/opt/sub2api/compose/.env.production`：

```text
SUB2API_IMAGE=sub2api:<GITHUB_SHA>
```

8. 把当前部署 SHA 写入：

```text
/opt/sub2api/releases/current
```

9. 只重启 `sub2api` 服务；
10. 执行健康检查：

```bash
sudo /opt/sub2api/compose/scripts/healthcheck.sh
```

### 怎么确认部署成功

在服务器上检查服务：

```bash
cd /opt/sub2api/compose
sudo docker compose ps
```

跑健康检查：

```bash
sudo /opt/sub2api/compose/scripts/healthcheck.sh
```

看当前部署的 SHA：

```bash
sudo cat /opt/sub2api/releases/current
```

看当前配置的镜像：

```bash
sudo grep '^SUB2API_IMAGE=' /opt/sub2api/compose/.env.production
```

看日志：

```bash
cd /opt/sub2api/compose
sudo docker compose logs --tail=100 sub2api
sudo docker compose logs --tail=100 caddy
```

看已经上传的镜像包：

```bash
sudo ls -lh /opt/sub2api/releases/images
```

## Action 3：Production Firewall

`Production Firewall` 用来应用生产 UFW 防火墙白名单。

### 什么时候跑

这些情况跑 `Production Firewall`：

- 第一次开启生产防火墙；
- 管理员 SSH IP 变了；
- CN2 网关 IP 变了；
- 需要改变是否开放公网 `80/tcp`。

### 防锁死提醒

跑这个 Action 前，先确认 `ADMIN_SSH_IPS` 包含你当前的出口 IP。

如果 `ADMIN_SSH_IPS` 配错，workflow 可能会把你的 SSH 访问关掉。

### 怎么跑

在 GitHub 页面进入：

```text
Actions → Production Firewall → Run workflow
```

Branch 选：

```text
release
```

输入：

```text
confirm = APPLY
```

然后点击运行。

### 它会做什么

这个 workflow 会：

1. 检查 SSH 和防火墙 secrets；
2. 上传一个临时 UFW 脚本到生产服务器；
3. 如果是 apt 系系统且没装 `ufw`，自动安装；
4. reset UFW 规则；
5. 设置默认入站 deny；
6. 设置默认出站 allow；
7. 允许 `ADMIN_SSH_IPS` 访问 `22/tcp`；
8. 允许 `CN2_GATEWAY_IPS` 访问 `443/tcp`；
9. 只有 `ALLOW_HTTP_80=true` 时才开放公网 `80/tcp`；
10. deny 公网 `3000/tcp`、`5432/tcp`、`6379/tcp`；
11. enable UFW 并打印最终状态。

### 怎么确认防火墙成功

在服务器上执行：

```bash
sudo ufw status verbose
```

预期：

- `22/tcp` 只允许管理员 IP；
- `443/tcp` 只允许 CN2 网关 IP；
- `80/tcp` 只有 `ALLOW_HTTP_80=true` 时开放；
- `3000/tcp`、`5432/tcp`、`6379/tcp` 没有公网开放。

维护窗口结束前，再从允许的管理员 IP 新开一个 SSH 连接确认没锁死。

## 日常检查命令

### 看所有服务

```bash
cd /opt/sub2api/compose
sudo docker compose ps
```

### 跑健康检查

```bash
sudo /opt/sub2api/compose/scripts/healthcheck.sh
```

### 看最近日志

```bash
cd /opt/sub2api/compose
sudo docker compose logs --tail=100 sub2api
sudo docker compose logs --tail=100 caddy
sudo docker compose logs --tail=100 postgres
sudo docker compose logs --tail=100 redis
```

### 实时跟 app 日志

```bash
cd /opt/sub2api/compose
sudo docker compose logs -f sub2api
```

### 看磁盘使用

```bash
sudo df -h
sudo du -sh /opt/sub2api/data/* /opt/sub2api/releases/images /opt/sub2api/logs 2>/dev/null
```

### 看当前部署版本

```bash
sudo cat /opt/sub2api/releases/current
sudo grep '^SUB2API_IMAGE=' /opt/sub2api/compose/.env.production
```

## 还没有 rollback workflow 时的手动恢复

现在还没有 rollback workflow。`deploy-app.sh` 会保留最近几个镜像包和 Docker 镜像，所以只要旧 SHA 还在服务器上，就可以手动切回旧版本。

看已有镜像包：

```bash
sudo ls -lh /opt/sub2api/releases/images
```

看本地 Docker 镜像：

```bash
sudo docker images 'sub2api'
```

如果旧镜像已经 loaded，切回旧 tag：

```bash
cd /opt/sub2api/compose
sudo sed -i.bak 's|^SUB2API_IMAGE=.*|SUB2API_IMAGE=sub2api:<previous-sha>|' .env.production
sudo docker compose up -d sub2api
sudo /opt/sub2api/compose/scripts/healthcheck.sh
```

如果旧镜像只有 tar 包，还没 loaded，先加载：

```bash
sudo gzip -dc /opt/sub2api/releases/images/sub2api-<previous-sha>.tar.gz | sudo docker load
```

然后再更新 `SUB2API_IMAGE` 并重启 `sub2api`。

rollback 时不要删除 `/opt/sub2api/data`。

## 常见失败排查

### Workflow 连不上 SSH

检查：

- `PROD_HOST` 是否能被 GitHub-hosted runner 访问；
- `PROD_SSH_USER` 是否正确；
- `PROD_SSH_KEY` 是否是该用户对应的私钥；
- 服务器是否允许这个 key 登录；
- 防火墙是否允许这次 SSH 连接。

### Production Init 在安装 Docker 时失败

初始化脚本只会在 apt 系系统上自动安装 Docker。如果服务器不是 Debian/Ubuntu 系，先手动安装 Docker，再重新跑 `Production Init`。

### Production Deploy 健康检查失败

看服务状态和日志：

```bash
cd /opt/sub2api/compose
sudo docker compose ps
sudo docker compose logs --tail=200 sub2api
sudo docker compose logs --tail=100 postgres
sudo docker compose logs --tail=100 redis
```

检查非敏感 env 配置：

```bash
sudo grep -E '^(SUB2API_IMAGE|DATABASE_HOST|DATABASE_PORT|DATABASE_NAME|DATABASE_USER|REDIS_HOST|REDIS_PORT|REDIS_DB)=' /opt/sub2api/compose/.env.production
```

### Firewall workflow 可能锁 SSH

应用防火墙前，先从允许的管理员 IP 开第二个 SSH session，并保持打开。等 `sudo ufw status verbose` 确认规则符合预期后，再结束维护。

## 已知缺口

第一版生产部署还没有：

- 自动备份和恢复；
- rollback workflow；
- 外部监控和告警；
- 压测流程；
- 基于真实生产流量的性能调优。
