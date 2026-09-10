package theme

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points PI_PACKAGE_DIR at testdata/theme so the built-in
// dark/light themes load (pi tests run against the repo source layout).
// The builtin cache is sync.Once-guarded, so the env must be set before
// the first theme load.
func TestMain(m *testing.M) {
	dir, err := filepath.Abs("testdata")
	if err != nil {
		panic(err)
	}
	os.Setenv("PI_PACKAGE_DIR", dir)
	os.Exit(m.Run())
}
