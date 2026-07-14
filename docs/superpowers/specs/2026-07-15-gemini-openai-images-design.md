# Gemini 分组支持 OpenAI Images 协议（透传模式）设计

- 日期：2026-07-15
- 状态：已确认（待实现）
- 分支：`feature/gemini-openai-images`（基于 `release` @ 0b89809a 切出；`pre-release` 当前落后于 release，本设计引用的代码锚点以 release 为准，完成后合入 `pre-release` 验证再进 `release`）

## 背景与目标

当前 `/v1/images/generations`、`/v1/images/edits`（OpenAI Images 协议）只对 OpenAI、Grok 两个平台分组开放，Gemini 分组请求会在 `backend/internal/server/routes/gateway.go` 的 `imagesHandler`（约 :45）落入 default 分支返回 404。

目标：Gemini 平台分组的 API Key 也能调用这两个端点。部署前提：Gemini 账号的 `base_url` 指向的上游池已支持 OpenAI Images 格式，因此采用**直接透传**而非翻译为 Gemini 原生 `generateContent`。

## 已确认的需求决策

| 决策点 | 结论 |
| --- | --- |
| 端点范围 | generations + edits 都支持 |
| 转发方式 | 透传 OpenAI Images 格式到 `{账号 base_url}/v1/images/*`，不做协议翻译 |
| 模型名 | 严格校验：映射后模型必须匹配 `gemini-*image*`；其他名字靠已有分组模型映射（channel mapping）兼容，映射不到报 `model_not_found` |
| 账号类型 | 仅 AI Studio API Key 账号（`AccountTypeAPIKey`）；OAuth / Vertex SA / Antigravity 混调账号选号时排除 |
| response_format | 透传，`url` 与 `b64_json` 均由上游池决定并原样返回 |
| stream | 第一版不支持，`stream=true` 返回 400 |
| n | 不限制，透传给上游池 |
| size 等其余参数 | 透传，不做映射 |

## 架构

复刻 Grok 接入模式（平台分发 → 平台专属 handler → service 层转发），三层新增：

### 1. 路由分发

`backend/internal/server/routes/gateway.go` `imagesHandler`（:45）增加：

```go
case service.PlatformGemini:
    h.OpenAIGateway.GeminiImages(c)
```

与 Grok 相同，`GeminiImages` 挂在 `OpenAIGatewayHandler` 上（复用其生图槽/用户槽/审核/计费编排设施）；选号所需的 `GeminiMessagesCompatService` 通过构造函数注入该 handler（wire 接线同步更新）。

`/v1/images/generations|edits` 的全部注册点（含 `/openai` 前缀别名与根路径别名）共用该 lambda，自动生效。

### 2. Handler：`GeminiImages`

新文件 `backend/internal/handler/gemini_images.go`，编排骨架对齐 `handleGrokMedia`（`backend/internal/handler/grok_media.go:49`）：

1. 鉴权上下文、读 body（复用现有大小限制与 `ReadRequestBodyWithPrealloc`）。
2. 解析复用 `ParseOpenAIImagesRequest`（`backend/internal/service/openai_images.go`，JSON 与 multipart 均已支持）。
3. 门槛：`GroupAllowsImageGeneration`；内容审核 `ContentModerationProtocolOpenAIImages`；`stream=true` 返回 400（`invalid_request_error`）。
4. 分组模型映射 `ResolveChannelMappingAndRestrict` → 校验**映射后**模型匹配 `gemini-*image*`（大小写不敏感，语义与运营报表 `model ILIKE 'gemini-%image%'` 一致），不匹配返回 `model_not_found`。
5. 并发：生图槽（`acquireImageGenerationSlot` 同款机制）+ 用户槽 + 账号槽，及 ops 上下文埋点，与 OpenAI/Grok 生图一致。
6. 选号循环：`GeminiMessagesCompatService` 新增 `SelectGeminiAPIKeyAccountForImages`（内部复用 `listSchedulableAccountsOnce` + `selectBestGeminiAccount`，`backend/internal/service/gemini_messages_compat_service.go`），过滤条件：`Platform == gemini && Type == apikey`，不使用粘性会话；失败账号排除、切号上限、池模式同号重试策略对齐 Grok handler。v1 简化：上游失败仅做本次请求内切号，不做账号级临时停调/健康统计。
7. 转发成功后异步 `RecordUsage`（见 §4）。

### 3. Service：透传转发

新文件 `backend/internal/service/gemini_images_passthrough.go`：

- 上游 URL：`{GetGeminiBaseURL(account)}/v1/images/generations|edits`（`backend/internal/service/account.go:889`），沿用账号代理设置。
- 认证：`Authorization: Bearer {api_key}`（OpenAI 协议语义；不发 `x-goog-api-key`）。
- 请求体：原样透传。仅当模型映射改名时改写 `model` 字段——JSON 直接改字段；multipart 重编码替换 `model` 一个 part，其余字节不动。
- 响应：状态码、body（`data[].url` / `data[].b64_json` / `usage`）原样回写客户端，同时旁路解析一份用于计费。
- 错误分类：429/5xx 包装为 `UpstreamFailoverError` 触发切号；4xx 用户错误原样透传、不切号、账号记成功调度。复用 `openai_images.go` 现有错误分类工具。

### 4. 计费与报表

- 完全复用现有 OpenAI 生图计费管线（`RecordUsage` → `calculateOpenAIRecordUsageCost`）：`ImageCount > 0` 时默认按张计费（张数取响应 `data[]` 数量、缺省回退请求 `n`；档位由请求 `size` 经 `NormalizeImageBillingTierOrDefault` 归一化），分组/渠道定价配置为 token 模式时自动改按 token 计费；响应 `usage` 的 token 数同步入账用于统计。
- 记账 platform=gemini、model=映射后模型名。运营生图报表（`backend/internal/repository/operation_image_report_repo.go:27`，`platform='gemini' AND model ILIKE 'gemini-%image%'`）自动覆盖，报表零改动。

## 错误处理

- 无可用账号：对齐 OpenAI images 的 `classifyNoAccountError` 语义（区分 model_not_found 与无可用账号）。
- 上游 429/5xx：切号重试，超上限后 `handleFailoverExhausted` 返回最后一次上游错误。
- 上游 4xx：透传原始状态码与 body。
- `stream=true` / 分组未开生图 / 模型不匹配：进入转发前直接 4xx。

## 测试

- service 单测：URL 构造、model 改写（JSON 与 multipart 两路）、错误分类（429/5xx/4xx）、usage 提取（有/无 usage 两种响应）、按张计费退化路径。
- handler 单测（仿 `backend/internal/service/openai_gateway_grok_test.go` 假上游模式）：平台分发命中 Gemini 分支、非 API Key 账号被过滤、stream 400、模型校验 404、切号循环与耗尽、成功路径响应透传。

## 明确不做（第一版）

- stream 流式生图（后续可做 SSE 直通 + 末帧 usage 解析）。
- 真·Google AI Studio 原生协议翻译（透传要求 base_url 上游支持 OpenAI images 格式；`generativelanguage.googleapis.com` 不满足）。
- OAuth / Vertex SA / Antigravity 账号。
- 图片托管/存储（`url` 由上游池负责生成与托管）。
