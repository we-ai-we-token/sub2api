# OpenAI Images input-images Retry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add narrow retry handling for OpenAI image `input-images per min` rate-limit errors: retry the same account, then switch accounts, with at most two cycles.

**Architecture:** Classify only `rate_limit_exceeded` image errors with `type=input-images` or an `input-images per min` message as the new retryable condition. The service layer must not write retryable image errors downstream before the handler can retry; the handler owns the retry loop, clears the previous retry error before every retry, and only emits the final exhausted error.

**Tech Stack:** Go, Gin handlers, existing OpenAI gateway service/handler tests, `testify/require`, existing `OpenAIImagesUpstreamError` and `UpstreamFailoverError` types.

---

## File Structure

- Modify `backend/internal/service/openai_images_responses.go`
  - Add helper functions for detecting the special `input-images` rate-limit error and parsing retry delay hints from the upstream message.
  - Keep `IsRetryableOpenAIImagesUpstreamError` as the public retryability gate used by existing code.
- Modify `backend/internal/service/openai_images_test.go`
  - Extend classification tests for `input-images` rate limits, non-matching rate limits, and message fallback.
  - Add tests for retry delay hint parsing.
- Modify `backend/internal/handler/openai_images.go`
  - Add a small per-request retry-cycle counter for special `input-images` rate limits.
  - On matching retryable image upstream errors, clear the captured error before retrying, wait, retry same account once, then switch account if the same-account retry fails.
  - Cap same-account-then-switch cycles at 2.
- Modify `backend/internal/handler/openai_images_controls_test.go` or create a focused handler test file only if existing handler test seams are sufficient.
  - Prefer unit-testing the new handler decision helper if the full `Images` handler needs too much setup.

---

### Task 1: Service classification for `input-images` rate limit

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go:111-122`
- Test: `backend/internal/service/openai_images_test.go:36-80`

- [ ] **Step 1: Write failing classification tests**

Add these cases to `TestIsRetryableOpenAIImagesUpstreamError` in `backend/internal/service/openai_images_test.go`:

```go
{
	name: "retryable for input images rate limit type",
	err: &OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "input-images",
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached for gpt-image-2-codex on input-images per min: Limit 4000, Used 4000, Requested 1. Please try again in 15ms.",
	},
	want: true,
},
{
	name: "retryable for input images rate limit message fallback",
	err: &OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached for gpt-image-2-codex on input-images per min: Limit 4000, Used 4000, Requested 1. Please try again in 15ms.",
	},
	want: true,
},
{
	name: "not retryable for unrelated rate limit",
	err: &OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "requests",
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached on requests per min.",
	},
	want: false,
},
```

- [ ] **Step 2: Run the classification test and verify it fails**

Run:

```bash
go test ./backend/internal/service -run TestIsRetryableOpenAIImagesUpstreamError -count=1
```

Expected: FAIL for the two new input-images cases because they currently return false.

- [ ] **Step 3: Add the special classifier**

In `backend/internal/service/openai_images_responses.go`, add this helper above `IsRetryableOpenAIImagesUpstreamError`:

```go
func IsOpenAIImagesInputImagesRateLimitError(err *OpenAIImagesUpstreamError) bool {
	if err == nil {
		return false
	}
	if err.StatusCode != 0 && err.StatusCode != http.StatusTooManyRequests {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(err.Code), "rate_limit_exceeded") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(err.ErrorType), "input-images") {
		return true
	}
	return strings.Contains(strings.ToLower(err.Message), "input-images per min")
}
```

Then update `IsRetryableOpenAIImagesUpstreamError` to call it before the existing server-error checks:

```go
func IsRetryableOpenAIImagesUpstreamError(err *OpenAIImagesUpstreamError) bool {
	if err == nil {
		return false
	}
	if IsOpenAIImagesInputImagesRateLimitError(err) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(err.Code), "server_error") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(err.ErrorType), "server_error") {
		return true
	}
	return isOpenAITransientProcessingError(http.StatusBadRequest, err.Message, nil)
}
```

- [ ] **Step 4: Run the classification test and verify it passes**

Run:

```bash
go test ./backend/internal/service -run TestIsRetryableOpenAIImagesUpstreamError -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "fix: classify input-images image rate limits as retryable"
```

---

### Task 2: Parse upstream retry delay hints

**Files:**
- Modify: `backend/internal/service/openai_images_responses.go`
- Test: `backend/internal/service/openai_images_test.go`

- [ ] **Step 1: Write failing delay parser tests**

Add this test to `backend/internal/service/openai_images_test.go` near the retry classification test:

```go
func TestOpenAIImagesRetryDelayFromMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want time.Duration
	}{
		{
			name: "milliseconds hint",
			msg:  "Please try again in 15ms.",
			want: 15 * time.Millisecond,
		},
		{
			name: "seconds hint",
			msg:  "Please try again in 2s.",
			want: 2 * time.Second,
		},
		{
			name: "no hint",
			msg:  "Rate limit reached.",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, OpenAIImagesRetryDelayFromMessage(tt.msg))
		})
	}
}
```

Ensure the existing import block includes `time`:

```go
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"
```

- [ ] **Step 2: Run the delay parser test and verify it fails**

Run:

```bash
go test ./backend/internal/service -run TestOpenAIImagesRetryDelayFromMessage -count=1
```

Expected: FAIL because `OpenAIImagesRetryDelayFromMessage` does not exist.

- [ ] **Step 3: Add delay parser implementation**

In `backend/internal/service/openai_images_responses.go`, add `regexp` to imports:

```go
import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
```

Add package-level regex and parser near the retry helpers:

```go
var openAIImagesRetryDelayPattern = regexp.MustCompile(`(?i)try again in\s+(\d+)\s*(ms|s|sec|secs|second|seconds)\b`)

