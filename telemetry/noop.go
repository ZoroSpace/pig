package telemetry

type noopSpan struct{}

func (noopSpan) StartSpan(options SpanOptions, callback func(TelemetrySpan) error) error {
	return callback(noopSpan{})
}

func (noopSpan) AddEvent(name string, attributes SpanAttributes) {}
func (noopSpan) SetAttributes(attributes SpanAttributes)         {}
func (noopSpan) SetStatus(status SpanStatus)                     {}

// NoopTelemetryContext is the NOOP_TELEMETRY_CONTEXT counterpart: spans are
// created and immediately discarded.
var NoopTelemetryContext TelemetryContext = noopSpan{}
