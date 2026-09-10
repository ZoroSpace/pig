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
