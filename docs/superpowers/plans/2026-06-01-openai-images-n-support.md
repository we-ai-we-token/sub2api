# OpenAI Images N Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support OpenAI Images `n` for OAuth/Responses accounts by generating up to 5 images through parallel `n=1` upstream calls, returning standard OpenAI Images responses with partial success and aggregated usage.

**Architecture:** Keep API-key image forwarding unchanged. For OAuth image requests with `n > 1`, the handler delegates to a new service-level fan-out path that schedules each image attempt independently, forwards each subrequest with `N=1`, retries each failed subrequest once with fresh scheduling, aggregates successful image results and usage, and returns a normal OpenAI Images JSON response. OAuth single-image and streaming behavior stay on the existing path.

**Tech Stack:** Go, Gin, OpenAI Responses API bridge, existing scheduler/account selection, existing usage/billing structs, `tidwall/gjson` and `tidwall/sjson`, Go unit tests.

---

## Files

- Modify: `backend/internal/service/openai_images.go`
  - Enforce `n <= 5` while parsing JSON and multipart image requests.
  - Keep API-key forwarding behavior unchanged.
  - Add small helpers for cloning image requests with `N=1` if not placed in the Responses file.

- Modify: `backend/internal/service/openai_images_responses.go`
  - Stop writing `tools[0].n` into Responses image_generation tool requests.
  - Add internal OAuth fan-out primitives that return parsed image results instead of immediately writing the HTTP response.
  - Add usage aggregation helpers.
  - Reuse `buildOpenAIImagesAPIResponse` so the final response remains the standard OpenAI Images format.

- Modify: `backend/internal/handler/openai_images.go`
  - Detect OAuth `n > 1` after account selection.
  - Release the initially selected account slot before fan-out scheduling if the selected account is OAuth and fan-out will handle its own scheduling.
  - Call the service fan-out method with group/model/capability/excluded-account context.
  - Preserve existing retry/failover behavior for API-key requests and OAuth `n == 1`.

- Modify: `backend/internal/service/openai_images_test.go`
  - Replace the existing test that expects `tools.0.n`.
  - Add tests for max `n`, no `tools.0.n`, parallel fan-out call count, partial success, usage aggregation, and zero-success failure.

- Reuse: `backend/internal/service/openai_oauth_passthrough_test.go`
  - Reuse `httpUpstreamRecorder` queued responses for fan-out tests.

---

### Task 1: Validate `n` maximum at request parsing

**Files:**
- Modify: `backend/internal/service/openai_images.go`
- Test: `backend/internal/service/openai_images_test.go`

- [ ] **Step 1: Add failing JSON parse test for `n > 5`**

Add this test near the existing OpenAI Images parse tests:

```go
func TestParseOpenAIImagesRequestRejectsJSONNAboveFive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","n":6}`)

	_, err := svc.ParseOpenAIImagesRequest(ctx, body)

	require.Error(t, err)
	require.Contains(t, err.Error(), "n must be less than or equal to 5")
}
```

- [ ] **Step 2: Add failing multipart parse test for `n > 5`**

Add this test near existing multipart image parse tests:

```go
func TestParseOpenAIImagesRequestRejectsMultipartNAboveFive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "draw a cat"))
	require.NoError(t, writer.WriteField("n", "6"))
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = req

	_, err := svc.ParseOpenAIImagesRequest(ctx, body.Bytes())

	require.Error(t, err)
	require.Contains(t, err.Error(), "n must be less than or equal to 5")
}
```

- [ ] **Step 3: Run the targeted tests and verify they fail**

Run:

```bash
go test ./backend/internal/service -run 'TestParseOpenAIImagesRequestRejects(JSON|Multipart)NAboveFive' -count=1
```

Expected: FAIL because `n=6` is currently accepted.

- [ ] **Step 4: Implement max `n` validation**

In `backend/internal/service/openai_images.go`, add a package constant near the image request parsing code:

```go
const maxOpenAIImagesN = 5
```

Update JSON parsing after `req.N <= 0` check:

```go
if req.N > maxOpenAIImagesN {
	return fmt.Errorf("n must be less than or equal to %d", maxOpenAIImagesN)
}
```

Update multipart parsing after `n <= 0` check:

```go
if n > maxOpenAIImagesN {
	return fmt.Errorf("n must be less than or equal to %d", maxOpenAIImagesN)
}
```

- [ ] **Step 5: Run tests and verify they pass**

Run:

```bash
go test ./backend/internal/service -run 'TestParseOpenAIImagesRequestRejects(JSON|Multipart)NAboveFive' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/openai_images.go backend/internal/service/openai_images_test.go
git commit -m "fix: validate OpenAI images n limit"
```

---

### Task 2: Stop sending `tools[0].n` to Responses image generation

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go`
- Test: `backend/internal/service/openai_images_test.go`

