package telemetry

// This file ports the runtime contract of pi's packages/telemetry
// (index.ts: AttributeValue through TelemetrySpan, plus noop.ts). pi's
// attribute-definition and type-inference scaffolding (TelemetryAttributeType
// onward) is compile-time typing with no runtime counterpart and is not
// ported; memory.ts (InMemoryTelemetryContext) arrives with test
// infrastructure.

// AttributeValue is a span attribute value: string, number, boolean, or a
// slice of one of those.
type AttributeValue any

// SpanAttributes are span attributes keyed by name; a nil value omits the key.
type SpanAttributes map[string]AttributeValue

// SpanOptions start one span.
type SpanOptions struct {
	Name       string
	Attributes SpanAttributes
}

// SpanStatusKind discriminates how a span ended.
type SpanStatusKind string

const (
	SpanStatusKindOK    SpanStatusKind = "ok"
	SpanStatusKindError SpanStatusKind = "error"
)

type SpanStatus struct {
	Kind  SpanStatusKind   `json:"status"`
	Error *SpanStatusError `json:"error,omitempty"`
}

type SpanStatusError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// TelemetryContext is an explicit parent for spans produced by one logical
// request. pi's generic StartSpan result propagates through the callback's
// return value; in Go the callback reports an error and other values cross
// via the closure.
type TelemetryContext interface {
	StartSpan(options SpanOptions, callback func(span TelemetrySpan) error) error
}

// TelemetrySpan is a running span; it also starts child spans.
type TelemetrySpan interface {
	TelemetryContext
	AddEvent(name string, attributes SpanAttributes)
	SetAttributes(attributes SpanAttributes)
	SetStatus(status SpanStatus)
}
