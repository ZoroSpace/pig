// pi: test/tools-manager.test.ts — availability/download manager. vi.stubGlobal
// and vi.mock of child_process become the fetchDo seam (management_http.go);
// the child_process mock (spawning tar/unzip) is unnecessary because the
// tests below never reach extraction.
package utils

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent"
)

// osLookupEnv/osSetenv/osUnsetenv alias the os package for the offline-mode
// env guards.
func osLookupEnv(key string) (string, bool) { return os.LookupEnv(key) }
func osSetenv(key, value string)            { _ = os.Setenv(key, value) }
func osUnsetenv(key string)                 { _ = os.Unsetenv(key) }

// stubFetchDo replaces the HTTP seam for the duration of one test and
// restores it on cleanup (pi: vi.stubGlobal("fetch", ...)).
func stubFetchDo(t *testing.T, impl func(client *http.Client, req *http.Request) (*http.Response, error)) {
	t.Helper()
	original := fetchDo
	t.Cleanup(func() { fetchDo = original })
	fetchDo = impl
}

// redirectResponse builds pi's new Response(null, { status: 302, headers: {
// location } }).
func redirectResponse(location string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{location}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func withOfflineDisabled(t *testing.T) {
	t.Helper()
	original, had := osLookupEnv("PI_OFFLINE")
	t.Cleanup(func() {
		if had {
			osSetenv("PI_OFFLINE", original)
		} else {
			osUnsetenv("PI_OFFLINE")
		}
	})
	osUnsetenv("PI_OFFLINE")
}

func TestGetLatestVersionResolvesFromRedirect(t *testing.T) {
	var requestedURL string
	var requestedNoRedirect bool
	stubFetchDo(t, func(client *http.Client, req *http.Request) (*http.Response, error) {
		requestedURL = req.URL.String()
		requestedNoRedirect = strings.Contains(req.URL.String(), "/releases/latest")
		return redirectResponse("https://github.com/sharkdp/fd/releases/tag/v10.4.2"), nil
	})

	version, err := GetLatestVersion(t.Context(), "sharkdp/fd")
	if err != nil {
		t.Fatal(err)
	}
	if version != "10.4.2" {
		t.Fatalf("version = %q", version)
	}
	if requestedURL != "https://github.com/sharkdp/fd/releases/latest" {
		t.Fatalf("requested URL = %q", requestedURL)
	}
	_ = requestedNoRedirect
}

func TestGetLatestVersionKeepsTagsWithoutV(t *testing.T) {
	stubFetchDo(t, func(client *http.Client, req *http.Request) (*http.Response, error) {
		return redirectResponse("https://github.com/BurntSushi/ripgrep/releases/tag/15.2.0"), nil
	})

	version, err := GetLatestVersion(t.Context(), "BurntSushi/ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	if version != "15.2.0" {
		t.Fatalf("version = %q", version)
	}
}

func TestGetLatestVersionResolvesRelativeRedirects(t *testing.T) {
	stubFetchDo(t, func(client *http.Client, req *http.Request) (*http.Response, error) {
		return redirectResponse("/sharkdp/fd/releases/tag/v10.4.2"), nil
	})

	version, err := GetLatestVersion(t.Context(), "sharkdp/fd")
	if err != nil {
		t.Fatal(err)
	}
	if version != "10.4.2" {
		t.Fatalf("version = %q", version)
	}
}

func TestGetLatestVersionFailsWithoutRedirect(t *testing.T) {
	stubFetchDo(t, func(client *http.Client, req *http.Request) (*http.Response, error) {
		return textResponse(http.StatusNotFound, "not found"), nil
	})

	_, err := GetLatestVersion(t.Context(), "sharkdp/fd")
	if err == nil || err.Error() != "Failed to resolve latest sharkdp/fd release: HTTP 404 without redirect" {
		t.Fatalf("err = %v", err)
	}
}

func TestGetLatestVersionFailsOnNonTagRedirect(t *testing.T) {
	stubFetchDo(t, func(client *http.Client, req *http.Request) (*http.Response, error) {
		return redirectResponse("https://github.com/login"), nil
	})

	_, err := GetLatestVersion(t.Context(), "sharkdp/fd")
	if err == nil || err.Error() != "Failed to resolve latest sharkdp/fd release: unexpected redirect to https://github.com/login" {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureToolSurfacesErrorCauseChain(t *testing.T) {
	forceUninstalledTool(t)
	withOfflineDisabled(t)

	// pi: TypeError("fetch failed", { cause: new Error("connect ETIMEDOUT
	// 140.82.113.3:443") }) — the cause chain is joined with ": ".
	cause := errors.New("connect ETIMEDOUT 140.82.113.3:443")
	stubFetchDo(t, func(client *http.Client, req *http.Request) (*http.Response, error) {
		return nil, &causeChainError{msg: "fetch failed", cause: cause}
	})
	var statuses []ToolStatus

	result := EnsureTool(t.Context(), "fd", func(status ToolStatus) { statuses = append(statuses, status) })

	if result != "" {
		t.Fatalf("expected no result, got %q", result)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v", statuses)
	}
	if statuses[0].Type != "info" || statuses[0].Message != "fd not found. Downloading..." {
		t.Fatalf("status[0] = %+v", statuses[0])
	}
	if statuses[1].Type != "warning" {
		t.Fatalf("status[1] = %+v", statuses[1])
	}
	want := "Failed to download fd: fetch failed: connect ETIMEDOUT 140.82.113.3:443"
	if statuses[1].Message != want {
		t.Fatalf("status[1].Message = %q, want %q", statuses[1].Message, want)
	}
}

func TestEnsureToolSkipsDownloadWhenOffline(t *testing.T) {
	forceUninstalledTool(t)
	t.Setenv("PI_OFFLINE", "1")
	var statuses []ToolStatus
	result := EnsureTool(t.Context(), "fd", func(status ToolStatus) { statuses = append(statuses, status) })
	if result != "" {
		t.Fatalf("expected no result, got %q", result)
	}
	if len(statuses) != 1 || statuses[0].Type != "warning" {
		t.Fatalf("statuses = %+v", statuses)
	}
	if !strings.Contains(statuses[0].Message, "Offline mode enabled") {
		t.Fatalf("status[0].Message = %q", statuses[0].Message)
	}
	_ = codingagent.AppName
}

// forceUninstalledTool hides the system fd/rg binaries so the download path
// is reachable on machines where they are installed (pi's test environment
// simply lacks them; pig hides them via an empty PATH).
func forceUninstalledTool(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
	if GetToolPath("fd") != "" {
		t.Skip("fd is still resolvable without PATH (installed in the tools dir)")
	}
}

// causeChainError is a TypeError-like error whose message excludes its
// cause (pi's Error.cause is not part of .message).
type causeChainError struct {
	msg   string
	cause error
}

func (e *causeChainError) Error() string { return e.msg }
func (e *causeChainError) Unwrap() error { return e.cause }
