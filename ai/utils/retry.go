// pi: src/utils/retry.ts — bounded retry for assistant-producing calls with
// transient-error classification.
//
// Deviations:
//   - pi's sleep-with-abort-signal collapses to a context-aware timer;
//     RetrySleepAbortError has no counterpart (ctx.Err() plays that role).
//   - RetryCallbacks members may return Promise in TS; Go callbacks are
//     synchronous (per the pi-Promise → sync-callback rule).
//   - produce may return an error, which propagates immediately (pi's
//     produce() promise rejection propagates the same way).
package utils

import (
	"context"
	"regexp"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

func buildProviderErrorPattern(patterns []string) *regexp.Regexp {
	joined := ""
	for i, p := range patterns {
		if i > 0 {
			joined += "|"
		}
		joined += p
	}
	return regexp.MustCompile("(?i)" + joined)
}

var nonRetryableProviderLimitErrorPattern = buildProviderErrorPattern([]string{
	// OpenCode Go/free-tier limits returned as 429 JSON error types by OpenCode's
	// Zen API. These are subscription/account limits, not transient throttles.
	"GoUsageLimitError",
	"FreeUsageLimitError",

	// OpenCode Go subscription-limit text asks users to enable available-balance
	// usage after rolling/weekly/monthly limits are reached.
	"Monthly usage limit reached",
	"available balance",

	// Generic quota/budget/billing exhaustion. `insufficient_quota` is OpenAI's
	// quota/billing error code; the other strings cover common gateway wording.
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",
})

var retryableProviderErrorPattern = buildProviderErrorPattern([]string{
	// Generic provider load, HTTP status, and server-side transient failures.
	"overloaded",
	"rate.?limit",
	"too many requests",
	"429",
	"500",
	"502",
	"503",
	"504",
	"524",
	"service.?unavailable",
	"server.?error",
	"internal.?error",

	// Wrapper/provider text for transient upstream failures, including OpenRouter
	// "Provider returned error" responses (#2264).
	"provider.?returned.?error",
	"exceeded request buffer limit while retrying upstream",

	// Network, proxy, and fetch transport failures. This includes OpenAI Codex
	// raw-fetch failures such as "upstream connect", "connection refused", and
	// "reset before headers" (#733), plus OpenRouter connection drops (#3317).
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"other side closed",
	"fetch failed",
	"getaddrinfo",
	"ENOTFOUND",
	"EAI_AGAIN",
	"upstream.?connect",
	"reset before headers",
	"socket hang up",
	"socket connection was closed",
	"timed? out",
	"timeout",
	"terminated",

	// WebSocket transports can report close/error text instead of HTTP/fetch text.
	"websocket.?closed",
	"websocket.?error",

	// Premature stream endings from SDKs and transports. Anthropic can throw
	// "stream ended without ..." and "Anthropic stream ended before message_stop"
	// (#4433); Bedrock/Smithy can throw an HTTP/2 no-response error (#3594).
	"ended without",
	"stream ended before message_stop",
	"stream ended before a terminal response event",
	"http2 request did not get a response",

	// Provider-requested retry delay cap failures should flow through the outer
	// retry policy so callers can surface/abort the backoff (#1123).
	"retry delay",

	// Explicit retry guidance emitted mid-stream by OpenAI Responses and Bedrock
	// stream exceptions (#6019).
	"you can retry your request",
	"try your request again",
	"please retry your request",

	// gRPC based providers (e.g. NVIDIA NIM)
	"ResourceExhausted",
})

// RetryPolicy is a retry policy: bounded attempts with exponential backoff
// (`baseDelayMs * 2^(attempt-1)`). Matches `settings.retry` (`enabled`,
// `maxRetries`, `baseDelayMs`) in coding-agent; kept here so the classifier
// and the policy-driven retry loop live together and stay reusable by the SDK
// and other callers (pi: interface RetryPolicy).
type RetryPolicy struct {
	Enabled bool
	// MaxRetries is the max retry attempts (0 = no retries). The initial call
	// never counts as a retry.
	MaxRetries int
	// BaseDelayMs is the base delay in ms. Per-attempt delay is
	// `baseDelayMs * 2^(attempt-1)` before jitter.
	BaseDelayMs int64
}

// RetryCallbacks are optional callbacks emitted by RetryAssistantCall around
// each retry (pi: interface RetryCallbacks).
type RetryCallbacks struct {
	// OnRetryScheduled is emitted before the backoff sleep of each retry
	// attempt (1-indexed).
	OnRetryScheduled func(attempt int, maxAttempts int, delayMs int64, errorMessage string)
	// OnRetryAttemptStart is emitted after the backoff sleep, immediately
	// before the retried call starts.
	OnRetryAttemptStart func()
	// OnRetryFinished is emitted once when the loop ends: success if a later
	// call completed normally.
	OnRetryFinished func(success bool, attempt int, finalError string)
}

// RetryAssistantCall runs a single assistant-producing call with bounded
// retry on transient errors (pi: retryAssistantCall).
//
// Behavior:
//   - A successful response is returned immediately. Aborts are terminal and
//     never retried, but reported as unsuccessful if they happen after a retry
//     was scheduled. Aborts during the backoff sleep are normalized to an
//     aborted AssistantMessage too, so callers do not need to care when
//     cancellation happened.
//   - A non-retryable error (per IsRetryableAssistantError, including quota/
//     billing exhaustion) is returned immediately so deterministic errors fail
//     fast.
//   - Otherwise retries up to maxRetries times with exponential backoff,
//     emitting onRetryScheduled before each sleep, onRetryAttemptStart after
//     each sleep before the retried call starts, and onRetryFinished once at
//     the end (whether the loop ends in success, exhausted retries, or an
//     aborted backoff).
//
// When policy is nil or disabled, the first response is returned unchanged
// (equivalent to calling produce directly). ctx carries the caller's
// cancellation (pi: options.signal).
func RetryAssistantCall(ctx context.Context, produce func() (*types.AssistantMessage, error), policy *RetryPolicy, callbacks *RetryCallbacks) (*types.AssistantMessage, error) {
	maxAttempts := 0
	if policy != nil && policy.Enabled {
		maxAttempts = policy.MaxRetries
	}

	attempt := 0
	lastRetry := struct {
		attempt      int
		errorMessage string
		exists       bool
	}{}
	for {
		response, err := produce()
		if err != nil {
			return nil, err
		}

		// Abort: terminal but not successful. Never retry an aborted message.
		if response.StopReason == types.StopReasonAborted {
			if lastRetry.exists && callbacks != nil && callbacks.OnRetryFinished != nil {
				callbacks.OnRetryFinished(false, lastRetry.attempt, "")
			}
			return response, nil
		}

		// Success: non-error, non-abort responses return as-is.
		if response.StopReason != types.StopReasonError {
			if lastRetry.exists && callbacks != nil && callbacks.OnRetryFinished != nil {
				callbacks.OnRetryFinished(true, lastRetry.attempt, "")
			}
			return response, nil
		}

		// Non-retryable, or budget exhausted: return the final error message.
		if attempt >= maxAttempts || !IsRetryableAssistantError(response) {
			if lastRetry.exists && callbacks != nil && callbacks.OnRetryFinished != nil {
				errorMessage := response.ErrorMessage
				if errorMessage == "" {
					errorMessage = "Unknown error"
				}
				callbacks.OnRetryFinished(false, lastRetry.attempt, errorMessage)
			}
			return response, nil
		}

		attempt++
		lastRetry.attempt = attempt
		lastRetry.errorMessage = response.ErrorMessage
		if lastRetry.errorMessage == "" {
			lastRetry.errorMessage = "Unknown error"
		}
		lastRetry.exists = true
		delayMs := policy.BaseDelayMs * int64(1<<uint(attempt-1))
		if callbacks != nil && callbacks.OnRetryScheduled != nil {
			callbacks.OnRetryScheduled(attempt, maxAttempts, delayMs, lastRetry.errorMessage)
		}

		// Normalize aborts during retry backoff to the same AssistantMessage
		// shape as provider stream aborts, so callers do not need to care when
		// cancellation happened.
		select {
		case <-ctx.Done():
			if callbacks != nil && callbacks.OnRetryFinished != nil {
				callbacks.OnRetryFinished(false, attempt, lastRetry.errorMessage)
			}
			normalized := *response
			normalized.StopReason = types.StopReasonAborted
			normalized.ErrorMessage = ""
			return &normalized, nil
		case <-time.After(time.Duration(delayMs) * time.Millisecond):
		}
		if callbacks != nil && callbacks.OnRetryAttemptStart != nil {
			callbacks.OnRetryAttemptStart()
		}
	}
}

// IsRetryableAssistantError classifies whether a failed assistant message
// looks like a transient provider or transport error, so callers can decide
// if the last assistant turn should be restarted (pi:
// isRetryableAssistantError).
//
// This does not implement retry policy. Callers should first handle context
// overflow separately, then apply their own retry budget, backoff, and
// reporting before restarting the assistant turn.
func IsRetryableAssistantError(message *types.AssistantMessage) bool {
	if message.StopReason != types.StopReasonError || message.ErrorMessage == "" {
		return false
	}
	errorMessage := message.ErrorMessage
	if nonRetryableProviderLimitErrorPattern.MatchString(errorMessage) {
		return false
	}
	return retryableProviderErrorPattern.MatchString(errorMessage)
}
