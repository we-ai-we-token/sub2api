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
  **重生成会污染 go.sum**：`ent/generate.go` 的 directive 自带 `-mod=mod`，`go generate ./ent` 会把 ent 代码生成器自己的依赖（`spf13/cobra`、`olekukonko/tablewriter`、`mattn/go-runewidth`、`rivo/uniseg`）写进 `backend/go.sum`。它不是冲突、`git status` 里只是一行不起眼的 ` M backend/go.sum`，`go build` / `go vet` / 单测全部看不出来，跟着提交就把构建依赖清单污染了。重生成后固定动作：`git checkout -- backend/go.sum` 回滚，再跑 `go mod tidy` 确认无 diff。
- **`backend/go.sum`**: take upstream's side, then confirm `go mod tidy` produces no diff before committing.
- **`backend/internal/handler/openai_images_failover_test.go`**: intentionally deleted locally (the local retry logic diverges and the upstream test breaks CI). Keep it deleted with `git rm` when it conflicts.
- **`usage_logs` column lists** (`usage_log_repo_query.go` / `usage_log_repo_insert.go`): the local fork adds an `image_quality` column. Keep both sides' columns, and keep the `SELECT` column order identical to the `scanner.Scan` field order. **Trap**: `usage_log_repo_insert.go` has two static `$1..$N` VALUES lists. When both sides add a column, each bumps `$56→$57` independently and git auto-merges "cleanly" one placeholder short. After every merge, verify: static placeholder count == column count == `len(usageLogInsertArgTypes)`. The batch path is generated dynamically and is not affected. **2026-09-02 起这条已有回归测试兜底**：`backend/internal/repository/usage_log_insert_placeholder_unit_test.go` 直接读源码断言两处静态 `$1..$N` 与 `usageLogInsertArgTypes` 等长且连续，跑 `go test -tags unit ./internal/repository/` 就会炸。放在独立文件里是为了不跟上游抢同一个测试文件。 **2026-09-05 合 v0.2.1 时这个坑第一次真的发生了**：上游加 `upstream_request_id`（$61→$62）、我方 `image_quality` 也占一位，git **没报冲突**、干净地合出「63 列 / 63 个 argTypes / 只有 62 个占位符」，靠这个测试抓住。合完先跑它，别等集成测试。
- **二开给 `Group` 加字段时，别漏 `cloneGroupForDuplicate`**（`backend/internal/service/admin_group_duplicate.go`）：该函数跟上游保持字节一致，二开新增字段漏拷没有任何编译期提示；而 `groupRepository.CreateFromSource` 是**无条件 `Set`** 的，漏拷 = 按零值写库——`image_use_responses_api` 的 schema 默认值 true 会被复制出来的分组静默写成 false，OAuth 生图直接换一条链路。**2026-09-05 起已有回归测试兜底**：`backend/internal/service/admin_group_duplicate_fork_fields_unit_test.go`，同样独立成文件以免跟上游抢 `admin_group_duplicate_test.go`。上游自己漏拷的 `LongContextPricingEnabled` / `ModelPricing` 也已一并补上（后者用现成的 `ChannelModelPricing.Clone()` 做深拷贝）。**核对方法**：拿 `Group` 结构体字段清单减去 `cloneGroupForDuplicate` 里的键，差集应当只剩 `ID` / `Hydrated` / `CreatedAt` / `UpdatedAt` / `AccountGroups`（绑定由 `DuplicateGroup` 单独重建）/ `AccountCount` / `ActiveAccountCount` / `RateLimitedAccountCount` 这 8 个本就不该复制的——多出任何一个就是漏拷。
- **上游用例硬编码 arg 下标**：合 v0.1.185 时 `TestPrepareUsageLogInsert_RequestedReasoningEffortArgWiring` 写死了 `usageLogInsertArgTypes[47]/[48]` 和 `prepared.args[47]/[48]`，那是上游列序。我方 `image_quality` 在下标 42，把它后面所有列整体后移一位，正确值是 **48/49**。这类下标断言以后每次上游加列都要重算——用 `INSERT INTO usage_logs (...)` 的列清单 `.index(列名)` 算，别手数。 **v0.2.1 起上游改用倒数下标**（`idx := len(prepared.args) - 4`），我方在中间插列不再影响它，这条暂时不触发；但上游哪天改回绝对下标就会重演，别默认它一直是倒数。
- **`backend/internal/repository/group_usage_rollup_trigger_integration_test.go`**: 本地补丁 v0.1.177 起存在、**v0.1.183 已撤销**——上游 `1bff06ea5` 用更好的方式修好了：给写入事务显式 `SET LOCAL TIME ZONE 'Asia/Shanghai'`，触发器的 `current_setting('TimeZone')` 因此和断言里硬编码的 `'Asia/Shanghai'` 对齐。现在整份文件跟上游一致，不要再把 `'Asia/Shanghai'` 换成 `current_setting('TimeZone')`。**血的教训**：v0.1.183 合并时两侧改的是不同的行，git 干净地把上游的 `SET LOCAL TIME ZONE`（写入事务 = Shanghai）和我方的 `current_setting('TimeZone')`（seed/sync/断言 = 会话 UTC）拼在了一起，`SerializesInsertTransactionAcrossMidnight` 变成跨两个时区，UTC 16:00–24:00（北京时间 0–8 点）之外照样绿，只在那个窗口挂——本机 08:00 跑集成测试全绿、推上去 CI 在 UTC 18:52 才炸。**时区相关的测试改动，验证要挑 UTC 16:00–24:00 这个窗口跑一遍**（本机 `date -u` 确认），否则等于没验。
- **OAuth 生图相关文件（`openai_images_responses.go` / `openai_images_test.go`）**: 二开把 `buildOpenAIImagesResponsesRequest`、`openAIImageUploadToDataURL`、`shouldPassOpenAIImagesN`、`openAIImagesUpstreamErrorResponseBody`、`handleOpenAIImagesOAuthResponseError`、`forwardOpenAIImagesOAuthResponses` 搬到了 `openai_images_responses_upstream.go`，两侧文件结构差太多，git 三方合并会把上游 hunk 对到完全不相干的位置（冲突块一侧几十行、另一侧一行）。**不要硬啃冲突块**，改用「以我方为底 + 重放上游本文件 diff」：

  ```bash
  git checkout --ours -- backend/internal/service/openai_images_responses.go
  git diff v0.1.<prev> v0.1.<new> -- backend/internal/service/openai_images_responses.go | git apply --reject
  # 逐个处理 .rej：属于已搬家函数的 hunk 手工落到 openai_images_responses_upstream.go
  ```

  想看清「我方是不是删了这一段」时，用 `git -c merge.conflictstyle=zdiff3 checkout --merge -- <file>` 重新生成带 base 的冲突块。

  **v0.1.185 补充**：`git apply --reject` 这次**没有生成任何 `.rej` 文件**——它对能打的 hunk 直接改文件、对打不上的只在 stderr 报 `error: patch failed:` 就算了。所以不要靠 `.rej` 是否存在判断，改用 grep 逐个确认上游新符号是否落地（如 `withOpenAIImagesSelfBuiltRequest` / `shouldCoolOpenAIImagesToolForError` / `openAIImagesOAuthUnavailableDefaultCooldown`）。打不上的 hunk 基本都属于已搬家到 `openai_images_responses_upstream.go` 的函数，手工落过去即可。
  另外注意跨文件依赖：上游新增的 `isOpenAIImagesSelfBuiltRequest` 会被上游新文件 `openai_account_runtime_block_fastpath.go` 直接调用，漏补就是编译不过。
  **v0.2.1 复核**：`git apply --reject` 依旧不产生 `.rej`、只在 stderr 报 `error: patch failed:`，确认这是常态。这轮上游 8 个改动点里有 6 个落在已搬家的函数里（`handleOpenAIImagesOAuthResponseError`、`forwardOpenAIImagesOAuthResponses`），全部手工落到 `openai_images_responses_upstream.go`。**收尾一律用计数核对**：拿上游文件里的符号出现次数当目标值（本轮 `opsUpstreamProxyID` 6 处、`UpstreamHeaders:  resp.Header` 2 处），跟我方两个文件的合计数对齐。