func OpenAIImagesRetryDelayFromMessage(message string) time.Duration {
	matches := openAIImagesRetryDelayPattern.FindStringSubmatch(message)
	if len(matches) != 3 {
		return 0
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil || value <= 0 {
		return 0
	}
	switch strings.ToLower(matches[2]) {
	case "ms":
		return time.Duration(value) * time.Millisecond
	case "s", "sec", "secs", "second", "seconds":
		return time.Duration(value) * time.Second
	default:
		return 0
	}
}
```

- [ ] **Step 4: Run service tests for the changed area**

Run:

```bash
go test ./backend/internal/service -run 'Test(IsRetryableOpenAIImagesUpstreamError|OpenAIImagesRetryDelayFromMessage)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "fix: parse OpenAI image retry delay hints"
```

---

### Task 3: Handler retry decision helper with two-cycle cap and error clearing

**Files:**
- Modify: `backend/internal/handler/openai_images.go`
- Test: `backend/internal/handler/openai_images_controls_test.go`

- [ ] **Step 1: Write failing helper tests**

Add these tests to `backend/internal/handler/openai_images_controls_test.go`:

```go
func TestOpenAIImagesInputImagesRetryState(t *testing.T) {
	state := newOpenAIImagesInputImagesRetryState(2)

	firstErr := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "input-images",
		Code:       "rate_limit_exceeded",
		Message:    "Please try again in 15ms.",
	}
	firstFailover := &service.UpstreamFailoverError{StatusCode: firstErr.StatusCode, ResponseBody: []byte(firstErr.Error())}
	state.remember(firstFailover)
	retry, delay := state.consumeForSameAccountRetry(firstErr, 500*time.Millisecond)
	require.True(t, retry)
	require.Equal(t, 15*time.Millisecond, delay)
	require.Nil(t, state.lastFailoverErr, "retry must discard the previous error before the next attempt")
	require.Equal(t, 1, state.cyclesStarted)

	secondErr := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "input-images",
		Code:       "rate_limit_exceeded",
		Message:    "Please try again in 20ms.",
	}
	secondFailover := &service.UpstreamFailoverError{StatusCode: secondErr.StatusCode, ResponseBody: []byte(secondErr.Error())}
	state.remember(secondFailover)
	switchAccount := state.consumeForAccountSwitch(secondErr)
	require.True(t, switchAccount)
	require.Nil(t, state.lastFailoverErr, "switch retry must discard the previous error before selecting another account")
	require.Equal(t, 1, state.cyclesStarted)

	state.remember(secondFailover)
	retry, delay = state.consumeForSameAccountRetry(secondErr, 500*time.Millisecond)
	require.True(t, retry)
	require.Equal(t, 20*time.Millisecond, delay)
	require.Equal(t, 2, state.cyclesStarted)

	state.remember(secondFailover)
	retry, _ = state.consumeForSameAccountRetry(secondErr, 500*time.Millisecond)
	require.False(t, retry)
	require.Same(t, secondFailover, state.lastFailoverErr, "exhausted retry keeps final error for downstream response")
}

