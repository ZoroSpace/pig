// edit_test.go — regression tests for the edit tool wire shape.
package tools

import (
	"encoding/json"
	"testing"
)

// pig-only regression (2026-10-10,
// docs/fix/2026-10-10-edit-details-map-form.md): EditToolDetails serializes
// with camelCase keys for session-JSONL parity with pi (edit.ts
// EditToolDetails { diff, patch, firstChangedLine? }). The struct was
// missing json tags, so pig wrote capitalized "Diff" keys — a wire-format
// violation that also blinded the restore renderer's map-form lookup.
// Stash-red check: reverting the json tags in edit.go fails the marshal
// assertion.
func TestEditToolDetailsWireKeys(t *testing.T) {
	line := 5
	encoded, err := json.Marshal(EditToolDetails{Diff: "-a\n+b", Patch: "patch", FirstChangedLine: &line})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"diff":"-a\n+b","patch":"patch","firstChangedLine":5}`
	if string(encoded) != want {
		t.Fatalf("wire keys drifted: got %s want %s", encoded, want)
	}

	// nil FirstChangedLine is omitted (pi: firstChangedLine?: number).
	encoded, err = json.Marshal(EditToolDetails{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"diff":"","patch":""}`; string(encoded) != want {
		t.Fatalf("omitempty drifted: got %s want %s", encoded, want)
	}

	// pi-form JSON decodes into the struct.
	var decoded EditToolDetails
	if err := json.Unmarshal([]byte(`{"diff":"d","patch":"p","firstChangedLine":7}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Diff != "d" || decoded.Patch != "p" || decoded.FirstChangedLine == nil || *decoded.FirstChangedLine != 7 {
		t.Fatalf("pi-form decode mismatch: %+v", decoded)
	}
}