- [ ] **Step 1: Replace the existing `tools.0.n` expectation test**

Find `TestOpenAIGatewayServiceForwardImages_OAuthPassesNAndReturnsAllImages` in `backend/internal/service/openai_images_test.go` and replace it with:

```go
func TestOpenAIGatewayServiceForwardImages_OAuthDoesNotPassToolN(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"1024x1024","quality":"high","n":3}`)

	upstream := &httpUpstreamRecorder{resp: jsonResponse(http.StatusOK, `{
		"id":"resp_1",
		"created_at":1710000000,
		"output":[{"type":"image_generation_call","result":"img-1","size":"1024x1024"}],
		"usage":{"input_tokens":2,"output_tokens":3,"output_tokens_details":{"image_tokens":4}}
	}`)}
	svc := newTestOpenAIGatewayServiceWithHTTPUpstream(upstream)
	account := &Account{ID: 1, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token"}
	parsed, err := svc.ParseOpenAIImagesRequest(testGinContext(body, "application/json"), body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))

	result, err := svc.ForwardImages(context.Background(), ctx, account, body, parsed, "")

	require.NoError(t, err)
	require.Equal(t, 1, upstream.calls)
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools.0.n").Exists())
	require.Len(t, gjson.Get(rec.Body.String(), "data").Array(), 1)
	require.Equal(t, 1, result.ImageCount)
}
```

If helper names differ in the file, keep the existing helper names already used by the old test and only change the assertions/body.

- [ ] **Step 2: Run the replacement test and verify it fails**

Run:

```bash
go test ./backend/internal/service -run TestOpenAIGatewayServiceForwardImages_OAuthDoesNotPassToolN -count=1
```

Expected: FAIL because `tools.0.n` currently exists for `n=3`.

- [ ] **Step 3: Remove `tools[0].n` emission**

In `backend/internal/service/openai_images_responses.go`, remove this block from `buildOpenAIImagesResponsesRequest`:

```go
if shouldPassOpenAIImagesN(toolModel, parsed.N) {
	tool, _ = sjson.SetBytes(tool, "n", parsed.N)
}
```

Delete `shouldPassOpenAIImagesN` if it is now unused.

- [ ] **Step 4: Run the test and verify it passes**

Run:

```bash
go test ./backend/internal/service -run TestOpenAIGatewayServiceForwardImages_OAuthDoesNotPassToolN -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "fix: omit unsupported OpenAI image tool n"
```

---

### Task 3: Extract OAuth image forwarding into a reusable single-attempt primitive

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go`
- Test: `backend/internal/service/openai_images_test.go`

- [ ] **Step 1: Add a regression test for unchanged single OAuth forwarding**

Add this test to ensure the extraction preserves the current single-image response behavior:

