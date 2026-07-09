# OpenAI 分组生图质量计费 — 设计文档

## 1. 背景

当前分组生图计费已有按分辨率计费能力：分组配置 `image_price_1k` / `image_price_2k` / `image_price_4k`，使用记录中保存 `image_size`，计费时按 `1K` / `2K` / `4K` 图片张数扣费。

本需求为 OpenAI 类型分组新增一个可选计费模式：按上游返回 usage 中的 `quality` 字段计费。开启后，同一张图不再按分辨率档位收费，而是按 `low` / `medium` / `high` 三个质量档位收费。

线上库 `ssh core` 已确认存在相关字段：

| 表 | 字段 | 类型 | 说明 |
| --- | --- | --- | --- |
| `groups` | `image_quality_billing` | `boolean not null default false` | 是否启用质量计费 |
| `groups` | `image_price_low` | `numeric nullable` | low 质量单张价格 |
| `groups` | `image_price_medium` | `numeric nullable` | medium 质量单张价格 |
| `groups` | `image_price_high` | `numeric nullable` | high 质量单张价格 |
| `usage_logs` | `image_quality` | `varchar(16) nullable` | 使用记录中的图片质量 |

线上仍存在 `usage_logs_image_billing_size_check`，限制 `usage_logs.image_size` 只能为 `1K` / `2K` / `4K` / `mixed`。因此质量值不能写入 `image_size`，必须单独写入 `image_quality`，实际计费层级写入 `billing_tier`。

## 2. 目标

- 只对 `platform = openai` 的分组启用质量计费规则。
- OpenAI 分组新增“是否质量计费”配置，默认关闭。
- 关闭质量计费时，保持现有按分辨率 `1K` / `2K` / `4K` 计费行为。
- 开启质量计费时，按返回 usage 中的 `quality` 值 `low` / `medium` / `high` 计费。
- `quality` 为空或非法时，默认按 `low` 计费。
- 渠道定价优先于分组定价：渠道定义了该模型收费方式时使用渠道定价；渠道没有定义该模型收费方式时回退到分组图片价格。
- 使用记录前端要能看出图片请求按哪个层级计费，以及上游 usage 的质量值。

## 3. 非目标

- 不改变非 OpenAI 分组的生图计费逻辑。
- 不把 `low` / `medium` / `high` 写入 `usage_logs.image_size`。
- 不从请求参数或非 usage 响应字段推断计费质量。
- 不要求第一版增加使用记录的质量筛选；展示即可。
- 不改变现有 `billing_mode = token` 的渠道定价行为。

## 4. 适用范围

质量计费只适用于 OpenAI 分组：

```text
group.platform == "openai"
```

非 OpenAI 分组行为：

- 不展示质量计费 checkbox。
- 后端忽略或清洗 `image_quality_billing` 为 `false`。
- 继续使用现有生图计费逻辑。

## 5. 分组配置

OpenAI 分组的生图计费区新增 checkbox：

```text
是否质量计费 -> groups.image_quality_billing
```

默认值：

```text
false
```

### 5.1 关闭质量计费

当 `image_quality_billing = false`：

- 展示并保存：
  - `image_price_1k`
  - `image_price_2k`
  - `image_price_4k`
- 计费层级来自图片分辨率归一化结果：
  - `1K`
  - `2K`
  - `4K`
- 行为与现有实现保持兼容。

### 5.2 开启质量计费

当 `image_quality_billing = true`：

- 展示并保存：
  - `image_price_low`
  - `image_price_medium`
  - `image_price_high`
- 计费层级来自上游返回 usage 中的 `quality`。
- 归一化后只允许：
  - `low`
  - `medium`
  - `high`
- 空值或非法值按 `low` 计费。

建议后端校验：OpenAI 分组开启质量计费时，`image_price_low` / `image_price_medium` / `image_price_high` 应至少全部显式配置。`0` 是有效价格，表示免费；`nil` 表示未配置。

## 6. Quality 来源规则

质量计费用的 `quality` 只能取自上游返回 usage 信息：

```text
usage.quality
```

禁止使用以下字段参与计费：

- 客户端请求 body 中的 `quality`
- 上游 response 顶层 `quality`
- tools 中的 `quality`
- output item 中的 `quality`
- SSE 非 usage event 中的 `quality`

归一化函数：

```text
low     -> low
medium  -> medium
high    -> high
其它/空 -> low
```

如果某条 OpenAI 生图响应没有返回 usage 或 usage 中没有 quality，则最终计费质量为 `low`。

## 7. 计费优先级

计费入口需要明确区分“渠道是否定义了该模型收费方式”。

### 7.1 渠道定义了该模型收费方式

如果渠道定价命中该模型，并且该模型有明确收费方式：

