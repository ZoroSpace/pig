// clipboard_command_test.go — pi: test/clipboard-command.test.ts (new in
// v0.86.0). runClipboardCommand is exercised against the test binary itself
// re-executed as a helper process (pig adaptation: pi spawns `node -e`
// scripts; Go's equivalent is the TestHelperProcess pattern from the exec
// package tests).
package utils

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

// TestHelperProcess is the re-exec target (pig-only). The spawning test sets
// PIG_CLIPBOARD_HELPER_MODE and re-runs this binary with
// -test.run=TestHelperProcess; the helper writes its payload and exits
// before the test framework prints anything on stdout.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("PIG_CLIPBOARD_HELPER") != "1" {
		return
	}
	defer os.Exit(0)
	switch os.Getenv("PIG_CLIPBOARD_HELPER_MODE") {
	case "binary":
		_, _ = os.Stdout.Write([]byte{0, 255, 10})
	case "empty":
		// Exit 0 with no output.
	case "fail":
		os.Exit(1)
	case "echo-stdin":
		data, _ := io.ReadAll(os.Stdin)
		if string(data) != os.Getenv("PIG_CLIPBOARD_HELPER_EXPECT") {
			os.Exit(1)
		}
	case "hang":
		time.Sleep(30 * time.Second)
	case "flood":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{0xAB}, 1024))
	default:
		os.Exit(2)
	}
}

// runHelper re-executes the test binary as the clipboard helper process.
func runHelper(t *testing.T, mode string, options *clipboardCommandOptions) ([]byte, bool) {
	t.Helper()
	t.Cleanup(func() { os.Unsetenv("PIG_CLIPBOARD_HELPER") })
	t.Cleanup(func() { os.Unsetenv("PIG_CLIPBOARD_HELPER_MODE") })
	t.Cleanup(func() { os.Unsetenv("PIG_CLIPBOARD_HELPER_EXPECT") })
	if err := os.Setenv("PIG_CLIPBOARD_HELPER", "1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("PIG_CLIPBOARD_HELPER_MODE", mode); err != nil {
		t.Fatal(err)
	}
	return runClipboardCommand(os.Args[0], []string{"-test.run=TestHelperProcess$"}, options)
}

func TestClipboardCommandPreservesBinaryOutputAndDistinguishesEmptySuccessFromFailure(t *testing.T) {
	got, ok := runHelper(t, "binary", nil)
	if !ok {
		t.Fatal("expected success for exiting helper")
	}
	if !bytes.Equal(got, []byte{0, 255, 10}) {
		t.Fatalf("binary output mangled: %v", got)
	}

	got, ok = runHelper(t, "empty", nil)
	if !ok {
		t.Fatal("expected empty stdout to be a success (pi Buffer.alloc(0))")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty output, got %d bytes", len(got))
	}

	if _, ok = runHelper(t, "fail", nil); ok {
		t.Fatal("expected failure for exit code 1 (pi undefined)")
	}

	if _, ok := runClipboardCommand("pi-clipboard-command-does-not-exist", nil, nil); ok {
		t.Fatal("expected failure for a missing command (pi undefined)")
	}
}

func TestClipboardCommandSendsUnicodeInputToWriters(t *testing.T) {
	t.Cleanup(func() { os.Unsetenv("PIG_CLIPBOARD_HELPER_EXPECT") })
	if err := os.Setenv("PIG_CLIPBOARD_HELPER_EXPECT", "caf\u00e9 \u65e5\u672c\u8a9e"); err != nil {
		t.Fatal(err)
	}
	got, ok := runClipboardCommand(os.Args[0], []string{"-test.run=TestHelperProcess$"},
		&clipboardCommandOptions{Input: "caf\u00e9 \u65e5\u672c\u8a9e", HasInput: true})
	if !ok {
		t.Fatal("writer failed (helper rejected the stdin text)")
	}
	// A writer's stdout is ignored (pi stdio ["pipe", "ignore", "ignore"]).
	if len(got) != 0 {
		t.Fatalf("writer stdout should be discarded, got %d bytes", len(got))
	}
}

func TestClipboardCommandTimesOutWithoutBlocking(t *testing.T) {
	started := time.Now()
	if _, ok := runHelper(t, "hang", &clipboardCommandOptions{TimeoutMs: 200}); ok {
		t.Fatal("expected the hanging helper to time out")
	}
	elapsed := time.Since(started)
	// pi also asserts the event loop keeps ticking; Go's synchronous call
	// only needs to return near the deadline, not block for the helper's
	// full sleep.
	if elapsed < 150*time.Millisecond {
		t.Fatalf("returned too early (%v): timeout not applied", elapsed)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("call blocked for %v — deadline not enforced", elapsed)
	}
}

func TestClipboardCommandRejectsOutputAboveBufferLimit(t *testing.T) {
	if got, ok := runHelper(t, "flood", &clipboardCommandOptions{MaxBufferBytes: 16}); ok {
		t.Fatalf("expected overflow to fail the capture, got %d bytes", len(got))
	}
}