```go
func TestOpenAIGatewayServiceForwardImages_OAuthSingleImageStillReturnsStandardResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","n":1}`)
	upstream := &httpUpstreamRecorder{resp: jsonResponse(http.StatusOK, `{
		"id":"resp_1",
		"created_at":1710000000,
		"output":[{"type":"image_generation_call","result":"img-1","size":"1024x1024"}],
		"usage":{"input_tokens":2,"output_tokens":3,"output_tokens_details":{"image_tokens":4}}
	}`)}
	svc := newTestOpenAIGatewayServiceWithHTTPUpstream(upstream)
	account := &Account{ID: 1, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token"}
	parsed, err := svc.ParseOpenAIImagesRequest(testGinContext(body, "application/json"), body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))

	result, err := svc.ForwardImages(context.Background(), ctx, account, body, parsed, "")

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "img-1", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.ImageOutputTokens)
}
```

- [ ] **Step 2: Run the regression test and verify it passes before refactor**

Run:

```bash
go test ./backend/internal/service -run TestOpenAIGatewayServiceForwardImages_OAuthSingleImageStillReturnsStandardResponse -count=1
```

Expected: PASS before refactor.

- [ ] **Step 3: Add internal result struct**

In `backend/internal/service/openai_images_responses.go`, add near the existing `openAIResponsesImageResult` type:

```go
type openAIImagesOAuthForwardOutput struct {
	Usage           OpenAIUsage
	ImageResults    []openAIResponsesImageResult
	ImageSizes      []string
	CreatedAt       int64
	UsageRaw        []byte
	FirstMeta       openAIResponsesImageResult
	ResponseHeaders http.Header
	UpstreamModel   string
	RequestID       string
	ResponseID      string
	FirstTokenMs    *int
}
```

Ensure `net/http` is already imported in this file; if not, add it.

- [ ] **Step 4: Extract the current OAuth request/parse logic**

Create a new method in `backend/internal/service/openai_images_responses.go`:

```go
func (s *OpenAIGatewayService) forwardOpenAIImagesOAuthOnce(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*openAIImagesOAuthForwardOutput, error) {
	// Move the existing upstream request loop from forwardOpenAIImagesOAuth here.
	// Keep maxEmptyOutputAttempts behavior inside this method.
	// Do not write to c.Writer here.
	// Return parsed image results, usage, headers, IDs, created_at, and metadata.
}
```

The moved code should keep these existing behaviors:

```go
responsesBody, err := buildOpenAIImagesResponsesRequest(parsed, requestModel)
```

```go
usage, _ := parseOpenAIImageToolUsage(usageRaw)
```

```go
return &openAIImagesOAuthForwardOutput{
	Usage:           usage,
	ImageResults:    results,
	ImageSizes:      openAIResponsesImageResultSizes(results),
	CreatedAt:       createdAt,
	UsageRaw:        usageRaw,
	FirstMeta:       firstMeta,
	ResponseHeaders: resp.Header.Clone(),
	UpstreamModel:   requestModel,
	RequestID:       resp.Header.Get("x-request-id"),
	ResponseID:      responseID,
	FirstTokenMs:    firstTokenMs,
}, nil
```

- [ ] **Step 5: Rewrite `forwardOpenAIImagesOAuth` as a response writer wrapper**

After extraction, `forwardOpenAIImagesOAuth` should call the primitive and write the standard response:

```go
func (s *OpenAIGatewayService) forwardOpenAIImagesOAuth(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	output, err := s.forwardOpenAIImagesOAuthOnce(ctx, c, account, parsed, channelMappedModel)
	if err != nil {
		return nil, err
	}

	responseBody, err := buildOpenAIImagesAPIResponse(output.ImageResults, output.CreatedAt, output.UsageRaw, output.FirstMeta, parsed.ResponseFormat)
	if err != nil {
		return nil, err
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), output.ResponseHeaders, s.responseHeaderFilter)
	c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)

	return &OpenAIForwardResult{
		RequestID:        output.RequestID,
		ResponseID:       output.ResponseID,
		Usage:            output.Usage,
		Model:            parsed.Model,
		BillingModel:     parsed.Model,
		UpstreamModel:    output.UpstreamModel,
		ResponseHeaders:  output.ResponseHeaders,
		FirstTokenMs:     output.FirstTokenMs,
		ImageCount:       len(output.ImageResults),
		ImageOutputSizes: output.ImageSizes,
	}, nil
}
```

Use the exact fields currently returned by the existing function if they differ; preserve existing billing/model semantics.

- [ ] **Step 6: Run OAuth image tests**

Run:

```bash
go test ./backend/internal/service -run 'TestOpenAIGatewayServiceForwardImages_OAuth' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "refactor: extract OpenAI image OAuth attempt"
```

---

### Task 4: Add OAuth fan-out aggregation helper

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go`
- Test: `backend/internal/service/openai_images_test.go`

- [ ] **Step 1: Add usage aggregation test**

Add a focused test for helper behavior:

```go
func TestAggregateOpenAIImagesOAuthOutputs(t *testing.T) {
	outputs := []*openAIImagesOAuthForwardOutput{
		{
			Usage: OpenAIUsage{InputTokens: 1, OutputTokens: 2, CacheCreationInputTokens: 3, CacheReadInputTokens: 4, ImageOutputTokens: 5},
			ImageResults: []openAIResponsesImageResult{{B64JSON: "img-1", Size: "1024x1024"}},
			ImageSizes: []string{"1024x1024"},
			CreatedAt: 100,
		},
		{
			Usage: OpenAIUsage{InputTokens: 10, OutputTokens: 20, CacheCreationInputTokens: 30, CacheReadInputTokens: 40, ImageOutputTokens: 50},
			ImageResults: []openAIResponsesImageResult{{B64JSON: "img-2", Size: "1536x1024"}},
			ImageSizes: []string{"1536x1024"},
			CreatedAt: 200,
		},
	}

	aggregated := aggregateOpenAIImagesOAuthOutputs(outputs)

	require.Equal(t, 11, aggregated.Usage.InputTokens)
	require.Equal(t, 22, aggregated.Usage.OutputTokens)
	require.Equal(t, 33, aggregated.Usage.CacheCreationInputTokens)
	require.Equal(t, 44, aggregated.Usage.CacheReadInputTokens)
	require.Equal(t, 55, aggregated.Usage.ImageOutputTokens)
	require.Len(t, aggregated.ImageResults, 2)
	require.Equal(t, []string{"1024x1024", "1536x1024"}, aggregated.ImageSizes)
	require.Equal(t, int64(100), aggregated.CreatedAt)
}
```

