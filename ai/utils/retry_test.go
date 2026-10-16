// Ported from pi's test/retry.test.ts (v0.86.0 increments): Azure peak-load
// classification (#9669), 520 status (#9627), and the retry delay cap
// regressions (#8826). pig never had a dedicated retry test file; the
// classifier and loop were previously only covered via harness compaction
// tests.
package utils

import (
	"context"
	"errors"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

func retryTestMessage(text string, stopReason types.StopReason, errorMessage string) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{types.NewTextContent(text)},
		API:          types.ApiOpenAICompletions,
		Provider:     "test",
		Model:        "test",
		Usage:        types.Usage{},
		StopReason:   stopReason,
		ErrorMessage: errorMessage,
		Timestamp:    0,
	}
}

func TestIsRetryableAssistantErrorMatchesAzurePeakLoad(t *testing.T) {
	// Regression for pi #9669.
	message := retryTestMessage("", types.StopReasonError,
		"The system is currently experiencing high demand and cannot process your request. "+
			"Your request exceeds the maximum usage size allowed during peak load. "+
			"For improved capacity reliability, consider switching to Provisioned Throughput.")
	if !IsRetryableAssistantError(message) {
		t.Fatal("Azure peak-load error should be retryable")
	}
}

func TestIsRetryableAssistantErrorMatches520(t *testing.T) {
	// Regression for pi #9627.
	message := retryTestMessage("", types.StopReasonError, "520 status code (no body)")
	if !IsRetryableAssistantError(message) {
		t.Fatal("520 should be retryable")
	}
	if IsRetryableAssistantError(retryTestMessage("ok", types.StopReasonStop, "")) {
		t.Fatal("successful message should not be retryable")
	}
}

func TestIsRetryableAssistantErrorMatchesServerBusy(t *testing.T) {
	// Regression for pi #10543: transient server-busy provider errors.
	for _, errorMessage := range []string{
		"server_busy",
		"All servers are currently busy, please try again.",
	} {
		message := retryTestMessage("", types.StopReasonError, errorMessage)
		if !IsRetryableAssistantError(message) {
			t.Fatalf("%q should be retryable", errorMessage)
		}
	}
}

func TestIsRetryableAssistantErrorMatchesPendingStreamCanceled(t *testing.T) {
	// Regression for pi #10379: Bedrock HTTP/2 pending stream cancellation.
	for _, errorMessage := range []string{
		"The pending stream has been canceled",
		"The pending stream has been canceled (caused by: socket closed)",
	} {
		message := retryTestMessage("", types.StopReasonError, errorMessage)
		if !IsRetryableAssistantError(message) {
			t.Fatalf("%q should be retryable", errorMessage)
		}
	}
}

func TestIsRetryableAssistantErrorMatchesUpstreamProviderReturnedError(t *testing.T) {
	// Regression for pi #10842: gateway wording such as "The upstream provider
	// returned an error" now that the optional article is allowed.
	for _, errorMessage := range []string{
		"Provider returned error",
		"The upstream provider returned an error",
		"provider returned an error",
	} {
		message := retryTestMessage("", types.StopReasonError, errorMessage)
		if !IsRetryableAssistantError(message) {
			t.Fatalf("%q should be retryable", errorMessage)
		}
	}
}

func TestRetryDelayMsCapsAgentRetryDelay(t *testing.T) {
	// Regression for pi #8826.
	if got := RetryDelayMs(&RetryPolicy{BaseDelayMs: 2000}, 6); got != 60000 {
		t.Fatalf("delay = %d, want 60000", got)
	}
	if got := RetryDelayMs(&RetryPolicy{BaseDelayMs: 2000, MaxAgentDelayMs: lo.ToPtr(int64(5000))}, 5); got != 5000 {
		t.Fatalf("delay = %d, want 5000", got)
	}
	if got := RetryDelayMs(&RetryPolicy{BaseDelayMs: 2000, MaxAgentDelayMs: lo.ToPtr(int64(0))}, 5); got != 0 {
		t.Fatalf("delay = %d, want 0", got)
	}
}

func TestRetryAssistantCallReportsCappedDelays(t *testing.T) {
	// Regression for pi #8826.
	n := 0
	policy := &RetryPolicy{Enabled: true, MaxRetries: 4, BaseDelayMs: 10, MaxAgentDelayMs: lo.ToPtr(int64(15))}
	var delays []int64
	_, err := RetryAssistantCall(context.Background(), func() (*types.AssistantMessage, error) {
		n++
		if n < 5 {
			return retryTestMessage("", types.StopReasonError, "terminated"), nil
		}
		return retryTestMessage("recovered", types.StopReasonStop, ""), nil
	}, policy, &RetryCallbacks{
		OnRetryScheduled: func(attempt int, maxAttempts int, delayMs int64, errorMessage string) {
			delays = append(delays, delayMs)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delays) != 4 || delays[0] != 10 || delays[1] != 15 || delays[2] != 15 || delays[3] != 15 {
		t.Fatalf("delays = %v, want [10 15 15 15]", delays)
	}
}

func TestRetryAssistantCallPropagatesProduceError(t *testing.T) {
	policy := &RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 1}
	sentinel := errors.New("boom")
	_, err := RetryAssistantCall(context.Background(), func() (*types.AssistantMessage, error) {
		return nil, sentinel
	}, policy, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

func TestIsRetryableAssistantErrorMatchesModelAtCapacity(t *testing.T) {
	// Regression for pi #10278: "Selected model is at capacity" provider
	// errors end the turn instead of being retried.
	message := retryTestMessage("", types.StopReasonError, "Selected model is at capacity. Please try again later.")
	if !IsRetryableAssistantError(message) {
		t.Fatal("model-at-capacity error should be retryable")
	}
}
