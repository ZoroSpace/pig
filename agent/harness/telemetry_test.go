// Ports of the runtime parts of pi's packages/agent/test/harness/
// telemetry.test.ts. The markdown reference-document check is not ported:
// pig has no generate-telemetry-docs script (pi repo dev tooling). The
// expectTypeOf inference tests are compile-time TS typing with no Go
// counterpart.
package harness

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ZoroSpace/pig/telemetry"
)

func TestAgentTelemetrySchemasSerializeAndSpanOrder(t *testing.T) {
	for _, schema := range []*TelemetrySchemaDefinition{AI_TELEMETRY_SCHEMA, HARNESS_TELEMETRY_SCHEMA} {
		for _, entry := range schema.Spans {
			if _, err := json.Marshal(entry.Span.StartAttributes); err != nil {
				t.Fatalf("start attributes of %s do not serialize: %v", entry.Name, err)
			}
			if _, err := json.Marshal(entry.Span.EndAttributes); err != nil {
				t.Fatalf("end attributes of %s do not serialize: %v", entry.Name, err)
			}
		}
	}
	if !reflect.DeepEqual(AGENT_TELEMETRY_SCHEMAS, []*TelemetrySchemaDefinition{AI_TELEMETRY_SCHEMA, HARNESS_TELEMETRY_SCHEMA}) {
		t.Fatal("AGENT_TELEMETRY_SCHEMAS must be [AI_TELEMETRY_SCHEMA, HARNESS_TELEMETRY_SCHEMA]")
	}
	expected := []string{
		"pi.harness.run",
		"pi.harness.compaction",
		"pi.harness.navigation",
		"pi.harness.checkpoint",
		"pi.harness.turn",
		"pi.harness.step",
		"pi.harness.tool",
		"pi.harness.hook",
		"pi.harness.sleep",
		"pi.harness.event_handler",
		"pi.session.write",
	}
	if got := HARNESS_TELEMETRY_SCHEMA.SpanNames(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("span order = %v", got)
	}
}

func TestStartsAiAndHarnessSpansThroughOneComposedStarter(t *testing.T) {
	// pi composes both schemas through createTypedSpanStarter and starts a
	// harness span with a nested AI-request span; the typed starter is
	// compile-time scaffolding, so pig exercises the two schema wrappers
	// directly against the NOOP context.
	err := StartHarnessSpan(telemetry.NoopTelemetryContext, "pi.harness.step", telemetry.SpanAttributes{
		"pi.lane.name":    "main",
		"pi.operation.id": "operation",
		"pi.step.kind":    "assistant",
		"pi.step.attempt": 1,
	}, func(stepSpan telemetry.TelemetrySpan) error {
		stepSpan.SetAttributes(telemetry.SpanAttributes{"pi.step.outcome": "succeeded"})
		return StartAiSpan(stepSpan, "pi.ai.request", telemetry.SpanAttributes{
			"pi.ai.operation": "stream",
			"pi.ai.provider":  "provider",
			"pi.ai.model":     "model",
			"pi.ai.api":       "api",
			"pi.ai.streaming": true,
		}, func(requestSpan telemetry.TelemetrySpan) error {
			requestSpan.SetAttributes(telemetry.SpanAttributes{"pi.ai.response.stop_reason": "stop"})
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAiSpanSchemaAttributeVocabulary(t *testing.T) {
	// pi locks the closed start-attribute value set and the optional end
	// attributes via expectTypeOf; pig asserts the runtime schema shape.
	span, ok := AI_TELEMETRY_SCHEMA.Span("pi.ai.request")
	if !ok {
		t.Fatal("missing pi.ai.request span")
	}
	operation := span.StartAttributes["pi.ai.operation"]
	if !reflect.DeepEqual(operation.Values, []string{"stream", "fetch_deferred", "cancel_deferred", "generate_images"}) {
		t.Fatalf("pi.ai.operation values = %v", operation.Values)
	}
	if !operation.Required {
		t.Fatal("pi.ai.operation must be required")
	}
	if span.StartAttributes["pi.ai.deferred"].Required {
		t.Fatal("pi.ai.deferred must be optional")
	}
	stopReason := span.EndAttributes["pi.ai.response.stop_reason"]
	if !reflect.DeepEqual(stopReason.Values, []string{"stop", "length", "tool_use", "error", "aborted", "deferred"}) {
		t.Fatalf("pi.ai.response.stop_reason values = %v", stopReason.Values)
	}
}