Adjust field names for `openAIResponsesImageResult` to match the actual struct if needed.

- [ ] **Step 2: Run helper test and verify it fails**

Run:

```bash
go test ./backend/internal/service -run TestAggregateOpenAIImagesOAuthOutputs -count=1
```

Expected: FAIL because the helper does not exist.

- [ ] **Step 3: Implement aggregation helper**

In `backend/internal/service/openai_images_responses.go`, add:

```go
func aggregateOpenAIUsage(usages ...OpenAIUsage) OpenAIUsage {
	var total OpenAIUsage
	for _, usage := range usages {
		total.InputTokens += usage.InputTokens
		total.OutputTokens += usage.OutputTokens
		total.CacheCreationInputTokens += usage.CacheCreationInputTokens
		total.CacheReadInputTokens += usage.CacheReadInputTokens
		total.ImageOutputTokens += usage.ImageOutputTokens
	}
	return total
}

func aggregateOpenAIImagesOAuthOutputs(outputs []*openAIImagesOAuthForwardOutput) *openAIImagesOAuthForwardOutput {
	aggregated := &openAIImagesOAuthForwardOutput{}
	for _, output := range outputs {
		if output == nil || len(output.ImageResults) == 0 {
			continue
		}
		if aggregated.CreatedAt == 0 || output.CreatedAt < aggregated.CreatedAt {
			aggregated.CreatedAt = output.CreatedAt
		}
		if aggregated.FirstMeta.IsZero() {
			aggregated.FirstMeta = output.FirstMeta
		}
		if aggregated.ResponseHeaders == nil && output.ResponseHeaders != nil {
			aggregated.ResponseHeaders = output.ResponseHeaders.Clone()
		}
		if aggregated.UpstreamModel == "" {
			aggregated.UpstreamModel = output.UpstreamModel
		}
		if aggregated.RequestID == "" {
			aggregated.RequestID = output.RequestID
		}
		if aggregated.ResponseID == "" {
			aggregated.ResponseID = output.ResponseID
		}
		if aggregated.FirstTokenMs == nil {
			aggregated.FirstTokenMs = output.FirstTokenMs
		}
		aggregated.Usage = aggregateOpenAIUsage(aggregated.Usage, output.Usage)
		aggregated.ImageResults = append(aggregated.ImageResults, output.ImageResults...)
		aggregated.ImageSizes = append(aggregated.ImageSizes, output.ImageSizes...)
	}
	return aggregated
}
```

If `openAIResponsesImageResult` has no `IsZero` method, implement this helper instead and use it:

```go
func isZeroOpenAIResponsesImageResult(result openAIResponsesImageResult) bool {
	return result.B64JSON == "" && result.URL == "" && result.RevisedPrompt == "" && result.Size == ""
}
```

- [ ] **Step 4: Run helper test and verify it passes**

Run:

```bash
go test ./backend/internal/service -run TestAggregateOpenAIImagesOAuthOutputs -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "feat: aggregate OpenAI image fanout results"
```

---

### Task 5: Implement service-level OAuth fan-out with one retry per image

**Files:**
- Modify: `backend/internal/service/openai_images.go`
- Modify: `backend/internal/service/openai_images_responses.go`
- Test: `backend/internal/service/openai_images_test.go`

- [ ] **Step 1: Add fan-out test that makes one upstream call per requested image**

Add this service test using a fake scheduler hook if one exists. If the existing tests instantiate real scheduler state, follow that pattern. The behavior to assert is:

