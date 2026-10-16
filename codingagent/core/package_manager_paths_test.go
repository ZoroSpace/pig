// pi: test/package-manager.test.ts — the resource-pattern `~` expansion slice
// (#10765). pig exercises the helpers directly: settings entries like
// `!~/extra/skills/glob-*` and `-~/extra/skills/exact-excluded` reach
// matchesAnyPattern / matchesAnyExactPattern with the prefix already stripped.
package core

import (
	"path/filepath"
	"testing"
)

func TestMatchesAnyPatternExpandsTildeInPattern(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	baseDir := filepath.Join(home, "extra", "skills")
	file := filepath.Join(baseDir, "glob-excluded", "SKILL.md")

	if !matchesAnyPattern(file, []string{"~/extra/skills/glob-*"}, baseDir) {
		t.Fatal("expected tilde glob pattern to match")
	}
}

func TestMatchesAnyExactPatternExpandsTildeInPattern(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	baseDir := filepath.Join(home, "extra", "skills")
	file := filepath.Join(baseDir, "exact-excluded", "SKILL.md")

	if !matchesAnyExactPattern(file, []string{"~/extra/skills/exact-excluded"}, baseDir) {
		t.Fatal("expected tilde exact pattern to match")
	}
}

// TestMatchesAnyPatternMatchesGlobAgainstFilePath locks the argument order of
// the Minimatch calls (glob, value): a swapped call would match nothing.
func TestMatchesAnyPatternMatchesGlobAgainstFilePath(t *testing.T) {
	baseDir := t.TempDir()
	file := filepath.Join(baseDir, "skills", "glob-excluded", "SKILL.md")

	if !matchesAnyPattern(file, []string{"skills/glob-*"}, baseDir) {
		t.Fatal("expected relative glob pattern to match")
	}
	if matchesAnyPattern(file, []string{"skills/keep-*"}, baseDir) {
		t.Fatal("expected a non-matching pattern not to match")
	}
}
