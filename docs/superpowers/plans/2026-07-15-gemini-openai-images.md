# Gemini 分组支持 OpenAI Images 协议（透传模式）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gemini 平台分组的 API Key 能调用 `/v1/images/generations|edits`，请求原样透传到账号 `base_url` 指向的 OpenAI 兼容上游池。

**Architecture:** 复刻 Grok 接入模式三层结构：`routes/gateway.go` 的 `imagesHandler` 按分组平台分发 → 新 handler 方法 `OpenAIGatewayHandler.GeminiImages`（编排：解析/门槛/并发槽/选号循环/计费）→ 新 service 方法 `OpenAIGatewayService.ForwardGeminiImagesPassthrough`（透传转发）。选号用 `GeminiMessagesCompatService` 新增的 API-Key-only 方法，需把该 service 注入 `OpenAIGatewayHandler`。

**Tech Stack:** Go 1.26（module root 在 `backend/`）、gin、gjson/sjson、google wire（`wire_gen.go` 已生成、可手改）、testify + `//go:build unit`。

**Spec:** `docs/superpowers/specs/2026-07-15-gemini-openai-images-design.md`

## Global Constraints

- 分支 `feature/gemini-openai-images`；提交信息用 `feat:`/`test:`/`docs:` 前缀，结尾加 `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`。
- 所有 Go 命令在 `/Users/astray/api-token/sub2api/backend` 下执行；单测命令模式：`go test -tags=unit ./internal/service/ -run '<TestName>' -count=1`。
- 不改变 OpenAI / Grok 平台现有行为（`imagesHandler` 的两个已有 case、`forwardOpenAIImagesAPIKey`、grok_media 均不动逻辑）。
- 仅支持非流式；`stream=true` 在 handler 层 400。
- 仅调度 `Platform==gemini && Type==apikey`（常量 `AccountTypeAPIKey = "apikey"`）账号。
- 映射后模型必须满足 `gemini-` 前缀且含 `image`（与运营报表 `ILIKE 'gemini-%image%'` 口径一致）。
- 注释风格：遵循所在文件的中英双语或中文注释习惯，只写代码本身表达不了的约束。
- v1 明确简化（写入代码注释）：上游 429/5xx 仅做本次请求内切号（`failedAccountIDs`），不做账号级临时停调/健康统计（Gemini 原生链路的 `TempUnscheduleRetryableError` 不复用）；不调用 openai 调度器的 `ReportOpenAIAccountScheduleResult`（gemini 账号不在其统计域内）。

---

### Task 1: Gemini 生图模型识别 + 共享校验器扩展

**Files:**
- Create: `backend/internal/service/gemini_images_passthrough.go`
- Modify: `backend/internal/service/openai_images.go`（`isOpenAIImageGenerationModel`，约 :457-460）
- Test: `backend/internal/service/gemini_images_passthrough_test.go`

**Interfaces:**
- Produces: `func IsGeminiImageGenerationModel(model string) bool`（exported，handler 与后续 task 都用）。
- Modifies: `isOpenAIImageGenerationModel` 额外接受 gemini 生图模型（使 `ParseOpenAIImagesRequest` 内的 `validateOpenAIImagesModel` 不拒绝 gemini 模型；grok 模型已有同样先例）。

- [ ] **Step 1: Write the failing test**

新建 `backend/internal/service/gemini_images_passthrough_test.go`：

```go
//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsGeminiImageGenerationModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gemini-2.5-flash-image", true},
		{"gemini-3.1-flash-image", true},
		{"Gemini-2.5-Flash-Image-Preview", true},
		{" gemini-2.5-flash-image ", true},
		{"gemini-2.5-flash", false}, // 不含 image
		{"gpt-image-2", false},
		{"grok-imagine", false},
		{"imagen-3", false}, // 无 gemini- 前缀
		{"", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, IsGeminiImageGenerationModel(tc.model), "model=%q", tc.model)
	}
}

func TestValidateOpenAIImagesModelAcceptsGeminiImageModels(t *testing.T) {
	require.NoError(t, validateOpenAIImagesModel("gemini-2.5-flash-image"))
	require.Error(t, validateOpenAIImagesModel("gemini-2.5-flash"))
	require.Error(t, validateOpenAIImagesModel("dall-e-3"))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/astray/api-token/sub2api/backend && go test -tags=unit ./internal/service/ -run 'TestIsGeminiImageGenerationModel|TestValidateOpenAIImagesModelAcceptsGeminiImageModels' -count=1`
Expected: FAIL（`IsGeminiImageGenerationModel` 未定义，编译错误）

- [ ] **Step 3: Write minimal implementation**

新建 `backend/internal/service/gemini_images_passthrough.go`：

