// pi: utils/diagnostics.ts.
package utils

import (
	"fmt"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

// ThrownValue names a non-error thrown value (pi: the "ThrownValue" name
// extractDiagnosticError uses for non-Error throws).
const thrownValueName = "ThrownValue"

// FormatThrownValue renders a thrown value as a string (pi:
// formatThrownValue): an error's message (falling back to its name), a string
// as-is, anything else via its default string form.
func FormatThrownValue(value any) string {
	switch v := value.(type) {
	case error:
		message := v.Error()
		if message == "" {
			return fmt.Sprintf("%T", v)
		}
		return message
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

// DiagnosticErrorNamer is the optional error surface carrying a JS-style
// error name (pi: error.name). Go errors have no name member; adapters that
// want one in diagnostics implement this.
type DiagnosticErrorNamer interface {
	ErrorName() string
}

// DiagnosticErrorCoder is the optional error surface carrying a JS-style
// error code (pi: (error as {code?}).code).
type DiagnosticErrorCoder interface {
	ErrorCode() any
}

// DiagnosticErrorStacker is the optional error surface carrying a stack trace
// (pi: error.stack, captured when the error was thrown). Go errors do not
// capture stacks; only errors implementing this contribute one.
type DiagnosticErrorStacker interface {
	ErrorStack() string
}

// ExtractDiagnosticError flattens a thrown error into diagnostic info (pi:
// extractDiagnosticError). pi's Error-shaped name/message/stack/code have no
// uniform Go counterpart: name comes from DiagnosticErrorNamer (falling back
// to "ThrownValue" only for nil), message from Error(), stack and code from
// the optional interfaces above.
func ExtractDiagnosticError(err error) types.DiagnosticErrorInfo {
	if err == nil {
		return types.DiagnosticErrorInfo{Name: thrownValueName}
	}
	info := types.DiagnosticErrorInfo{Message: err.Error()}
	if namer, ok := err.(DiagnosticErrorNamer); ok {
		info.Name = namer.ErrorName()
	}
	if coder, ok := err.(DiagnosticErrorCoder); ok {
		info.Code = coder.ErrorCode()
	}
	if stacker, ok := err.(DiagnosticErrorStacker); ok {
		info.Stack = stacker.ErrorStack()
	}
	return info
}

// CreateAssistantMessageDiagnostic builds one error diagnostic stamped now
// (pi: createAssistantMessageDiagnostic).
func CreateAssistantMessageDiagnostic(diagType string, err any, details map[string]any) types.AssistantMessageDiagnostic {
	diagnostic := types.AssistantMessageDiagnostic{Type: diagType, Timestamp: time.Now().UnixMilli(), Details: details}
	if errValue, ok := err.(error); ok {
		info := ExtractDiagnosticError(errValue)
		diagnostic.Error = &info
	} else if err != nil {
		info := types.DiagnosticErrorInfo{Name: thrownValueName, Message: FormatThrownValue(err)}
		diagnostic.Error = &info
	}
	return diagnostic
}

// AppendAssistantMessageDiagnostic appends one diagnostic to the message's
// diagnostics list (pi: appendAssistantMessageDiagnostic). pi rebuilds the
// array; pig appends in place — the message is adapter-owned during streaming.
func AppendAssistantMessageDiagnostic(message *types.AssistantMessage, diagnostic types.AssistantMessageDiagnostic) {
	message.Diagnostics = append(message.Diagnostics, diagnostic)
}
