# Codex 专用图片端点 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **⚠️ 实现修正（上线后，commit `1350cdcb`）**：本计划中"edits 请求体用 `multipart/form-data`"的设计**未经上游实测，且是错误的**。codex `images/edits` 端点拒绝 multipart（返回 `{"detail":"Unsupported content type"}`），只接受 `application/json`——图片输入须以 `images[].image_url` 的 base64 data URL 内联、mask 为 `mask.image_url`。实际实现以此为准（`buildOpenAIImagesCodexEditsBody`）。下文 multipart 相关的描述与代码示例保留为原始计划记录，**勿照抄**。

**Goal:** 把 OAuth 账号的生图从"注入 image_generation 工具走 `/backend-api/codex/responses`"改为直接调用两个原生端点 `/backend-api/codex/images/generations` 与 `/backend-api/codex/images/edits`，保持重试/计费/客户端行为不变。

**Architecture:** 只改链路 A 的 OAuth 路径内部三件事——请求 URL、请求体（generations=JSON / edits=multipart）、响应解析（原生 `data[].b64_json`+`usage`）。handler 的切号/failover 循环、`ForwardImages` 分流、`forwardOpenAIImagesOAuth` 签名、`OpenAIForwardResult` 字段、计费全部不动。新代码集中在新文件 `openai_images_codex.go`。

**Tech Stack:** Go，gin，tidwall/gjson + sjson，google/uuid，`mime/multipart`。测试用标准 `testing`。

## Global Constraints

- 仅链路 A 的 OAuth 路径。APIKey 路径（`forwardOpenAIImagesAPIKey`）与链路 B（codex/responses 透传）不动。
- 全量替换，不加配置开关。
- 端点：`https://chatgpt.com/backend-api/codex/images/generations`、`https://chatgpt.com/backend-api/codex/images/edits`。
- 请求头（两端点通用）：`Authorization: Bearer <token>`、`chatgpt-account-id`、`OpenAI-Beta: responses=experimental`、`originator: codex_cli_rs`、`session_id: <uuid v4>`、`User-Agent: codexCLIUserAgent`、`Accept: application/json`（非流式）/ `text/event-stream`（流式）。`req.Host = "chatgpt.com"`。
- TLS/指纹/协议版本全部复用现有机制：`HTTPUpstreamProfileOpenAI`、`overrideBrowserUserAgent`、`codexCLIUserAgent` 常量。不新造。
- `model` 用解析后的 `requestModel`（经 `validateOpenAIImagesModel`，默认 `gpt-image-2`）。
- `stream:true && n>1` → 返回 `400 {"code":"unsupported_parameter","message":"Streaming is only supported with n=1.","type":"invalid_request_error","param":"n"}`。
- 重试兼容性硬约束（见 spec section 7）：错误体分类等价、`data` 空 → empty-output failover、403 cf-challenge 归类可重试、流式重试不重复 error 帧。
- 工作目录为仓库根；Go module 在 `backend/`，所有 `go` 命令在 `backend/` 下执行。
- Spec：`docs/superpowers/specs/2026-06-19-codex-images-dedicated-endpoint-design.md`。代码地图：`docs/CODEX_IMAGE_GENERATION.md`。

---

### Task 1: 顶层 error code 回退（重试分类前置修复）

新端点的扁平错误体 `{"code":"...","message":"..."}` 当前无法被 `extractUpstreamErrorCode` 解析（只认 `error.code`），导致按 code 的重试分类漏判。先补这个回退。

**Files:**
- Modify: `backend/internal/service/gateway_service.go:7658-7679`（`extractUpstreamErrorCode`）
- Test: `backend/internal/service/gateway_service_test.go`（已存在则追加）

**Interfaces:**
- Consumes: 无。
- Produces: `extractUpstreamErrorCode(body []byte) string` 现在也回退顶层 `code` 字段。被 `openAIImagesUpstreamErrorFromHTTP` 间接消费。

- [ ] **Step 1: Write the failing test**

在 `backend/internal/service/gateway_service_test.go` 追加：

```go
func TestExtractUpstreamErrorCodeTopLevel(t *testing.T) {
	body := []byte(`{"code":"unsupported_parameter","message":"Streaming is only supported with n=1."}`)
	if got := extractUpstreamErrorCode(body); got != "unsupported_parameter" {
		t.Fatalf("extractUpstreamErrorCode = %q, want unsupported_parameter", got)
	}
	// 既有 error.code 行为不回归
	nested := []byte(`{"error":{"code":"server_error","message":"x"}}`)
	if got := extractUpstreamErrorCode(nested); got != "server_error" {
		t.Fatalf("extractUpstreamErrorCode(nested) = %q, want server_error", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestExtractUpstreamErrorCodeTopLevel -v`
Expected: FAIL（顶层 code 返回空字符串）。

- [ ] **Step 3: Add top-level fallback**

在 `extractUpstreamErrorCode` 的 `return ""`（行 7678）之前插入：

