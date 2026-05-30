# OpenAI Images Uniform Account Scheduling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make OpenAI image-generation requests ignore sticky/session routing so they enter normal account load balancing on every request.

**Architecture:** The image handler will stop deriving a request `sessionHash` from `session_id`, `conversation_id`, or `prompt_cache_key` before account selection. `SelectAccountWithSchedulerForImages` will continue to accept a session hash for non-image callers/future use, but the image entry point will pass an empty hash so both legacy and advanced schedulers skip the sticky-session layer. The existing pool-mode one-off retry hash remains after account selection to keep same-request pool retries stable.

**Tech Stack:** Go, Gin, testify, existing `service.OpenAIGatewayService` scheduling APIs.

---

## File Structure

- Modify `backend/internal/handler/openai_images.go`
  - Remove pre-selection explicit session hash generation for images.
  - Initialize image routing with an empty `sessionHash` so scheduler selection cannot sticky-route across requests.
  - Keep `ensureOpenAIPoolModeSessionHash` after account selection unchanged.
- Modify `backend/internal/handler/openai_images_controls_test.go`
  - Add a regression test that sends image headers/body containing explicit session signals and verifies the image scheduler receives an empty session hash.
  - Use a lightweight test gateway service in the same handler package if the existing handler service field supports substitution; otherwise add the smallest seam needed for testing the scheduler call.

---

### Task 1: Add failing handler regression test

**Files:**
- Modify: `backend/internal/handler/openai_images_controls_test.go:1-49`
- Reference: `backend/internal/handler/openai_images.go:136-153`

- [ ] **Step 1: Inspect handler service field type and existing test setup**

Run:

```bash
grep -R "type OpenAIGatewayHandler" -n backend/internal/handler && grep -R "gatewayService" -n backend/internal/handler/*_test.go
```

Expected: find the concrete `OpenAIGatewayHandler` definition and confirm whether `gatewayService` can be replaced by a fake directly or needs a small interface/seam.

- [ ] **Step 2: Write the failing test**

Add this test to `backend/internal/handler/openai_images_controls_test.go`, adapting only the fake wiring if Step 1 shows a different existing test seam:

```go
func TestOpenAIGatewayHandlerImages_DoesNotPassExplicitSessionHashToScheduler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-image-2","prompt":"draw","prompt_cache_key":"image-session","size":"1024x1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("session_id", "header-session")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	groupID := int64(111)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      222,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                   groupID,
			AllowImageGeneration: true,
		},
		User: &service.User{ID: 333},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 333, Concurrency: 1})

	gateway := newImagesSchedulerProbeGateway(t)
	h := newImagesSchedulerProbeHandler(gateway)

	h.Images(c)

	require.True(t, gateway.selectCalled)
	require.Empty(t, gateway.receivedSessionHash)
}
```

Also add the minimal helper fake in the same test file. If `OpenAIGatewayHandler.gatewayService` is concrete and cannot take this fake, defer the fake helper until Task 2 adds the smallest interface/seam:

```go
type imagesSchedulerProbeGateway struct {
	t                   *testing.T
	selectCalled        bool
	receivedSessionHash string
}

func newImagesSchedulerProbeGateway(t *testing.T) *imagesSchedulerProbeGateway {
	return &imagesSchedulerProbeGateway{t: t}
}

func newImagesSchedulerProbeHandler(gateway *imagesSchedulerProbeGateway) *OpenAIGatewayHandler {
	return &OpenAIGatewayHandler{
		gatewayService:      gateway,
		billingCacheService: &service.BillingCacheService{},
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}},
	}
}
```

The fake must implement only the image handler methods needed to reach scheduling:

```go
func (g *imagesSchedulerProbeGateway) ParseOpenAIImagesRequest(c *gin.Context, body []byte) (*service.OpenAIImagesRequest, error) {
	return (&service.OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
}

func (g *imagesSchedulerProbeGateway) ResolveChannelMappingAndRestrict(ctx context.Context, groupID *int64, model string) (*service.ChannelMappingResult, error) {
	return &service.ChannelMappingResult{MappedModel: model}, nil
}

func (g *imagesSchedulerProbeGateway) SelectAccountWithSchedulerForImages(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, requiredCapability service.OpenAIImagesCapability) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
	g.selectCalled = true
	g.receivedSessionHash = sessionHash
	return nil, service.OpenAIAccountScheduleDecision{}, errors.New("stop after scheduler probe")
}
```

- [ ] **Step 3: Run the test and verify it fails for the right reason**

Run:

```bash
go test ./backend/internal/handler -run TestOpenAIGatewayHandlerImages_DoesNotPassExplicitSessionHashToScheduler -count=1
```

Expected before implementation: fail because `receivedSessionHash` is non-empty, or fail to compile because a small interface/seam is needed. If it fails earlier due to missing dependencies in `newImagesSchedulerProbeHandler`, add only the missing no-op dependencies required to reach `SelectAccountWithSchedulerForImages`.

