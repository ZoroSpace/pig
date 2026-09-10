// pi: utils/error-body.ts.
//
// Shared normalization for provider HTTP error objects. Endpoints behind a
// proxy / gateway may return a non-2xx response whose body the provider SDK
// cannot fold into error.message; pig's adapters return *ProviderHTTPError for
// those, and NormalizeProviderError extracts status/body from it.
package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// MaxProviderErrorBodyChars caps the raw HTTP body kept in NormalizedProviderError.
const MaxProviderErrorBodyChars = 4000

// ProviderHTTPError is the Go counterpart of an SDK error with a known HTTP
// status and headers (pi: the SdkErrorShape probe targets). Adapters return it
// for non-2xx responses; the retry helper and error normalizer both read it.
type ProviderHTTPError struct {
	Status  int
	Headers http.Header
	// Body is the raw HTTP response body, already read and size-capped by the
	// caller. Empty when the body was not captured.
	Body string
	// Message is the error message; when Body is empty the adapter usually
	// sets it to the reason line.
	Message string
}

func (e *ProviderHTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Status > 0 {
		return http.StatusText(e.Status)
	}
	return "provider request failed"
}

// NormalizedProviderError is the flattened display surface of one provider
// failure (pi: NormalizedProviderError).
type NormalizedProviderError struct {
	// Status is the HTTP status code, when one could be extracted.
	Status int
	// Body is the raw HTTP body reason, already trimmed and truncated to the
	// cap.
	Body string
	// Message is the error message.
	Message string
	// MessageCarriesBody is true when Message already contains the body (no
	// separate body to add).
	MessageCarriesBody bool
}

// NormalizeProviderError probes a thrown error for an HTTP status and raw body
// (pi: normalizeProviderError). *ProviderHTTPError carries them directly; any
// other error yields just its message.
func NormalizeProviderError(err error) NormalizedProviderError {
	var httpErr *ProviderHTTPError
	if !asProviderHTTPError(err, &httpErr) || httpErr == nil {
		return NormalizedProviderError{Message: err.Error(), MessageCarriesBody: true}
	}
	status := httpErr.Status
	body := httpErr.Body
	message := httpErr.Message
	if message == "" {
		message = err.Error()
	}
	body = trimAndCapBody(body)
	messageCarriesBody := body == "" || strings.Contains(message, body)
	return NormalizedProviderError{
		Status:             status,
		Body:               body,
		Message:            message,
		MessageCarriesBody: messageCarriesBody,
	}
}

// asProviderHTTPError unwraps a wrapped error chain into a *ProviderHTTPError.
// pi's SDK-shape probe has no wrap semantics; Go errors travel via both direct
// return and wrapping, so wraps still resolve here.
func asProviderHTTPError(err error, target **ProviderHTTPError) bool {
	for err != nil {
		if e, ok := err.(*ProviderHTTPError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func trimAndCapBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	return TruncateErrorText(trimmed, MaxProviderErrorBodyChars)
}

// FormatProviderError composes a display string from a normalized error (pi:
// formatProviderError). When the message already carries the body or no
// body/status was extracted, the message is returned unchanged. Otherwise the
// status and body are surfaced, with an optional provider prefix:
//
//   - no prefix: "<status>: <body>"
//   - prefix:    "<prefix> (<status>): <body>"
func FormatProviderError(norm NormalizedProviderError, prefix string) string {
	if norm.MessageCarriesBody || norm.Status == 0 || norm.Body == "" {
		if prefix != "" && norm.Status != 0 {
			return prefix + " (" + strconv.Itoa(norm.Status) + "): " + norm.Message
		}
		return norm.Message
	}
	if prefix != "" {
		return prefix + " (" + strconv.Itoa(norm.Status) + "): " + norm.Body
	}
	return strconv.Itoa(norm.Status) + ": " + norm.Body
}

// TruncateErrorText caps text at maxChars, appending a truncation note (pi:
// truncateErrorText).
func TruncateErrorText(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "... [truncated " + strconv.Itoa(len(text)-maxChars) + " chars]"
}

// SafeJSONStringify serializes a value, falling back to fmt-style rendering
// when marshaling fails (pi: safeJsonStringify, whose catch path uses
// String(value)).
func SafeJSONStringify(value any) string {
	if b, err := json.Marshal(value); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", value)
}
