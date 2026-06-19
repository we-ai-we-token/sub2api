# Codex 生图代码地图（Handoff）

> 上游为 OAuth (ChatGPT) 账号时，所有请求最终都打到
> `https://chatgpt.com/backend-api/codex/responses`
> 常量：`backend/internal/service/openai_gateway_service.go:41` (`chatgptCodexURL`)
>
> 上游请求构建（按账号类型选 URL/Host/header）：
> `buildUpstreamRequest` — `backend/internal/service/openai_gateway_service.go:4148`
> （OAuth → `chatgptCodexURL`，并设 `req.Host = "chatgpt.com"` + `chatgpt-account-id`）

Codex 相关的"生图"有**两条独立链路**，不要混淆：

---

## 链路 A：专用 Images API（`/v1/images/generations`、`/v1/images/edits`）

客户端直接调用 OpenAI 图片接口。OAuth 账号下，当前实现**直接调用两个专用 codex 图片端点**（`/backend-api/codex/images/generations`、`/backend-api/codex/images/edits`），收发原生 OpenAI images 格式。早期实现是把请求**转换成 Responses-API 的 `image_generation` 工具请求**发往 `codex/responses`，已被替换，保留在下方「旧实现（Responses image_generation 工具，保留备查）」一节。

> **上游请求体格式（网关→codex 端点）**：generations 与 edits **均为 `application/json`**。edits 的图片输入以 `images[].image_url` 的 base64 data URL 内联、mask 为 `mask.image_url`——codex edits 端点**拒绝 `multipart/form-data`**（返回 `{"detail":"Unsupported content type"}`）。
>
> 这只影响**网关→上游**这一段；**客户端→网关**入站仍同时支持 multipart 上传与 JSON（`parseOpenAIImagesMultipartRequest` / `parseOpenAIImagesJSONRequest`），客户端调用方式不变。APIKey 路径走公网 `api.openai.com`，edits 仍按公共 API 用 multipart。

| 阶段 | 位置 |
|---|---|
| 路由 | `backend/internal/server/routes/gateway.go:105,118`（platform 分组内）和 `:187,200`（顶层）。仅 `PlatformOpenAI` 分组可用 |
| Handler | `OpenAIGatewayHandler.Images` — `backend/internal/handler/openai_images.go:35`（调度账号、重试/切账号循环、计费记录都在这里） |
| 请求解析 | `parseOpenAIImagesJSONRequest` / `parseOpenAIImagesMultipartRequest` — `backend/internal/service/openai_images.go`（约 245、390 行附近，含 `n`/`size`/`quality` 校验） |
| Service 入口 | `ForwardImages` — `backend/internal/service/openai_images.go:538`（按 `account.Type` 分流） |
| └ APIKey 路径 | `forwardOpenAIImagesAPIKey` — `backend/internal/service/openai_images.go:559`（走 `api.openai.com/v1/images` 或自定义 base_url） |
| └ OAuth 路径 | `forwardOpenAIImagesOAuth` — `backend/internal/service/openai_images_responses.go:1388`（**这条才是 codex 生图**） |
| &nbsp;&nbsp;├ 非流式单次 | `forwardOpenAIImagesOAuthOnce` — `:1443` |
| &nbsp;&nbsp;├ 流式 | `forwardOpenAIImagesOAuthStreaming` — `:1582` |
| &nbsp;&nbsp;├ 上游请求体构建 | `buildOpenAIImagesCodexRequestBody` — `backend/internal/service/openai_images_codex.go:26`（generations=JSON `buildOpenAIImagesCodexGenerationsBody:33`；edits=JSON `buildOpenAIImagesCodexEditsBody:56`，图片走 `images[].image_url` data URL；端点 URL 常量 `:18-19`） |
| &nbsp;&nbsp;├ 上游请求构建 | `buildOpenAIImagesCodexUpstreamRequest` — `openai_images_codex.go:241`（设 codex 请求头与 `Content-Type`） |
| &nbsp;&nbsp;└ 响应解析 | 非流式 `parseOpenAIImagesCodexNonStreamingOutput` — `openai_images_codex.go:164`；流式逐行 `parseOpenAIImagesCodexStreamLine` — `:290`；下游写出 `handleOpenAIImagesOAuthNonStreamingResponse` — `openai_images_responses.go:984` |
| 共享返回结构 | `openAIImagesOAuthForwardOutput`（struct）、`OpenAIForwardResult` — `openai_images_responses.go` / `openai_gateway_service.go` |
| failover/重试错误类型 | `OpenAIImagesUpstreamError`、`IsRetryableOpenAIImagesUpstreamError`、`newOpenAIImagesEmptyOutputFailoverError`、`IsOpenAIImagesEmptyOutputFailoverError` — `openai_images_responses.go` 顶部（约 50–70 行） |

### 旧实现（Responses `image_generation` 工具，保留备查）

> 早期 OAuth 生图把 `/v1/images/*` 转换成 Responses-API 的 `image_generation` 工具请求，发往 `/backend-api/codex/responses`，再解析 `response.completed`。已被上方专用 codex 端点替换；下列请求体构建/解析函数（`buildOpenAIImagesResponsesRequest` 等）已在 commit `a6a7b0cf` 删除，仅作背景参考，**勿据此行号查找现有代码**。

