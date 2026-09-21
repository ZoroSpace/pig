// bug_report_test.go (pi: test/bug-report.test.ts, pi v0.86.0) — redaction
// only: URL credentials/secret query params and nested secret values.
package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBugReportRedactURL(t *testing.T) {
	// "removes URL credentials and secret query parameters"
	cases := []struct{ in, want string }{
		{"https://user:pass@proxy.example.com:8080/", "https://proxy.example.com:8080/"},
		{"git:https://pat@github.com/org/repo", "git:https://github.com/org/repo"},
		{"https://api.example/v1?api-key=abc&model=x", "https://api.example/v1?api-key=%3Credacted%3E&model=x"},
	}
	for _, tc := range cases {
		if got := RedactUrl(tc.in); got != tc.want {
			t.Errorf("RedactUrl(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBugReportRedactJsonValue(t *testing.T) {
	// "redacts nested secret values without hiding token counts"
	input := map[string]any{
		"apiKey": "sk-123",
		"headers": map[string]any{
			"Authorization": "Bearer x",
			"X-Trace":       "1",
		},
		"compaction": map[string]any{
			"reserveTokens":    16384,
			"keepRecentTokens": 20000,
		},
		"baseUrl": "https://me:secret@example.com/",
	}
	redacted := RedactJsonValue(input)
	// Compare through JSON to normalize numbers (pi: JSON.parse(JSON.stringify)).
	inputJSON, _ := json.Marshal(input)
	redactedJSON, _ := json.Marshal(redacted)
	var inputAny, redactedAny any
	_ = json.Unmarshal(inputJSON, &inputAny)
	_ = json.Unmarshal(redactedJSON, &redactedAny)

	want := map[string]any{
		"apiKey": "<redacted>",
		"headers": map[string]any{
			"Authorization": "<redacted>",
			"X-Trace":       "1",
		},
		"compaction": map[string]any{
			"reserveTokens":    float64(16384),
			"keepRecentTokens": float64(20000),
		},
		"baseUrl": "https://example.com/",
	}
	if !reflect.DeepEqual(redactedAny, want) {
		t.Fatalf("redacted = %v, want %v", redactedAny, want)
	}
}