```go
func TestOpenAIGatewayServiceForwardImagesOAuthFanoutCallsUpstreamOncePerImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","n":3}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		jsonResponse(http.StatusOK, `{"id":"resp_1","created_at":1710000001,"output":[{"type":"image_generation_call","result":"img-1","size":"1024x1024"}],"usage":{"input_tokens":1,"output_tokens":2,"output_tokens_details":{"image_tokens":3}}}`),
		jsonResponse(http.StatusOK, `{"id":"resp_2","created_at":1710000002,"output":[{"type":"image_generation_call","result":"img-2","size":"1024x1024"}],"usage":{"input_tokens":4,"output_tokens":5,"output_tokens_details":{"image_tokens":6}}}`),
		jsonResponse(http.StatusOK, `{"id":"resp_3","created_at":1710000003,"output":[{"type":"image_generation_call","result":"img-3","size":"1024x1024"}],"usage":{"input_tokens":7,"output_tokens":8,"output_tokens_details":{"image_tokens":9}}}`),
	}}
	svc := newTestOpenAIGatewayServiceWithHTTPUpstream(upstream)
	accounts := []*Account{
		{ID: 1, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-1"},
		{ID: 2, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-2"},
		{ID: 3, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-3"},
	}
	installTestOpenAIImageScheduler(t, svc, accounts)

	parsed, err := svc.ParseOpenAIImagesRequest(testGinContext(body, "application/json"), body)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))

	result, err := svc.ForwardImagesOAuthFanout(context.Background(), ctx, nil, parsed, "", nil)

	require.NoError(t, err)
	require.Equal(t, 3, upstream.calls)
	for _, upstreamBody := range upstream.bodies {
		require.False(t, gjson.GetBytes(upstreamBody, "tools.0.n").Exists())
	}
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, gjson.Get(rec.Body.String(), "data").Array(), 3)
	require.Equal(t, 3, result.ImageCount)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 15, result.Usage.OutputTokens)
	require.Equal(t, 18, result.Usage.ImageOutputTokens)
}
```

If `ForwardImagesOAuthFanout` should remain unexported, name it `forwardImagesOAuthFanout` and keep the test in package `service`.

- [ ] **Step 2: Add partial success test**

Add:

```go
func TestOpenAIGatewayServiceForwardImagesOAuthFanoutReturnsPartialSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","n":3}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		jsonResponse(http.StatusInternalServerError, `{"error":{"message":"temporary","type":"server_error"}}`),
		jsonResponse(http.StatusInternalServerError, `{"error":{"message":"temporary","type":"server_error"}}`),
		jsonResponse(http.StatusOK, `{"id":"resp_2","created_at":1710000002,"output":[{"type":"image_generation_call","result":"img-2","size":"1024x1024"}],"usage":{"input_tokens":4,"output_tokens":5,"output_tokens_details":{"image_tokens":6}}}`),
		jsonResponse(http.StatusOK, `{"id":"resp_3","created_at":1710000003,"output":[{"type":"image_generation_call","result":"img-3","size":"1024x1024"}],"usage":{"input_tokens":7,"output_tokens":8,"output_tokens_details":{"image_tokens":9}}}`),
	}}
	svc := newTestOpenAIGatewayServiceWithHTTPUpstream(upstream)
	installTestOpenAIImageScheduler(t, svc, []*Account{
		{ID: 1, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-1"},
		{ID: 2, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-2"},
		{ID: 3, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-3"},
		{ID: 4, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-4"},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(testGinContext(body, "application/json"), body)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))

	result, err := svc.ForwardImagesOAuthFanout(context.Background(), ctx, nil, parsed, "", nil)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, gjson.Get(rec.Body.String(), "data").Array(), 2)
	require.Equal(t, 2, result.ImageCount)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 13, result.Usage.OutputTokens)
	require.Equal(t, 15, result.Usage.ImageOutputTokens)
}
```

- [ ] **Step 3: Add zero-success test**

Add:

```go
func TestOpenAIGatewayServiceForwardImagesOAuthFanoutReturnsErrorWhenAllAttemptsFail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","n":2}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		jsonResponse(http.StatusInternalServerError, `{"error":{"message":"temporary","type":"server_error"}}`),
		jsonResponse(http.StatusInternalServerError, `{"error":{"message":"temporary","type":"server_error"}}`),
		jsonResponse(http.StatusInternalServerError, `{"error":{"message":"temporary","type":"server_error"}}`),
		jsonResponse(http.StatusInternalServerError, `{"error":{"message":"temporary","type":"server_error"}}`),
	}}
	svc := newTestOpenAIGatewayServiceWithHTTPUpstream(upstream)
	installTestOpenAIImageScheduler(t, svc, []*Account{
		{ID: 1, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-1"},
		{ID: 2, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-2"},
		{ID: 3, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-3"},
		{ID: 4, Type: AccountTypeOAuth, Platform: PlatformOpenAI, AccessToken: "token-4"},
	})
	parsed, err := svc.ParseOpenAIImagesRequest(testGinContext(body, "application/json"), body)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))

	result, err := svc.ForwardImagesOAuthFanout(context.Background(), ctx, nil, parsed, "", nil)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 4, upstream.calls)
}
```