```go
	if code := strings.TrimSpace(gjson.GetBytes(body, "code").String()); code != "" {
		return code
	}

```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestExtractUpstreamErrorCodeTopLevel -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/gateway_service.go backend/internal/service/gateway_service_test.go
git commit -m "fix(openai-images): extract top-level error code for flat error bodies"
```

---

### Task 2: 新建常量 + 原生请求体构建器

**Files:**
- Create: `backend/internal/service/openai_images_codex.go`
- Test: `backend/internal/service/openai_images_codex_test.go`

**Interfaces:**
- Consumes: `OpenAIImagesRequest`（字段 `Prompt`/`Size`/`Quality`/`Background`/`OutputFormat`/`N`/`Stream`/`Uploads`/`MaskUpload`/`InputImageURLs`/`MaskImageURL`），`OpenAIImagesUpload{FieldName,FileName,ContentType,Data}`，`(*OpenAIImagesRequest).IsEdits()`。
- Produces:
  - `const chatgptCodexImagesGenerationsURL`、`chatgptCodexImagesEditsURL string`
  - `func buildOpenAIImagesCodexRequestBody(parsed *OpenAIImagesRequest, model string) (body []byte, contentType string, err error)` — generations 返回 JSON body + `"application/json"`；edits 返回 multipart body + `writer.FormDataContentType()`。

- [ ] **Step 1: Write the failing test**

创建 `backend/internal/service/openai_images_codex_test.go`：

```go
package service

import (
	"bytes"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestBuildOpenAIImagesCodexRequestBodyGenerations(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint,
		Prompt:   "a red apple",
		Size:     "1024x1024",
		Quality:  "medium",
		N:        2,
		Stream:   false,
	}
	body, ct, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if got := gjson.GetBytes(body, "model").String(); got != "gpt-image-2" {
		t.Fatalf("model = %q", got)
	}
	if got := gjson.GetBytes(body, "prompt").String(); got != "a red apple" {
		t.Fatalf("prompt = %q", got)
	}
	if got := gjson.GetBytes(body, "size").String(); got != "1024x1024" {
		t.Fatalf("size = %q", got)
	}
	if got := gjson.GetBytes(body, "n").Int(); got != 2 {
		t.Fatalf("n = %d", got)
	}
	if gjson.GetBytes(body, "stream").Bool() {
		t.Fatalf("stream should be false")
	}
	// 空字段不应写入
	if gjson.GetBytes(body, "background").Exists() {
		t.Fatalf("background should be omitted when empty")
	}
}

func TestBuildOpenAIImagesCodexRequestBodyEditsMultipart(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesEditsEndpoint,
		Prompt:   "make it blue",
		N:        1,
		Uploads: []OpenAIImagesUpload{
			{FieldName: "image", FileName: "a.png", ContentType: "image/png", Data: []byte("PNGDATA")},
		},
	}
	body, ct, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content-type = %q (mediaType=%q err=%v)", ct, mediaType, err)
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	form, err := mr.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("read form: %v", err)
	}
	if got := form.Value["prompt"]; len(got) != 1 || got[0] != "make it blue" {
		t.Fatalf("prompt field = %v", got)
	}
	if got := form.Value["model"]; len(got) != 1 || got[0] != "gpt-image-2" {
		t.Fatalf("model field = %v", got)
	}
	if len(form.File["image"]) != 1 {
		t.Fatalf("want 1 image file, got %d", len(form.File["image"]))
	}
}

