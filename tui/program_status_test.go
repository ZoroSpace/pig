// tui/program_status_test.go — ported from pi: packages/tui/test/program-status.test.ts
// (S17 503c60552, #10607).
package tui

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

// decodeProgramStatusMessage extracts the msg pair from a report (pi:
// decodeMessage helper).
func decodeProgramStatusMessage(t *testing.T, sequence string) (string, bool) {
	t.Helper()
	re := regexp.MustCompile(`:msg=([A-Za-z0-9+/=]*)`)
	m := re.FindStringSubmatch(sequence)
	if m == nil {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("decode msg: %v", err)
	}
	return string(raw), true
}

func TestFormatProgramStatus(t *testing.T) {
	t.Run("encodes state, app, kind, and a base64 message", func(t *testing.T) {
		want := "\x1b]7501;state=blocked:app=pi:kind=permission:msg=" +
			base64.StdEncoding.EncodeToString([]byte("Allow bash?")) + "\x1b\\"
		if got := formatProgramStatus(ProgramStatus{State: ProgramStatusBlocked, App: "pi", Kind: ProgramStatusBlockedPermission, Message: "Allow bash?"}); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if got := formatProgramStatus(ProgramStatus{State: ProgramStatusClear}); got != "\x1b]7501;state=clear\x1b\\" {
			t.Fatalf("clear: got %q", got)
		}
	})

	t.Run("omits kind outside blocked, invalid app names, and empty messages", func(t *testing.T) {
		if got := formatProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "my app", Kind: ProgramStatusBlockedAuth, Message: " \n "}); got != "\x1b]7501;state=working\x1b\\" {
			t.Fatalf("working: got %q", got)
		}
		if got := formatProgramStatus(ProgramStatus{State: ProgramStatusIdle, App: strings.Repeat("a", 32)}); !strings.Contains(got, ":app="+strings.Repeat("a", 32)+"\x1b\\") {
			t.Fatalf("32-char app must be kept: %q", got)
		}
		if got := formatProgramStatus(ProgramStatus{State: ProgramStatusIdle, App: strings.Repeat("a", 33)}); strings.Contains(got, "app=") {
			t.Fatalf("33-char app must be omitted: %q", got)
		}
	})

	t.Run("replaces control characters, which make terminals discard the report", func(t *testing.T) {
		sequence := formatProgramStatus(ProgramStatus{State: ProgramStatusError, Message: "first\nsecond\x1b[31m\u009bthird\t"})
		message, ok := decodeProgramStatusMessage(t, sequence)
		if !ok {
			t.Fatalf("no msg pair in %q", sequence)
		}
		if message != "first second [31m third" {
			t.Fatalf("message = %q", message)
		}
	})

	t.Run("cuts long messages at a UTF-8 boundary within the spec limits", func(t *testing.T) {
		sequence := formatProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "pi", Message: strings.Repeat("é", 2000)})
		message, ok := decodeProgramStatusMessage(t, sequence)
		if !ok {
			t.Fatalf("no msg pair in %q", sequence)
		}
		if message != strings.Repeat("é", 1024) {
			t.Fatalf("message = %d runes, want 1024", len([]rune(message)))
		}
		if len(message) > 2048 {
			t.Fatalf("message = %d bytes, want <= 2048", len(message))
		}
	})
}

func TestIsProgramStatusReply(t *testing.T) {
	cases := []struct {
		sequence string
		want     bool
	}{
		{"\x1b]7501;?\x1b\\", true},
		{"\x1b]7501;?\x07", true},
		{"\x1b]7501;?version=2\x1b\\", true},
		{"\x1b]7501;state=idle\x1b\\", false},
		{"\x1b]11;rgb:0000/0000/0000\x07", false},
	}
	for _, tc := range cases {
		if got := isProgramStatusReply(tc.sequence); got != tc.want {
			t.Fatalf("isProgramStatusReply(%q) = %v, want %v", tc.sequence, got, tc.want)
		}
	}
}