- **上游改生图函数签名时，二开的 gemini 透传调用点要跟着改**：v0.2.1 给 `handleOpenAIImagesNonStreamingResponse` 加了 `ctx` / `account` / `parsed` 三个形参（为了调 `backfillOpenAIImagesB64JSON` 把 URL 回填成 b64_json），二开的 `backend/internal/service/gemini_images_passthrough.go` 里 `ForwardGeminiImagesPassthrough` 也调这个函数，漏改就是编译不过（这条 `go build` 抓得到，不算隐形坑）。该函数手上正好有 ctx/account/parsed，直接透传即可，语义上 gemini 透传链路也该享受同样的 b64 回填。
- **上游新增的生图用例走错链路**: 二开按 `Group.ImageUseResponsesAPI`（DB 列默认 true）给 OAuth 生图分流，裸 `gin.Context` 无分组会退化到二开专用 codex images 端点，上游用例的 URL/failover 断言全部落空。补一行让它像线上一样走 Responses 链路：`c.Set("api_key", &APIKey{Group: &Group{ImageUseResponsesAPI: true}})`。
- **`handleOpenAIImagesOAuthNonStreamingResponse` / `...StreamingResponse`**: 二开多一个末位 `retryableEmptyOutput bool` 形参，上游新增用例按上游签名调用会编译失败，补 `false`（沿用上游不做空输出重试的语义）。
- **gofmt 对齐**: 双方各自往同一个结构体字面量加字段时，auto-merge 出来的字段名列宽不再是 gofmt 结果，CI 的 golangci-lint gofmt formatter 会报错，但 `go build`/`go vet` 都看不出来。每次合并后跑一遍 `gofmt -l ./cmd ./internal ./pkg`。

