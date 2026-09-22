// pi: test/config.test.ts — package asset path helpers. Only the
// findNodePackageDir describe is portable: the rest of the file exercises
// the deferred Install Method / self-update segment (npm/pnpm/yarn/bun
// machinery with no Go counterpart — see config.go's deferred-segment
// declaration).
package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVersionTracksPiBaseline pins Version to the pi baseline pig currently
// tracks. Regression lock for docs/fix/2026-09-22-stale-version-after-sync.md:
// the S7 batch (pi v0.86.1 sync) forgot to bump the constant, leaving the TUI
// logo and --version at v0.86.0. Update the expected value in the same commit
// as every future pi baseline sync — a red here means the sync batch missed
// the bump.
func TestVersionTracksPiBaseline(t *testing.T) {
	const wantPiBaseline = "0.86.1" // current tracked pi tag
	if Version != wantPiBaseline {
		t.Errorf("codingagent.Version = %q, want %q (pi baseline moved? bump the constant in the sync batch)", Version, wantPiBaseline)
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