func TestOpenAIImagesInputImagesRetryStateIgnoresOtherErrors(t *testing.T) {
	state := newOpenAIImagesInputImagesRetryState(2)
	err := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "requests",
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached on requests per min.",
	}
	failover := &service.UpstreamFailoverError{StatusCode: err.StatusCode, ResponseBody: []byte(err.Error())}
	state.remember(failover)

	retry, _ := state.consumeForSameAccountRetry(err, 500*time.Millisecond)
	require.False(t, retry)
	require.Same(t, failover, state.lastFailoverErr)
	require.False(t, state.consumeForAccountSwitch(err))
	require.Same(t, failover, state.lastFailoverErr)
}
```

Add `time` to the imports in `backend/internal/handler/openai_images_controls_test.go`:

```go
import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
```

- [ ] **Step 2: Run helper tests and verify they fail**

Run:

```bash
go test ./backend/internal/handler -run TestOpenAIImagesInputImagesRetryState -count=1
```

Expected: FAIL because the helper does not exist.

- [ ] **Step 3: Add retry state helper**

In `backend/internal/handler/openai_images.go`, near the existing helper functions or above `Images`, add:

```go
const openAIImagesInputImagesRateLimitMaxCycles = 2

type openAIImagesInputImagesRetryState struct {
	maxCycles      int
	cyclesStarted  int
	lastFailoverErr *service.UpstreamFailoverError
}

func newOpenAIImagesInputImagesRetryState(maxCycles int) *openAIImagesInputImagesRetryState {
	return &openAIImagesInputImagesRetryState{maxCycles: maxCycles}
}

func (s *openAIImagesInputImagesRetryState) remember(err *service.UpstreamFailoverError) {
	s.lastFailoverErr = err
}

func (s *openAIImagesInputImagesRetryState) consumeForSameAccountRetry(err *service.OpenAIImagesUpstreamError, fallbackDelay time.Duration) (bool, time.Duration) {
	if !service.IsOpenAIImagesInputImagesRateLimitError(err) {
		return false, 0
	}
	if s.cyclesStarted >= s.maxCycles {
		return false, 0
	}
	s.cyclesStarted++
	s.lastFailoverErr = nil
	if delay := service.OpenAIImagesRetryDelayFromMessage(err.Message); delay > 0 {
		return true, delay
	}
	return true, fallbackDelay
}

