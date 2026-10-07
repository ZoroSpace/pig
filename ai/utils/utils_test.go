package utils

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestShortHash(t *testing.T) {
	// Reference values computed with the pi implementation (node).
	cases := map[string]string{
		"":                "k4n83c7h0j2b",
		"hello world":     "n7rb4n1m39uz8",
		"a":               "m8735310ae7sx",
		"call_1|item_abc": "149sg7q1f2wslc",
	}
	for input, want := range cases {
		if got := ShortHash(input); got != want {
			t.Errorf("ShortHash(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSanitizeSurrogates(t *testing.T) {
	// Raw UTF-8 encodings of unpaired surrogates (ED A0-BD high, ED 80-BD low).
	high := string([]byte{0xed, 0xa0, 0xbd})
	low := string([]byte{0xed, 0x80, 0xbd})
	if got := SanitizeSurrogates("a" + high + "b" + low + "c"); got != "abc" {
		t.Fatalf("got %q, want %q", got, "abc")
	}
	// Valid non-BMP chars (emoji) are untouched.
	emoji := "\U0001F648"
	if got := SanitizeSurrogates("x" + emoji + "y"); got != "x"+emoji+"y" {
		t.Fatalf("emoji mangled: %q", got)
	}
}

func TestRepairJSON(t *testing.T) {
	cases := map[string]string{
		`{"a":"hello` + "\n" + `world"}`: `{"a":"hello\nworld"}`,
		`{"a":"\q"}`:                     `{"a":"\\q"}`,
	}
	for input, want := range cases {
		if got := RepairJSON(input); got != want {
			t.Errorf("RepairJSON(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseStreamingJSON(t *testing.T) {
	// Complete JSON parses normally.
	complete := `{"city":"beijing","n":42}`
	got := ParseStreamingJSON(&complete)
	if got["city"] != "beijing" || got["n"] != float64(42) {
		t.Fatalf("complete parse = %v", got)
	}
	// Partial JSON still yields a usable object.
	partial := `{"city":"beijing","n":4`
	got = ParseStreamingJSON(&partial)
	if got["city"] != "beijing" {
		t.Fatalf("partial parse = %v", got)
	}
	// Blank input yields an empty map.
	blank := ""
	if got := ParseStreamingJSON(&blank); len(got) != 0 {
		t.Fatalf("blank parse = %v", got)
	}
	var nilPtr *string
	if got := ParseStreamingJSON(nilPtr); len(got) != 0 {
		t.Fatalf("nil parse = %v", got)
	}
}

func TestFormatProviderError(t *testing.T) {
	norm := NormalizeProviderError(&ProviderHTTPError{
		Status:  401,
		Headers: http.Header{},
		Body:    `{"error":"bad key"}`,
		Message: "401 Unauthorized",
	})
	got := FormatProviderError(norm, "")
	if got != "401: {\"error\":\"bad key\"}" {
		t.Fatalf("got %q", got)
	}
	// messageCarriesBody: when the message embeds the body, return it.
	norm2 := NormalizeProviderError(errors.New("plain error"))
	if got := FormatProviderError(norm2, "DeepSeek"); got != "plain error" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateErrorText(t *testing.T) {
	got := TruncateErrorText("1234567890", 5)
	want := "12345... [truncated 5 chars]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGetProviderEnvValue(t *testing.T) {
	t.Setenv("PIG_TEST_ENV", "from-env")
	if got := GetProviderEnvValue("PIG_TEST_ENV", nil); got != "from-env" {
		t.Fatalf("env lookup = %q", got)
	}
	scoped := map[string]string{"PIG_TEST_ENV": "from-scope"}
	if got := GetProviderEnvValue("PIG_TEST_ENV", scoped); got != "from-scope" {
		t.Fatalf("scoped lookup = %q", got)
	}
}

func TestRetryProviderRequest(t *testing.T) {
	attempts := 0
	request := func() (int, error) {
		attempts++
		if attempts < 3 {
			return 0, &ProviderHTTPError{Status: 429, Headers: http.Header{"retry-after": {"0"}}}
		}
		return 42, nil
	}
	got, err := RetryProviderRequest(request, ProviderRetryOptions{MaxRetries: 3})
	if err != nil || got != 42 || attempts != 3 {
		t.Fatalf("got=%d err=%v attempts=%d", got, err, attempts)
	}

	// Non-provider errors are never retried.
	attempts = 0
	_, err = RetryProviderRequest(func() (int, error) {
		attempts++
		return 0, errors.New("boom")
	}, ProviderRetryOptions{MaxRetries: 3})
	if err == nil || attempts != 1 {
		t.Fatalf("non-provider error retried: attempts=%d err=%v", attempts, err)
	}
}

// S11-1: pi #9571 — an unparseable Retry-After falls through to the
// exponential backoff instead of sleeping 0.
func TestGetRetryDelayMSUnparseableRetryAfterFallsBackToExponential(t *testing.T) {
	for _, headers := range []http.Header{
		{"Retry-After": {"garbage"}},
		{"Retry-After-Ms": {"NaN"}, "Retry-After": {"garbage"}},
	} {
		err := &ProviderHTTPError{Status: 429, Headers: headers}
		delay, delayErr := getRetryDelayMS(err, 0, nil)
		if delayErr != nil {
			t.Fatalf("%v: unexpected error %v", headers, delayErr)
		}
		// Index 0: 500ms jittered down by up to 25% (375–500ms); a parsed
		// garbage value would give 0, the old behavior.
		if delay < 375*time.Millisecond || delay > 500*time.Millisecond {
			t.Fatalf("%v: delay = %v, want the 375–500ms exponential window", headers, delay)
		}
	}

	// A NaN/Infinity retry-after-ms also falls through (JS Number.isFinite).
	delay, err := getRetryDelayMS(&ProviderHTTPError{Status: 429, Headers: http.Header{"Retry-After-Ms": {"Infinity"}}}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if delay < 375*time.Millisecond || delay > 500*time.Millisecond {
		t.Fatalf("delay = %v, want the exponential window", delay)
	}
}

func TestEstimateTokens(t *testing.T) {
	// 4 chars ÷ 3.5 chars/token rounds up to 2 (pi: CHARS_PER_TOKEN = 3.5
	// since #10497).
	if got := EstimateTextTokens("abcd"); got != 2 {
		t.Fatalf("EstimateTextTokens = %d", got)
	}
	if got := EstimateTextTokens("abcde"); got != 2 {
		t.Fatalf("EstimateTextTokens = %d", got)
	}
}

func TestEstimateContextReservesThreePointFiveCharsPerToken(t *testing.T) {
	// Regression for pi #10497: large new inputs need more room than
	// chars/4 allows — 3500 trailing chars now estimate to 1000 tokens.
	messages := []types.Message{
		types.AssistantMessage{Role: types.RoleAssistant, Usage: types.Usage{TotalTokens: 2000}, Timestamp: 100},
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: strings.Repeat("x", 3500)}, Timestamp: 200},
	}
	estimate := EstimateContextTokensOfMessages(messages)
	if estimate.Tokens != 3000 || estimate.UsageTokens != 2000 || estimate.TrailingTokens != 1000 {
		t.Fatalf("estimate = %+v, want {tokens: 3000, usageTokens: 2000, trailingTokens: 1000}", estimate)
	}
}

func TestHeadersToRecord(t *testing.T) {
	h := http.Header{}
	h.Set("X-Test", "v1")
	h.Add("X-Test", "v2")
	rec := HeadersToRecord(h)
	if rec["X-Test"] != "v2" {
		t.Fatalf("got %v", rec)
	}
	if !reflect.DeepEqual(rec, map[string]string{"X-Test": "v2"}) {
		t.Fatalf("got %v", rec)
	}
}