- [ ] **Step 4: Run fan-out tests and verify they fail**

Run:

```bash
go test ./backend/internal/service -run 'TestOpenAIGatewayServiceForwardImagesOAuthFanout' -count=1
```

Expected: FAIL because fan-out method and scheduler test helper do not exist.

- [ ] **Step 5: Add request clone helper**

In `backend/internal/service/openai_images.go` or `openai_images_responses.go`, add:

```go
func cloneOpenAIImagesRequestForSingleImage(parsed *OpenAIImagesRequest) *OpenAIImagesRequest {
	if parsed == nil {
		return nil
	}
	clone := *parsed
	clone.N = 1
	return &clone
}
```

- [ ] **Step 6: Implement fan-out method**

In `backend/internal/service/openai_images_responses.go`, add:

```go
func (s *OpenAIGatewayService) ForwardImagesOAuthFanout(
	ctx context.Context,
	c *gin.Context,
	groupID *int64,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
	initialExcludedIDs map[int64]struct{},
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	if parsed.N <= 1 {
		return nil, fmt.Errorf("fanout requires n greater than 1")
	}

	requested := parsed.N
	type fanoutResult struct {
		output *openAIImagesOAuthForwardOutput
		err    error
	}
	results := make(chan fanoutResult, requested)

	for i := 0; i < requested; i++ {
		go func() {
			var lastErr error
			excludedIDs := cloneOpenAIAccountExcludedIDs(initialExcludedIDs)
			for attempt := 0; attempt < 2; attempt++ {
				selection, _, err := s.SelectAccountWithSchedulerForImages(ctx, groupID, "", parsed.Model, excludedIDs, parsed.RequiredCapability)
				if err != nil {
					lastErr = err
					break
				}
				if selection == nil || selection.Account == nil {
					lastErr = fmt.Errorf("No available compatible accounts")
					break
				}
				account := selection.Account
				single := cloneOpenAIImagesRequestForSingleImage(parsed)
				output, err := s.forwardOpenAIImagesOAuthOnce(ctx, c, account, single, channelMappedModel)
				if err == nil && output != nil && len(output.ImageResults) > 0 {
					s.ReportOpenAIAccountScheduleResult(account.ID, true, output.FirstTokenMs)
					results <- fanoutResult{output: output}
					return
				}
				s.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
				excludedIDs[account.ID] = struct{}{}
				lastErr = err
			}
			results <- fanoutResult{err: lastErr}
		}()
	}

	outputs := make([]*openAIImagesOAuthForwardOutput, 0, requested)
	var lastErr error
	for i := 0; i < requested; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-results:
			if result.output != nil {
				outputs = append(outputs, result.output)
			}
			if result.err != nil {
				lastErr = result.err
			}
		}
	}

	aggregated := aggregateOpenAIImagesOAuthOutputs(outputs)
	if aggregated == nil || len(aggregated.ImageResults) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("OpenAI image generation returned no images")
	}

	usageRaw := buildOpenAIImagesUsageRaw(aggregated.Usage)
	responseBody, err := buildOpenAIImagesAPIResponse(aggregated.ImageResults, aggregated.CreatedAt, usageRaw, aggregated.FirstMeta, parsed.ResponseFormat)
	if err != nil {
		return nil, err
	}
	if aggregated.ResponseHeaders != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), aggregated.ResponseHeaders, s.responseHeaderFilter)
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)

	return &OpenAIForwardResult{
		RequestID:        aggregated.RequestID,
		ResponseID:       aggregated.ResponseID,
		Usage:            aggregated.Usage,
		Model:            parsed.Model,
		BillingModel:     parsed.Model,
		UpstreamModel:    aggregated.UpstreamModel,
		ResponseHeaders:  aggregated.ResponseHeaders,
		FirstTokenMs:     aggregated.FirstTokenMs,
		ImageCount:       len(aggregated.ImageResults),
		ImageOutputSizes: aggregated.ImageSizes,
	}, nil
}
```

Add helper:

```go
func cloneOpenAIAccountExcludedIDs(ids map[int64]struct{}) map[int64]struct{} {
	clone := make(map[int64]struct{}, len(ids)+1)
	for id := range ids {
		clone[id] = struct{}{}
	}
	return clone
}
```

Add usage JSON helper matching the shape expected by `buildOpenAIImagesAPIResponse`:

```go
func buildOpenAIImagesUsageRaw(usage OpenAIUsage) []byte {
	body := []byte(`{}`)
	body, _ = sjson.SetBytes(body, "input_tokens", usage.InputTokens)
	body, _ = sjson.SetBytes(body, "output_tokens", usage.OutputTokens)
	if usage.CacheCreationInputTokens > 0 {
		body, _ = sjson.SetBytes(body, "input_tokens_details.cache_creation_tokens", usage.CacheCreationInputTokens)
	}
	if usage.CacheReadInputTokens > 0 {
		body, _ = sjson.SetBytes(body, "input_tokens_details.cached_tokens", usage.CacheReadInputTokens)
	}
	if usage.ImageOutputTokens > 0 {
		body, _ = sjson.SetBytes(body, "output_tokens_details.image_tokens", usage.ImageOutputTokens)
	}
	return body
}
```

If the existing usage raw shape differs, mirror the exact fields that `parseOpenAIImageToolUsage` reads.

- [ ] **Step 7: Make fan-out concurrency safe for Gin writer usage**

Ensure goroutines never call `c.Data` or write response headers. Only `forwardOpenAIImagesOAuthOnce` may run in goroutines, and it must not write to `c.Writer`. The final `ForwardImagesOAuthFanout` response write must happen once after aggregation.

If `forwardOpenAIImagesOAuthOnce` writes ops metadata into `c`, protect only service-local state as needed or move ops writes to the final aggregation step. Do not write response body from goroutines.

- [ ] **Step 8: Run fan-out tests and fix compile/test failures**

Run:

```bash
go test ./backend/internal/service -run 'TestOpenAIGatewayServiceForwardImagesOAuthFanout|TestAggregateOpenAIImagesOAuthOutputs' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/service/openai_images.go backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "feat: fan out OpenAI image OAuth n requests"
```

---

### Task 6: Wire handler scheduling to OAuth fan-out

**Files:**
- Modify: `backend/internal/handler/openai_images.go`
- Test: `backend/internal/service/openai_images_test.go` or existing handler test file

- [ ] **Step 1: Add handler-level test for OAuth `n > 1` scheduling**

If existing handler tests can construct `OpenAIGatewayHandler`, add a test asserting that one downstream request with `n=3` produces three upstream calls and one standard response. Use existing handler setup helpers for auth context, billing eligibility, group permission, and scheduler accounts.

The assertion body should include:

```go
require.Equal(t, 3, upstream.calls)
require.Equal(t, http.StatusOK, rec.Code)
require.Len(t, gjson.Get(rec.Body.String(), "data").Array(), 3)
for _, upstreamBody := range upstream.bodies {
	require.False(t, gjson.GetBytes(upstreamBody, "tools.0.n").Exists())
}
```

If handler setup is too large, add a service integration test that calls the new handler-facing method and explicitly document in the test name that it covers fan-out scheduling.

- [ ] **Step 2: Run handler fan-out test and verify it fails**

Run the selected test:

```bash
go test ./backend/internal/handler ./backend/internal/service -run 'Test.*Images.*Fanout' -count=1
```

Expected: FAIL until the handler calls the fan-out path.

- [ ] **Step 3: Wire fan-out after selected OAuth account detection**

In `backend/internal/handler/openai_images.go`, after account selection and after `account := selection.Account`, add a branch before acquiring the selected account slot for normal forwarding:

```go
if account.Type == service.AccountTypeOAuth && parsed.N > 1 && !parsed.Stream {
	result, err := h.gatewayService.ForwardImagesOAuthFanout(
		c.Request.Context(),
		c,
		apiKey.GroupID,
		parsed,
		channelMapping.MappedModel,
		failedAccountIDs,
	)
	if err != nil {
		h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
		wroteFallback := h.ensureForwardErrorResponse(c, streamStarted)
		fields := []zap.Field{
			zap.Int64("account_id", account.ID),
			zap.Bool("fallback_error_response_written", wroteFallback),
			zap.Error(err),
		}
		if shouldLogOpenAIForwardFailureAsWarn(c, wroteFallback) {
			reqLog.Warn("openai.images.forward_failed", fields...)
			return
		}
		reqLog.Error("openai.images.forward_failed", fields...)
		return
	}
	if result != nil {
		h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, true, result.FirstTokenMs)
	}
	// Continue with the existing usage-recording block using this account and aggregated result.
}
```

Then refactor the existing post-forward usage recording block so both normal forwarding and fan-out can share it. The minimal safe shape is:

```go
var result *service.OpenAIForwardResult
var err error
fanoutHandled := false
if account.Type == service.AccountTypeOAuth && parsed.N > 1 && !parsed.Stream {
	result, err = h.gatewayService.ForwardImagesOAuthFanout(...)
	fanoutHandled = true
} else {
	accountReleaseFunc, acquired := h.acquireResponsesAccountSlot(...)
	...
	result, err = h.gatewayService.ForwardImages(...)
}
```

Keep the existing error handling for `OpenAIImagesUpstreamError`, `UpstreamFailoverError`, and generic errors for the normal path. For fan-out, partial success is already success; zero success returns `err` and should use generic fallback error handling.

- [ ] **Step 4: Avoid double account-slot acquisition for fan-out**

Do not call `h.acquireResponsesAccountSlot` for the initial selected account when using fan-out. Fan-out schedules and forwards each subrequest independently. If fan-out needs per-account concurrency protection, add it in the service fan-out method only if the service has access to the same limiter; otherwise keep handler-level user/image slots as the outer guard and rely on upstream transport account concurrency.

- [ ] **Step 5: Preserve usage recording with aggregated result**

Ensure the existing `RecordUsage` call receives:

```go
Result: result,
Account: account,
ChannelUsageFields: channelMapping.ToUsageFields(parsed.Model, upstreamModel),
```

For fan-out, `result.ImageCount` must be successful image count and `result.Usage` must be aggregated successful usage. It is acceptable that `Account` is the first selected OAuth account for the mandatory usage record as long as token/image counts are aggregated.

- [ ] **Step 6: Run handler/service tests**

Run:

```bash
go test ./backend/internal/handler ./backend/internal/service -run 'Test.*Images.*Fanout|TestOpenAIGatewayServiceForwardImagesOAuthFanout' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/handler/openai_images.go backend/internal/service/openai_images.go backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "feat: route OpenAI image n through OAuth fanout"
```

---

### Task 7: Full verification and regression sweep

**Files:**
- Verify only unless failures require fixes.

- [ ] **Step 1: Run all OpenAI images tests**

Run:

```bash
go test ./backend/internal/service ./backend/internal/handler -run 'OpenAI.*Images|Images.*OpenAI|ImagesOAuth|ImageGeneration' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full backend tests if the targeted suite passes**

Run:

```bash
go test ./backend/...
```

Expected: PASS.

- [ ] **Step 3: Inspect diff for unsupported `tools[0].n`**

Run:

```bash
grep -R '"n"' backend/internal/service/openai_images_responses.go backend/internal/service/openai_images.go backend/internal/handler/openai_images.go
```

Expected: no `sjson.SetBytes(tool, "n", ...)` in Responses image tool construction. Request parsing and max validation may still reference `n`.

- [ ] **Step 4: Manual local smoke test if backend can run locally**

Start the backend using the repository’s existing local command, then send:

```bash
curl -sS http://localhost:<port>/v1/images/generations \
  -H 'Authorization: Bearer <test-api-key>' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-image-2","prompt":"draw a cat","n":3}'
```

Expected: JSON response with standard OpenAI Images shape and `data` length between 1 and 3 depending on upstream partial success. No upstream request contains `tools[0].n`.

- [ ] **Step 5: Final git status**

Run:

```bash
git status --short
```

Expected: only intentionally changed files remain. Do not stage or commit unrelated untracked files such as `docs/superpowers/plans/2026-05-31-openai-images-server-error-retry.md` unless the user explicitly asks.

- [ ] **Step 6: Commit fixes if verification required changes**

If verification revealed fixes, commit only the relevant files:

```bash
git add backend/internal/service/openai_images.go backend/internal/service/openai_images_responses.go backend/internal/handler/openai_images.go backend/internal/service/openai_images_test.go
git commit -m "test: verify OpenAI image n fanout"
```

---

## Self-Review

**Spec coverage:** The plan covers max `n=5`, no `tools[0].n`, parallel fan-out for OAuth `n>1`, fresh scheduling per subrequest, one retry per subrequest, partial success in standard OpenAI Images response format, zero-success failure, and usage aggregation.

**Placeholder scan:** No TBD/TODO placeholders remain. Where exact test helper names may differ, the plan instructs using existing helper names from the same file while preserving the exact assertions and behavior.

**Type consistency:** The plan consistently uses `OpenAIImagesRequest.N`, `OpenAIUsage`, `OpenAIForwardResult`, `openAIResponsesImageResult`, and the extracted `openAIImagesOAuthForwardOutput` across tasks.
