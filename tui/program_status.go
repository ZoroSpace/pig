// tui/program_status (pi: packages/tui/src/program-status.ts, S17
// 503c60552) — Program Status Protocol (OSC 7501): a program tells the
// terminal whether it is idle, working, blocked on the user, done, or
// failed. Only the root record is supported.
//
// Spec: https://www.superlogical.com/rex/docs/build/program-status
package tui

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ProgramStatusState is the state pair of a status report. "clear" removes
// the status instead of reporting one (pi: ProgramStatus["state"]).
type ProgramStatusState string

const (
	ProgramStatusIdle    ProgramStatusState = "idle"
	ProgramStatusWorking ProgramStatusState = "working"
	ProgramStatusBlocked ProgramStatusState = "blocked"
	ProgramStatusDone    ProgramStatusState = "done"
	ProgramStatusError   ProgramStatusState = "error"
	ProgramStatusClear   ProgramStatusState = "clear"
)

// ProgramStatusBlockedKind is what a blocked program waits for (pi:
// ProgramStatus["kind"]; omitted for other states).
type ProgramStatusBlockedKind string

const (
	ProgramStatusBlockedPermission ProgramStatusBlockedKind = "permission"
	ProgramStatusBlockedQuestion   ProgramStatusBlockedKind = "question"
	ProgramStatusBlockedAuth       ProgramStatusBlockedKind = "auth"
)

// ProgramStatus is one OSC 7501 report (pi: ProgramStatus).
type ProgramStatus struct {
	// State reports idle/working/blocked/done/error/clear.
	State ProgramStatusState
	// App is the stable program name, `[A-Za-z0-9_.+-]{1,32}`. Other values
	// are omitted.
	App string
	// Kind is what a blocked program waits for; omitted for other states.
	Kind ProgramStatusBlockedKind
	// Message is one human-readable line. Control characters become spaces;
	// longer text is cut to the spec limit.
	Message string
}

// ProgramStatusQuery is the feature detection query. A supporting terminal
// replies with the same body (pi: PROGRAM_STATUS_QUERY).
const ProgramStatusQuery = "\x1b]7501;?\x1b\\"

// programStatusReplyRegex matches the reply to ProgramStatusQuery. Later
// spec revisions may add pairs after the `?` (pi: isProgramStatusReply).
var programStatusReplyRegex = regexp.MustCompile(`^\x1b\]7501;\?[^\x07\x1b]*(?:\x07|\x1b\\)$`)

// isProgramStatusReply reports whether the sequence is an OSC 7501 reply.
func isProgramStatusReply(sequence string) bool {
	return programStatusReplyRegex.MatchString(sequence)
}

var programStatusAppPattern = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,32}$`)

// programStatusControlCharacters is the C0/C1 control range; terminals
// discard reports whose text contains control characters, so they are
// replaced (pi: CONTROL_CHARACTERS).
var programStatusControlCharacters = regexp.MustCompile("[\u0000-\u001f\u007f-\u009f]+")

// programStatusMaxMessageBytes is the decoded msg limit. Its base64 encoding
// stays under the 2732-byte encoded limit (pi: MAX_MESSAGE_BYTES).
const programStatusMaxMessageBytes = 2048

// truncateProgramStatusMessage cuts text at a UTF-8 byte limit on a rune
// boundary (pi: truncateUtf8 — the for-of loop advances by code points).
func truncateProgramStatusMessage(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	bytes := 0
	end := 0
	for _, char := range text {
		size := utf8.RuneLen(char)
		if size < 0 || bytes+size > maxBytes {
			break
		}
		bytes += size
		end += len(string(char))
	}
	return text[:end]
}

// formatProgramStatus encodes a status report (pi: formatProgramStatus).
func formatProgramStatus(status ProgramStatus) string {
	pairs := []string{"state=" + string(status.State)}
	if status.App != "" && programStatusAppPattern.MatchString(status.App) {
		pairs = append(pairs, "app="+status.App)
	}
	if status.State == ProgramStatusBlocked && status.Kind != "" {
		pairs = append(pairs, "kind="+string(status.Kind))
	}
	message := strings.TrimSpace(programStatusControlCharacters.ReplaceAllString(status.Message, " "))
	message = truncateProgramStatusMessage(message, programStatusMaxMessageBytes)
	if message != "" {
		pairs = append(pairs, "msg="+base64.StdEncoding.EncodeToString([]byte(message)))
	}
	return "\x1b]7501;" + strings.Join(pairs, ":") + "\x1b\\"
}
