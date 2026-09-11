// pi: auth/oauth/device-code.ts.
package auth

import (
	"context"
	"fmt"
	"time"
)

const (
	deviceCodeCancelMessage          = "Login cancelled"
	deviceCodeTimeoutMessage         = "Device flow timed out"
	deviceCodeSlowDownTimeoutMessage = "Device flow timed out after one or more slow_down responses. This is often caused by clock drift in WSL or VM environments. Please sync or restart the VM clock and try again."
	deviceCodeMinimumIntervalMs      = int64(1000)
	// RFC 8628 section 3.2: if the authorization server omits `interval`, the
	// client must use 5 seconds.
	deviceCodeDefaultIntervalSeconds = int64(5)
	// RFC 8628 section 3.5: `slow_down` means the polling interval must
	// increase by 5 seconds.
	deviceCodeSlowDownIncrementMs = int64(5000)
)

// DeviceCodePollStatus discriminates one poll outcome.
type DeviceCodePollStatus string

const (
	DeviceCodePending  DeviceCodePollStatus = "pending"
	DeviceCodeSlowDown DeviceCodePollStatus = "slow_down"
	DeviceCodeFailed   DeviceCodePollStatus = "failed"
	DeviceCodeComplete DeviceCodePollStatus = "complete"
)

// DeviceCodePollResult is one poll callback outcome (pi:
// OAuthDeviceCodePollResult<T>).
type DeviceCodePollResult[T any] struct {
	Status DeviceCodePollStatus
	// IntervalSeconds carries the server-provided new interval of a
	// slow_down response (pi: optional intervalSeconds).
	IntervalSeconds int64
	Message         string
	Value           T
}

// DeviceCodePollOptions bounds one device-code poll loop (pi:
// OAuthDeviceCodePollOptions; signal → ctx).
type DeviceCodePollOptions[T any] struct {
	// IntervalSeconds is the initial poll interval; 0 uses the RFC default.
	IntervalSeconds int64
	// ExpiresInSeconds bounds the whole flow; 0 means no deadline.
	ExpiresInSeconds    int64
	WaitBeforeFirstPoll bool
	Poll                func() DeviceCodePollResult[T]
	// Ctx cancels the wait (pi: signal; pi's cancel message becomes the
	// ctx-caused error text).
	Ctx context.Context
}

// deviceCodeAbortableSleep is pi's abortableSleep: ctx cancellation rejects
// the wait with the cancel message.
func deviceCodeAbortableSleep(ctx context.Context, ms int64) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s", deviceCodeCancelMessage)
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("%s", deviceCodeCancelMessage)
	case <-timer.C:
		return nil
	}
}

// PollOAuthDeviceCodeFlow runs the RFC 8628 polling loop (pi:
// pollOAuthDeviceCodeFlow).
func PollOAuthDeviceCodeFlow[T any](options DeviceCodePollOptions[T]) (T, error) {
	var zero T
	deadline := time.Time{}
	if options.ExpiresInSeconds > 0 {
		deadline = time.Now().Add(time.Duration(options.ExpiresInSeconds) * time.Second)
	}
	intervalMs := deviceCodeMinimumIntervalMs
	if candidate := options.IntervalSeconds * 1000; candidate > intervalMs {
		intervalMs = candidate
	}

	slowDownResponses := 0
	if options.WaitBeforeFirstPoll {
		remaining := time.Duration(0)
		if !deadline.IsZero() {
			remaining = time.Until(deadline)
			if remaining <= 0 {
				return zero, fmt.Errorf("%s", deviceCodeTimeoutMessage)
			}
		}
		wait := time.Duration(intervalMs) * time.Millisecond
		if !deadline.IsZero() && wait > remaining {
			wait = remaining
		}
		if err := deviceCodeAbortableSleep(options.Ctx, wait.Milliseconds()); err != nil {
			return zero, err
		}
	}

	for {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			break
		}
		if err := options.Ctx.Err(); err != nil {
			return zero, fmt.Errorf("%s", deviceCodeCancelMessage)
		}

		result := options.Poll()
		switch result.Status {
		case DeviceCodeComplete:
			return result.Value, nil
		case DeviceCodeFailed:
			return zero, fmt.Errorf("%s", result.Message)
		case DeviceCodeSlowDown:
			slowDownResponses++
			// Use the server-provided interval when given (GitHub reports
			// the new required minimum in `interval`); trusting only a
			// client-tracked value risks polling early forever under
			// WSL/VM clock drift. Otherwise apply RFC 8628 section 3.5:
			// increase by 5 seconds.
			if result.IntervalSeconds > 0 {
				intervalMs = result.IntervalSeconds * 1000
				if intervalMs < deviceCodeMinimumIntervalMs {
					intervalMs = deviceCodeMinimumIntervalMs
				}
			} else {
				intervalMs += deviceCodeSlowDownIncrementMs
				if intervalMs < deviceCodeMinimumIntervalMs {
					intervalMs = deviceCodeMinimumIntervalMs
				}
			}
		}

		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			wait := time.Duration(intervalMs) * time.Millisecond
			if wait > remaining {
				wait = remaining
			}
			if err := deviceCodeAbortableSleep(options.Ctx, wait.Milliseconds()); err != nil {
				return zero, err
			}
		} else if err := deviceCodeAbortableSleep(options.Ctx, intervalMs); err != nil {
			return zero, err
		}
	}

	if slowDownResponses > 0 {
		return zero, fmt.Errorf("%s", deviceCodeSlowDownTimeoutMessage)
	}
	return zero, fmt.Errorf("%s", deviceCodeTimeoutMessage)
}