func TestBuildOpenAIImagesCodexRequestBodyEditsRemoteURLErrors(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint:       openAIImagesEditsEndpoint,
		Prompt:         "x",
		N:              1,
		InputImageURLs: []string{"https://example.com/a.png"},
	}
	_, _, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2")
	if err == nil || !strings.Contains(err.Error(), "image input") {
		t.Fatalf("want remote-url image error, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestBuildOpenAIImagesCodexRequestBody -v`
Expected: FAIL（`buildOpenAIImagesCodexRequestBody` 未定义、`chatgptCodexImages*URL` 未定义）。

- [ ] **Step 3: Write minimal implementation**

创建 `backend/internal/service/openai_images_codex.go`：

```go
package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"

	"github.com/tidwall/sjson"
)

const (
	chatgptCodexImagesGenerationsURL = "https://chatgpt.com/backend-api/codex/images/generations"
	chatgptCodexImagesEditsURL       = "https://chatgpt.com/backend-api/codex/images/edits"
)

// buildOpenAIImagesCodexRequestBody builds the native OpenAI images request for
// the dedicated codex image endpoints. generations -> JSON; edits -> multipart.
func buildOpenAIImagesCodexRequestBody(parsed *OpenAIImagesRequest, model string) ([]byte, string, error) {
	if parsed.IsEdits() {
		return buildOpenAIImagesCodexEditsBody(parsed, model)
	}
	return buildOpenAIImagesCodexGenerationsBody(parsed, model), "application/json", nil
}

func buildOpenAIImagesCodexGenerationsBody(parsed *OpenAIImagesRequest, model string) []byte {
	body := []byte(`{}`)
	body, _ = sjson.SetBytes(body, "model", strings.TrimSpace(model))
	body, _ = sjson.SetBytes(body, "prompt", parsed.Prompt)
	body, _ = sjson.SetBytes(body, "n", parsed.N)
	body, _ = sjson.SetBytes(body, "stream", parsed.Stream)
	for _, f := range []struct{ path, value string }{
		{"size", parsed.Size},
		{"quality", parsed.Quality},
		{"output_format", parsed.OutputFormat},
		{"background", parsed.Background},
	} {
		if v := strings.TrimSpace(f.value); v != "" {
			body, _ = sjson.SetBytes(body, f.path, v)
		}
	}
	return body
}

func buildOpenAIImagesCodexEditsBody(parsed *OpenAIImagesRequest, model string) ([]byte, string, error) {
	images, err := openAIImagesCodexEditImageBytes(parsed)
	if err != nil {
		return nil, "", err
	}
	if len(images) == 0 {
		return nil, "", fmt.Errorf("image input is required")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for _, img := range images {
		fileName := img.FileName
		if strings.TrimSpace(fileName) == "" {
			fileName = "image.png"
		}
		fw, err := w.CreateFormFile("image", fileName)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write(img.Data); err != nil {
			return nil, "", err
		}
	}

	if mask, ok, err := openAIImagesCodexEditMaskBytes(parsed); err != nil {
		return nil, "", err
	} else if ok {
		fileName := mask.FileName
		if strings.TrimSpace(fileName) == "" {
			fileName = "mask.png"
		}
		fw, err := w.CreateFormFile("mask", fileName)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write(mask.Data); err != nil {
			return nil, "", err
		}
	}

	_ = w.WriteField("prompt", parsed.Prompt)
	_ = w.WriteField("model", strings.TrimSpace(model))
	_ = w.WriteField("n", strconv.Itoa(parsed.N))
	if parsed.Stream {
		_ = w.WriteField("stream", "true")
	}
	for _, f := range []struct{ field, value string }{
		{"size", parsed.Size},
		{"quality", parsed.Quality},
		{"output_format", parsed.OutputFormat},
		{"background", parsed.Background},
	} {
		if v := strings.TrimSpace(f.value); v != "" {
			_ = w.WriteField(f.field, v)
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// openAIImagesCodexEditImageBytes returns raw image bytes for edits: from
// Uploads (raw) or data-URL InputImageURLs. Remote http(s) URLs are rejected.
func openAIImagesCodexEditImageBytes(parsed *OpenAIImagesRequest) ([]OpenAIImagesUpload, error) {
	out := make([]OpenAIImagesUpload, 0, len(parsed.Uploads)+len(parsed.InputImageURLs))
	for _, up := range parsed.Uploads {
		if len(up.Data) == 0 {
			continue
		}
		out = append(out, up)
	}
	for _, raw := range parsed.InputImageURLs {
		data, fileName, err := decodeOpenAIImagesDataURL(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, OpenAIImagesUpload{FieldName: "image", FileName: fileName, Data: data})
	}
	return out, nil
}

func openAIImagesCodexEditMaskBytes(parsed *OpenAIImagesRequest) (OpenAIImagesUpload, bool, error) {
	if parsed.MaskUpload != nil && len(parsed.MaskUpload.Data) > 0 {
		return *parsed.MaskUpload, true, nil
	}
	if raw := strings.TrimSpace(parsed.MaskImageURL); raw != "" {
		data, fileName, err := decodeOpenAIImagesDataURL(raw)
		if err != nil {
			return OpenAIImagesUpload{}, false, err
		}
		return OpenAIImagesUpload{FieldName: "mask", FileName: fileName, Data: data}, true, nil
	}
	return OpenAIImagesUpload{}, false, nil
}

// decodeOpenAIImagesDataURL decodes a base64 data: URL into bytes. Remote URLs
// (no inline bytes) are rejected because multipart needs the actual file.
func decodeOpenAIImagesDataURL(raw string) ([]byte, string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "data:") {
		return nil, "", fmt.Errorf("image input must be uploaded bytes or a data URL")
	}
	idx := strings.Index(raw, ",")
	if idx < 0 {
		return nil, "", fmt.Errorf("invalid data URL image input")
	}
	meta, payload := raw[5:idx], raw[idx+1:]
	if !strings.Contains(meta, "base64") {
		return nil, "", fmt.Errorf("only base64 data URL image input is supported")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", fmt.Errorf("decode data URL image input: %w", err)
	}
	return data, "image.png", nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestBuildOpenAIImagesCodexRequestBody -v`
Expected: PASS（3 个子测试）。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_codex.go backend/internal/service/openai_images_codex_test.go
git commit -m "feat(openai-images): native codex image request body builder"
```

---

### Task 3: 原生上游请求构建器

**Files:**
- Modify: `backend/internal/service/openai_images_codex.go`
- Test: `backend/internal/service/openai_images_codex_test.go`

**Interfaces:**
- Consumes: `buildOpenAIImagesCodexRequestBody`（Task 2），`(*OpenAIGatewayService).overrideBrowserUserAgent`，`account.GetChatGPTAccountID()`，常量 `codexCLIUserAgent`。
- Produces: `func (s *OpenAIGatewayService) buildOpenAIImagesCodexUpstreamRequest(ctx context.Context, c *gin.Context, account *Account, parsed *OpenAIImagesRequest, body []byte, contentType, token string) (*http.Request, error)`。

- [ ] **Step 1: Write the failing test**

追加到 `openai_images_codex_test.go`：

```go
func TestBuildOpenAIImagesCodexUpstreamRequestHeaders(t *testing.T) {
	s := &OpenAIGatewayService{}
	acc := &Account{Type: AccountTypeOAuth}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)

	parsedGen := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Prompt: "x", N: 1}
	req, err := s.buildOpenAIImagesCodexUpstreamRequest(context.Background(), c, acc, parsedGen, []byte(`{}`), "application/json", "tok")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if req.URL.String() != chatgptCodexImagesGenerationsURL {
		t.Fatalf("url = %s", req.URL.String())
	}
	if req.Host != "chatgpt.com" {
		t.Fatalf("host = %s", req.Host)
	}
	for k, want := range map[string]string{
		"Authorization": "Bearer tok",
		"OpenAI-Beta":   "responses=experimental",
		"originator":    "codex_cli_rs",
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	} {
		if got := req.Header.Get(k); got != want {
			t.Fatalf("header %s = %q, want %q", k, got, want)
		}
	}
	if req.Header.Get("session_id") == "" {
		t.Fatalf("session_id must be set")
	}
	if req.Header.Get("User-Agent") != codexCLIUserAgent {
		t.Fatalf("ua = %q", req.Header.Get("User-Agent"))
	}

	// 流式 edits：Accept event-stream，URL=edits
	parsedEdit := &OpenAIImagesRequest{Endpoint: openAIImagesEditsEndpoint, Prompt: "x", N: 1, Stream: true}
	reqE, err := s.buildOpenAIImagesCodexUpstreamRequest(context.Background(), c, acc, parsedEdit, []byte("multipart-bytes"), "multipart/form-data; boundary=zzz", "tok")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reqE.URL.String() != chatgptCodexImagesEditsURL {
		t.Fatalf("edit url = %s", reqE.URL.String())
	}
	if reqE.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("stream accept = %q", reqE.Header.Get("Accept"))
	}
	if reqE.Header.Get("Content-Type") != "multipart/form-data; boundary=zzz" {
		t.Fatalf("edit content-type = %q", reqE.Header.Get("Content-Type"))
	}
}
```

> 注：`chatgpt-account-id` header 的值来自 `account.GetChatGPTAccountID()`（内部读 credential `chatgpt_account_id` 且要求 `IsOpenAIOAuth()`）。本测试用空 account id（header 不写出），只验证 URL/Host/其余固定 header。若要验证 account-id 写出，需构造完整 OAuth 凭据，留作可选增强。

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestBuildOpenAIImagesCodexUpstreamRequestHeaders -v`
Expected: FAIL（方法未定义）。先在测试文件顶部补 imports：`context`、`net/http/httptest`、`github.com/gin-gonic/gin`。