| 渠道 `billing_mode` | 行为 |
| --- | --- |
| `token` | 走渠道 token 计费，不回退分组图片价格 |
| `image` | 走渠道图片/按次计费，不回退分组图片价格 |
| `per_request` | 走渠道图片/按次计费，不回退分组图片价格 |

渠道 `image` / `per_request` 计费时，传给渠道定价的 tier 选择仍受 OpenAI 分组质量计费开关影响：

```text
OpenAI group image_quality_billing=false -> tier = 1K / 2K / 4K
OpenAI group image_quality_billing=true  -> tier = low / medium / high
```

这允许渠道对同一个 OpenAI 生图模型覆盖不同质量或分辨率档位的价格。

### 7.2 渠道没有定义该模型收费方式

如果渠道没有定义该模型的收费方式，则回退到 OpenAI 分组图片价格：

```text
image_quality_billing=false -> image_price_1k / image_price_2k / image_price_4k
image_quality_billing=true  -> image_price_low / image_price_medium / image_price_high
```

回退时仍按 `image_count` 乘以单张价格计费，再应用现有图片倍率规则：

- `image_rate_independent=false`：共享当前有效分组倍率。
- `image_rate_independent=true`：使用 `image_rate_multiplier`。

### 7.3 非 OpenAI 分组

非 OpenAI 分组不启用质量计费开关。即使请求或响应中存在 quality，也不改变其现有分组或渠道计费逻辑。

## 8. 成本计算细节

OpenAI 生图成本计算应拆出两个概念：

- `imageSizeTier`：尺寸审计层级，取值 `1K` / `2K` / `4K` / `mixed`。
- `imageBillingTier`：实际计费层级，取值可能是 `1K` / `2K` / `4K`，也可能是 `low` / `medium` / `high`。

伪代码：

```text
imageSizeTier = NormalizeImageBillingTierOrDefault(result.ImageSize)

if group.platform == "openai" and group.image_quality_billing:
    imageQuality = NormalizeOpenAIImageQualityOrLow(result.Usage.Quality)
    imageBillingTier = imageQuality
else:
    imageQuality = NormalizeOpenAIImageQualityOrEmpty(result.Usage.Quality)
    imageBillingTier = imageSizeTier

if channel pricing is explicitly resolved for model:
    if mode == token:
        use token billing
    if mode == image/per_request:
        use channel per-request price with imageBillingTier
else:
    if openai quality billing:
        use group image_price_low/medium/high with imageBillingTier
    else:
        use group image_price_1k/2k/4k with imageSizeTier
```

注意：现有逻辑中如果 `resolveOpenAIChannelPricing` 返回 nil 或非 token 会进入图片计费。实现时要区分“渠道没有定义该模型收费方式”和“渠道定义了 image/per_request 收费方式”，避免把未定义渠道价误判成渠道覆盖。

## 9. Usage 落库

使用记录应保存以下字段：

| 字段 | 写入规则 |
| --- | --- |
| `image_count` | 实际生成图片数量 |
| `image_size` | 继续写尺寸层级：`1K` / `2K` / `4K` / `mixed` |
| `image_quality` | OpenAI 生图写归一化后的 usage quality；空/非法按 `low` 写入 |
| `billing_tier` | 写实际计费层级：`1K` / `2K` / `4K` 或 `low` / `medium` / `high` |
| `billing_mode` | 生图按张计费写 `image`；渠道 token 模式写 `token` |

兼容性要求：

- 不修改 `image_size` 的语义。
- 不破坏 `usage_logs_image_billing_size_check`。
- 历史记录没有 `image_quality` 时前端应正常展示。

## 10. 前端使用记录

管理端和用户端使用记录需要展示图片计费维度。

### 10.1 类型和接口

前端 `UsageLog` 类型增加：

```ts
image_quality?: string | null
```

后端 usage DTO / mapper 增加：

```json
"image_quality": "low|medium|high|null"
```

### 10.2 表格展示

当 `billing_mode = image` 时：

```text
billing_tier = high -> 图片 / high
billing_tier = 2K   -> 图片 / 2K
```

如果 `billing_tier` 为空：

- 有 `image_size`：显示 `图片 / {image_size}`。
- 有 `image_quality`：显示 `图片 / {image_quality}`。
- 都没有：显示 `图片`。

### 10.3 Tooltip / 详情展示

图片使用记录详情中展示：

- 图片数：`image_count`
- 尺寸：`image_size`
- 质量：`image_quality`
- 计费层级：`billing_tier`
- 计费模式：`billing_mode`

历史兼容：

- 旧记录 `image_quality = null` 时不显示质量，或显示 `-`。
- 旧图片记录没有 `billing_tier` 时，继续按现有逻辑展示，不强行回填 `2K`。

