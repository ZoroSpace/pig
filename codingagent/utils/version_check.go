// version_check.go (pi: src/utils/version-check.ts) — pi.dev latest-version
// lookup and semver comparison for update notifications.
//
// Segment deviations:
//   - semver (npm) valid/compare is reimplemented locally: strict
//     major.minor.patch with an optional -prerelease; build metadata is
//     ignored. Only what the version check consumes is supported.
//   - Node AggregateError cause chains have no Go equivalent; errno codes
//     are collected from the error unwrap chain (utils.ErrnoCode).
package utils

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/samber/lo"
)

// DefaultVersionCheckTimeoutMs (pi: DEFAULT_VERSION_CHECK_TIMEOUT_MS).
const DefaultVersionCheckTimeoutMs = 10000

// latestVersionURL is a var for test seams (pig: pi stubs global fetch;
// tests point this at an httptest server).
var latestVersionURL = "https://pi.dev/api/latest-version"

// LatestPiRelease is the version-check API payload (pi: LatestPiRelease).
type LatestPiRelease struct {
	Version     string
	PackageName string // "" = pi's undefined
	Note        string // "" = pi's undefined
}

// semverRe matches strict semver (pi uses semver.valid).
var semverRe = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*)?$`)

type semverValue struct {
	major, minor, patch int
	prerelease          []string
	hasPrerelease       bool
}

func parseSemver(v string) (semverValue, bool) {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return semverValue{}, false
	}
	out := semverValue{}
	out.major, _ = strconv.Atoi(m[1])
	out.minor, _ = strconv.Atoi(m[2])
	out.patch, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		out.hasPrerelease = true
		out.prerelease = strings.Split(m[4], ".")
	}
	return out, true
}

func compareSemverIdentifier(a, b string) int {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return signInt(ai - bi)
	case aerr == nil:
		return -1 // numeric < alphanumeric
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func compareSemver(a, b semverValue) int {
	if v := signInt(a.major - b.major); v != 0 {
		return v
	}
	if v := signInt(a.minor - b.minor); v != 0 {
		return v
	}
	if v := signInt(a.patch - b.patch); v != 0 {
		return v
	}
	// A pre-release version has lower precedence than the associated normal
	// version; larger prerelease sets have higher precedence.
	switch {
	case a.hasPrerelease && !b.hasPrerelease:
		return -1
	case !a.hasPrerelease && b.hasPrerelease:
		return 1
	case a.hasPrerelease && b.hasPrerelease:
		n := len(a.prerelease)
		if len(b.prerelease) < n {
			n = len(b.prerelease)
		}
		for i := 0; i < n; i++ {
			if v := compareSemverIdentifier(a.prerelease[i], b.prerelease[i]); v != 0 {
				return v
			}
		}
		return signInt(len(a.prerelease) - len(b.prerelease))
	}
	return 0
}

// FormatVersionCheckError includes errno details hidden behind Go's generic
// transport errors (pi: formatVersionCheckError).
func FormatVersionCheckError(err error) string {
	if err == nil {
		return ""
	}
	rootMessage := err.Error()

	// Collect errno codes along the unwrap chain (pi: AggregateError.errors'
	// code members, deduplicated). errors.Join produces multi-unwrap errors;
	// walk both Unwrap shapes.
	var codes []string
	seen := map[string]bool{}
	stack := []error{err}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e == nil {
			continue
		}
		if code := ErrnoCode(e); code != "" && !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
		stack = append(stack, errors.Unwrap(e))
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			stack = append(stack, multi.Unwrap()...)
		}
	}
	if len(codes) > 0 {
		return rootMessage + " (" + strings.Join(codes, ", ") + ")"
	}
	return rootMessage
}

// ComparePackageVersions compares two semver strings; undefined = one of
// them is not valid semver (pi: comparePackageVersions → number | undefined).
func ComparePackageVersions(leftVersion, rightVersion string) (int, bool) {
	left, ok := parseSemver(strings.TrimSpace(leftVersion))
	if !ok {
		return 0, false
	}
	right, ok := parseSemver(strings.TrimSpace(rightVersion))
	if !ok {
		return 0, false
	}
	return compareSemver(left, right), true
}

// IsNewerPackageVersion reports whether candidate is newer than current
// (pi: isNewerPackageVersion; non-semver falls back to string inequality).
func IsNewerPackageVersion(candidateVersion, currentVersion string) bool {
	if comparison, ok := ComparePackageVersions(candidateVersion, currentVersion); ok {
		return comparison > 0
	}
	return strings.TrimSpace(candidateVersion) != strings.TrimSpace(currentVersion)
}

// GetLatestPiRelease queries the pi.dev version API (pi: getLatestPiRelease).
// PI_OFFLINE short-circuits to undefined.
func GetLatestPiRelease(ctx context.Context, currentVersion string, options *FetchRetryOptions) (*LatestPiRelease, error) {
	if os.Getenv("PI_OFFLINE") != "" {
		return nil, nil
	}
	if options == nil {
		options = &FetchRetryOptions{}
	}
	// pi: maxRetries = options.retry ? 2 : 0 — retries only when the caller
	// explicitly passes MaxRetries (pig: no implicit default retries).
	if options.MaxRetries == nil {
		noRetries := 0
		options.MaxRetries = &noRetries
	}
	timeoutMs := options.TimeoutMs
	if timeoutMs == nil {
		timeoutMs = lo.ToPtr(int64(DefaultVersionCheckTimeoutMs))
	}
	opts := *options
	opts.TimeoutMs = timeoutMs

	resp, err := FetchWithRetry(ctx, "GET", latestVersionURL, map[string]string{
		"User-Agent": GetPiUserAgent(currentVersion),
		"accept":     "application/json",
	}, &opts)
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil
	}
	var data struct {
		PackageName any `json:"packageName"`
		Version     any `json:"version"`
		Note        any `json:"note"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, nil
	}
	version, _ := data.Version.(string)
	if strings.TrimSpace(version) == "" {
		return nil, nil
	}
	release := &LatestPiRelease{Version: strings.TrimSpace(version)}
	if name, ok := data.PackageName.(string); ok && strings.TrimSpace(name) != "" {
		release.PackageName = strings.TrimSpace(name)
	}
	if note, ok := data.Note.(string); ok && strings.TrimSpace(note) != "" {
		release.Note = strings.TrimSpace(note)
	}
	return release, nil
}

// GetLatestPiVersion returns just the latest version string (pi:
// getLatestPiVersion); "" = undefined.
func GetLatestPiVersion(ctx context.Context, currentVersion string, options *FetchRetryOptions) (string, error) {
	release, err := GetLatestPiRelease(ctx, currentVersion, options)
	if err != nil || release == nil {
		return "", err
	}
	return release.Version, nil
}

// CheckForNewPiVersion returns the release when a newer version exists (pi:
// checkForNewPiVersion). PI_SKIP_VERSION_CHECK short-circuits; all errors
// are swallowed (best-effort check).
func CheckForNewPiVersion(ctx context.Context, currentVersion string) *LatestPiRelease {
	if os.Getenv("PI_SKIP_VERSION_CHECK") != "" {
		return nil
	}
	release, err := GetLatestPiRelease(ctx, currentVersion, nil)
	if err != nil || release == nil {
		return nil
	}
	if IsNewerPackageVersion(release.Version, currentVersion) {
		return release
	}
	return nil
}
