// pi: test/google-shared-retry.test.ts.
package api

import (
	"context"
	"errors"
	"testing"

	"github.com/ZoroSpace/pig/ai/utils"
)

// googleApiError shapes @google/genai's ApiError: a status with no headers
// (pig: *utils.ProviderHTTPError with nil Headers).
func googleApiError(status int) error {
	return &utils.ProviderHTTPError{Status: status, Message: "got status: " + itoa(status)}
}

func itoa(v int) string {
	digits := "0123456789"
	if v == 0 {
		return "0"
	}
	out := ""
	for v > 0 {
		out = string(digits[v%10]) + out
		v /= 10
	}
	return out
}

func TestGoogleRetryRetriesHeadersLessErrorWithRetryableStatus(t *testing.T) {
	calls := 0
	request := func() (string, error) {
		calls++
		if calls == 1 {
			return "", googleApiError(429)
		}
		return "ok", nil
	}

	result, err := RetryGoogleRequest(request, GoogleRetryOptions{MaxRetries: 1, Ctx: context.Background()})
	if err != nil {
		t.Fatalf("RetryGoogleRequest failed: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want ok", result)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestGoogleRetryDoesNotRetryWhenMaxRetriesUnset(t *testing.T) {
	err429 := googleApiError(429)
	calls := 0
	request := func() (string, error) {
		calls++
		return "", err429
	}

	if _, err := RetryGoogleRequest(request, GoogleRetryOptions{Ctx: context.Background()}); !errors.Is(err, err429) {
		t.Fatalf("err = %v, want the original error", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestGoogleRetryDoesNotRetryNonRetryableStatus(t *testing.T) {
	calls := 0
	request := func() (string, error) {
		calls++
		return "", googleApiError(400)
	}

	if _, err := RetryGoogleRequest(request, GoogleRetryOptions{MaxRetries: 2, Ctx: context.Background()}); err == nil {
		t.Fatal("expected the 400 to propagate")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
