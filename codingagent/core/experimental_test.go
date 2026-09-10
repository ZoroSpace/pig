// pi: test/experimental.test.ts — the PI_EXPERIMENTAL gate.
package core

import (
	"os"
	"testing"
)

func TestAreExperimentalFeaturesEnabled(t *testing.T) {
	t.Run("returns false when PI_EXPERIMENTAL is unset", func(t *testing.T) {
		os.Unsetenv("PI_EXPERIMENTAL")
		if AreExperimentalFeaturesEnabled() {
			t.Fatal("expected false")
		}
	})

	t.Run("returns false when PI_EXPERIMENTAL is empty", func(t *testing.T) {
		t.Setenv("PI_EXPERIMENTAL", "")
		if AreExperimentalFeaturesEnabled() {
			t.Fatal("expected false")
		}
	})

	t.Run("returns true when PI_EXPERIMENTAL is set to 1", func(t *testing.T) {
		t.Setenv("PI_EXPERIMENTAL", "1")
		if !AreExperimentalFeaturesEnabled() {
			t.Fatal("expected true")
		}
	})

	t.Run("returns false when PI_EXPERIMENTAL is set to 0", func(t *testing.T) {
		t.Setenv("PI_EXPERIMENTAL", "0")
		if AreExperimentalFeaturesEnabled() {
			t.Fatal("expected false")
		}
	})

	t.Run("returns false when PI_EXPERIMENTAL is set to a non-1 value", func(t *testing.T) {
		t.Setenv("PI_EXPERIMENTAL", "true")
		if AreExperimentalFeaturesEnabled() {
			t.Fatal("expected false")
		}
	})
}
