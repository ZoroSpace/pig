// session_manager_bug_report_compat_test.go — pig-only read compatibility:
// historical sessions (pi or pig before 2026-09-30) may contain
// {"type":"custom","customType":"pi.bug-report"} entries written by the
// removed /bug command. The command is gone (docs/fix/2026-09-30-remove-bug-command.md)
// but the generic custom-entry decode must keep reading those sessions —
// this test pins that so nobody mistakes pi.bug-report for an unknown type
// to clean up.
package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionManagerReadsLegacyBugReportEntries(t *testing.T) {
	tempDir := newTempDir(t)
	sourcePath := filepath.Join(tempDir, "legacy.jsonl")
	content := mustJSONLines(
		map[string]any{
			"type": "session", "version": 3, "id": "legacy-bug-report-session",
			"timestamp": sessionTimestamp(), "cwd": tempDir,
		},
		map[string]any{
			"type": "custom", "id": "entry-bug-1", "parentId": nil,
			"timestamp":  sessionTimestamp(),
			"customType": "pi.bug-report",
			"data": map[string]any{
				"delivery": "zip",
				"path":     "/tmp/pi-bug-report-20260901.zip",
				"id":       "report-123",
			},
		},
		map[string]any{
			"type": "custom", "id": "entry-bug-2", "parentId": "entry-bug-1",
			"timestamp":  sessionTimestamp(),
			"customType": "pi.bug-report",
			"data":       map[string]any{"delivery": "upload"},
		},
	)
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := OpenSession(sourcePath, tempDir, nil)
	if err != nil {
		t.Fatalf("session with pi.bug-report entries failed to open: %v", err)
	}
	entries := session.GetEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	found := 0
	for _, entry := range entries {
		custom, ok := entry.(*CustomEntry)
		if !ok {
			t.Fatalf("entry %#v is not a CustomEntry", entry)
		}
		if custom.CustomType != "pi.bug-report" {
			t.Fatalf("customType = %q", custom.CustomType)
		}
		data, ok := custom.Data.(map[string]any)
		if !ok {
			t.Fatalf("data = %#v, want a decoded object", custom.Data)
		}
		if _, ok := data["delivery"]; !ok {
			t.Fatalf("data missing delivery: %#v", data)
		}
		found++
	}
	if found != 2 {
		t.Fatalf("found %d pi.bug-report entries, want 2", found)
	}
}