### After the merge

`VERSION` lives in `backend/cmd/server/VERSION` (trailing newline). **一次跨两个 tag 时它会冲突**（如从 0.1.183 直接合 v0.1.185，上游文件是 0.1.184，我方是 0.1.183）——合并时取上游值，bump 仍然单独提一个 commit。单版本递进时，上游 tag 里的 VERSION **永远落后 tag 一版**（`v0.1.179` 的文件内容是 `0.1.178`），刚好等于我们上一轮 bump 后的值，所以合并时不会冲突、也不会自动更新。仍然要单独提一个 commit 把它 bump 到刚合入的 tag 版本：

```
chore: bump VERSION to x.y.z
```

Test and CI fixes also go in their own commits, not folded into the merge commit.

### Verification checklist

```bash
# backend
cd backend
GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./...
gofmt -l ./cmd ./internal ./pkg                       # must print nothing; go vet does NOT catch merge-broken alignment
go test -tags unit ./internal/...                     # -tags unit is REQUIRED, else //go:build unit cases silently skip
TESTCONTAINERS_RYUK_DISABLED=true go test -tags=integration ./...   # CI equivalent: make test-integration
GOFLAGS=-mod=mod go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0 run --timeout=30m ./...  # same version as backend-ci.yml

# frontend
cd frontend
npx pnpm@9 install --frozen-lockfile
npx pnpm@9 run lint:check && npx pnpm@9 typecheck && npx pnpm@9 test:run
```

- Use **pnpm 9** (what CI uses). pnpm 11 no longer reads the `pnpm.overrides` field in `package.json`, so `--frozen-lockfile` fails with `ERR_PNPM_LOCKFILE_CONFIG_MISMATCH`, and it will rewrite `pnpm-lock.yaml` and drop a stray `pnpm-workspace.yaml` — never commit those.
- vitest exits non-zero on Unhandled Errors even when every test passes; check the exit code, not just the summary line.
- CI 跑的是 `make test-frontend` = `lint:check`（eslint）+ `typecheck` + 关键用例，**别漏了 `lint:check`**：typecheck 和 vitest 都不跑 eslint 规则。仓库没装 prettier，也没有 prettier 检查，不要用 `npx prettier --check` 判定前端格式（外部版本对存量文件本来就一片报警）。

Keep `main` clean so it remains easy to compare with upstream.
