# OAuth 生图改走专用 codex 图片端点 — 设计文档

> 日期：2026-06-19
> 范围：仅链路 A（专用 Images API）的 **OAuth 账号路径**。APIKey 路径与链路 B（Codex Responses 透传）不动。
> 背景与代码地图见 [docs/CODEX_IMAGE_GENERATION.md](../../CODEX_IMAGE_GENERATION.md)。

## 1. 目标

OAuth 账号下生图当前的实现是：把 `/v1/images/*` 请求**转换成 Responses-API 的 `image_generation` 工具请求**，发往 `https://chatgpt.com/backend-api/codex/responses`，再解析 SSE `response.completed`。

改为**直接调用两个专用 codex 图片端点**：

| 用途 | 方法 | URL |
|---|---|---|
| 文生图 | POST | `https://chatgpt.com/backend-api/codex/images/generations` |
| 图生图/编辑 | POST | `https://chatgpt.com/backend-api/codex/images/edits` |

这两个端点直接收发**原生 OpenAI images 格式**（请求 `{model,prompt,size,quality,n,stream,...}`，响应 `{created,data[].b64_json,usage,size,quality,output_format,...}`）。

## 2. 决策记录

- **范围**：仅链路 A 的 OAuth 路径。
- **替换策略**：全量替换，不加配置开关；旧的 Responses 生图路径在 OAuth 下不再调用，相关专用函数删除。
- **流式**：保留。客户端传 `stream:true` → 网关向上游也发 `stream:true`，按 SSE 解析；`stream:true && n>1` → 返回 `400 unsupported_parameter`（"Streaming is only supported with n=1."）。
- **TLS / 指纹伪装 / 协议版本**：全部复用项目现有机制（`HTTPUpstreamProfileOpenAI` 的协议模式配置 `openai_h1/openai_h2/openai_h1_fallback`、`overrideBrowserUserAgent` 浏览器 UA 兜底、`codexCLIUserAgent` 常量），不新造。HTTP/1.1 由现有协议模式配置控制，运维侧已可强制。

## 3. 改动边界

**保持不变**：路由；`OpenAIGatewayHandler.Images`；`ForwardImages` 按账号类型分流；计费 `calculateOpenAIImageCost`（仍按 `ImageCount` × `ImageSize` 尺寸档）；`forwardOpenAIImagesOAuth` 函数签名；`OpenAIForwardResult` 字段；下游 SSE 写出/keepalive/计费框架。

**替换 OAuth 路径内部三件事**：

1. 请求 URL：`codex/responses` → 两个专用端点。
2. 请求体构建：注入 `image_generation` 工具的 Responses body → 原生 images 请求体（generations=JSON，edits=multipart）。
3. 响应解析：SSE `response.completed` 解析 → 原生 `data[].b64_json` + `usage` 解析。

**删除**（OAuth 路径不再调用、APIKey 路径不依赖）：`buildOpenAIImagesResponsesRequest` 及其专属解析链 `collectOpenAIImagesFromResponsesBody`、`extractOpenAIImagesFromResponsesCompleted`、`usageFromOpenAIImageGenToolUsageRaw` 等。删除前确认无其它引用。

## 4. 新增常量与请求构建

```go
chatgptCodexImagesGenerationsURL = "https://chatgpt.com/backend-api/codex/images/generations"
chatgptCodexImagesEditsURL       = "https://chatgpt.com/backend-api/codex/images/edits"
```

新增专用请求构建器 `buildOpenAIImagesCodexUpstreamRequest`（不复用 `buildUpstreamRequest`——后者绑死 responses URL / path-suffix / compact 等逻辑）。按 handoff §2 设请求头：

| Header | 值 |
|---|---|
| `Authorization` | `Bearer <token>`（复用 `GetAccessToken`） |
| `chatgpt-account-id` | `account.GetChatGPTAccountID()` |
| `OpenAI-Beta` | `responses=experimental` |
| `originator` | `codex_cli_rs` |
| `session_id` | 随机 UUID v4（`uuid.NewString()`） |
| `User-Agent` | `codexCLIUserAgent` 常量 + `overrideBrowserUserAgent` 兜底 |
| `Accept` | 非流式 `application/json` / 流式 `text/event-stream` |
| `Content-Type` | generations: `application/json`；edits: multipart（writer 自动带 boundary，不手设） |

`req.Host = "chatgpt.com"`，请求走 `HTTPUpstreamProfileOpenAI`（协议模式由现有配置控制）。

## 5. 请求体

### generations（文生图）
原生 JSON，仅在字段非空时写入：

```json
{ "model": "<requestModel>", "prompt": "...", "size": "1024x1024",
  "quality": "medium", "n": 1, "stream": false, "output_format": "png", "background": "opaque" }
```

`model` 用解析后的 `requestModel`（经 `validateOpenAIImagesModel`，默认 `gpt-image-2`）。

### edits（图生图/编辑）
`multipart/form-data`：

- `image`：来自 `parsed.Uploads` 原始字节；若图片来源是 `InputImageURLs` 中的 data URL，则解码为字节。多图 → 多个 `image` 字段。
- `mask`（可选）：来自 `parsed.MaskUpload` 或 `MaskImageURL`（data URL 解码）。
- `prompt`、`model`、`size?`、`quality?`、`n`、`stream?`。