```go
package service

import "strings"

// IsGeminiImageGenerationModel 判断是否为 Gemini 生图模型。
// 口径与运营生图报表一致：platform='gemini' AND model ILIKE 'gemini-%image%'。
func IsGeminiImageGenerationModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gemini-") && strings.Contains(model, "image")
}
```

修改 `backend/internal/service/openai_images.go` 的 `isOpenAIImageGenerationModel`（:457-460）：

```go
func isOpenAIImageGenerationModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-image-") || isGrokImageGenerationModel(model) || IsGeminiImageGenerationModel(model)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: 同 Step 2 命令，再加回归：`go test -tags=unit ./internal/service/ -run 'TestParseOpenAIImages|TestValidateOpenAIImages|TestIsGemini' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/gemini_images_passthrough.go backend/internal/service/gemini_images_passthrough_test.go backend/internal/service/openai_images.go
git commit -m "feat: recognize gemini image models in openai images validation"
```

---

### Task 2: Gemini API-Key 账号选号方法

**Files:**
- Create: `backend/internal/service/gemini_images_account_selection.go`
- Test: `backend/internal/service/gemini_images_account_selection_test.go`

**Interfaces:**
- Consumes（均为 `GeminiMessagesCompatService` 既有私有方法/字段，同包可用）：`listSchedulableAccountsOnce(ctx, groupID, platform, hasForcePlatform)`（gemini_messages_compat_service.go:445）、`selectBestGeminiAccount(ctx, accounts, requestedModel, excludedIDs, platform, useMixedScheduling)`（:322）、`hydrateSelectedAccount(ctx, account)`（:431）。
- Produces: `func (s *GeminiMessagesCompatService) SelectGeminiAPIKeyAccountForImages(ctx context.Context, groupID *int64, requestedModel string, excludedIDs map[int64]struct{}) (*Account, error)` — Task 4 的 handler 调用它。

- [ ] **Step 1: Write the failing test**

新建 `backend/internal/service/gemini_images_account_selection_test.go`。账号仓库 stub 采用"内嵌接口 + 只实现所需方法"模式（参考 `gateway_multiplatform_test.go:23` 的 `mockAccountRepoForPlatform`）。注意 `listSchedulableAccountsOnce` 在 `schedulerSnapshot == nil` 且 `groupID != nil` 时调用 `ListSchedulableByGroupIDAndPlatforms`；先阅读该接口方法在 `AccountRepository` 中的准确签名再写 stub。

```go
//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type geminiImagesSelectionAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

func (s *geminiImagesSelectionAccountRepoStub) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	return s.accounts, nil
}

func geminiImagesSelectionService(accounts []Account) *GeminiMessagesCompatService {
	return &GeminiMessagesCompatService{
		accountRepo: &geminiImagesSelectionAccountRepoStub{accounts: accounts},
	}
}

func TestSelectGeminiAPIKeyAccountForImagesFiltersTypesAndPlatforms(t *testing.T) {
	groupID := int64(7)
	oauthUsed := time.Now().Add(-time.Hour)
	accounts := []Account{
		{ID: 1, Platform: PlatformGemini, Type: AccountTypeOAuth, LastUsedAt: &oauthUsed},
		{ID: 2, Platform: PlatformAntigravity, Type: AccountTypeAPIKey, Extra: map[string]any{"mixed_scheduling": true}},
		{ID: 3, Platform: PlatformGemini, Type: AccountTypeAPIKey},
	}
	svc := geminiImagesSelectionService(accounts)

	selected, err := svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "gemini-2.5-flash-image", nil)
	require.NoError(t, err)
	require.Equal(t, int64(3), selected.ID)
}

func TestSelectGeminiAPIKeyAccountForImagesHonorsExclusionsAndLRU(t *testing.T) {
	groupID := int64(7)
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-time.Minute)
	accounts := []Account{
		{ID: 11, Platform: PlatformGemini, Type: AccountTypeAPIKey, LastUsedAt: &newer},
		{ID: 12, Platform: PlatformGemini, Type: AccountTypeAPIKey, LastUsedAt: &older},
	}
	svc := geminiImagesSelectionService(accounts)

	// LRU：先选最久未用的 12
	selected, err := svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "", nil)
	require.NoError(t, err)
	require.Equal(t, int64(12), selected.ID)

	// 排除 12 后选 11
	selected, err = svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "", map[int64]struct{}{12: {}})
	require.NoError(t, err)
	require.Equal(t, int64(11), selected.ID)

	// 全部排除 → error
	_, err = svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "", map[int64]struct{}{11: {}, 12: {}})
	require.Error(t, err)
}

