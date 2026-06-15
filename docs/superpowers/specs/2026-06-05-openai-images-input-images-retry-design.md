# OpenAI Images input-images Rate Limit Retry Design

## Context

OpenAI image requests can return transient rate-limit errors like:

```json
{
  "error": {
    "code": "rate_limit_exceeded",
    "message": "Rate limit reached for gpt-image-2-codex (for limit gpt-image) ... on input-images per min ... Please try again in 15ms.",
    "type": "input-images"
  }
}
```

Current retry classification does not specifically treat this `input-images` limit as retryable. The new behavior should handle this narrow transient condition without broadening retries for unrelated user, quota, or permission errors.

## Scope

Apply only to OpenAI image forwarding paths. The retryable condition is:

- upstream error code is `rate_limit_exceeded`, and
- upstream error type is `input-images`, or the upstream message contains `input-images per min`.

Do not change retry behavior for non-image endpoints or unrelated 429 responses.

## Behavior

For the matching `input-images` rate-limit error:

1. Retry the same account after a short delay.
2. If the same-account retry still fails with the same retryable condition, switch to another eligible account.
3. Execute this same-account-then-switch cycle at most 2 times.
4. If the second cycle is exhausted, return the final upstream error.

Delay selection:

- Prefer the upstream hint in text like `Please try again in 15ms`.
- If no parseable hint exists, use the existing image same-account retry delay.

## Error Handling Requirements

Before each retry starts, discard the retryable error captured for the failed attempt so it cannot be emitted downstream later. Only the final exhausted error may be written to the client.

This is required for SSE/streaming paths because a previous retry implementation retained an earlier error and produced duplicate downstream SSE error frames.

Do not retry if a partial image result has already been produced. Partial success should keep the current behavior to avoid duplicate output or duplicate billing side effects.

## Tests

Add focused tests for:

- `rate_limit_exceeded` + `input-images` is classified as OpenAI image retryable.
- `rate_limit_exceeded` without `input-images` is not classified as this special retryable condition.
- Message fallback with `input-images per min` works.
- Retry logic clears/consumes the previous retryable error before starting another attempt.
- The handler/service path performs no more than 2 same-account-then-switch cycles for this special condition.

## Non-Goals

- Do not make every OpenAI image 429 retryable.
- Do not alter billing/accounting for successful or partial-success image responses.
- Do not introduce a new user-facing configuration option unless existing retry configuration is insufficient during implementation planning.
