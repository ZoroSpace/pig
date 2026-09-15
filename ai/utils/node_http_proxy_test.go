// pi: test/node-http-proxy.test.ts.
package utils

import (
	"os"
	"slices"
	"testing"
)

// pi's PROXY_ENV_KEYS.
var proxyEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "no_proxy", "all_proxy",
	"npm_config_http_proxy", "npm_config_https_proxy", "npm_config_proxy", "npm_config_no_proxy",
}

func resetProxyEnv(t *testing.T) {
	t.Helper()
	original := map[string]string{}
	for _, key := range proxyEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			original[key] = value
		}
		os.Unsetenv(key)
	}
	t.Cleanup(func() {
		for _, key := range proxyEnvKeys {
			os.Unsetenv(key)
		}
		for key, value := range original {
			os.Setenv(key, value)
		}
	})
}

func TestResolveHTTPProxyURLForTarget(t *testing.T) {
	t.Run("respects NO_PROXY exclusions", func(t *testing.T) {
		resetProxyEnv(t)
		os.Setenv("HTTPS_PROXY", "http://proxy.example:8080")
		os.Setenv("NO_PROXY", "bedrock-runtime.us-east-1.amazonaws.com")

		proxy, err := ResolveHTTPProxyURLForTarget("https://bedrock-runtime.us-east-1.amazonaws.com", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if proxy != nil {
			t.Fatalf("expected no proxy, got %s", proxy.String())
		}
	})

	t.Run("resolves HTTP and HTTPS proxy URLs", func(t *testing.T) {
		resetProxyEnv(t)
		os.Setenv("HTTPS_PROXY", "http://proxy.example:8080")

		proxy, err := ResolveHTTPProxyURLForTarget("https://bedrock-runtime.us-east-1.amazonaws.com", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if proxy == nil || proxy.String() != "http://proxy.example:8080" {
			t.Fatalf("expected http://proxy.example:8080, got %v", proxy)
		}
	})

	t.Run("prefers scoped proxy env aliases before process env aliases", func(t *testing.T) {
		resetProxyEnv(t)
		os.Setenv("https_proxy", "http://process-proxy.example:8080")

		proxy, err := ResolveHTTPProxyURLForTarget("https://bedrock-runtime.us-east-1.amazonaws.com", map[string]string{
			"HTTPS_PROXY": "http://scoped-proxy.example:8080",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if proxy == nil || proxy.String() != "http://scoped-proxy.example:8080" {
			t.Fatalf("expected http://scoped-proxy.example:8080, got %v", proxy)
		}
	})

	t.Run("rejects SOCKS and PAC proxy URLs explicitly", func(t *testing.T) {
		resetProxyEnv(t)
		os.Setenv("HTTPS_PROXY", "socks5://proxy.example:1080")

		_, err := ResolveHTTPProxyURLForTarget("https://bedrock-runtime.us-east-1.amazonaws.com", nil)
		if err == nil {
			t.Fatalf("expected an error")
		}
		if err.Error() != UnsupportedProxyProtocolMessage+" Got socks5:" {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("handles subdomain wildcards, IPv6, and ports in NO_PROXY", func(t *testing.T) {
		resetProxyEnv(t)
		os.Setenv("HTTPS_PROXY", "http://proxy.example:8080")
		os.Setenv("NO_PROXY", "example.com, .wildcard.org, *.star.net, ::1, [2001:db8::1], 127.0.0.1:8080")

		for _, target := range []string{
			"https://example.com",
			"https://api.example.com",
			"https://wildcard.org",
			"https://api.wildcard.org",
			"https://star.net",
			"https://api.star.net",
			"https://[::1]:80",
			"https://[2001:db8::1]",
			"https://127.0.0.1:8080",
		} {
			proxy, err := ResolveHTTPProxyURLForTarget(target, nil)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", target, err)
			}
			if proxy != nil {
				t.Errorf("%s: expected no proxy, got %s", target, proxy.String())
			}
		}

		for _, target := range []string{"https://notexample.com", "https://127.0.0.1:3000"} {
			proxy, err := ResolveHTTPProxyURLForTarget(target, nil)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", target, err)
			}
			if proxy == nil || proxy.String() != "http://proxy.example:8080" {
				t.Errorf("%s: expected the proxy, got %v", target, proxy)
			}
		}
	})

	t.Run("parses NO_PROXY entries like pi", func(t *testing.T) {
		// pi: node-http-proxy.test.ts does not cover parseNoProxyEntry directly;
		// these pin the entry grammar (bare IPv6, bracketed IPv6, ports,
		// non-numeric port fallback).
		cases := []struct {
			entry  string
			host   string
			port   int
			wantOK bool
		}{
			{"example.com", "example.com", 0, true},
			{".wildcard.org", ".wildcard.org", 0, true},
			{"::1", "::1", 0, true},
			{"[2001:db8::1]", "2001:db8::1", 0, true},
			{"[2001:db8::1]:8443", "2001:db8::1", 8443, true},
			{"127.0.0.1:8080", "127.0.0.1", 8080, true},
			{"host:not-a-port", "host:not-a-port", 0, true},
		}
		for _, tc := range cases {
			host, port, ok := parseNoProxyEntry(tc.entry)
			if ok != tc.wantOK || host != tc.host || port != tc.port {
				t.Errorf("parseNoProxyEntry(%q) = (%q, %d, %v), want (%q, %d, %v)",
					tc.entry, host, port, ok, tc.host, tc.port, tc.wantOK)
			}
		}
		if _, _, ok := parseNoProxyEntry(""); ok {
			t.Errorf("empty entries must not parse")
		}
		if !slices.Contains(splitNoProxy("a, b\tc"), "b") {
			t.Errorf("splitNoProxy must split on commas and whitespace")
		}
	})
}
