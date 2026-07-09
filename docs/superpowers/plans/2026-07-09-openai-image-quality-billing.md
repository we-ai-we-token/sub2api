# OpenAI 分组生图质量计费 Implementation Plan

> **For agentic workers:** 本计划按 checkbox (`- [ ]`) 跟踪。实现时逐任务推进，每个任务先写/补测试，再改代码，再跑对应验证。Spec: `docs/superpowers/specs/2026-07-09-openai-image-quality-billing.md`。

**Goal:** 为 `platform=openai` 的分组新增可选“质量计费”模式。关闭时保持现有 `1K/2K/4K` 按张计费；开启时只按上游返回 `usage.quality` 的 `low/medium/high` 按张计费，缺失或非法按 `low`。渠道定价命中模型时优先使用渠道价格；渠道未定义该模型收费方式时回退分组价格。

**Architecture:** 补齐线上已存在但本地代码缺失的 schema/model 字段，将 OpenAI 生图 usage 中的 quality 作为独立字段贯穿 `OpenAIUsage -> OpenAIForwardResult/UsageLog -> usage_logs.image_quality -> 前端使用记录`。计费时拆分“尺寸审计 tier”和“实际计费 tier”：`image_size` 保持 `1K/2K/4K/mixed`，`billing_tier` 记录实际计费层级，质量计费时为 `low/medium/high`。

**Tech Stack:** Go + Ent + PostgreSQL + Gin；前端 Vue/TypeScript；测试使用 Go `testing`/`testify` 与现有前端测试栈。

## Global Constraints

- 仅 OpenAI 分组启用质量计费规则：`group.platform == "openai"`。
- 非 OpenAI 分组不展示质量计费 UI，后端不得改变其现有计费行为。
- 计费质量只允许来自上游返回 usage 对象中的 `quality`。
- 不允许使用请求 body、tools、output item、response 顶层等非 usage 字段作为计费 quality。
- `usage_logs.image_size` 继续只保存 `1K/2K/4K/mixed`，不得写 `low/medium/high`。
- `usage_logs.billing_tier` 保存实际计费层级。
- 渠道 `billing_mode=token` 命中时必须走 token 计费，不回退分组图片价格。
- 工作目录为仓库根；Go module 在 `backend/`，后端命令在 `backend/` 下执行。

---

## Task 1: Schema 与迁移补齐

本地代码尚未包含线上已有字段，先补齐 schema 和迁移，确保本地/新环境/线上一致。

**Files:**
- Add: `backend/migrations/160_openai_image_quality_billing.sql` 或下一个可用编号
- Modify: `backend/ent/schema/group.go`
- Modify: `backend/ent/schema/usage_log.go`
- Generated: `backend/ent/**`

**Fields:**
- `groups.image_quality_billing BOOLEAN NOT NULL DEFAULT false`
- `groups.image_price_low DECIMAL(20,8) NULL`
- `groups.image_price_medium DECIMAL(20,8) NULL`
- `groups.image_price_high DECIMAL(20,8) NULL`
- `usage_logs.image_quality VARCHAR(16) NULL`

- [ ] **Step 1: Add migration**

使用 `ADD COLUMN IF NOT EXISTS`，线上已有字段时安全跳过。不要修改或删除现有 `usage_logs_image_billing_size_check`。

- [ ] **Step 2: Update Ent schema**

在 `Group` schema 的图片计费字段附近增加质量计费字段；在 `UsageLog` schema 的图片字段附近增加 `image_quality`。

- [ ] **Step 3: Generate Ent code**

Run: `cd backend && make generate`

Expected: Ent 生成代码包含新字段的 create/update/query helpers；Wire 生成不应出现无关改动。

- [ ] **Step 4: Validate schema tests**

Run: `cd backend && go test ./internal/repository -run 'Test.*Migration|Test.*Schema' -count=1`

Expected: PASS。若已有迁移集成测试枚举 usage image 字段，补充 `image_quality` 断言。

---

## Task 2: Service/DTO/Repository 字段贯通

把新字段接入后端领域模型、DTO、mapper 与 repository。此任务只做字段读写，不改计费算法。

**Files:**
- Modify: `backend/internal/service/group.go`
- Modify: `backend/internal/service/admin_service.go`
- Modify: `backend/internal/service/usage_log.go`
- Modify: `backend/internal/handler/dto/types.go`
- Modify: `backend/internal/handler/dto/mappers.go`
- Modify: `backend/internal/repository/group_repo.go`
- Modify: `backend/internal/repository/api_key_repo.go`
- Modify: `backend/internal/repository/usage_log_repo.go`