| 阶段（历史） | 说明 |
|---|---|
| Images→Responses 请求体构建 | `buildOpenAIImagesResponsesRequest`（已删除）：注入 `image_generation` 工具，输出 Responses 请求体；图片输入走工具的 `input_image.image_url`（base64 data URL） |
| SSE/输出提取 | `extractOpenAIImagesFromResponsesCompleted` / `collectOpenAIImagesFromResponsesBody` 等（已删除）：`response.completed` 解析、图片 base64/URL 抽取 |
| 空输出判据 | 旧：`response.completed` 无 `image_generation_call`；新：原生响应 `data` 为空数组（映射到同一个 `errOpenAIImagesEmptyOutputRetryable`） |

### 计费
- 入口 `calculateOpenAIImageCost` — `backend/internal/service/openai_gateway_service.go:6197`
- 按尺寸分桶字段 `ImageSizeBreakdown`（map[size]count）：定义/归一在 `backend/internal/service/image_billing_size.go`（`normalizeImageSizeBreakdown`、`SortedImageBillingBreakdownKeys`），并落库到 ent 字段 `image_size_breakdown`（`backend/ent/usagelog*.go`）、DTO `backend/internal/handler/dto/types.go`。
  > 注意：此 breakdown 字段早于 n fan-out 引入，独立于本仓库已移除的 n 参数功能。

---

## 链路 B：Codex Responses 透传 + `image_generation` 工具（`/backend-api/codex/responses`、`/v1/responses`、`/responses`）

Codex CLI / Responses 客户端直接发 Responses 请求，请求里带（或需要被注入）`image_generation` 工具。这里**不做 Images→Responses 转换**，而是对透传的 Responses body 做 Codex 适配。

| 阶段 | 位置 |
|---|---|
| 路由 | `backend/internal/server/routes/gateway.go:148-165`：`/responses`、`/responses/*subpath`、以及 `codexDirect := r.Group("/backend-api/codex")` 下的 `/responses` |
| Handler（非流式/SSE） | `OpenAIGatewayHandler.Responses` — `backend/internal/handler/openai_gateway_handler.go:137` |
| Handler（WebSocket） | `OpenAIGatewayHandler.ResponsesWebSocket` — `backend/internal/handler/openai_gateway_handler.go:1145` |
| Codex OAuth 请求变换 | `applyCodexOAuthTransform` — `backend/internal/service/openai_codex_transform.go:106`（`...WithOptions` 在 `:113`） |
| 模型映射 | `codexModelMap` / `normalizeCodexModel` — `openai_codex_transform.go`（顶部，约 11–60 行） |

### `image_generation` 工具相关（都在 `backend/internal/service/openai_codex_transform.go`）
| 功能 | 函数 / 行 |
|---|---|
| 检测请求是否带 image_generation 工具 | `hasOpenAIImageGenerationTool` — `:584` |
| 归一化 image_generation 工具字段 | `normalizeOpenAIResponsesImageGenerationTools` — `:639` |
| 确保注入 image_generation 工具 | `ensureOpenAIResponsesImageGenerationTool` — `:679` |
| 注入"桥接"指令（告诉 Codex CLI 用原生 image_generation，别因缺 `image_gen` 命名空间就退回 CLI fallback） | `applyCodexImageGenerationBridgeInstructions` — `:717`；文案常量 `codexImageGenerationBridgeText` — `:84` |
| 检测请求是否含图片输入 | `hasOpenAIInputImage` / `hasOpenAIInputImageValue` — `:605` / `:612` |
| 判断是否图片生成模型 | `isOpenAIImageGenerationModel` — `:514` 附近 |

### Spark 模型限制（gpt-5.3-codex-spark 不支持生图/图片输入）
| 功能 | 函数 / 行 |
|---|---|
| 是否 Spark 模型 | `isCodexSparkModel` — `:580` |
| 校验 Spark + 图片输入 → 报错 | `validateCodexSparkInput` — `:632` |
| 注入"Spark 不支持生图"说明指令 | `applyCodexSparkImageUnsupportedInstructions` — `:740`；文案常量 `codexSparkImageUnsupportedText` — `:86`；在 `applyCodexOAuthTransform` 中触发 — `:224` |

---

## 速查：从哪进
- 客户端走 **`/v1/images/*`** → 链路 A，看 `openai_images.go` + `openai_images_responses.go`。
- 客户端走 **`/backend-api/codex/responses` 或 `/responses`** 带 image_generation → 链路 B，看 `openai_codex_transform.go` 的 image/spark 段。
- 两条链路对 OAuth 账号最终都经 `buildUpstreamRequest`（`openai_gateway_service.go:4148`）打到 `chatgptCodexURL`。

> 历史背景：本仓库曾有一个"Images OAuth `n>1` fan-out"功能（commit `c1b33d83`），已在 commit `09983ff4` 移除。当前 OAuth 生图对 `n>1` 不再 fan-out，按单次请求处理。
