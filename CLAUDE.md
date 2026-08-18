# CLAUDE.md

## Code Maps

- **Codex 生图 (codex image generation)**: see [docs/CODEX_IMAGE_GENERATION.md](docs/CODEX_IMAGE_GENERATION.md) — maps the two flows (Images API `/v1/images/*` vs Codex Responses `/backend-api/codex/responses` with the `image_generation` tool), with `file:line` anchors for handlers, forwarding, transform, Spark limits, and billing.

- **运营管理 / 生图报表 (operation image report)**: see [docs/superpowers/specs/2026-06-20-operation-image-report-design.md](docs/superpowers/specs/2026-06-20-operation-image-report-design.md) — 运营管理一级菜单下的只读监控看板（OpenAI/Gemini 生图调用）：并发卡片、今日看板（平台/模型/分组×成功/失败）、耗时分位数曲线、请求量与成功率曲线。Key anchors: backend handler `backend/internal/handler/admin/operation_image_report_handler.go`; service `backend/internal/service/operation_image_report_service.go`; repo (raw SQL) `backend/internal/repository/operation_image_report_repo.go`; routes `backend/internal/server/routes/admin_operation.go`; frontend page `frontend/src/views/admin/operation/ImageReportView.vue`; frontend API client `frontend/src/api/admin/operationImageReport.ts`.

## Branch Management

This repository is a fork of the upstream `Wei-Shaw/sub2api` project.

Branches:

- `main` tracks the upstream project only for comparison and should stay clean.
- `release` is the production **and** integration branch: upstream release tags and local changes are merged here directly, and verification happens here. (There is no `pre-release` branch anymore.)
- `upstream-v0.1.x` — a bookmark branch pinned at each synced upstream tag.
- `backup/release-before-v0.1.x` — a snapshot of `release` taken right before each upstream merge, so a bad merge can be reset away.

Never merge upstream `main`; only merge upstream **release tags**.

### Upstream release sync workflow

```bash
git fetch upstream --tags
git branch upstream-v0.1.x v0.1.x                  # bookmark the tag
git branch backup/release-before-v0.1.x release    # snapshot before merging
git switch release
git merge --no-ff v0.1.x -m "Merge tag 'v0.1.x' into release"
```

Or run the helper script, which does the same thing with interactive tag selection:

```bash
scripts/sync-upstream-release.sh
```

If the `upstream` remote is missing, add it first:

```bash
git remote add upstream <upstream-repository-url>
```

### Recurring merge conflicts

- **`backend/ent` generated code** (`group.go`, `mutation.go`, `runtime/runtime.go`, …): do not hand-merge. Take upstream's generated code with `git checkout v0.1.x -- backend/ent`, restore the merged `backend/ent/schema/`, then regenerate with `GOPROXY=https://goproxy.cn,direct go generate ./ent` (`proxy.golang.org` is unreachable here).
- **`backend/go.sum`**: take upstream's side, then confirm `go mod tidy` produces no diff before committing.
- **`backend/internal/handler/openai_images_failover_test.go`**: intentionally deleted locally (the local retry logic diverges and the upstream test breaks CI). Keep it deleted with `git rm` when it conflicts.
- **`usage_logs` column lists** (`usage_log_repo_query.go` / `usage_log_repo_insert.go`): the local fork adds an `image_quality` column. Keep both sides' columns, and keep the `SELECT` column order identical to the `scanner.Scan` field order. **Trap**: `usage_log_repo_insert.go` has two static `$1..$N` VALUES lists. When both sides add a column, each bumps `$56→$57` independently and git auto-merges "cleanly" one placeholder short. After every merge, verify: static placeholder count == column count == `len(usageLogInsertArgTypes)`. The batch path is generated dynamically and is not affected.
- **`backend/internal/repository/group_usage_rollup_trigger_integration_test.go`**: locally patched (since v0.1.177). Upstream's `SerializesInsertTransactionAcrossMidnight` / `KeepsWatermarkForTodayInsert` compute expected dates with a hardcoded `'Asia/Shanghai'`, while migration 223 made the trigger read `current_setting('TimeZone')`. CI's Postgres session is UTC, so both fail during UTC 16:00–24:00 (北京时间 0–8 点) and pass the rest of the day. Keep our side (`AT TIME ZONE current_setting('TimeZone')`) unless upstream fixes it. **Trap**: this failure is time-of-day dependent — a green CI run outside that window does not mean the patch survived the merge; grep for `Asia/Shanghai` in those two functions instead.
- **`frontend/src/components/account/__tests__/CreateAccountModal.grok.spec.ts`**: locally patched (since v0.1.178). 上游把 `CreateAccountModal.vue` 的 `apiKeyValuePlaceholder` 从三元表达式改成 `switch`（为了支持 kimi/zhipu/deepseek），却没同步这个「读源码字符串」的断言，导致 `? 'xai-...'` 在上游 tag 上就是红灯。我们改成断言 `case 'grok':` + `return 'xai-...'`。上游修好后可以还原成上游侧。

### After the merge

`VERSION` lives in `backend/cmd/server/VERSION` (trailing newline). Upstream tags do not bump it, so make a separate commit:

```
chore: bump VERSION to x.y.z
```

Test and CI fixes also go in their own commits, not folded into the merge commit.

### Verification checklist

```bash
# backend
cd backend
GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./...
go test -tags unit ./internal/...                     # -tags unit is REQUIRED, else //go:build unit cases silently skip
TESTCONTAINERS_RYUK_DISABLED=true go test -tags=integration ./...   # CI equivalent: make test-integration

# frontend
cd frontend
npx pnpm@9 install --frozen-lockfile
npx pnpm@9 typecheck && npx pnpm@9 test:run
```

- Use **pnpm 9** (what CI uses). pnpm 11 no longer reads the `pnpm.overrides` field in `package.json`, so `--frozen-lockfile` fails with `ERR_PNPM_LOCKFILE_CONFIG_MISMATCH`, and it will rewrite `pnpm-lock.yaml` and drop a stray `pnpm-workspace.yaml` — never commit those.
- vitest exits non-zero on Unhandled Errors even when every test passes; check the exit code, not just the summary line.

Keep `main` clean so it remains easy to compare with upstream.