func TestSelectGeminiAPIKeyAccountForImagesNoAccounts(t *testing.T) {
	groupID := int64(7)
	svc := geminiImagesSelectionService(nil)
	_, err := svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "gemini-2.5-flash-image", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gemini-2.5-flash-image")
}
```

若 `selectBestGeminiAccount` 内部（`buildPreCheckUsageResultMap` / `passesRateLimitPreCheckWithCache` / `IsSchedulableForModelWithContext`）对 nil `rateLimitService`、空 `Extra` 不安全导致 panic，参考 `gemini_messages_compat_service_test.go` 里现有构造方式补齐最小依赖，不要改生产代码来迁就测试。

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/astray/api-token/sub2api/backend && go test -tags=unit ./internal/service/ -run 'TestSelectGeminiAPIKeyAccountForImages' -count=1`
Expected: FAIL（方法未定义，编译错误）

- [ ] **Step 3: Write minimal implementation**

新建 `backend/internal/service/gemini_images_account_selection.go`：

```go
package service

import (
	"context"
	"errors"
	"fmt"
)

// SelectGeminiAPIKeyAccountForImages 为 OpenAI Images 透传选取 Gemini 平台的
// AI Studio API Key 账号。
// 与 SelectAccountForModelWithExclusions 的差异：
//   - 只接受 Platform==gemini && Type==apikey（OAuth / Vertex SA / Antigravity 混调账号不参与）；
//   - 不使用粘性会话（生图请求无会话语义，与生图调度禁用粘性的策略一致）。
func (s *GeminiMessagesCompatService) SelectGeminiAPIKeyAccountForImages(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	excludedIDs map[int64]struct{},
) (*Account, error) {
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, PlatformGemini, false)
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	candidates := make([]Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].Platform != PlatformGemini || accounts[i].Type != AccountTypeAPIKey {
			continue
		}
		candidates = append(candidates, accounts[i])
	}
	selected := s.selectBestGeminiAccount(ctx, candidates, requestedModel, excludedIDs, PlatformGemini, false)
	if selected == nil {
		if requestedModel != "" {
			return nil, fmt.Errorf("no available Gemini API-key accounts supporting model: %s", requestedModel)
		}
		return nil, errors.New("no available Gemini API-key accounts")
	}
	return s.hydrateSelectedAccount(ctx, selected)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: 同 Step 2 命令。
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/gemini_images_account_selection.go backend/internal/service/gemini_images_account_selection_test.go
git commit -m "feat: add gemini api-key account selection for images passthrough"
```

---

### Task 3: 透传转发 ForwardGeminiImagesPassthrough

**Files:**
- Modify: `backend/internal/service/gemini_images_passthrough.go`（Task 1 创建）
- Test: `backend/internal/service/gemini_images_passthrough_test.go`（追加）

**Interfaces:**
- Consumes（`OpenAIGatewayService` 既有私有设施，均在 openai_images.go 一族中）：`rewriteOpenAIImagesModel(body, contentType, model) ([]byte, string, error)`（:780）、`s.validateUpstreamBaseURL(raw) (string, error)`、`buildOpenAIEndpointURL(base, endpoint) string`（openai_endpoint_url.go:8）、`s.httpUpstream.Do(...)`、`s.readUpstreamErrorBody(resp)`、`extractUpstreamErrorMessage` / `sanitizeUpstreamErrorMessage` / `setOpsUpstreamError` / `appendOpsUpstreamError` / `safeUpstreamURL`、`s.shouldFailoverOpenAIUpstreamResponse(status, msg, body)`（openai_gateway_upstream_errors.go:221）、`openAIImagesUpstreamErrorFromHTTP(status, header, body)`（openai_images_responses.go:783）、`s.handleOpenAIImagesNonStreamingResponse(resp, c)`（openai_images.go:866，负责把成功响应写回客户端并抽取 usage/张数/尺寸）、`account.GetCredential` / `account.GetMappedModel` / `account.ApplyHeaderOverrides` / `account.IsPoolMode` / `account.IsPoolModeRetryableStatus`、`SetOpsLatencyMs` + `OpsUpstreamLatencyMsKey`。
- Produces: `func (s *OpenAIGatewayService) ForwardGeminiImagesPassthrough(ctx context.Context, c *gin.Context, account *Account, body []byte, parsed *OpenAIImagesRequest, channelMappedModel string) (*OpenAIForwardResult, error)` — Task 4 handler 调用。错误契约与 `forwardOpenAIImagesAPIKey` 一致：可切号错误返回 `*UpstreamFailoverError`；用户侧上游错误已写回客户端并返回 `*OpenAIImagesUpstreamError`；其余为普通 error（handler 兜底 502）。

- [ ] **Step 1: Write the failing tests**

在 `gemini_images_passthrough_test.go` 追加（imports 增加 `bytes`, `context`, `io`, `net/http`, `net/http/httptest`, `strings`, `github.com/gin-gonic/gin`）。上游 stub 复用包内已有 `httpUpstreamRecorder`（openai_oauth_passthrough_test.go:27）：