func (s *openAIImagesInputImagesRetryState) consumeForAccountSwitch(err *service.OpenAIImagesUpstreamError) bool {
	if !service.IsOpenAIImagesInputImagesRateLimitError(err) {
		return false
	}
	if s.cyclesStarted == 0 || s.cyclesStarted > s.maxCycles {
		return false
	}
	s.lastFailoverErr = nil
	return true
}
```

If `openai_images.go` does not already import `time`, keep the existing import. It already uses `time` for image slot/retry logic.

- [ ] **Step 4: Run helper tests and verify they pass**

Run:

```bash
go test ./backend/internal/handler -run TestOpenAIImagesInputImagesRetryState -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/handler/openai_images.go backend/internal/handler/openai_images_controls_test.go
git commit -m "fix: cap input-images retry cycles"
```

---

### Task 4: Wire handler retry flow into `Images`

**Files:**
- Modify: `backend/internal/handler/openai_images.go:142-306`
- Test: `backend/internal/handler/openai_images_controls_test.go`

- [ ] **Step 1: Add a focused test for retry state behavior in the handler path**

If full `Images` integration setup is too large, extend `TestOpenAIImagesInputImagesRetryState` with this account-switch sequence to lock the exact two-cycle behavior:

```go
func TestOpenAIImagesInputImagesRetryStateAllowsTwoSameAccountThenSwitchCycles(t *testing.T) {
	state := newOpenAIImagesInputImagesRetryState(2)
	err := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "input-images",
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached on input-images per min. Please try again in 15ms.",
	}
	failover := &service.UpstreamFailoverError{StatusCode: err.StatusCode, ResponseBody: []byte(err.Error())}

	state.remember(failover)
	retry, _ := state.consumeForSameAccountRetry(err, 500*time.Millisecond)
	require.True(t, retry)
	require.True(t, state.consumeForAccountSwitch(err))

	state.remember(failover)
	retry, _ = state.consumeForSameAccountRetry(err, 500*time.Millisecond)
	require.True(t, retry)
	require.True(t, state.consumeForAccountSwitch(err))

	state.remember(failover)
	retry, _ = state.consumeForSameAccountRetry(err, 500*time.Millisecond)
	require.False(t, retry)
	require.Same(t, failover, state.lastFailoverErr)
}
```

Run:

```bash
go test ./backend/internal/handler -run 'TestOpenAIImagesInputImagesRetryState' -count=1
```

Expected: PASS before wiring, because this validates the helper contract that wiring will use.

- [ ] **Step 2: Initialize the retry state in `Images`**

In `backend/internal/handler/openai_images.go`, replace this block:

```go
sameAccountRetryCount := make(map[int64]int)
var lastFailoverErr *service.UpstreamFailoverError
```

with:

```go
sameAccountRetryCount := make(map[int64]int)
inputImagesRetryState := newOpenAIImagesInputImagesRetryState(openAIImagesInputImagesRateLimitMaxCycles)
var lastFailoverErr *service.UpstreamFailoverError
```

- [ ] **Step 3: Use retry state when no account is available**

In the account selection error block, replace:

```go
if lastFailoverErr != nil {
	h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
} else {
	h.handleFailoverExhaustedSimple(c, 502, streamStarted)
}
```

with:

```go
if lastFailoverErr != nil {
	h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
} else if inputImagesRetryState.lastFailoverErr != nil {
	h.handleFailoverExhausted(c, inputImagesRetryState.lastFailoverErr, streamStarted)
} else {
	h.handleFailoverExhaustedSimple(c, 502, streamStarted)
}
```

- [ ] **Step 4: Wire special same-account retry and switch handling**

In the `errors.As(err, &imageUpstreamErr)` block, replace the current retryable section:

```go
if service.IsRetryableOpenAIImagesUpstreamError(imageUpstreamErr) {
	h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
	h.gatewayService.RecordOpenAIAccountSwitch()
	failedAccountIDs[account.ID] = struct{}{}
	failoverErr := &service.UpstreamFailoverError{StatusCode: imageUpstreamErr.StatusCode, ResponseBody: []byte(imageUpstreamErr.Error())}
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
```

with:

```go
if service.IsRetryableOpenAIImagesUpstreamError(imageUpstreamErr) {
	h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
	failoverErr := &service.UpstreamFailoverError{StatusCode: imageUpstreamErr.StatusCode, ResponseBody: []byte(imageUpstreamErr.Error())}
	inputImagesRetryState.remember(failoverErr)
	lastFailoverErr = failoverErr

	if sameAccount, delay := inputImagesRetryState.consumeForSameAccountRetry(imageUpstreamErr, sameAccountRetryDelay); sameAccount {
		lastFailoverErr = nil
		reqLog.Warn("openai.images.input_images_rate_limit_same_account_retry",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", imageUpstreamErr.StatusCode),
			zap.String("error_type", imageUpstreamErr.ErrorType),
			zap.String("error_code", imageUpstreamErr.Code),
			zap.Duration("retry_delay", delay),
			zap.Int("retry_cycle", inputImagesRetryState.cyclesStarted),
			zap.Int("max_retry_cycles", inputImagesRetryState.maxCycles),
		)
		select {
		case <-c.Request.Context().Done():
			return
		case <-time.After(delay):
		}
		continue
	}

	if inputImagesRetryState.consumeForAccountSwitch(imageUpstreamErr) {
		lastFailoverErr = nil
		h.gatewayService.RecordOpenAIAccountSwitch()
		failedAccountIDs[account.ID] = struct{}{}
		if switchCount >= maxAccountSwitches {
			h.handleFailoverExhausted(c, failoverErr, streamStarted)
			return
		}
		switchCount++
		reqLog.Warn("openai.images.input_images_rate_limit_switching",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", imageUpstreamErr.StatusCode),
			zap.String("error_type", imageUpstreamErr.ErrorType),
			zap.String("error_code", imageUpstreamErr.Code),
			zap.Int("switch_count", switchCount),
			zap.Int("max_switches", maxAccountSwitches),
			zap.Int("retry_cycle", inputImagesRetryState.cyclesStarted),
			zap.Int("max_retry_cycles", inputImagesRetryState.maxCycles),
		)
		continue
	}

	h.gatewayService.RecordOpenAIAccountSwitch()
	failedAccountIDs[account.ID] = struct{}{}
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
```

This clears `lastFailoverErr` before each retry path. If retries are exhausted, the final `failoverErr` remains available for downstream response.

- [ ] **Step 5: Run handler tests**

Run:

```bash
go test ./backend/internal/handler -run 'Test(OpenAIImages|ShouldSkipOpenAIImagesForwardFallback)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/handler/openai_images.go backend/internal/handler/openai_images_controls_test.go
git commit -m "fix: retry input-images limits before switching accounts"
```

---

### Task 5: Verify no retryable image error is written early

**Files:**
- Modify: `backend/internal/service/openai_images_test.go`
- No production changes expected unless this test reveals an existing early-write path.

- [ ] **Step 1: Add a regression test for input-images stream error not writing downstream**

Add this test near `TestOpenAIGatewayServiceForwardImages_OAuthStreamServerErrorIsRetryableWithoutWritingClientError` in `backend/internal/service/openai_images_test.go`:

```go
func TestOpenAIGatewayServiceForwardImages_OAuthStreamInputImagesRateLimitIsRetryableWithoutWritingClientError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","prompt":"draw cat","stream":true}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set("api_key", &APIKey{ID: 42})

	svc := &OpenAIGatewayService{}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	svc.httpUpstream = &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req_img_input_images_limit"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000020}}\n\n" +
					"data: {\"type\":\"error\",\"error\":{\"type\":\"input-images\",\"code\":\"rate_limit_exceeded\",\"message\":\"Rate limit reached for gpt-image-2-codex on input-images per min: Limit 4000, Used 4000, Requested 1. Please try again in 15ms.\"}}\n\n",
			)),
		},
	}

	account := &Account{
		ID:       1,
		Name:     "openai-oauth",
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		},
	}

	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.Nil(t, result)
	var upstreamErr *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.True(t, IsOpenAIImagesInputImagesRateLimitError(upstreamErr))
	require.True(t, IsRetryableOpenAIImagesUpstreamError(upstreamErr))
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}
```

- [ ] **Step 2: Run the regression test**

Run:

```bash
go test ./backend/internal/service -run TestOpenAIGatewayServiceForwardImages_OAuthStreamInputImagesRateLimitIsRetryableWithoutWritingClientError -count=1
```

Expected: PASS after Task 1, because retryable upstream image errors should not be written by the service before the handler retries.

- [ ] **Step 3: Fix early-write behavior only if the test fails**

If the test fails because a downstream error was written, update the service path that writes `OpenAIImagesUpstreamError` so it uses the existing guard:

```go
if upstreamErr, ok := err.(*OpenAIImagesUpstreamError); ok && !IsRetryableOpenAIImagesUpstreamError(upstreamErr) {
	writeOpenAIImagesUpstreamErrorResponse(c, upstreamErr)
}
```

Do not add any write for retryable errors.

- [ ] **Step 4: Run the regression test again**

Run:

```bash
go test ./backend/internal/service -run TestOpenAIGatewayServiceForwardImages_OAuthStreamInputImagesRateLimitIsRetryableWithoutWritingClientError -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