**Interfaces:**
- `service.Group` 增加 `ImageQualityBilling`, `ImagePriceLow`, `ImagePriceMedium`, `ImagePriceHigh`
- `service.UsageLog` 增加 `ImageQuality *string`
- admin create/update group 输入结构增加对应字段
- DTO `Group`/`UsageLog` 增加 JSON 字段

- [ ] **Step 1: Add focused mapper/repo tests**

优先补已有测试：

- `backend/internal/service/admin_service_group_test.go`
- `backend/internal/handler/dto/mappers_usage_test.go`
- `backend/internal/service/usage_log_test.go` 或 repository 对应测试

覆盖：

- create/update group 能保存质量计费字段。
- api key 加载 group 时携带质量计费字段。
- usage log DTO 输出 `image_quality`。

- [ ] **Step 2: Update service models and DTOs**

字段命名保持与现有图片价格一致：

```go
ImageQualityBilling bool
ImagePriceLow       *float64
ImagePriceMedium    *float64
ImagePriceHigh      *float64
```

- [ ] **Step 3: Update repositories**

`group_repo` create/update/select、`api_key_repo` 的 group field selection、`usage_log_repo` 的 select/insert/batch insert/scan/arg type 列表都要同步更新。

`usage_log_repo.go` 中新增列时必须同时更新：

- `usageLogSelectColumns`
- `usageLogInsertArgTypes`
- single insert column list
- batch insert CTE column list
- `prepareUsageLogInsert`
- `scanUsageLog`

- [ ] **Step 4: Run backend tests**

Run: `cd backend && go test ./internal/service ./internal/repository ./internal/handler/dto -run 'Group|UsageLog|Mapper|Migration' -count=1`

Expected: PASS。

---

## Task 3: OpenAI usage quality 解析

只从 usage 对象读取 quality，并把它带到计费结果中。不要从 request 或非 usage response 字段读取计费 quality。

**Files:**
- Modify: `backend/internal/service/openai_gateway_service.go`
- Modify: `backend/internal/service/openai_images.go`
- Modify: `backend/internal/service/openai_images_codex.go`
- Modify: `backend/internal/service/openai_images_responses.go`
- Modify as needed: `backend/internal/service/openai_ws_forwarder.go`, `backend/internal/service/openai_ws_v2/**`

**Interfaces:**
- `OpenAIUsage` 增加 `Quality string`
- `OpenAIForwardResult` 如有必要增加 `ImageQuality string`，但首选统一从 `result.Usage.Quality` 读取，减少重复状态
- 新增 helper:

```go
func NormalizeOpenAIImageQualityOrLow(value string) string
func NormalizeOpenAIImageQualityOrEmpty(value string) string
```

- [ ] **Step 1: Add parser tests**

覆盖所有实际 OpenAI 生图路径：

- APIKey `/v1/images` 非流式响应：`{"usage":{"quality":"high"}}`
- Codex images 非流式响应：只从 `usage.quality` 读取
- Codex images 流式行：只从带 usage 的行读取
- Responses `response.completed.response.usage.quality`
- request body 有 `quality=high`、tools 有 `quality=high`、output item 有 `quality=high`，但 usage 缺失时，计费质量为空/low，不得取 high

- [ ] **Step 2: Implement quality parsing**

在已有 usage parsing 函数里读取 `usage.quality`。如果 usage 不存在或字段为空，`OpenAIUsage.Quality` 保持空字符串；默认 low 的决策放在计费/落库归一化处。

- [ ] **Step 3: Run parser tests**

Run: `cd backend && go test ./internal/service -run 'OpenAIImages|Codex|Responses|UsageQuality|ImageQuality' -count=1`

Expected: PASS。

---

## Task 4: 计费 tier 决策 helper

先把“尺寸审计 tier”和“实际计费 tier”的选择做成小 helper，再接入成本计算，降低主流程复杂度。

**Files:**
- Modify: `backend/internal/service/openai_gateway_service.go`
- Modify or add tests: `backend/internal/service/openai_gateway_record_usage_test.go`

**Interfaces:**

建议新增内部结构：

```go
type openAIImageBillingTiers struct {
	SizeTier      string
	Quality       string
	BillingTier   string
	QualityMode   bool
}
```

规则：

