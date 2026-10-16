// pi: test/config.test.ts — package asset path helpers. Only the
// findNodePackageDir describe is portable: the rest of the file exercises
// the deferred Install Method / self-update segment (npm/pnpm/yarn/bun
// machinery with no Go counterpart — see config.go's deferred-segment
// declaration).
package codingagent

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestVersionTracksPiBaseline pins Version to the pi baseline pig currently
// tracks. Regression lock for docs/fix/2026-09-22-stale-version-after-sync.md:
// the S7 batch (pi v0.86.1 sync) forgot to bump the constant, leaving the TUI
// logo and --version at v0.86.0. Update the expected value in the same commit
// as every future pi baseline sync — a red here means the sync batch missed
// the bump.
func TestVersionTracksPiBaseline(t *testing.T) {
	const wantPiBaseline = "1.2.0" // current tracked pi tag
	if Version != wantPiBaseline {
		t.Errorf("codingagent.Version = %q, want %q (pi baseline moved? bump the constant in the sync batch)", Version, wantPiBaseline)
	}
}

// TestCIWorkflowGoVersionTracksGoMod pins the CI workflow's Go version to
// go.mod's go directive. Regression lock for
// docs/fix/2026-09-24-ci-go-toolchain-mismatch.md: commit 44cabf6 bumped
// go.mod to 1.26.0 (x/text、x/sys、x/image deps) without bumping the workflow,
// so CI installed go1.25.14 while GOTOOLCHAIN auto-downloaded go1.26.0 —
// mixed-version builds failed with `compile: version "go1.26.0" does not
// match go tool version "go1.25.14"`. Bump both files in the same commit
// whenever dependencies force the go directive up.
func TestCIWorkflowGoVersionTracksGoMod(t *testing.T) {
	goMod, err := os.ReadFile(filepath.Join("..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "go.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	goModLine := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindSubmatch(goMod)
	if goModLine == nil {
		t.Fatalf("no `go <major.minor>` directive found in go.mod")
	}
	ciLine := regexp.MustCompile(`go-version:\s*"(\d+\.\d+)`).FindSubmatch(workflow)
	if ciLine == nil {
		t.Fatalf("no go-version major.minor entry found in .github/workflows/go.yml")
	}
	if string(goModLine[1]) != string(ciLine[1]) {
		t.Errorf("CI go-version %q does not match go.mod go directive %q — bump .github/workflows/go.yml in the same commit as the go.mod bump", ciLine[1], goModLine[1])
	}
}

func TestFindNodePackageDir(t *testing.T) {
	t.Run("skips binary metadata copied into dist", func(t *testing.T) {
		tempDir := t.TempDir()
		distDir := filepath.Join(tempDir, "dist")
		bundleDir := filepath.Join(distDir, "bundle")
		if err := os.MkdirAll(bundleDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(distDir, "package.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := FindNodePackageDir(bundleDir); got != tempDir {
			t.Fatalf("FindNodePackageDir = %q, want %q", got, tempDir)
		}
	})
}