If only the test changed:

```bash
git add backend/internal/service/openai_images_test.go
git commit -m "test: cover input-images retry without early error writes"
```

If production code also changed:

```bash
git add backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "fix: avoid writing retryable input-images errors early"
```

---

### Task 6: Final verification

**Files:**
- Verify changed files only.

- [ ] **Step 1: Run focused service tests**

Run:

```bash
go test ./backend/internal/service -run 'Test(IsRetryableOpenAIImagesUpstreamError|OpenAIImagesRetryDelayFromMessage|OpenAIGatewayServiceForwardImages_OAuthStream.*RetryableWithoutWritingClientError)' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused handler tests**

Run:

```bash
go test ./backend/internal/handler -run 'Test(OpenAIImages|ShouldSkipOpenAIImagesForwardFallback)' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run package tests for touched packages**

Run:

```bash
go test ./backend/internal/service ./backend/internal/handler -count=1
```

Expected: PASS.

- [ ] **Step 4: Inspect git status**

Run:

```bash
git status --short
```

Expected: only intentional changes are present. Existing unrelated untracked files from the starting workspace must not be committed unless the user explicitly asks.

---

## Self-Review Notes

- Spec coverage: classification, same-account retry, account switch, two-cycle cap, retry delay parsing, no partial-result retry, and clearing errors before retry are covered.
- Placeholder scan: no `TBD`, `TODO`, or unspecified test steps remain.
- Type consistency: helper names are defined before use; exported service helpers are used by handler tests and handler code.