```go
func geminiPassthroughAccount() *Account {
	return &Account{
		ID:          91,
		Name:        "gemini-pool",
		Platform:    PlatformGemini,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "pool-key",
			"base_url": "https://gemini-pool.test",
		},
	}
}

func geminiPassthroughTestContext(t *testing.T, body []byte, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	return c, rec
}

func TestForwardGeminiImagesPassthroughSuccess(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","prompt":"draw a cat","response_format":"url"}`)
	c, rec := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"created":1,"data":[{"url":"https://img.test/1.png"}],"usage":{"input_tokens":8,"output_tokens":1290}}`)),
	}}}
	upstream := svc.httpUpstream.(*httpUpstreamRecorder)

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gemini-2.5-flash-image")
	require.NoError(t, err)

	// 上游侧：URL / 认证 / model 改写
	require.Equal(t, "https://gemini-pool.test/v1/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer pool-key", upstream.lastReq.Header.Get("Authorization"))
	require.JSONEq(t, `{"model":"gemini-2.5-flash-image","prompt":"draw a cat","response_format":"url"}`, string(upstream.lastBody))

	// 客户端侧：响应原样透传（url 字段保留）
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "https://img.test/1.png")

	// 计费字段
	require.Equal(t, "gemini-2.5-flash-image", result.UpstreamModel)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 1290, result.Usage.OutputTokens)
}

func TestForwardGeminiImagesPassthroughRejectsNonGeminiMappedModel(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","prompt":"draw"}`)
	c, _ := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	// 未配置映射：channelMappedModel == 请求模型 gpt-image-1，不是 gemini 生图模型
	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gpt-image-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "gemini image model")
}

func TestForwardGeminiImagesPassthroughMissingBaseURLFailsOver(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw"}`)
	c, _ := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := geminiPassthroughAccount()
	account.Credentials = map[string]any{"api_key": "pool-key"} // 无 base_url

	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, account, body, parsed, "")
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
}

func TestForwardGeminiImagesPassthrough429ReturnsFailover(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw"}`)
	c, rec := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "")
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusTooManyRequests, failover.StatusCode)
	require.Zero(t, rec.Body.Len(), "failover 错误不应写客户端响应")
}

func TestForwardGeminiImagesPassthroughUserErrorWritesThrough(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw"}`)
	c, rec := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad prompt","type":"invalid_request_error"}}`)),
	}}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "")
	var imgErr *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &imgErr)
	require.Equal(t, http.StatusBadRequest, imgErr.StatusCode)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad prompt")
}

func TestForwardGeminiImagesPassthroughMultipartEditsRewritesModel(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf) // import "mime/multipart"
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	require.NoError(t, writer.WriteField("prompt", "make it blue"))
	part, err := writer.CreateFormFile("image", "in.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("\x89PNG fake"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	body := buf.Bytes()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	svc := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":1,"data":[{"b64_json":"aGk="}]}`)),
	}}}
	upstream := svc.httpUpstream.(*httpUpstreamRecorder)

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gemini-2.5-flash-image")
	require.NoError(t, err)
	require.Equal(t, "https://gemini-pool.test/v1/images/edits", upstream.lastReq.URL.String())
	require.Contains(t, upstream.lastReq.Header.Get("Content-Type"), "multipart/form-data")
	require.Contains(t, string(upstream.lastBody), "gemini-2.5-flash-image")
	require.NotContains(t, string(upstream.lastBody), "gpt-image-1")
	require.Contains(t, string(upstream.lastBody), "make it blue")
	require.Equal(t, 1, result.ImageCount)
}
```

