package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShellPathExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "shell")
	if err := os.WriteFile(existing, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !shellPathExists(existing) {
		t.Fatal("expected existing file to be found")
	}
	if shellPathExists(filepath.Join(dir, "missing")) {
		t.Fatal("expected missing file not to be found")
	}
}
