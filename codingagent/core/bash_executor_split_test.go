// Escape sequences split across bash output chunks (pi:
// test/suite/agent-session-bash-persistence.test.ts "escape sequences split
// across output chunks", S16-3 port of pi 27c7b6ff4 / issue #10504), plus a
// pig regression for the decoder's buffering of multi-byte runes cut across
// chunks (the pre-S16-3 decoder replaced truncated prefixes immediately
// instead of buffering them like TextDecoder(stream:true)).
package core

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
)

// runBashChunks executes a user bash command whose fake shell emits the given
// byte chunks; beforeExit sees what had been streamed by then (pi: runChunks).
func runBashChunks(t *testing.T, chunks [][]byte, beforeExit func(streamed string)) (output string, streamed string, recorded string) {
	t.Helper()
	session, sessionManager, _ := createAgentTestSession(t, nil)
	var deltas []string
	operations := tools.BashOperations{
		Exec: func(ctx context.Context, command string, cwd string, options *tools.BashExecOptions) (*int, error) {
			for _, chunk := range chunks {
				options.OnData(chunk)
			}
			if beforeExit != nil {
				beforeExit(strings.Join(deltas, ""))
			}
			code := 0
			return &code, nil
		},
	}
	result, err := session.ExecuteBash(context.Background(), "custom", func(delta string) { deltas = append(deltas, delta) },
		&BashExecuteOptions{Operations: operations, HasOperations: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(deltas, ""); got != "" {
		streamed = got
	}
	entries := sessionManager.GetEntries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entry, ok := entries[i].(*SessionMessageEntry); ok {
			if bash, ok := entry.Message.(harness.BashExecutionMessage); ok {
				return result.Output, streamed, bash.Output
			}
		}
	}
	t.Fatal("no bashExecution entry recorded")
	return "", "", ""
}

// TestExecuteBashStripsSequencesSplitAcrossChunks is the #10504 regression:
// a strip must not depend on escape sequences arriving in single chunks.
func TestExecuteBashStripsSequencesSplitAcrossChunks(t *testing.T) {
	t.Run("strips a color reset split inside its parameters", func(t *testing.T) {
		output, streamed, recorded := runBashChunks(t, [][]byte{[]byte("\x1b[31mERROR: file.py:1\x1b[0"), []byte("m\n")}, nil)
		if output != "ERROR: file.py:1\n" || streamed != "ERROR: file.py:1\n" || recorded != "ERROR: file.py:1\n" {
			t.Fatalf("output=%q streamed=%q recorded=%q", output, streamed, recorded)
		}
	})

	t.Run("strips a color code split right after ESC", func(t *testing.T) {
		output, streamed, _ := runBashChunks(t, [][]byte{[]byte("before\x1b"), []byte("[32mafter\n")}, nil)
		if output != "beforeafter\n" || streamed != "beforeafter\n" {
			t.Fatalf("output=%q streamed=%q", output, streamed)
		}
	})

	t.Run("strips an OSC sequence split before its terminator", func(t *testing.T) {
		output, _, _ := runBashChunks(t, [][]byte{[]byte("a\x1b]0;window "), []byte("title\x1b"), []byte("\\b\n")}, nil)
		if output != "ab\n" {
			t.Fatalf("output = %q", output)
		}
	})

	t.Run("flushes an incomplete multi-byte character at the end of output", func(t *testing.T) {
		output, streamed, _ := runBashChunks(t, [][]byte{[]byte("ok"), {0xC3}}, nil)
		if output != "ok\uFFFD" || streamed != "ok\uFFFD" {
			t.Fatalf("output=%q streamed=%q", output, streamed)
		}
	})

	t.Run("does not hold back output behind a long unterminated sequence", func(t *testing.T) {
		long := strings.Repeat("x", 300)
		var streamedBeforeExit string
		_, _, _ = runBashChunks(t, [][]byte{[]byte("\x1b]" + long)}, func(streamed string) {
			streamedBeforeExit = streamed
		})
		if streamedBeforeExit != "]"+long {
			t.Fatalf("streamed before exit = %q", streamedBeforeExit)
		}
	})

	// Pig regression for the chunk decoder itself: a multi-byte rune split
	// across chunks must buffer and reassemble, not degrade per byte.
	t.Run("reassembles a multi-byte rune split across chunks", func(t *testing.T) {
		output, streamed, _ := runBashChunks(t, [][]byte{[]byte("ok"), {0xE4, 0xBD}, {0xA0}, []byte("\n")}, nil)
		if output != "ok你\n" || streamed != "ok你\n" {
			t.Fatalf("output=%q streamed=%q", output, streamed)
		}
	})
}
