package utils

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
)

// pi: test/version-check.test.ts — "compares package versions" (full).
func TestComparePackageVersions(t *testing.T) {
	if v, ok := ComparePackageVersions("0.70.6", "0.70.5"); !ok || v <= 0 {
		t.Errorf("0.70.6 > 0.70.5 expected, got %d ok=%v", v, ok)
	}
	if v, ok := ComparePackageVersions("0.70.5", "0.70.5"); !ok || v != 0 {
		t.Errorf("0.70.5 == 0.70.5 expected, got %d ok=%v", v, ok)
	}
	if v, ok := ComparePackageVersions("0.70.4", "0.70.5"); !ok || v >= 0 {
		t.Errorf("0.70.4 < 0.70.5 expected, got %d ok=%v", v, ok)
	}
	if v, ok := ComparePackageVersions("5.0.0-beta.20", "5.0.0-beta.9"); !ok || v <= 0 {
		t.Errorf("5.0.0-beta.20 > 5.0.0-beta.9 expected, got %d ok=%v", v, ok)
	}
	if v, ok := ComparePackageVersions("1.0.0", "nonsense"); ok {
		t.Errorf("invalid semver should be undefined, got %d", v)
	}

	if IsNewerPackageVersion("0.70.5", "0.70.5") {
		t.Errorf("equal versions are not newer")
	}
	if !IsNewerPackageVersion("0.70.6", "0.70.5") {
		t.Errorf("0.70.6 should be newer than 0.70.5")
	}
}

// pi: test/version-check.test.ts — version-check API tests against a stub
// server (pig seam: latestVersionURL var; pi stubs global fetch).
func TestGetLatestPiRelease(t *testing.T) {
	originalURL := latestVersionURL
	t.Cleanup(func() { latestVersionURL = originalURL })

	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest-version", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); len(got) < 4 || got[:3] != "pi/" {
			t.Errorf("missing pi user agent: %q", got)
		}
		if accept := r.Header.Get("accept"); accept != "application/json" {
			t.Errorf("accept header: %q", accept)
		}
		fmt.Fprint(w, `{"version":"1.2.3"}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	latestVersionURL = server.URL + "/api/latest-version"

	t.Run("returns only newer versions", func(t *testing.T) {
		release := CheckForNewPiVersion(context.Background(), "1.2.3")
		if release != nil {
			t.Errorf("same version should not update, got %+v", release)
		}
		release = CheckForNewPiVersion(context.Background(), "1.2.2")
		if release == nil || release.Version != "1.2.3" {
			t.Errorf("expected 1.2.3 release, got %+v", release)
		}
	})

	t.Run("keeps automatic version checks to one request", func(t *testing.T) {
		requests := 0
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.Error(w, "boom", http.StatusInternalServerError)
		})
		release := CheckForNewPiVersion(context.Background(), "1.2.3")
		if release != nil {
			t.Errorf("expected nil on failure, got %+v", release)
		}
		if requests != 1 {
			t.Errorf("expected exactly one request, got %d", requests)
		}
	})

	t.Run("returns the active package metadata and notes from the version check api", func(t *testing.T) {
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"packageName":"@new-scope/pi","version":"1.2.4","note":"  **Read this**  "}`)
		})
		release, err := GetLatestPiRelease(context.Background(), "1.2.3", nil)
		if err != nil || release == nil {
			t.Fatalf("expected release, got %+v err=%v", release, err)
		}
		if release.PackageName != "@new-scope/pi" {
			t.Errorf("packageName: %q", release.PackageName)
		}
		if release.Note != "**Read this**" {
			t.Errorf("note should be trimmed: %q", release.Note)
		}
	})

	t.Run("skips automatic api calls when version checks are disabled", func(t *testing.T) {
		t.Setenv("PI_SKIP_VERSION_CHECK", "1")
		requests := 0
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			fmt.Fprint(w, `{"version":"1.2.4"}`)
		})
		if release := CheckForNewPiVersion(context.Background(), "1.2.3"); release != nil {
			t.Errorf("expected nil, got %+v", release)
		}
		if requests != 0 {
			t.Errorf("automatic check should be skipped, got %d requests", requests)
		}
		// allows direct api calls when automatic version checks are disabled
		version, err := GetLatestPiVersion(context.Background(), "1.2.3", nil)
		if err != nil || version != "1.2.4" {
			t.Errorf("direct call expected 1.2.4, got %q err=%v", version, err)
		}
	})

	t.Run("short-circuits under PI_OFFLINE", func(t *testing.T) {
		t.Setenv("PI_OFFLINE", "1")
		requests := 0
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ })
		release, err := GetLatestPiRelease(context.Background(), "1.2.3", nil)
		if release != nil || err != nil {
			t.Errorf("expected nil release/err, got %+v err=%v", release, err)
		}
		if requests != 0 {
			t.Errorf("PI_OFFLINE should skip the request, got %d", requests)
		}
	})
}

// pi: test/version-check.test.ts — "formats nested network error details"
// (AggregateError → Go errno chain, declared in the file header).
func TestFormatVersionCheckError(t *testing.T) {
	err := errors.Join(
		syscall.EIO,   // "connect timeout"
		syscall.EPIPE, // "network unreachable"
	)
	got := FormatVersionCheckError(err)
	// pi's root message stays clean ("fetch failed"); Go joins cause text
	// into the message, so assert the appended code suffix (declared).
	// ErrnoCode only names the codes pi branches on (EIO/EPIPE/ENOTCONN).
	want := " (EIO, EPIPE)"
	if !strings.HasSuffix(got, want) {
		t.Errorf("expected suffix %q, got %q", want, got)
	}
}