注意：若 `s.validateUpstreamBaseURL` 在零值 cfg 下拒绝自定义域名（URL allowlist），参考 `openai_images_test.go` 中使用 `image-upstream.example` 自定义 base_url 的测试如何构造 cfg/env，并照搬。

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/astray/api-token/sub2api/backend && go test -tags=unit ./internal/service/ -run 'TestForwardGeminiImagesPassthrough' -count=1`
Expected: FAIL（方法未定义，编译错误）

- [ ] **Step 3: Write minimal implementation**

在 `backend/internal/service/gemini_images_passthrough.go` 追加（imports 参照 `openai_images.go` 同族文件补齐：`bytes`, `context`, `fmt`, `io`, `net/http`, `time`, gin, responseheaders 包）：

```go
// ForwardGeminiImagesPassthrough 将 OpenAI Images 请求原样透传到 Gemini AI Studio
// API Key 账号 base_url 指向的 OpenAI 兼容上游（如自建号池）。
// 前提：上游支持 OpenAI Images 协议；真·Google AI Studio 原生接口不适用，
// 因此账号必须显式配置 base_url。
// 错误契约与 forwardOpenAIImagesAPIKey 一致：
//   - *UpstreamFailoverError：可切号（429/5xx/账号缺配置）；
//   - *OpenAIImagesUpstreamError：用户侧上游错误，响应已原样写回客户端；
//   - 其他 error：未写响应，由 handler 兜底 502。
func (s *OpenAIGatewayService) ForwardGeminiImagesPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	if account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("unsupported account type for gemini images passthrough: %s", account.Type)
	}
	startTime := time.Now()

	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	upstreamModel := account.GetMappedModel(requestModel)
	if !IsGeminiImageGenerationModel(upstreamModel) {
		return nil, fmt.Errorf("gemini images passthrough requires a gemini image model, got %q", upstreamModel)
	}

	forwardBody, forwardContentType, err := rewriteOpenAIImagesModel(body, parsed.ContentType, upstreamModel)
	if err != nil {
		return nil, err
	}

	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		// 账号缺配置按可切号处理，让调度循环尝试下一个账号
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte("gemini account api_key not configured"),
		}
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte("gemini account base_url not configured for openai images passthrough"),
		}
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	targetURL := buildOpenAIEndpointURL(validatedURL, parsed.Endpoint)

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(forwardBody))
	if err != nil {
		return nil, fmt.Errorf("create upstream request failed: %w", err)
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
	if forwardContentType != "" {
		upstreamReq.Header.Set("Content-Type", forwardContentType)
	}
	account.ApplyHeaderOverrides(upstreamReq.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
			Kind:               "request_error",
			Message:            safeErr,
		})
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		// 用户侧错误：状态码与 body 原样透传给客户端
		imgErr := openAIImagesUpstreamErrorFromHTTP(resp.StatusCode, resp.Header, respBody)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, respBody)
		return nil, imgErr
	}
	defer func() { _ = resp.Body.Close() }()

	usage, imageCount, outputSizes, err := s.handleOpenAIImagesNonStreamingResponse(resp, c)
	if err != nil {
		return nil, err
	}
	finalCount := parsed.N
	if imageCount > 0 {
		finalCount = imageCount
	}
	return &OpenAIForwardResult{
		RequestID:        resp.Header.Get("x-request-id"),
		Usage:            usage,
		Model:            requestModel,
		UpstreamModel:    upstreamModel,
		ResponseHeaders:  resp.Header.Clone(),
		Duration:         time.Since(startTime),
		ImageCount:       finalCount,
		ImageSize:        parsed.SizeTier,
		ImageInputSize:   parsed.Size,
		ImageOutputSizes: outputSizes,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/astray/api-token/sub2api/backend && go test -tags=unit ./internal/service/ -run 'TestForwardGeminiImagesPassthrough|TestIsGeminiImageGenerationModel' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/gemini_images_passthrough.go backend/internal/service/gemini_images_passthrough_test.go
git commit -m "feat: forward openai images passthrough for gemini api-key accounts"
```

---

### Task 4: Handler `GeminiImages` + DI 注入

**Files:**
- Create: `backend/internal/handler/gemini_images.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`（struct :29-41、constructor :117-148）
- Modify: `backend/cmd/server/wire_gen.go`（:259 附近 `handler.NewOpenAIGatewayHandler(...)` 调用）
- Test: `backend/internal/handler/gemini_images_test.go`

**Interfaces:**
- Consumes: Task 2 的 `SelectGeminiAPIKeyAccountForImages`、Task 3 的 `ForwardGeminiImagesPassthrough`、Task 1 的 `service.IsGeminiImageGenerationModel`；handler 包既有辅助（签名见 grok_media.go 同款调用）：`recoverResponsesPanic` / `ensureResponsesDependencies` / `errorResponse` / `checkContentModeration` / `acquireImageGenerationSlot` / `acquireResponsesUserSlot` / `acquireResponsesAccountSlot` / `handleFailoverExhausted` / `classifyNoAccountErrorFromGin` / `setOps*` / `GetInboundEndpoint` / `GetUpstreamEndpoint` / `submitOpenAIUsageRecordTask` / `billingErrorDetails` / `contentModerationStatus` / `contentModerationErrorCode` / `requestLogger` / `extractMaxBytesError` / `buildBodyTooLargeMessage` / `sameAccountRetryDelay`。
- Produces: `func (h *OpenAIGatewayHandler) GeminiImages(c *gin.Context)` — Task 5 路由分发调用；`OpenAIGatewayHandler` 新字段 `geminiCompatService *service.GeminiMessagesCompatService`。

- [ ] **Step 1: DI 注入（先改依赖，保证编译）**

`backend/internal/handler/openai_gateway_handler.go`：

struct 增加字段（放在 `gatewayService` 之后）：

```go
	gatewayService           *service.OpenAIGatewayService
	geminiCompatService      *service.GeminiMessagesCompatService
```

constructor 增加参数（放在 `gatewayService` 之后，body 里同步赋值）：

```go
func NewOpenAIGatewayHandler(
	gatewayService *service.OpenAIGatewayService,
	geminiCompatService *service.GeminiMessagesCompatService,
	concurrencyService *service.ConcurrencyService,
	...
```

`backend/cmd/server/wire_gen.go` :259 的调用同步插入实参 `geminiMessagesCompatService`（该变量在 :157 已构造）：

```go
openAIGatewayHandler := handler.NewOpenAIGatewayHandler(openAIGatewayService, geminiMessagesCompatService, concurrencyService, billingCacheService, apiKeyService, usageRecordWorkerPool, errorPassthroughService, contentModerationService, opsService, configConfig)
```

Run: `cd /Users/astray/api-token/sub2api/backend && go build ./...`
Expected: 编译通过。同时全仓 grep `NewOpenAIGatewayHandler(` 确认没有其他调用点（若测试里有，同步补参）。

- [ ] **Step 2: Write the failing handler tests**

新建 `backend/internal/handler/gemini_images_test.go`（构造模式照抄 `openai_images_controls_test.go:51`；import 别名 `middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"`）：

```go
package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newGeminiImagesTestHandler() *OpenAIGatewayHandler {
	return &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		geminiCompatService: &service.GeminiMessagesCompatService{},
		billingCacheService: &service.BillingCacheService{},
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}},
	}
}

func geminiImagesTestContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	// GroupID 置空：跳过渠道映射的 DB 依赖（映射结果 = 请求模型原样）
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:    222,
		Group: &service.Group{ID: 111, Platform: service.PlatformGemini, AllowImageGeneration: true},
		User:  &service.User{ID: 333},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 333, Concurrency: 1})
	return c, rec
}

func TestGeminiImagesRejectsStream(t *testing.T) {
	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw","stream":true}`))
	h := newGeminiImagesTestHandler()
	h.GeminiImages(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "stream")
}

