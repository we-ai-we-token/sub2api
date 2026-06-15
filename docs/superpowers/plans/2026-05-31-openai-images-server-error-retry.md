# OpenAI Images Server Error Retry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retry OpenAI image generation upstream `server_error` events by returning them through the existing image failover path so another account can be selected.

**Architecture:** Keep the change narrow: classify only retryable OpenAI image upstream business errors, then convert those errors to `UpstreamFailoverError` at the handler boundary before the current non-retryable upstream-user-error branch returns. This reuses the existing `failedAccountIDs` account exclusion and `openai.images.upstream_failover_switching` loop, so the next attempt prefers a different account when one is available.

**Tech Stack:** Go, Gin, existing `OpenAIGatewayHandler.Images` failover loop, existing `service.OpenAIImagesUpstreamError` parsing, `go test`.

---

## File Structure

- Modify `backend/internal/service/openai_images_responses.go`
  - Add `IsRetryableOpenAIImagesUpstreamError(err *OpenAIImagesUpstreamError) bool` near the `OpenAIImagesUpstreamError` methods.
  - Responsibility: service-level classification of retryable OpenAI image upstream business errors.
- Modify `backend/internal/handler/openai_images.go`
  - In the `OpenAIImagesUpstreamError` branch, convert retryable image upstream errors into `UpstreamFailoverError`, mark the current account failed, and continue the existing loop.
  - Responsibility: routing/failover behavior, not error classification.
- Modify `backend/internal/service/openai_images_test.go`
  - Add focused tests for retryable vs non-retryable `OpenAIImagesUpstreamError` classification.

### Task 1: Add retryable image upstream error classification

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go:64-95`
- Test: `backend/internal/service/openai_images_test.go`

- [ ] **Step 1: Write the failing tests**

Append these tests to `backend/internal/service/openai_images_test.go`:

```go
func TestIsRetryableOpenAIImagesUpstreamError_ServerErrorCode(t *testing.T) {
	err := &OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadGateway,
		Code:       "server_error",
		Message:    "An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID 58d019a4-cfab-4c01-929c-9dd78e0931d4 in your message.",
	}

	require.True(t, IsRetryableOpenAIImagesUpstreamError(err))
}

func TestIsRetryableOpenAIImagesUpstreamError_ServerErrorType(t *testing.T) {
	err := &OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadGateway,
		ErrorType:  "server_error",
		Message:    "temporary upstream image failure",
	}

	require.True(t, IsRetryableOpenAIImagesUpstreamError(err))
}

func TestIsRetryableOpenAIImagesUpstreamError_TransientRetryMessage(t *testing.T) {
	err := &OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadGateway,
		Code:       "",
		ErrorType:  "",
		Message:    "An error occurred while processing your request. You can retry your request.",
	}

	require.True(t, IsRetryableOpenAIImagesUpstreamError(err))
}

func TestIsRetryableOpenAIImagesUpstreamError_UserErrorIsNotRetryable(t *testing.T) {
	err := &OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadRequest,
		ErrorType:  "image_generation_user_error",
		Code:       "moderation_blocked",
		Message:    "Your request was rejected as a result of our safety system.",
	}

	require.False(t, IsRetryableOpenAIImagesUpstreamError(err))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./backend/internal/service -run 'TestIsRetryableOpenAIImagesUpstreamError' -count=1