## 11. 后端改动清单

### 11.1 Schema / 迁移

本地 schema 补齐线上字段：

- `backend/ent/schema/group.go`
  - `image_quality_billing`
  - `image_price_low`
  - `image_price_medium`
  - `image_price_high`
- `backend/ent/schema/usage_log.go`
  - `image_quality`

新增迁移 SQL 使用 `ADD COLUMN IF NOT EXISTS`，确保线上已有字段时安全跳过。

### 11.2 Service 模型

- `service.Group` 增加质量计费字段。
- `service.UsageLog` 增加 `ImageQuality *string`。
- `ImagePriceConfig` 或等价结构增加 low/medium/high 价格。
- 增加：

```go
NormalizeOpenAIImageQualityOrLow(value string) string
NormalizeOpenAIImageQualityOrEmpty(value string) string
```

### 11.3 Repository / DTO / Mapper

- `group_repo` create/update/select 补齐新字段。
- `api_key_repo` 加载 group 时补齐新字段，否则计费路径拿不到开关和价格。
- `usage_log_repo` insert/select/scan 补齐 `image_quality`。
- DTO mapper 补齐 group 和 usage log 的新字段。

### 11.4 OpenAI 生图解析

- OpenAI image APIKey 路径、OAuth codex images 路径、Responses image_generation 路径都要确认 usage 解析。
- 只从 usage 对象读取 `quality`。
- `OpenAIUsage` 或 `OpenAIForwardResult` 中增加质量字段，避免下游再从非 usage 原始 JSON 取值。

### 11.5 计费

- `calculateOpenAIImageCost` 增加 OpenAI 质量计费分支。
- 明确渠道定价命中与未命中的区别。
- 渠道 `image/per_request` 命中时使用 `imageBillingTier`。
- 渠道未命中时回退分组价格。
- 非 OpenAI 分组保持现有路径。

## 12. 前端改动清单

- `frontend/src/types/index.ts`
  - Group 类型增加 `image_quality_billing` / `image_price_low` / `image_price_medium` / `image_price_high`
  - UsageLog 类型增加 `image_quality`
- `frontend/src/views/admin/GroupsView.vue`
  - 仅 OpenAI 分组展示“是否质量计费”
  - 开启后展示 low/medium/high 价格
  - 关闭时展示 1K/2K/4K 价格
  - create/edit payload 补齐字段
- i18n
  - 增加质量计费相关文案
- 管理端和用户端使用记录
  - 展示 `image_quality`
  - 展示 `billing_tier`
  - 图片记录 tooltip/详情补齐质量字段

## 13. 测试要求

### 13.1 后端单测

- OpenAI 分组 `image_quality_billing=false` 时仍按 `1K` / `2K` / `4K` 计费。
- OpenAI 分组 `image_quality_billing=true` 且 `usage.quality=high` 时按 `image_price_high` 计费。
- OpenAI 分组 `image_quality_billing=true` 且 `usage.quality=""` 时按 `image_price_low` 计费。
- 请求 body 有 `quality=high`，但返回 usage 没有 quality 时，仍按 `low` 计费。
- 返回 tools/output item 有 `quality=high`，但 usage 没有 quality 时，仍按 `low` 计费。
- 渠道 `image/per_request` 定价命中时优先使用渠道价格。
- 渠道未定义该模型收费方式时回退 OpenAI 分组价格。
- 渠道 `token` 定价命中时走 token 计费，不回退分组图片价格。
- 非 OpenAI 分组不受 `image_quality_billing` 影响。
- `usage_logs.image_size` 不写入 `low` / `medium` / `high`。
- `usage_logs.image_quality` 和 `billing_tier` 正确落库。

### 13.2 前端测试

- OpenAI 分组显示质量计费 checkbox。
- 非 OpenAI 分组不显示质量计费 checkbox。
- checkbox 默认关闭。
- checkbox 开启后显示 low/medium/high 价格输入。
- checkbox 关闭时显示 1K/2K/4K 价格输入。
- 创建/编辑 payload 正确包含质量计费字段。
- 使用记录正确展示：
  - `图片 / high`
  - `图片 / 2K`
  - 历史无 `image_quality` 数据时不报错。

## 14. 验收标准

- 管理员可以在 OpenAI 分组中开启质量计费并配置 low/medium/high 单价。
- OpenAI 生图返回 usage quality 时，系统按该 quality 对应价格扣费。
- usage quality 缺失时，系统按 low 扣费。
- 渠道定价覆盖模型时，优先使用渠道价格。
- 渠道未定义模型价格时，回退分组按分辨率或按质量价格。
- 使用记录中能明确看到图片请求的质量、尺寸和实际计费层级。
- 非 OpenAI 分组行为不变化。