- [ ] **Step 3: Write minimal implementation**

在 `openai_images_codex.go` 追加（并把 import 补上 `context`、`net/http`、`github.com/gin-gonic/gin`、`github.com/google/uuid`）：

```go
func (s *OpenAIGatewayService) buildOpenAIImagesCodexUpstreamRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	body []byte,
	contentType string,
	token string,
) (*http.Request, error) {
	targetURL := chatgptCodexImagesGenerationsURL
	if parsed.IsEdits() {
		targetURL = chatgptCodexImagesEditsURL
	}
	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"

	req.Header.Set("Authorization", "Bearer "+token)
	if accountID := account.GetChatGPTAccountID(); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("session_id", uuid.NewString())
	req.Header.Set("User-Agent", codexCLIUserAgent)
	req.Header.Set("Content-Type", contentType)
	if parsed.Stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	// 复用现有自定义 UA / 浏览器 UA 兜底（仅 OAuth 生效）。
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		req.Header.Set("User-Agent", codexCLIUserAgent)
	}
	s.overrideBrowserUserAgent(ctx, account, req)
	return req, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestBuildOpenAIImagesCodexUpstreamRequestHeaders -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_codex.go backend/internal/service/openai_images_codex_test.go
git commit -m "feat(openai-images): native codex image upstream request builder"
```

---

### Task 4: 原生非流式响应解析器

**Files:**
- Modify: `backend/internal/service/openai_images_codex.go`
- Test: `backend/internal/service/openai_images_codex_test.go`

**Interfaces:**
- Consumes: `ReadUpstreamResponseBody`、`openAIUsageFromGJSON`、`openAIResponsesImageResult`、`openAIImagesOAuthForwardOutput`、`errOpenAIImagesEmptyOutputRetryable`、`responseheaders` 非必需。
- Produces: `func (s *OpenAIGatewayService) parseOpenAIImagesCodexNonStreamingOutput(resp *http.Response, c *gin.Context, fallbackModel string) (*openAIImagesOAuthForwardOutput, error)`。返回的 output 直接喂给现有 `buildOpenAIImagesAPIResponse`（在 `forwardOpenAIImagesOAuth`）。`data` 为空 → 返回带 usage 的 output + `errOpenAIImagesEmptyOutputRetryable`。

- [ ] **Step 1: Write the failing test**

追加到 `openai_images_codex_test.go`：