```text
SizeTier = NormalizeImageBillingTierOrDefault(result.ImageSize)

if group.platform == openai && group.image_quality_billing:
    Quality = NormalizeOpenAIImageQualityOrLow(result.Usage.Quality)
    BillingTier = Quality
    QualityMode = true
else:
    Quality = NormalizeOpenAIImageQualityOrEmpty(result.Usage.Quality)
    BillingTier = SizeTier
    QualityMode = false
```

- [ ] **Step 1: Write helper unit tests**

覆盖：

- OpenAI + quality billing + `high` -> `BillingTier=high`
- OpenAI + quality billing + empty -> `BillingTier=low`
- OpenAI + quality billing + invalid -> `BillingTier=low`
- OpenAI + no quality billing + `high` -> `BillingTier=1K/2K/4K`
- Gemini/Antigravity 即使有 quality 也使用 size tier

- [ ] **Step 2: Implement helper**

不要在 helper 内做价格计算，只返回 tier 决策。

- [ ] **Step 3: Run helper tests**

Run: `cd backend && go test ./internal/service -run 'ImageBillingTier|QualityBillingTier|OpenAI.*Quality' -count=1`

Expected: PASS。

---

## Task 5: 渠道定价优先级修正

当前 OpenAI 图片计费逻辑需要明确区分“渠道命中 token/image/per_request”和“渠道没有定义该模型收费方式”。这是本需求最容易写偏的地方。

**Files:**
- Modify: `backend/internal/service/openai_gateway_service.go`
- Modify as needed: channel pricing resolver tests/stubs
- Test: `backend/internal/service/openai_gateway_record_usage_test.go`

**Desired behavior:**

```text
if channel pricing resolved for billing model:
    if mode == token:
        token billing
    if mode == image/per_request:
        channel price using imageBillingTier
else:
    group image price using imageBillingTier/sizeTier
```

- [ ] **Step 1: Write failing tests**

Add tests for:

- 渠道 `image` 命中 + OpenAI quality billing on + `usage.quality=high` -> 使用渠道 `high` tier 价格。
- 渠道 `per_request` 命中 + OpenAI quality billing off + size `2K` -> 使用渠道 `2K` tier 价格。
- 渠道 `token` 命中 -> 走 token 计费，不使用分组质量价格。
- 渠道未定义该模型收费方式 + OpenAI quality billing on -> 回退分组 `image_price_high`。
- 渠道未定义该模型收费方式 + OpenAI quality billing off -> 回退分组 `image_price_2k`。

- [ ] **Step 2: Adjust channel pricing resolution**

如果现有 `resolveOpenAIChannelPricing` 无法表达“未定义模型收费方式”与“命中默认/回退价格”的差异，新增更明确的 helper，例如：

```go
resolveOpenAIExplicitChannelPricing(ctx, billingModel, apiKey) (*ResolvedPricing, bool)
```

`bool` 表示渠道是否明确覆盖该模型收费方式。

- [ ] **Step 3: Use billing tier in channel image/per_request mode**

调用 `CalculateCostUnified` 时传入 `SizeTier: tiers.BillingTier`。这里字段名虽然叫 `SizeTier`，在现有统一计费里本质是按次/图片 tier label，可以承载 `low/medium/high`。

- [ ] **Step 4: Run billing tests**

Run: `cd backend && go test ./internal/service -run 'RecordUsage.*Image|CalculateRecordUsageCost|Channel.*Image|QualityBilling' -count=1`

Expected: PASS。

---

## Task 6: 分组质量价格计费

当渠道没有定义模型收费方式时，OpenAI quality billing 使用分组 `image_price_low/medium/high`。

**Files:**
- Modify: `backend/internal/service/group.go`
- Modify: `backend/internal/service/billing_service.go`
- Modify: `backend/internal/service/billing_service_image_test.go`
- Modify: `backend/internal/service/openai_gateway_service.go`

**Interfaces:**
- `Group.GetImagePrice` 可扩展支持 `low/medium/high`，或新增 `GetImageQualityPrice`
- `ImagePriceConfig` 增加 `PriceLow`, `PriceMedium`, `PriceHigh`
- `BillingService.CalculateImageCost` 不应强制把所有 tier 都归一化为尺寸；否则会把 `high` 错误变成默认尺寸

- [ ] **Step 1: Add BillingService tests**

覆盖：

