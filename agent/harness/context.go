// pi: harness/context.ts — telemetry-context propagation helpers.
//
// pi's Context/ContextKey come from @earendil-works/chord (a type-only
// dependency, per the SYNC-0 exclusion decision); pig maps the chord Context
// onto Go's context.Context: values travel via context.WithValue with
// package-private key types, BACKGROUND_CONTEXT/TODO_CONTEXT collapse to
// context.Background()/context.TODO(), and awaitWithContext/withCancel/
// withAbortSignal collapse into plain ctx propagation (single-threaded event
// loop → goroutine convention). Only the telemetry-parent members have a
// real consumer in pig today.
package harness

import (
	"context"

	"github.com/ZoroSpace/pig/telemetry"
)

// telemetryContextKey is the createContextKey<TelemetryContext>("pi.telemetryContext")
// counterpart.
type telemetryContextKey struct{}

// GetTelemetryContext returns the telemetry parent attached to a context, or
// the shared no-op parent (pi: getTelemetryContext).
func GetTelemetryContext(ctx context.Context) telemetry.TelemetryContext {
	if ctx != nil {
		if tc, ok := ctx.Value(telemetryContextKey{}).(telemetry.TelemetryContext); ok && tc != nil {
			return tc
		}
	}
	return telemetry.NoopTelemetryContext
}

// WithTelemetryContext derives a context whose telemetry children use the
// supplied parent or active span (pi: withTelemetryContext).
func WithTelemetryContext(ctx context.Context, telemetryContext telemetry.TelemetryContext) context.Context {
	return context.WithValue(ctx, telemetryContextKey{}, telemetryContext)
}
