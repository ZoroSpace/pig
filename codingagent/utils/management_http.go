// pi: src/utils/management-http.ts — fetch with bounded immediate retry for
// idempotent management requests.
//
// Segment deviations:
//   - The fetch Response body is returned as a raw reader plus status; pig
//     callers read to completion.
//   - Caller cancellation rides the context (pi: parent signal /
//     AbortSignal.any).
package utils

import (
	"context"
	"io"
	"net/http"
	"time"
)

// retryableStatusCodes mirror pi's RETRYABLE_STATUS_CODES.
var retryableStatusCodes = map[int]struct{}{
	408: {}, 425: {}, 429: {}, 500: {}, 502: {}, 503: {}, 504: {},
}

// FetchRetryOptions parameterize FetchWithRetry (pi: FetchRetryOptions).
type FetchRetryOptions struct {
	// MaxRetries is the number of additional attempts after the initial
	// request. Defaults to two.
	MaxRetries *int
	// RetryTransientStatus also retries transient HTTP responses. Defaults
	// to true; nil = default.
	RetryOnStatus *bool
	// TimeoutMs is the overall time budget shared by all attempts (pi:
	// timeoutMs; millis).
	TimeoutMs *int64
	// AttemptTimeoutMs aborts only the current attempt so a hung connection
	// can be retried (pi: attemptTimeoutMs; millis).
	AttemptTimeoutMs *int64
}

// FetchResponse is the minimal response surface needed by callers. The
// attempt context's cancel travels with the response (pi: the attempt
// AbortSignal also governs the body stream); callers must call Close, which
// closes the body and releases the attempt context.
type FetchResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	cancel     context.CancelFunc
}

// Close releases the response body and the attempt context.
func (r *FetchResponse) Close() {
	if r.Body != nil {
		r.Body.Close()
	}
	if r.cancel != nil {
		r.cancel()
	}
}

// FetchWithRetry fetches a management HTTP resource with a bounded immediate
// retry (pi: fetchWithRetry). Caller cancellation and the overall timeout are
// terminal; the attempt timeout aborts only the current attempt.
func FetchWithRetry(ctx context.Context, method string, url string, headers map[string]string, options *FetchRetryOptions) (*FetchResponse, error) {
	maxRetries := 2
	retryOnStatus := true
	if options != nil {
		if options.MaxRetries != nil {
			maxRetries = *options.MaxRetries
			if maxRetries < 0 {
				maxRetries = 0
			}
		}
		if options.RetryOnStatus != nil {
			retryOnStatus = *options.RetryOnStatus
		}
	}

	var deadline time.Time
	if options != nil && options.TimeoutMs != nil && *options.TimeoutMs > 0 {
		deadline = time.Now().Add(time.Duration(*options.TimeoutMs) * time.Millisecond)
	}

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, ctx.Err()
		}
		attemptCtx := ctx
		var cancel context.CancelFunc = func() {}
		if options != nil && options.AttemptTimeoutMs != nil && *options.AttemptTimeoutMs > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(*options.AttemptTimeoutMs)*time.Millisecond)
		}

		req, err := http.NewRequestWithContext(attemptCtx, method, url, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			// Transport failures retry within the budget.
			if attempt < maxRetries && ctx.Err() == nil {
				cancel()
				continue
			}
			cancel()
			return nil, err
		}
		_, retryable := retryableStatusCodes[resp.StatusCode]
		shouldRetry := retryOnStatus && retryable && attempt < maxRetries
		if !shouldRetry {
			return &FetchResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body, cancel: cancel}, nil
		}
		// Discard the body before retrying.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
	}
}