**边界**：edits 图片来源为远程 http URL（既非上传字节也非 data URL）→ multipart 无字节可用 → 返回 400 明确报错，不静默。当前客户端基本走 multipart 上传或 data URL，影响面小。

### 校验
`parsed.Stream && parsed.N > 1` → 构建前返回 `400`：

```json
{ "code": "unsupported_parameter", "message": "Streaming is only supported with n=1." }
```

## 6. 响应解析

### 非流式
解析原生 JSON：

- `data[i].b64_json` → `openAIResponsesImageResult{Result, Size, OutputFormat, Quality, Background}`。
- 顶层 `size/quality/output_format/background` → `FirstMeta`。
- `usage` 整段保留为 `UsageRaw`，并映射到 `OpenAIUsage`（`output_tokens_details.image_tokens` 等）。
- `response_format=url` 时复用现有 `buildOpenAIImagesAPIResponse` 转 data URL。
- `data` 为空 → 走现有 empty-output failover（`errOpenAIImagesEmptyOutputRetryable` / `newOpenAIImagesEmptyOutputFailoverError`）。

### 流式
按 SSE 逐 `data:` 行 JSON 解析（防御式，不依赖具体 event 名）：

- 带 `b64_json` 的行 → 最终图。
- 带 `usage` 的行 → 用量。
- 遇 `data: [DONE]` 结束。

产物与非流式同构。复用现有 `forwardOpenAIImagesOAuthStreaming` 的下游 SSE 写出 / keepalive / 计费框架，仅替换"解析上游行"的部分。

### 结果回填
`OpenAIForwardResult`：`ImageCount=len(results)`、`ImageOutputSizes`（每张返回 size）、`ImageSize=parsed.SizeTier`、`ImageInputSize=parsed.Size`、`Usage`。计费与日志逻辑不变。

## 7. 错误处理与重试兼容性（硬约束）

重试/切号逻辑分两层，本次改动**只改第二层的"翻译"**，第一层保持不动：

- **第一层 = handler 的切号/failover 循环**（`handler/openai_images.go` 的 `for` 大循环，即二开保留的那套）：special-transient 切号（≤3）、empty-output failover（≤1）、pool-mode 同号重试、429 stop 判定。**完全靠错误类型驱动，不改**。
- **第二层 = service OAuth 路径**（`forwardOpenAIImagesOAuthOnce` / `...Streaming`）：把上游响应翻译成第一层认识的错误类型。本次只改这一层。

> 第一层依赖错误**分类**，分类依赖解析上游**错误体**。新端点返回的是原生 images 错误格式（与旧 Responses SSE 错误帧不同），因此第二层翻译必须对新格式做到**等价映射**，否则会间接改变重试行为。这是本次改动唯一真正影响重试的风险点。

复用现有 OAuth 路径错误框架，并满足以下**重试兼容性约束**（实现 + 单测均需覆盖）：

1. **错误体分类等价**：新端点错误体（`{"error":{...}}` 或 `{"code":...,"message":...}`，状态码分布可能不同）必须能被 `extractUpstreamErrorMessage` / `shouldFailoverOpenAIUpstreamResponse` / `handleOpenAIImagesErrorResponse` / `extractOpenAIImagesUpstreamError` 正确解析，使 failover vs 用户错误 vs special-transient 的判定与旧路径一致。必要时补全这些函数对新格式的解析。
2. **空输出信号映射**：旧的"`response.completed` 无 `image_generation_call`"判据，改为"`data` 为空数组（或无 `b64_json`）"，并映射到同一个 `errOpenAIImagesEmptyOutputRetryable` → `newOpenAIImagesEmptyOutputFailoverError`，确保 empty-output failover（≤1 次切号）照常触发。
3. **403 cf-mitigated challenge**：靠正确的 codex 前缀 URL + 指纹头规避（本设计已满足）；若仍触发（返回 HTML / `cf-mitigated: challenge`），必须归类为**可重试/failover**，不能当终态错误直接返回客户端。
4. **流式去重保持**：重试/切号前先清除上一次的可重试上游错误，避免向客户端写出重复 SSE error 帧（沿用既有 [[discard_error_before_retry]] 行为）；新流式解析路径必须保留这一点。

- `resp.StatusCode >= 400` → `shouldFailoverOpenAIUpstreamResponse` 判定 failover / 切号，否则 `handleOpenAIImagesErrorResponse` 透传；保持现有 `OpenAIImagesUpstreamError` / `UpstreamFailoverError` 语义。
- 空响应 / 首字节超时：复用现有上游错误与重试逻辑。

## 8. 不在本次范围

- APIKey 路径（`forwardOpenAIImagesAPIKey`）。
- 链路 B（`/backend-api/codex/responses` 透传 + image_generation 工具，含 Spark 限制）。
- 计费规则本身（仅保证字段正确回填）。

## 9. 验证

- `gofmt` / `go build ./...` / `go vet`。
- 端点 URL、请求头、请求体（generations JSON / edits multipart）、`stream+n>1` 400、非流式与流式响应解析、空输出 failover 的单元测试。
- **重试兼容性测试**（section 7 的 4 条约束）：新端点错误体 → 正确的错误类型/分类（failover / 用户错误 / special-transient）、`data` 为空 → empty-output failover、403 challenge → 可重试、流式重试不重复 error 帧。
- generations 已有实测确认；edits 仅确认端点可达，实现后以实际返回为准。