```

Expected: FAIL because `IsRetryableOpenAIImagesUpstreamError` is undefined.

- [ ] **Step 3: Add the minimal classification implementation**

In `backend/internal/service/openai_images_responses.go`, after `func (e *OpenAIImagesUpstreamError) clientMessage() string`, add:

```go
func IsRetryableOpenAIImagesUpstreamError(err *OpenAIImagesUpstreamError) bool {
	if err == nil {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(err.Code))
	errType := strings.ToLower(strings.TrimSpace(err.ErrorType))
	if code == "server_error" || errType == "server_error" {
		return true
	}
	return isOpenAITransientProcessingError(http.StatusBadRequest, err.Message, nil)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./backend/internal/service -run 'TestIsRetryableOpenAIImagesUpstreamError' -count=1
```

Expected: PASS.

### Task 2: Route retryable image upstream errors through account failover

**Files:**
- Modify: `backend/internal/handler/openai_images.go:228-239`

- [ ] **Step 1: Confirm there is no handler-level production change yet**

Run:

```bash
git diff -- backend/internal/handler/openai_images.go
```

Expected: no diff for `backend/internal/handler/openai_images.go` before this task starts.

- [ ] **Step 2: Replace the retryable upstream error branch**

In `backend/internal/handler/openai_images.go`, replace the current `OpenAIImagesUpstreamError` branch:

```go
					if errors.As(err, &imageUpstreamErr) {
						h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, true, nil)
						reqLog.Warn("openai.images.upstream_user_error",
							zap.Int64("account_id", account.ID),
							zap.Int("status_code", imageUpstreamErr.StatusCode),
							zap.String("error_type", imageUpstreamErr.ErrorType),
							zap.String("error_code", imageUpstreamErr.Code),
							zap.Error(err),
						)
						return
					}
```

with:

```go
					if errors.As(err, &imageUpstreamErr) {
						if service.IsRetryableOpenAIImagesUpstreamError(imageUpstreamErr) {
							h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
							h.gatewayService.RecordOpenAIAccountSwitch()
							failedAccountIDs[account.ID] = struct{}{}
							failoverErr := &service.UpstreamFailoverError{
								StatusCode:   imageUpstreamErr.StatusCode,
								ResponseBody: []byte(imageUpstreamErr.Error()),
							}
							lastFailoverErr = failoverErr
							if switchCount >= maxAccountSwitches {
								h.handleFailoverExhausted(c, failoverErr, streamStarted)
								return
							}
							switchCount++
							reqLog.Warn("openai.images.upstream_retryable_error_switching",
								zap.Int64("account_id", account.ID),
								zap.Int("upstream_status", imageUpstreamErr.StatusCode),
								zap.String("error_type", imageUpstreamErr.ErrorType),
								zap.String("error_code", imageUpstreamErr.Code),
								zap.Int("switch_count", switchCount),
								zap.Int("max_switches", maxAccountSwitches),
								zap.Error(err),
							)
							continue
						}
						h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, true, nil)
						reqLog.Warn("openai.images.upstream_user_error",
							zap.Int64("account_id", account.ID),
							zap.Int("status_code", imageUpstreamErr.StatusCode),
							zap.String("error_type", imageUpstreamErr.ErrorType),
							zap.String("error_code", imageUpstreamErr.Code),
							zap.Error(err),
						)
						return
					}
```

- [ ] **Step 3: Format changed files**

Run:

```bash
gofmt -w backend/internal/handler/openai_images.go backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
```

Expected: command exits 0.

- [ ] **Step 4: Run focused tests**

Run:

```bash
go test ./backend/internal/service -run 'TestIsRetryableOpenAIImagesUpstreamError' -count=1
```

Expected: PASS.

### Task 3: Verify broader package health

**Files:**
- No code changes.

- [ ] **Step 1: Run service package tests**

Run:

```bash
go test ./backend/internal/service -count=1
```

Expected: PASS.

- [ ] **Step 2: Run handler package tests**

Run:

```bash
go test ./backend/internal/handler -count=1
```

Expected: PASS.

- [ ] **Step 3: Review final diff**

Run:

```bash
git diff -- backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go backend/internal/handler/openai_images.go
```

Expected: diff only adds retryable classification, focused tests, and retryable image upstream error switch-account handling.

---

## Self-Review

- Spec coverage: The plan handles the exact logged `server_error` shape and routes it through existing account switching. It keeps non-retryable user/content errors on the existing direct-return path.
- Placeholder scan: No TBD/TODO placeholders remain.
- Type consistency: Uses existing `service.OpenAIImagesUpstreamError`, `service.UpstreamFailoverError`, `failedAccountIDs`, `switchCount`, and `handleFailoverExhausted` names from the current code.