---

### Task 2: Make the image handler pass an empty scheduler session hash

**Files:**
- Modify: `backend/internal/handler/openai_images.go:136-153`
- Possibly modify: `backend/internal/handler/openai_gateway_handler.go` only if Task 1 proved a test seam is required
- Test: `backend/internal/handler/openai_images_controls_test.go`

- [ ] **Step 1: Apply the minimal production change**

In `backend/internal/handler/openai_images.go`, replace:

```go
	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, body)
```

with:

```go
	sessionHash := ""
```

Do not change this later block:

```go
		account := selection.Account
		sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
```

- [ ] **Step 2: Add the smallest handler test seam only if needed**

If `gatewayService` is currently typed as `*service.OpenAIGatewayService`, do not refactor the whole handler. Add a narrow package-local interface covering only methods called by `Images`, then change the handler field type to that interface if all existing assignments still compile.

The interface should include the exact methods used by `Images`, for example:

```go
type openAIImagesGateway interface {
	ParseOpenAIImagesRequest(c *gin.Context, body []byte) (*service.OpenAIImagesRequest, error)
	ResolveChannelMappingAndRestrict(ctx context.Context, groupID *int64, model string) (*service.ChannelMappingResult, error)
	SelectAccountWithSchedulerForImages(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, requiredCapability service.OpenAIImagesCapability) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error)
	ForwardImages(ctx context.Context, c *gin.Context, account *service.Account, body []byte, parsed *service.OpenAIImagesRequest, mappedModel string) (*service.OpenAIForwardResult, error)
}
```

Only include methods the compiler requires for the field's broader usage. If changing the field type causes broad churn, avoid the interface and instead test through an existing concrete-service seam.

- [ ] **Step 3: Run the targeted regression test**

Run:

```bash
go test ./backend/internal/handler -run TestOpenAIGatewayHandlerImages_DoesNotPassExplicitSessionHashToScheduler -count=1
```

Expected: PASS. The fake scheduler should be called and should observe `sessionHash == ""` even when the image request includes `session_id` and `prompt_cache_key`.

---

### Task 3: Verify existing explicit session hash behavior remains available outside image scheduling

**Files:**
- Test: `backend/internal/service/openai_gateway_service_test.go:230-263`
- Test: `backend/internal/handler/openai_images_controls_test.go`

- [ ] **Step 1: Run the existing explicit hash tests**

Run:

```bash
go test ./backend/internal/service -run TestOpenAIGatewayService_GenerateExplicitSessionHash_SkipsContentFallback -count=1
```

Expected: PASS. This confirms the utility function still behaves as before; the image handler simply no longer calls it for account scheduling.

- [ ] **Step 2: Run handler image control tests**

Run:

```bash
go test ./backend/internal/handler -run 'TestOpenAIGatewayHandlerImages_' -count=1
```

Expected: PASS. This covers the new image no-sticky regression and the existing disabled-group guard.

---

### Task 4: Run focused package tests and inspect diff

**Files:**
- Verify: `backend/internal/handler/openai_images.go`
- Verify: `backend/internal/handler/openai_images_controls_test.go`
- Verify: any seam file touched in `backend/internal/handler/`

- [ ] **Step 1: Run focused packages**

Run:

```bash
go test ./backend/internal/handler ./backend/internal/service -count=1
```

Expected: PASS.

- [ ] **Step 2: Inspect the diff**

Run:

```bash
git diff -- backend/internal/handler/openai_images.go backend/internal/handler/openai_images_controls_test.go backend/internal/handler/openai_gateway_handler.go
```

Expected diff:
- `openai_images.go` initializes `sessionHash` as empty before image account selection.
- Test verifies explicit image session signals are not passed to the scheduler.
- No changes to scheduler weighting, account priority, sticky-session storage, or `GenerateExplicitSessionHash` utility behavior.

- [ ] **Step 3: Commit when requested by the user**

Only if the user asks for a commit, run:

```bash
git status --short
git add backend/internal/handler/openai_images.go backend/internal/handler/openai_images_controls_test.go
git commit -m "$(cat <<'EOF'
fix: disable sticky routing for image scheduling
EOF
)"
```

Expected: new commit created. Do not commit the plan file unless the user explicitly wants plan docs committed.

---

## Self-Review

- Spec coverage: The plan implements the approved approach A: image entry does not generate/pass `sessionHash`, so image requests skip sticky session and use load balancing.
- Placeholder scan: No TBD/TODO/fill-in-later placeholders remain; test and implementation steps include concrete commands and expected outcomes.
- Type consistency: The plan consistently uses `sessionHash`, `SelectAccountWithSchedulerForImages`, `GenerateExplicitSessionHash`, and `OpenAIImagesCapability` with names matching the observed code.
