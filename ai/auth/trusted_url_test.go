package auth

import "testing"

func TestTrustedHTTPURL(t *testing.T) {
	for _, want := range []struct {
		name  string
		value any
		out   string
	}{
		{"https is kept", "https://example.com/device?code=1", "https://example.com/device?code=1"},
		{"http is kept", "http://localhost:1455/callback", "http://localhost:1455/callback"},
		{"control characters are percent-encoded", "https://example.com/\x01", "https://example.com/%01"},
		{"file is rejected", "file:///etc/passwd", ""},
		{"javascript is rejected", "javascript:alert(1)", ""},
		{"opaque http is rejected", "http:foo", ""},
		{"non-string is rejected", 42, ""},
	} {
		if got := trustedHTTPURL(want.value); got != want.out {
			t.Errorf("%s: trustedHTTPURL(%v) = %q, want %q", want.name, want.value, got, want.out)
		}
	}
}
