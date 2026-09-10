package interactive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
)

// TestMain points PI_PACKAGE_DIR at the theme package's testdata so the
// built-in dark/light themes load (pi tests run against the repo source
// layout). Must run before the first theme load (builtin cache is
// sync.Once-guarded).
func TestMain(m *testing.M) {
	dir, err := filepath.Abs(filepath.Join("theme", "testdata"))
	if err != nil {
		panic(err)
	}
	if os.Setenv("PI_PACKAGE_DIR", dir) != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// initTestTheme re-initializes the global theme (pi tests: initTheme("dark")
// inside each test).
func initTestTheme(t *testing.T) {
	t.Helper()
	theme.InitTheme("dark", false)
}
