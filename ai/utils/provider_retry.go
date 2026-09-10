// pi: utils/provider-retry.ts.
//
// Reproduces the retry behavior used by the OpenAI and Anthropic SDKs while
// making their backoff sleep interruptible. Their built-in retry timers
// ignore the request AbortSignal, so callers must invoke the SDK with
// maxRetries: 0 and wrap the request with this helper. Provider-requested
// delays above MaxRetryDelayMS fail immediately (60s default); set it to 0 to
// disable the limit.
package utils

import (
	"context"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const defaultMaxRetryDelayMS = 60_000

// ProviderRetryOptions are RetryProviderRequest's knobs (pi:
// ProviderRetryOptions; signal → Ctx).
type ProviderRetryOptions struct {
	// MaxRetries caps client-side retries; the caller decides its default
	// (openai-completions passes options.MaxRetries through, nil defaulting
	// to 0 / no retries in pi).
	MaxRetries int
	// MaxRetryDelayMs caps how long a server-requested retry wait may be; nil
	// means the 60s default, 0 disables the cap (pi: ?? default semantics).
	MaxRetryDelayMs *int64
	// Ctx replaces pi's AbortSignal: cancellation interrupts backoff sleeps.
	Ctx context.Context
}

// RetryProviderRequest wraps one request in the SDK-style retry loop (pi:
// retryProviderRequest). Each retry is a fresh request. Non-provider errors
// are never retried; provider errors are retried when their status/headers
// mark them retryable.
func RetryProviderRequest[T any](request func() (T, error), options ProviderRetryOptions) (T, error) {
	maxRetries := options.MaxRetries
	retriesRemaining := maxRetries
	for {
		result, err := request()
		if err == nil {
			return result, nil
		}
		if options.Ctx != nil && options.Ctx.Err() != nil {
			return result, newAbortError()
		}
		if retriesRemaining <= 0 || !isRetryableError(err) {
			return result, err
		}
		httpErr := err.(*ProviderHTTPError)
		retryIndex := maxRetries - retriesRemaining
		retriesRemaining--
		delayMS, retryErr := getRetryDelayMS(httpErr, retryIndex, options.MaxRetryDelayMs)
		if retryErr != nil {
			return result, retryErr
		}
		if err := abortableSleep(delayMS, options.Ctx); err != nil {
			return result, err
		}
	}
}

// isRetryableError mirrors the pinned OpenAI/Anthropic SDK retry policy
// (pi: isRetryableProviderError); review when either SDK is upgraded. Only
// *ProviderHTTPError qualifies (pi requires the error to carry status and
// headers); plain errors are never retried.
func isRetryableError(err error) bool {
	httpErr, ok := err.(*ProviderHTTPError)
	if !ok {
		return false
	}
	var shouldRetry string
	if httpErr.Headers != nil {
		shouldRetry = httpErr.Headers.Get("x-should-retry")
	}
	if shouldRetry == "true" {
		return true
	}
	if shouldRetry == "false" {
		return false
	}
	if httpErr.Status == 0 {
		return true
	}
	return httpErr.Status == 408 || httpErr.Status == 409 || httpErr.Status == 429 || httpErr.Status >= 500
}

// getRetryDelayMS computes the backoff for one retry, honoring server-requested
// delays (pi: getRetryDelayMs). It returns an error when a server-requested
// delay exceeds the cap.
func getRetryDelayMS(err *ProviderHTTPError, retryIndex int, maxRetryDelayMS *int64) (time.Duration, error) {
	if err.Headers != nil {
		if retryAfterMS := err.Headers.Get("retry-after-ms"); retryAfterMS != "" {
			value, parseErr := strconv.ParseFloat(retryAfterMS, 64)
			if parseErr == nil {
				return validateServerRetryDelayMS(value, maxRetryDelayMS, err.Error())
			}
		}
		if retryAfter := err.Headers.Get("retry-after"); retryAfter != "" {
			seconds, parseErr := strconv.ParseFloat(retryAfter, 64)
			var delayMS float64
			if parseErr == nil {
				delayMS = seconds * 1000
			} else if t, timeErr := http.ParseTime(retryAfter); timeErr == nil {
				delayMS = float64(time.Until(t).Milliseconds())
			} else {
				// pi: Date.parse failure yields NaN; setTimeout(NaN) sleeps 0.
				delayMS = 0
			}
			return validateServerRetryDelayMS(delayMS, maxRetryDelayMS, err.Error())
		}
	}
	exponentialDelay := math.Min(0.5*math.Pow(2, float64(retryIndex)), 8) * 1000
	return time.Duration(exponentialDelay*(1-rand.Float64()*0.25)) * time.Millisecond, nil
}

// validateServerRetryDelayMS fails a server-requested delay above the cap so
// higher-level retry logic can surface it (pi:
// validateServerRetryDelayMs). A delay at or below 0 (e.g. unparseable
// retry-after) passes the cap check unchanged, sleeping 0 like pi's
// setTimeout(Math.max(0, NaN)).
func validateServerRetryDelayMS(delayMS float64, maxRetryDelayMS *int64, providerErrorMessage string) (time.Duration, error) {
	maxDelayMS := int64(defaultMaxRetryDelayMS)
	if maxRetryDelayMS != nil {
		maxDelayMS = *maxRetryDelayMS
	}
	if maxDelayMS > 0 && delayMS > float64(maxDelayMS) {
		return 0, &retryDelayExceededError{
			delayMS: delayMS,
			maxMS:   maxDelayMS,
			message: providerErrorMessage,
		}
	}
	return time.Duration(delayMS) * time.Millisecond, nil
}

// retryDelayExceededError surfaces a server-requested retry delay over the cap
// (pi: throws a plain Error with the composed message).
type retryDelayExceededError struct {
	delayMS float64
	maxMS   int64
	message string
}

func (e *retryDelayExceededError) Error() string {
	return "Server requested " + strconv.Itoa(int(math.Ceil(e.delayMS/1000))) + "s retry delay (max: " +
		strconv.Itoa(int(math.Ceil(float64(e.maxMS)/1000))) + "s). " + e.message
}

// abortableSleep sleeps, aborting immediately when ctx is cancelled (pi:
// abortableSleep + createAbortError). A nil ctx sleeps uninterruptibly.
func abortableSleep(ms time.Duration, ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return newAbortError()
	}
	timer := time.NewTimer(ms)
	defer timer.Stop()
	if ctx == nil {
		<-timer.C
		return nil
	}
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return newAbortError()
	}
}

// AbortError marks a cancelled request (pi: createAbortError — an Error named
// AbortError).
type AbortError struct{}

func (e *AbortError) Error() string { return "Request aborted" }

func newAbortError() error { return &AbortError{} }
