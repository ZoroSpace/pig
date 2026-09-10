// pi: src/harness/session/jsonl/errors.ts — jsonl decode errors and fs-error
// mapping.
//
// Deviations:
//   - JsonlDecodeError extends Error with a kind; Go: struct implementing
//     error with Kind + Err (cause) fields.
//   - pi's fileResult unwraps Result<T, FileError> and throws SessionError;
//     Go fileResult maps a *harness.FileError return into a *session.
//     SessionError (not_found → not_found, everything else → storage) and
//     returns it.
package jsonl

import (
	"errors"
	"fmt"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
)

// JsonlDecodeError reports a session-line decode failure (pi:
// JsonlDecodeError). Kind is "syntax" (not valid JSON) or "schema" (valid
// JSON, wrong shape).
type JsonlDecodeError struct {
	Kind  string // "syntax" | "schema"
	Msg   string
	Cause error
}

func (e *JsonlDecodeError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s (cause: %v)", e.Msg, e.Cause)
	}
	return e.Msg
}

func (e *JsonlDecodeError) Unwrap() error { return e.Cause }

// NewJsonlDecodeError builds a decode error.
func NewJsonlDecodeError(kind string, message string, cause error) *JsonlDecodeError {
	return &JsonlDecodeError{Kind: kind, Msg: message, Cause: cause}
}

// fileResult converts a harness.FileError from a filesystem call into a
// session error (pi: fileResult).
func fileResult(err error, message string) error {
	if err == nil {
		return nil
	}
	var fileErr *harness.FileError
	code := session.CodeStorage
	if errors.As(err, &fileErr) && fileErr.Code == harness.FileCodeNotFound {
		code = session.CodeNotFound
	}
	return session.NewSessionError(code, fmt.Sprintf("%s: %s", message, err.Error()), err)
}

// invalidFile builds the load-time invalid-entry error (pi: invalidFile).
func invalidFile(path string, line int, cause error) *session.SessionError {
	return session.NewSessionError(
		session.CodeInvalidEntry,
		fmt.Sprintf("Invalid JSONL v4 session %s: line %d %s", path, line, cause.Error()),
		cause,
	)
}