func TestGeminiImagesRejectsDisabledGroup(t *testing.T) {
	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw"}`))
	apiKeyVal, _ := c.Get(string(middleware2.ContextKeyAPIKey))
	apiKeyVal.(*service.APIKey).Group.AllowImageGeneration = false
	h := newGeminiImagesTestHandler()
	h.GeminiImages(c)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}

func TestGeminiImagesRejectsNonGeminiMappedModel(t *testing.T) {
	// 请求模型 gpt-image-1，无渠道映射（GroupID 为空）→ 映射后仍为 gpt-image-1 → 404
	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gpt-image-1","prompt":"draw"}`))
	h := newGeminiImagesTestHandler()
	h.GeminiImages(c)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "gpt-image-1")
}
```

Run: `cd /Users/astray/api-token/sub2api/backend && go test -tags=unit ./internal/handler/ -run 'TestGeminiImages' -count=1`
Expected: FAIL（`GeminiImages` 未定义，编译错误）

注意：`TestGeminiImagesRejectsNonGeminiMappedModel` 依赖 `h.gatewayService.ResolveChannelMappingAndRestrict` 在零值 service + `GroupID == nil` 时直接返回 `{MappedModel: model}`（channel_service.go:516-522 有 nil groupID 短路）。写测试前先确认该短路在 `OpenAIGatewayService` 的委托方法里同样成立（grep `func (s *OpenAIGatewayService) ResolveChannelMappingAndRestrict`）；若零值会 panic，改为给 handler 注入最小可用 channel 依赖（参考 openai handler 测试的做法），不要改生产代码。

- [ ] **Step 3: Write the handler implementation**

新建 `backend/internal/handler/gemini_images.go`。整体结构 = `handleGrokMedia` 骨架 + `openai_images.go` 的 Images 细节，按下面代码实现（imports 与 grok_media.go 相同一族）：

```go
package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GeminiImages 处理 Gemini 分组的 OpenAI Images 请求：
// 请求原样透传到账号 base_url 指向的 OpenAI 兼容上游（仅 AI Studio API Key 账号）。
// 编排结构对齐 handleGrokMedia；v1 仅支持非流式。
func (h *OpenAIGatewayHandler) GeminiImages(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.gemini_images",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}
	if h.geminiCompatService == nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Gemini gateway is not configured")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	setOpsRequestContext(c, "", false)

	parsed, err := h.gatewayService.ParseOpenAIImagesRequest(c, body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if parsed.Stream {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "stream is not supported for image generation on Gemini groups")
		return
	}
	requestModel := parsed.Model
	reqLog = reqLog.With(
		zap.String("model", requestModel),
		zap.Bool("multipart", parsed.Multipart),
	)

	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, requestModel, parsed.ModerationBody()); decision != nil && decision.Blocked {
		h.errorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
		return
	}
	imageReleaseFunc, acquired := h.acquireImageGenerationSlot(c, streamStarted)
	if !acquired {
		return
	}
	if imageReleaseFunc != nil {
		defer imageReleaseFunc()
	}

	setOpsRequestContext(c, requestModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))

	// 分组模型映射（无映射时 MappedModel == 请求模型），随后严格校验映射结果
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, requestModel)
	mappedModel := channelMapping.MappedModel
	if !service.IsGeminiImageGenerationModel(mappedModel) {
		h.errorResponse(c, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("Model %q is not available for image generation on Gemini groups (mapped model %q is not a gemini image model)", requestModel, mappedModel))
		return
	}

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("gemini_images.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	requestCtx := service.WithOpenAIImageGenerationIntent(c.Request.Context())

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError

	for {
		account, err := h.geminiCompatService.SelectGeminiAPIKeyAccountForImages(requestCtx, apiKey.GroupID, mappedModel, failedAccountIDs)
		if err != nil || account == nil {
			if err != nil {
				reqLog.Warn("gemini_images.account_select_failed",
					zap.Error(err),
					zap.Int("excluded_account_count", len(failedAccountIDs)),
				)
			}
			if len(failedAccountIDs) == 0 {
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, mappedModel, requestModel, service.PlatformGemini)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				h.errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")
			}
			return
		}

		sessionHash := ensureOpenAIPoolModeSessionHash("", account)
		setOpsSelectedAccount(c, account.ID, account.Platform)
		reqLog.Debug("gemini_images.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		writerSizeBeforeForward := c.Writer.Size()

		selection := &service.AccountSelectionResult{Account: account}
		accountReleaseFunc, acquired := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, false, &streamStarted, reqLog)
		if !acquired {
			return
		}
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			return h.gatewayService.ForwardGeminiImagesPassthrough(requestCtx, c, account, body, parsed, mappedModel)
		}()
		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)

		if err != nil {
			var imageUpstreamErr *service.OpenAIImagesUpstreamError
			if errors.As(err, &imageUpstreamErr) {
				// 用户侧上游错误：响应已由 service 原样写回，不切号
				reqLog.Warn("gemini_images.upstream_user_error",
					zap.Int64("account_id", account.ID),
					zap.Int("status_code", imageUpstreamErr.StatusCode),
					zap.String("error_type", imageUpstreamErr.ErrorType),
				)
				return
			}
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				if c.Writer.Size() != writerSizeBeforeForward {
					reqLog.Warn("gemini_images.upstream_failover_skipped_after_flush",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
					)
					h.handleFailoverExhausted(c, failoverErr, true)
					return
				}
				if failoverErr.RetryableOnSameAccount {
					retryLimit := account.GetPoolModeRetryCount()
					if sameAccountRetryCount[account.ID] < retryLimit {
						sameAccountRetryCount[account.ID]++
						reqLog.Warn("gemini_images.pool_mode_same_account_retry",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
							zap.Int("retry_count", sameAccountRetryCount[account.ID]),
						)
						select {
						case <-requestCtx.Done():
							return
						case <-time.After(sameAccountRetryDelay):
						}
						continue
					}
				}
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				switchCount++
				reqLog.Warn("gemini_images.upstream_failover_switching",
					zap.Int64("account_id", account.ID),
					zap.Int("upstream_status", failoverErr.StatusCode),
					zap.Int("switch_count", switchCount),
					zap.Int("max_switches", maxAccountSwitches),
				)
				continue
			}
			if c.Writer.Size() == writerSizeBeforeForward {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			reqLog.Warn("gemini_images.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
			return
		}

		recordGeminiImagesUsage(c, h, reqLog, apiKey, subject, subscription, account, result, requestModel, body, parsed, channelMapping)
		reqLog.Debug("gemini_images.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

func recordGeminiImagesUsage(
	c *gin.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	subscription *service.UserSubscription,
	account *service.Account,
	result *service.OpenAIForwardResult,
	requestModel string,
	body []byte,
	parsed *service.OpenAIImagesRequest,
	channelMapping service.ChannelMappingResult,
) {
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	if parsed.Multipart {
		requestPayloadHash = service.HashUsageRequestPayload([]byte(parsed.StickySessionSeed()))
	}
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	upstreamModel := ""
	if result != nil {
		upstreamModel = result.UpstreamModel
	}
	h.submitOpenAIUsageRecordTask(c.Request.Context(), result, func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result:             result,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			RequestPayloadHash: requestPayloadHash,
			APIKeyService:      h.apiKeyService,
			QuotaPlatform:      quotaPlatform,
			ChannelUsageFields: channelMapping.ToUsageFields(requestModel, upstreamModel),
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.gemini_images"),
				zap.Int64("user_id", subject.UserID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
				zap.String("model", requestModel),
				zap.Int64("account_id", account.ID),
			).Error("gemini_images.record_usage_failed", zap.Error(err))
			reqLog.Debug("gemini_images.record_usage_failed", zap.Error(err))
		}
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/astray/api-token/sub2api/backend && go test -tags=unit ./internal/handler/ -run 'TestGeminiImages' -count=1 && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 5: Commit**

```bash
git add backend/internal/handler/gemini_images.go backend/internal/handler/gemini_images_test.go backend/internal/handler/openai_gateway_handler.go backend/cmd/server/wire_gen.go
git commit -m "feat: add gemini images handler with api-key failover loop"
```

---

### Task 5: 路由分发 + 上游端点归一化

**Files:**
- Modify: `backend/internal/server/routes/gateway.go`（imagesHandler，:45-60）
- Modify: `backend/internal/handler/endpoint.go`（`DeriveUpstreamEndpoint` 的 `PlatformGemini` case，:205-206）
- Test: `backend/internal/server/routes/gateway_test.go`、`backend/internal/handler/endpoint_test.go`

**Interfaces:**
- Consumes: Task 4 的 `h.OpenAIGateway.GeminiImages`。
- Produces: Gemini 分组路由生效；usage 日志的 `upstream_endpoint` 对 gemini 生图记为 `/v1/images/*` 而不是 `/v1beta/models`。

- [ ] **Step 1: Write the failing tests**

`backend/internal/server/routes/gateway_test.go` 追加（模式照抄 `TestGatewayRoutesGrokImagesAndVideosPathsAreRegistered`，:116）：

```go
func TestGatewayRoutesGeminiImagesPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformGemini)

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gemini-2.5-flash-image","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Gemini images handler", path)
		require.NotContains(t, w.Body.String(), "Images API is not supported for this platform", "path=%s", path)
	}
}
```

`backend/internal/handler/endpoint_test.go` 在 `DeriveUpstreamEndpoint` 的表驱动用例（:126 附近）追加两行：

```go
		{"gemini image generations", EndpointImagesGenerations, "/v1/images/generations", service.PlatformGemini, EndpointImagesGenerations},
		{"gemini image edits", EndpointImagesEdits, "/openai/v1/images/edits", service.PlatformGemini, EndpointImagesEdits},
```

Run: `cd /Users/astray/api-token/sub2api/backend && go test -tags=unit ./internal/server/routes/ -run 'TestGatewayRoutesGeminiImages' -count=1; go test -tags=unit ./internal/handler/ -run 'TestDeriveUpstreamEndpoint' -count=1`
Expected: 两者均 FAIL（路由 404 + 派生端点为 `/v1beta/models`）

- [ ] **Step 2: Write minimal implementation**

`backend/internal/server/routes/gateway.go` imagesHandler 加分支：

```go
	imagesHandler := func(c *gin.Context) {
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI:
			h.OpenAIGateway.Images(c)
		case service.PlatformGrok:
			h.OpenAIGateway.GrokImages(c)
		case service.PlatformGemini:
			h.OpenAIGateway.GeminiImages(c)
		default:
			...（原样不动）
		}
	}
```

`backend/internal/handler/endpoint.go` `DeriveUpstreamEndpoint` 的 gemini case 改为：

```go
	case service.PlatformGemini:
		switch inbound {
		case EndpointImagesGenerations, EndpointImagesEdits:
			// Gemini 分组的 OpenAI Images 透传：上游即同名 images 端点
			return inbound
		}
		return EndpointGeminiModels
```

- [ ] **Step 3: Run tests to verify they pass**

Run: 同 Step 1 两条命令。
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add backend/internal/server/routes/gateway.go backend/internal/server/routes/gateway_test.go backend/internal/handler/endpoint.go backend/internal/handler/endpoint_test.go
git commit -m "feat: route gemini groups to openai images passthrough"
```

---

### Task 6: 全量验证 + 文档收尾

**Files:**
- Modify: `docs/superpowers/specs/2026-07-15-gemini-openai-images-design.md`（状态行 → 已实现）

- [ ] **Step 1: 全量单测 + 构建 + vet**

Run:
```bash
cd /Users/astray/api-token/sub2api/backend && go build ./... && go vet ./... && make test-unit
```
Expected: 全部通过。若既有测试因 `NewOpenAIGatewayHandler` 签名变化失败，在对应测试中补 `nil` 或最小实参修复。

- [ ] **Step 2: 更新 spec 状态并提交**

spec 头部 `- 状态：已确认（待实现）` 改为 `- 状态：已实现（feature/gemini-openai-images）`。

```bash
git add docs/superpowers/specs/2026-07-15-gemini-openai-images-design.md
git commit -m "docs: mark gemini openai images passthrough spec as implemented"
```
