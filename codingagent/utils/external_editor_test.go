package utils

import (
	"strings"
	"testing"
)

func TestEditInExternalEditorReportsLaunchFailure(t *testing.T) {
	result, err := EditInExternalEditor(t.Context(), "pig-command-that-does-not-exist", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Message, "Failed to launch external editor") {
		t.Fatalf("message = %q", result.Message)
	}
}