- `CalculateImageCost(..., "high", count=2, groupConfig.PriceHigh=0.3)` -> cost `0.6 * multiplier`
- unknown quality tier 在 OpenAI 层已经归一到 low，BillingService 可不负责处理非法 quality
- 既有 `1K/2K/4K` 测试不回归

- [ ] **Step 2: Refactor image unit price lookup**

保持旧尺寸逻辑，同时支持 quality tier：

```text
1K -> Price1K
2K -> Price2K
4K -> Price4K
low -> PriceLow
medium -> PriceMedium
high -> PriceHigh
```

未配置时按现有默认价格回退策略处理。

- [ ] **Step 3: Run tests**

Run: `cd backend && go test ./internal/service -run 'BillingService.*Image|CalculateImageCost|ImageQualityPrice' -count=1`

Expected: PASS。

---

## Task 7: Usage 落库与审计字段

RecordUsage 时写入 `image_quality` 和 `billing_tier`，但保持 `image_size` 仍为尺寸 tier。

**Files:**
- Modify: `backend/internal/service/openai_gateway_service.go`
- Modify: `backend/internal/repository/usage_log_repo.go`
- Test: `backend/internal/service/openai_gateway_record_usage_test.go`

**Rules:**

| 字段 | 规则 |
| --- | --- |
| `image_size` | 写尺寸 tier，不写 quality |
| `image_quality` | OpenAI 生图写归一化 usage quality；quality billing 开启且缺失时写 `low` |
| `billing_tier` | 写实际计费 tier |
| `billing_mode` | 图片按张计费写 `image`；渠道 token 写 `token` |

- [ ] **Step 1: Add RecordUsage tests**

覆盖：

- quality billing on + `usage.quality=high` -> `ImageQuality=high`, `BillingTier=high`, `ImageSize=2K`
- quality billing on + missing quality -> `ImageQuality=low`, `BillingTier=low`
- quality billing off + `usage.quality=high` -> `ImageQuality=high`, `BillingTier=2K`
- non-OpenAI + quality present -> 不改变 billing tier；是否记录 `image_quality` 可按 spec 保守处理，建议仅 OpenAI 生图写

- [ ] **Step 2: Implement usage log assignment**

在 `RecordUsage` 构造 `UsageLog` 时使用 Task 4 helper 的结果，显式设置：

```go
usageLog.ImageQuality = optionalTrimmedStringPtr(tiers.Quality)
usageLog.BillingTier = optionalTrimmedStringPtr(tiers.BillingTier)
```

`tiers.Quality` 为空时不写，除 quality billing on 且默认 low 的情况。

- [ ] **Step 3: Run usage tests**

Run: `cd backend && go test ./internal/service -run 'RecordUsage.*Quality|RecordUsage.*BillingTier|ImageQuality' -count=1`

Expected: PASS。

---

## Task 8: Admin 分组 UI

前端分组管理页只在 OpenAI 分组展示质量计费配置。

**Files:**
- Modify: `frontend/src/types/index.ts`
- Modify: `frontend/src/views/admin/GroupsView.vue`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Test: `frontend/src/views/admin/__tests__/GroupsView*.spec.ts`

**Behavior:**

- OpenAI 分组显示 `是否质量计费` checkbox。
- 非 OpenAI 分组不显示 checkbox。
- 默认 unchecked。
- unchecked: 展示 `1K/2K/4K` 价格输入。
- checked: 展示 `low/medium/high` 价格输入。
- create/edit/reset/open dialog/payload 全部包含新字段。

- [ ] **Step 1: Add frontend tests**

覆盖 UI 可见性、默认值、切换后字段显示、payload。

- [ ] **Step 2: Update types and form state**

`AdminGroup` / create/update request 类型增加：

```ts
image_quality_billing: boolean
image_price_low: number | null
image_price_medium: number | null
image_price_high: number | null
```

- [ ] **Step 3: Update GroupsView**

建议复用现有图片计费区布局；切换时不要清空另一组价格，避免管理员误点造成配置丢失。提交时两组价格都可传，后端以 `image_quality_billing` 决定使用哪组。

- [ ] **Step 4: Run frontend tests**

Run: `cd frontend && pnpm test -- GroupsView`

Expected: PASS。

---

## Task 9: 使用记录前端展示

管理端和用户端使用记录展示 `image_quality` 与实际 `billing_tier`。