```go
func TestParseOpenAIImagesCodexNonStreamingOutput(t *testing.T) {
	s := &OpenAIGatewayService{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	bodyJSON := `{"created":1781809378,"background":"opaque","data":[{"b64_json":"QUJD"}],` +
		`"output_format":"png","quality":"medium","size":"2880x2880",` +
		`"usage":{"input_tokens":24,"output_tokens":5930,"output_tokens_details":{"image_tokens":5930}}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(bodyJSON))}

	out, err := s.parseOpenAIImagesCodexNonStreamingOutput(resp, c, "gpt-image-2")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out.ImageResults) != 1 || out.ImageResults[0].Result != "QUJD" {
		t.Fatalf("results = %#v", out.ImageResults)
	}
	if out.ImageSizes[0] != "2880x2880" {
		t.Fatalf("sizes = %v", out.ImageSizes)
	}
	if out.FirstMeta.Quality != "medium" || out.FirstMeta.OutputFormat != "png" || out.FirstMeta.Background != "opaque" {
		t.Fatalf("meta = %#v", out.FirstMeta)
	}
	if out.Usage.OutputTokens != 5930 || out.Usage.ImageOutputTokens != 5930 || out.Usage.InputTokens != 24 {
		t.Fatalf("usage = %#v", out.Usage)
	}
	if out.CreatedAt != 1781809378 {
		t.Fatalf("createdAt = %d", out.CreatedAt)
	}
}

