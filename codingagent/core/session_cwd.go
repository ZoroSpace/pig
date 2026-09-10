// pi: src/core/session-cwd.ts — stored session cwd validation.
//
// SessionCwdSource collapses pi's inline interface; MissingSessionCwdError
// → error struct with the same message and an exposed Issue (pi subclasses
// Error).
package core

import (
	"fmt"
	"os"
)

// SessionCwdIssue describes a session whose stored cwd no longer exists (pi:
// SessionCwdIssue; sessionFile "" = undefined).
type SessionCwdIssue struct {
	SessionFile string
	SessionCwd  string
	FallbackCwd string
}

// SessionCwdSource is the session-manager surface session-cwd consumes (pi:
// inline SessionCwdSource; GetSessionFile "" = undefined).
type SessionCwdSource interface {
	GetCwd() string
	GetSessionFile() string
}

// GetMissingSessionCwdIssue returns the issue when the stored session cwd
// does not exist (pi: getMissingSessionCwdIssue).
func GetMissingSessionCwdIssue(sessionManager SessionCwdSource, fallbackCwd string) *SessionCwdIssue {
	sessionFile := sessionManager.GetSessionFile()
	if sessionFile == "" {
		return nil
	}

	sessionCwd := sessionManager.GetCwd()
	if sessionCwd == "" {
		return &SessionCwdIssue{
			SessionFile: sessionFile,
			SessionCwd:  sessionCwd,
			FallbackCwd: fallbackCwd,
		}
	}
	if _, err := os.Stat(sessionCwd); err == nil {
		return nil
	}

	return &SessionCwdIssue{
		SessionFile: sessionFile,
		SessionCwd:  sessionCwd,
		FallbackCwd: fallbackCwd,
	}
}

// FormatMissingSessionCwdError renders the issue as an error message (pi:
// formatMissingSessionCwdError).
func FormatMissingSessionCwdError(issue SessionCwdIssue) string {
	sessionFile := ""
	if issue.SessionFile != "" {
		sessionFile = fmt.Sprintf("\nSession file: %s", issue.SessionFile)
	}
	return fmt.Sprintf("Stored session working directory does not exist: %s%s\nCurrent working directory: %s",
		issue.SessionCwd, sessionFile, issue.FallbackCwd)
}

// FormatMissingSessionCwdPrompt renders the issue as a user prompt (pi:
// formatMissingSessionCwdPrompt).
func FormatMissingSessionCwdPrompt(issue SessionCwdIssue) string {
	return fmt.Sprintf("cwd from session file does not exist\n%s\n\ncontinue in current cwd\n%s",
		issue.SessionCwd, issue.FallbackCwd)
}

// MissingSessionCwdError is the controlled error raised when a session's
// stored cwd is gone (pi: MissingSessionCwdError).
type MissingSessionCwdError struct {
	issue SessionCwdIssue
}

// NewMissingSessionCwdError builds the error (pi: constructor).
func NewMissingSessionCwdError(issue SessionCwdIssue) *MissingSessionCwdError {
	return &MissingSessionCwdError{issue: issue}
}

func (e *MissingSessionCwdError) Error() string {
	return FormatMissingSessionCwdError(e.issue)
}

// Issue returns the underlying issue (pi: readonly issue field).
func (e *MissingSessionCwdError) Issue() SessionCwdIssue {
	return e.issue
}

// AssertSessionCwdExists errors when the stored session cwd is missing (pi:
// assertSessionCwdExists throws).
func AssertSessionCwdExists(sessionManager SessionCwdSource, fallbackCwd string) error {
	issue := GetMissingSessionCwdIssue(sessionManager, fallbackCwd)
	if issue != nil {
		return NewMissingSessionCwdError(*issue)
	}
	return nil
}
