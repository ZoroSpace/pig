// jsdiff_word_test.go — DiffWords golden tests against the real npm diff
// package (v8.0.4) run under node (same harness as the jsdiff golden tests).
package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// rawGolden is the JSONL shape produced by the node harness.
type rawGolden struct {
	Old      string       `json:"old"`
	New      string       `json:"new"`
	Expected []goldenPart `json:"expected"`
}

type goldenPart struct {
	V       string `json:"value"`
	Added   bool   `json:"added"`
	Removed bool   `json:"removed"`
}

const wordGoldenJSONL = `
{"old": "foo bar baz", "new": "foo baz", "expected": [{"value": "foo ", "added": false, "removed": false}, {"value": "bar ", "added": false, "removed": true}, {"value": "baz", "added": false, "removed": false}]}

{"old": "foo bar baz", "new": "foo qux baz", "expected": [{"value": "foo ", "added": false, "removed": false}, {"value": "bar", "added": false, "removed": true}, {"value": "qux", "added": true, "removed": false}, {"value": " baz", "added": false, "removed": false}]}

{"old": "foo\nbar baz", "new": "foo baz", "expected": [{"value": "foo", "added": false, "removed": false}, {"value": "\nbar", "added": false, "removed": true}, {"value": " baz", "added": false, "removed": false}]}

{"old": "foo baz", "new": "foo\nbar baz", "expected": [{"value": "foo\n", "added": false, "removed": false}, {"value": "bar ", "added": true, "removed": false}, {"value": "baz", "added": false, "removed": false}]}

{"old": "foo   bar baz", "new": "foo  baz", "expected": [{"value": "foo  ", "added": false, "removed": false}, {"value": " bar ", "added": false, "removed": true}, {"value": "baz", "added": false, "removed": false}]}

{"old": "The quick brown fox jumps over the lazy dog", "new": "The quick red fox leaps over the sleepy dog", "expected": [{"value": "The quick ", "added": false, "removed": false}, {"value": "brown", "added": false, "removed": true}, {"value": "red", "added": true, "removed": false}, {"value": " fox ", "added": false, "removed": false}, {"value": "jumps", "added": false, "removed": true}, {"value": "leaps", "added": true, "removed": false}, {"value": " over the ", "added": false, "removed": false}, {"value": "lazy", "added": false, "removed": true}, {"value": "sleepy", "added": true, "removed": false}, {"value": " dog", "added": false, "removed": false}]}

{"old": "hello world", "new": "hello brave new world", "expected": [{"value": "hello ", "added": false, "removed": false}, {"value": "brave new ", "added": true, "removed": false}, {"value": "world", "added": false, "removed": false}]}

{"old": "remove these words please", "new": "keep these words", "expected": [{"value": "remove", "added": false, "removed": true}, {"value": "keep", "added": true, "removed": false}, {"value": " these words", "added": false, "removed": false}, {"value": " please", "added": false, "removed": true}]}

{"old": "x = 1; y = 2;", "new": "x = 10; z = 2;", "expected": [{"value": "x = ", "added": false, "removed": false}, {"value": "1", "added": false, "removed": true}, {"value": "10", "added": true, "removed": false}, {"value": "; ", "added": false, "removed": false}, {"value": "y", "added": false, "removed": true}, {"value": "z", "added": true, "removed": false}, {"value": " = 2;", "added": false, "removed": false}]}

{"old": "indentation matters", "new": "  indentation matters", "expected": [{"value": "  indentation matters", "added": false, "removed": false}]}

{"old": "  leading and trailing  ", "new": "  leading and trailing now  ", "expected": [{"value": "  leading and trailing ", "added": false, "removed": false}, {"value": "now  ", "added": true, "removed": false}]}

{"old": "a-b-c-d", "new": "a-b-c-d-e", "expected": [{"value": "a-b-c-d", "added": false, "removed": false}, {"value": "-e", "added": true, "removed": false}]}

{"old": "one", "new": "one two three", "expected": [{"value": "one ", "added": false, "removed": false}, {"value": "two three", "added": true, "removed": false}]}

{"old": "", "new": "something", "expected": [{"value": "something", "added": true, "removed": false}]}

{"old": "old value", "new": "", "expected": [{"value": "old value", "added": false, "removed": true}]}

{"old": "const x = computeEditsDiff(path, edits);", "new": "const y = computeEdits(ctx, edit);", "expected": [{"value": "const ", "added": false, "removed": false}, {"value": "x", "added": false, "removed": true}, {"value": "y", "added": true, "removed": false}, {"value": " = ", "added": false, "removed": false}, {"value": "computeEditsDiff", "added": false, "removed": true}, {"value": "computeEdits", "added": true, "removed": false}, {"value": "(", "added": false, "removed": false}, {"value": "path", "added": false, "removed": true}, {"value": "ctx", "added": true, "removed": false}, {"value": ", ", "added": false, "removed": false}, {"value": "edits", "added": false, "removed": true}, {"value": "edit", "added": true, "removed": false}, {"value": ");", "added": false, "removed": false}]}

`

func TestDiffWordsGolden(t *testing.T) {
	for i, line := range strings.Split(strings.TrimRight(wordGoldenJSONL, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var g rawGolden
		if err := json.Unmarshal([]byte(line), &g); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		got := DiffWords(g.Old, g.New)
		if len(got) != len(g.Expected) {
			t.Fatalf("case %d (%q -> %q): got %d changes, want %d: %+v", i, g.Old, g.New, len(got), len(g.Expected), got)
		}
		for j, want := range g.Expected {
			if got[j].Value != want.V || got[j].Added != want.Added || got[j].Removed != want.Removed {
				t.Errorf("case %d change %d: got {value:%q added:%v removed:%v}, want {value:%q added:%v removed:%v}",
					i, j, got[j].Value, got[j].Added, got[j].Removed, want.V, want.Added, want.Removed)
			}
		}
	}
}