func TestParseOpenAIImagesCodexNonStreamingEmptyData(t *testing.T) {
	s := &OpenAIGatewayService{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"created":1,"data":[],"usage":{"output_tokens":0}}`))}
	_, err := s.parseOpenAIImagesCodexNonStreamingOutput(resp, c, "gpt-image-2")
	if err != errOpenAIImagesEmptyOutputRetryable {
		t.Fatalf("err = %v, want errOpenAIImagesEmptyOutputRetryable", err)
	}
}
```

测试文件顶部 imports 补 `io`。

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestParseOpenAIImagesCodexNonStreaming -v`
Expected: FAIL（方法未定义）。

- [ ] **Step 3: Write minimal implementation**

在 `openai_images_codex.go` 追加（import 补 `github.com/tidwall/gjson`）：

```go
func (s *OpenAIGatewayService) parseOpenAIImagesCodexNonStreamingOutput(
	resp *http.Response,
	c *gin.Context,
	fallbackModel string,
) (*openAIImagesOAuthForwardOutput, error) {
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	root := gjson.ParseBytes(body)

	createdAt := root.Get("created").Int()

	var usage OpenAIUsage
	var usageRaw []byte
	if u := root.Get("usage"); u.Exists() && u.IsObject() {
		usageRaw = []byte(u.Raw)
		if parsed, ok := openAIUsageFromGJSON(u); ok {
			usage = parsed
		}
	}

	firstMeta := openAIResponsesImageResult{
		OutputFormat: strings.TrimSpace(root.Get("output_format").String()),
		Size:         strings.TrimSpace(root.Get("size").String()),
		Background:   strings.TrimSpace(root.Get("background").String()),
		Quality:      strings.TrimSpace(root.Get("quality").String()),
		Model:        strings.TrimSpace(fallbackModel),
	}

	var results []openAIResponsesImageResult
	var sizes []string
	for _, item := range root.Get("data").Array() {
		b64 := strings.TrimSpace(item.Get("b64_json").String())
		if b64 == "" {
			continue
		}
		size := strings.TrimSpace(item.Get("size").String())
		if size == "" {
			size = firstMeta.Size
		}
		results = append(results, openAIResponsesImageResult{
			Result:        b64,
			RevisedPrompt: strings.TrimSpace(item.Get("revised_prompt").String()),
			OutputFormat:  firstMeta.OutputFormat,
			Size:          size,
			Background:    firstMeta.Background,
			Quality:       firstMeta.Quality,
		})
		sizes = append(sizes, size)
	}

	if len(results) == 0 {
		return &openAIImagesOAuthForwardOutput{
			Usage:           usage,
			CreatedAt:       createdAt,
			UsageRaw:        usageRaw,
			FirstMeta:       firstMeta,
			ResponseHeaders: resp.Header.Clone(),
			StatusCode:      resp.StatusCode,
		}, errOpenAIImagesEmptyOutputRetryable
	}

	return &openAIImagesOAuthForwardOutput{
		Usage:           usage,
		ImageResults:    results,
		ImageSizes:      sizes,
		CreatedAt:       createdAt,
		UsageRaw:        usageRaw,
		FirstMeta:       firstMeta,
		ResponseHeaders: resp.Header.Clone(),
		UpstreamModel:   strings.TrimSpace(fallbackModel),
		RequestID:       resp.Header.Get("x-request-id"),
		StatusCode:      resp.StatusCode,
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestParseOpenAIImagesCodexNonStreaming -v`
Expected: PASS（2 个子测试）。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_codex.go backend/internal/service/openai_images_codex_test.go
git commit -m "feat(openai-images): parse native codex image non-streaming response"
```

---

### Task 5: `stream && n>1` → 400，并接线非流式 OAuth 路径

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go`（`forwardOpenAIImagesOAuth` 约 1468；`forwardOpenAIImagesOAuthOnce` 约 1512）
- Test: `backend/internal/service/openai_images_codex_test.go`

**Interfaces:**
- Consumes: `buildOpenAIImagesCodexRequestBody`、`buildOpenAIImagesCodexUpstreamRequest`、`parseOpenAIImagesCodexNonStreamingOutput`、`writeOpenAIImagesUpstreamErrorResponse`、`OpenAIImagesUpstreamError`。
- Produces: 非流式 OAuth 生图改走原生端点；`stream+n>1` 返回非可重试 400。

- [ ] **Step 1: Write the failing test**

追加到 `openai_images_codex_test.go`：

```go
func TestOpenAIImagesStreamNGreaterThanOneRejected(t *testing.T) {
	s := &OpenAIGatewayService{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	acc := &Account{Type: AccountTypeOAuth}
	parsed := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Prompt: "x", Stream: true, N: 2}

	_, err := s.forwardOpenAIImagesOAuth(context.Background(), c, acc, parsed, "")
	var upErr *OpenAIImagesUpstreamError
	if !errors.As(err, &upErr) {
		t.Fatalf("err type = %T (%v)", err, err)
	}
	if upErr.StatusCode != 400 || upErr.Code != "unsupported_parameter" {
		t.Fatalf("upErr = %#v", upErr)
	}
	if IsRetryableOpenAIImagesUpstreamError(upErr) {
		t.Fatalf("must be non-retryable")
	}
	if rec.Code != 400 {
		t.Fatalf("status written = %d", rec.Code)
	}
}
```

测试 imports 补 `errors`。

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestOpenAIImagesStreamNGreaterThanOneRejected -v`
Expected: FAIL（当前未校验，会进入账号调度/请求逻辑）。

- [ ] **Step 3: Implement — 校验 + 接线非流式**

(a) 在 `forwardOpenAIImagesOAuth`（约 1468）函数体最前面、`startTime := time.Now()` 之后插入：

```go
	if parsed.Stream && parsed.N > 1 {
		upstreamErr := &OpenAIImagesUpstreamError{
			StatusCode: http.StatusBadRequest,
			ErrorType:  "invalid_request_error",
			Code:       "unsupported_parameter",
			Message:    "Streaming is only supported with n=1.",
			Param:      "n",
		}
		writeOpenAIImagesUpstreamErrorResponse(c, upstreamErr)
		return nil, upstreamErr
	}
```

(b) 在 `forwardOpenAIImagesOAuthOnce`（约 1549-1558）把：

```go
		responsesBody, err := buildOpenAIImagesResponsesRequest(parsed, requestModel)
		if err != nil {
			return nil, err
		}
		upstreamReq, err := s.buildUpstreamRequest(upstreamCtx, c, account, responsesBody, token, true, parsed.StickySessionSeed(), false)
		if err != nil {
			return nil, err
		}
		upstreamReq.Header.Set("Content-Type", "application/json")
		upstreamReq.Header.Set("Accept", "text/event-stream")
```

替换为：

```go
		reqBody, contentType, err := buildOpenAIImagesCodexRequestBody(parsed, requestModel)
		if err != nil {
			return nil, err
		}
		upstreamReq, err := s.buildOpenAIImagesCodexUpstreamRequest(upstreamCtx, c, account, parsed, reqBody, contentType, token)
		if err != nil {
			return nil, err
		}
```

(c) 在 `forwardOpenAIImagesOAuthOnce` 把（约 1611）：

```go
		output, err := s.handleOpenAIImagesOAuthNonStreamingOutput(resp, c, parsed.ResponseFormat, requestModel, retryableEmptyOutput)
```

替换为：

```go
		output, err := s.parseOpenAIImagesCodexNonStreamingOutput(resp, c, requestModel)
```

> 注：`retryableEmptyOutput` 仍由外层 for 循环控制 empty-output failover；本函数仍把 `errOpenAIImagesEmptyOutputRetryable` 透传给现有的重试分支（约 1614-1620），无需改动。

- [ ] **Step 4: Run focused + package tests**

Run: `cd backend && go test ./internal/service/ -run 'TestOpenAIImagesStreamNGreaterThanOneRejected|TestParseOpenAIImagesCodex|TestBuildOpenAIImagesCodex' -v`
Expected: PASS
Run: `cd backend && go build ./...`
Expected: 成功（此时 `buildOpenAIImagesResponsesRequest` 可能变为未被非流式引用，但流式仍引用 → 不报错）。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_codex_test.go
git commit -m "feat(openai-images): route non-streaming OAuth image gen to native codex endpoint"
```

---

### Task 6: 接线流式 OAuth 路径 + 原生 SSE 行解析

流式复用现有 `handleOpenAIImagesOAuthStreamingResponse` 的下游写出/keepalive/计费框架，只在其 `processData` 中增加对"原生扁平行"的识别。原生端点不发 Responses 事件类型，故原有 Responses 分支对该端点为惰性分支（保留，零风险）。

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go`（`forwardOpenAIImagesOAuthStreaming` 约 1671-1680 的请求构建；`handleOpenAIImagesOAuthStreamingResponse` 的 `processData` 约 1173 的 `switch` 之后）
- Test: `backend/internal/service/openai_images_codex_test.go`

**Interfaces:**
- Consumes: Task 2/3 的请求构建器；现有 `openAIResponsesImageResult`、`pendingResults`、`usage`、`openAIUsageFromGJSON`。
- Produces: 流式 OAuth 生图改走原生端点；`data:` 行若顶层带 `b64_json` 即作为最终图收集，带 `usage` 即合并用量。

- [ ] **Step 1: Write the failing test（原生行解析单元）**

为避免依赖整段流式框架，抽一个纯函数做行解析并单测。追加到 `openai_images_codex_test.go`：

```go
func TestParseOpenAIImagesCodexStreamLine(t *testing.T) {
	// 带 b64_json 的行
	img, isImg, usage, hasUsage := parseOpenAIImagesCodexStreamLine([]byte(`{"b64_json":"QUJD","size":"1024x1024","output_format":"png"}`))
	if !isImg || img.Result != "QUJD" || img.Size != "1024x1024" {
		t.Fatalf("img=%#v isImg=%v", img, isImg)
	}
	if hasUsage {
		t.Fatalf("should not report usage for image-only line")
	}

	// 带 usage 的行
	_, isImg2, usage2, hasUsage2 := parseOpenAIImagesCodexStreamLine([]byte(`{"usage":{"output_tokens":5930,"output_tokens_details":{"image_tokens":5930}}}`))
	if isImg2 {
		t.Fatalf("usage line should not be an image")
	}
	if !hasUsage2 || usage2.ImageOutputTokens != 5930 {
		t.Fatalf("usage=%#v has=%v", usage2, hasUsage2)
	}
	_ = usage
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestParseOpenAIImagesCodexStreamLine -v`
Expected: FAIL（函数未定义）。

- [ ] **Step 3: Implement 行解析函数 + 接线**

(a) 在 `openai_images_codex.go` 追加：

```go
// parseOpenAIImagesCodexStreamLine inspects one native SSE data line. The codex
// images endpoint emits the final image as a flat object carrying b64_json, and
// usage in a (possibly separate) object. No partial-preview frames.
func parseOpenAIImagesCodexStreamLine(data []byte) (img openAIResponsesImageResult, isImage bool, usage OpenAIUsage, hasUsage bool) {
	if !gjson.ValidBytes(data) {
		return openAIResponsesImageResult{}, false, OpenAIUsage{}, false
	}
	root := gjson.ParseBytes(data)
	if u := root.Get("usage"); u.Exists() && u.IsObject() {
		if parsed, ok := openAIUsageFromGJSON(u); ok {
			usage = parsed
			hasUsage = true
		}
	}
	b64 := strings.TrimSpace(root.Get("b64_json").String())
	if b64 == "" {
		// 兼容 data[].b64_json 包裹形态
		b64 = strings.TrimSpace(root.Get("data.0.b64_json").String())
	}
	if b64 != "" {
		img = openAIResponsesImageResult{
			Result:        b64,
			RevisedPrompt: strings.TrimSpace(root.Get("revised_prompt").String()),
			OutputFormat:  strings.TrimSpace(root.Get("output_format").String()),
			Size:          strings.TrimSpace(root.Get("size").String()),
			Background:    strings.TrimSpace(root.Get("background").String()),
			Quality:       strings.TrimSpace(root.Get("quality").String()),
		}
		isImage = true
	}
	return img, isImage, usage, hasUsage
}
```

(b) 在 `forwardOpenAIImagesOAuthStreaming`（约 1671-1680）做与 Task 5(b) 相同的请求构建替换：

```go
		reqBody, contentType, err := buildOpenAIImagesCodexRequestBody(parsed, requestModel)
		if err != nil {
			return nil, err
		}
		upstreamReq, err := s.buildOpenAIImagesCodexUpstreamRequest(upstreamCtx, c, account, parsed, reqBody, contentType, token)
		if err != nil {
			return nil, err
		}
```

（删除原 `buildOpenAIImagesResponsesRequest`+`buildUpstreamRequest`+两行 Header.Set。）

(c) 在 `handleOpenAIImagesOAuthStreamingResponse` 的 `processData` 里，`switch gjson.GetBytes(dataBytes, "type").String() {`（约 1173）之前插入原生行处理：

```go
		// 原生 codex images 端点：扁平行（无 Responses event type）。
		if strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String()) == "" {
			if img, isImg, lineUsage, hasUsage := parseOpenAIImagesCodexStreamLine(dataBytes); isImg || hasUsage {
				if hasUsage {
					usage = lineUsage
				}
				if isImg {
					mergeOpenAIResponsesImageMeta(&img, streamMeta)
					key := openAIResponsesImageResultKey("", img)
					if _, exists := emitted[key]; !exists {
						if _, exists := pendingSeen[key]; !exists {
							pendingSeen[key] = struct{}{}
							pendingResults = append(pendingResults, img)
						}
					}
				}
				return
			}
		}
```

> `finalizePending()`（约 1291）会把 `pendingResults` 作为最终图向下游发出并计 `imageCount`/`imageOutputSizes`，对原生流式同样适用；EOF/Flush 时触发。`data` 为空（无任何带 b64 的行）→ `finalizePending` 走 `stream disconnected before...` 错误分支；为符合 empty-output 语义，将该兜底改为返回 `errOpenAIImagesEmptyOutputRetryable`：把 `finalizePending` 末尾的
> ```go
> 	streamErr := fmt.Errorf("stream disconnected before image generation completed")
> 	s.tryWriteOpenAIImagesStreamEvent(c, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", buildOpenAIImagesStreamErrorBody(streamErr.Error()))
> 	return streamErr
> ```
> 改为：
> ```go
> 	return errOpenAIImagesEmptyOutputRetryable
> ```
> （上层 `forwardOpenAIImagesOAuthStreaming` 约 1743 已对 `errOpenAIImagesEmptyOutputRetryable` 做 empty-output failover/重试处理。）

- [ ] **Step 4: Run focused + package tests + build**

Run: `cd backend && go test ./internal/service/ -run TestParseOpenAIImagesCodexStreamLine -v`
Expected: PASS
Run: `cd backend && go build ./... && go test ./internal/service/ -run 'OpenAIImages|Codex' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_codex.go backend/internal/service/openai_images_codex_test.go
git commit -m "feat(openai-images): route streaming OAuth image gen to native codex endpoint"
```

---

### Task 7: 清理死代码 + 重试兼容性回归 + 全量验证

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go`（删除 `buildOpenAIImagesResponsesRequest`，约 410-498）
- Test: `backend/internal/service/openai_images_codex_test.go`

**Interfaces:**
- Consumes: 全部前序任务。
- Produces: 无新接口；删除不再被引用的 `buildOpenAIImagesResponsesRequest`。

- [ ] **Step 1: 确认 `buildOpenAIImagesResponsesRequest` 无引用**

Run: `cd backend && grep -rn "buildOpenAIImagesResponsesRequest" internal/`
Expected: 仅其自身定义（Task 5/6 已移除两处调用）。若仍有调用，先处理调用方再删。

- [ ] **Step 2: Write retry-compat regression test（错误体分类）**

追加到 `openai_images_codex_test.go`：

```go
func TestOpenAIImagesNativeErrorClassification(t *testing.T) {
	// 扁平 server_error → 可重试
	serverErr := openAIImagesUpstreamErrorFromHTTP(500, http.Header{}, []byte(`{"code":"server_error","message":"oops"}`))
	if serverErr.Code != "server_error" {
		t.Fatalf("code = %q (顶层 code 未解析，检查 Task 1)", serverErr.Code)
	}
	if !IsRetryableOpenAIImagesUpstreamError(serverErr) {
		t.Fatalf("server_error must be retryable")
	}
	// 扁平 unsupported_parameter → 不可重试（用户错误）
	userErr := openAIImagesUpstreamErrorFromHTTP(400, http.Header{}, []byte(`{"code":"unsupported_parameter","message":"bad"}`))
	if IsRetryableOpenAIImagesUpstreamError(userErr) {
		t.Fatalf("unsupported_parameter must not be retryable")
	}
}
```

- [ ] **Step 3: Run test to verify it passes（依赖 Task 1 的顶层 code 回退）**

Run: `cd backend && go test ./internal/service/ -run TestOpenAIImagesNativeErrorClassification -v`
Expected: PASS。若 `server_error` 断言失败，说明 Task 1 的顶层 code 回退缺失或 `openAIImagesUpstreamErrorFromHTTP` 未用 `extractUpstreamErrorCode` —— 修复后再继续。

- [ ] **Step 4: 删除死代码并全量验证**

删除 `openai_images_responses.go` 中整个 `buildOpenAIImagesResponsesRequest` 函数（约 410-498，从 `func buildOpenAIImagesResponsesRequest(` 到其闭合 `}`）。

Run: `cd backend && gofmt -l internal/service/openai_images_codex.go internal/service/openai_images_responses.go`
Expected: 无输出（已格式化）。
Run: `cd backend && go vet ./internal/service/ && go build ./...`
Expected: 成功。
Run: `cd backend && go test ./internal/service/ -count=1`
Expected: PASS（全包）。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_codex_test.go
git commit -m "chore(openai-images): drop responses-based image request builder; add retry-compat tests"
```

---

## 备注：本计划与 spec 的范围差异（需告知用户）

- spec section 3 提到删除"Responses 专属解析链"（`collectOpenAIImagesFromResponsesBody`、`extractOpenAIImagesFromResponsesCompleted`、`usageFromOpenAIImageGenToolUsageRaw`）。本计划**仅删除请求构建器** `buildOpenAIImagesResponsesRequest`；上述解析函数仍被流式 `processData` 的惰性 Responses 分支引用，故**保留**以把风险降到最低（新端点不发这些事件，分支自然不触发）。待生产确认原生流式行格式后，再做后续清理（独立 follow-up）。
- 原生**流式**行格式仅 doc 描述、未实测；Task 6 的 `parseOpenAIImagesCodexStreamLine` 做了防御式解析（顶层 `b64_json` 与 `data.0.b64_json` 两种形态）。上线后需用真实上游 SSE 校验，必要时微调。
- 重试约束 #3（403 cf-challenge 归类可重试）由"正确的 codex 前缀 URL + 指纹头"在设计层规避，未单测：403+HTML 的分类走现有 `shouldFailoverOpenAIUpstreamResponse`/handler failover，行为不变。上线后用真实流量验证；若发现 403 challenge 未进 failover，再补 `shouldFailoverOpenAIUpstreamResponse` 的 403 分支处理（独立 follow-up）。