**Files:**
- Modify: `frontend/src/types/index.ts`
- Modify: `frontend/src/api/usage.ts`
- Modify: `frontend/src/api/admin/usage.ts`
- Modify: `frontend/src/views/user/UsageView.vue`
- Modify: `frontend/src/views/admin/UsageView.vue`
- Modify: `frontend/src/components/admin/usage/UsageTable.vue`
- Modify as needed: `frontend/src/utils/billingMode.ts`
- Tests: existing UsageView/UsageTable specs

**Display rules:**

```text
billing_mode=image + billing_tier=high -> 图片 / high
billing_mode=image + billing_tier=2K   -> 图片 / 2K
billing_mode=image + billing_tier empty + image_size present -> 图片 / {image_size}
billing_mode=image + billing_tier empty + image_quality present -> 图片 / {image_quality}
```

Tooltip/detail shows:

- `image_count`
- `image_size`
- `image_quality`
- `billing_tier`
- `billing_mode`

- [ ] **Step 1: Add frontend usage tests**

覆盖：

- `billing_tier=high` 显示 `图片 / high`
- `billing_tier=2K` 显示 `图片 / 2K`
- historical row with `image_quality=null` does not crash
- no implicit fallback to `2K` when `billing_tier` is missing

- [ ] **Step 2: Update usage types/API**

`UsageLog` 增加 `image_quality?: string | null`。

- [ ] **Step 3: Update display components**

优先修改已有 billing mode/tier 展示 helper，避免 admin/user 两套逻辑分叉。

- [ ] **Step 4: Run usage frontend tests**

Run: `cd frontend && pnpm test -- Usage`

Expected: PASS。

---

## Task 10: 后端端到端回归

完成核心实现后跑一组聚焦回归，确保 OpenAI 质量计费、渠道覆盖、usage 落库联动正确。

- [ ] **Step 1: Run service tests**

Run:

```bash
cd backend
go test ./internal/service -run 'OpenAI.*Image|RecordUsage|BillingService|Channel.*Pricing|QualityBilling' -count=1
```

Expected: PASS。

- [ ] **Step 2: Run repository/handler tests**

Run:

```bash
cd backend
go test ./internal/repository ./internal/handler ./internal/handler/dto -run 'Usage|Group|Image|Quality' -count=1
```

Expected: PASS。

- [ ] **Step 3: Build backend**

Run: `cd backend && go test ./...`

Expected: PASS or document unrelated existing failures with exact package/test names.

---

## Task 11: 前端回归

- [ ] **Step 1: Typecheck**

Run: `cd frontend && pnpm typecheck`

Expected: PASS.

- [ ] **Step 2: Targeted tests**

Run:

```bash
cd frontend
pnpm test -- GroupsView Usage UsageTable
```

Expected: PASS.

- [ ] **Step 3: Build**

Run: `cd frontend && pnpm build`

Expected: PASS.

---

## Task 12: Manual verification checklist

用本地 dev 环境或测试环境手动确认关键路径。

- [ ] OpenAI 分组创建页显示质量计费 checkbox，默认关闭。
- [ ] 非 OpenAI 分组不显示质量计费 checkbox。
- [ ] OpenAI 分组开启质量计费后能保存 low/medium/high 价格。
- [ ] Mock 或测试上游返回 `usage.quality=high`，使用记录显示 `图片 / high`。
- [ ] Mock 或测试上游不返回 `usage.quality`，使用记录显示 `图片 / low`，扣费走 low。
- [ ] 渠道对该模型配置 image/per_request tier 价格时，扣费使用渠道价格。
- [ ] 渠道没有定义该模型收费方式时，扣费回退分组价格。
- [ ] 渠道对该模型配置 token 模式时，扣费走 token，使用记录 `billing_mode=token`。
- [ ] `usage_logs.image_size` 没有出现 `low/medium/high`。
- [ ] `usage_logs.image_quality` 正确记录 `low/medium/high`。

---

## Notes / Risk Points

- `usage_log_repo.go` 是高风险文件，列顺序必须保持完全一致；新增列时不要只改 single insert，batch insert 与 scan 也要同步。
- `CostInput.SizeTier` 命名偏尺寸，但统一计费中实际是按次/图片 tier label；可以传 `low/medium/high`，但测试必须锁住。
- `BillingService.CalculateImageCost` 当前可能会强制 normalize image size；质量 tier 支持时要避免把 `high` 误归一为默认尺寸。
- 渠道定价优先级要以“是否明确命中模型收费方式”为准，不能把“resolver nil/未命中”误当作渠道 image 价格。
- 前端切换质量计费 checkbox 时不要自动清空另一套价格，避免配置误丢。
