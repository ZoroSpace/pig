// pi: src/harness/telemetry.ts — agent-owned telemetry schemas and typed
// span starters.
//
// Segment deviations:
//   - pi's schema objects are `as const satisfies TelemetrySchemaDefinition`
//     literals whose span/attribute keys rely on object insertion order. Go
//     maps are unordered, so spans are stored as an ordered entry slice
//     (TelemetrySchemaDefinition.Spans) preserving pi's declaration order;
//     attributes stay plain maps (their order is not consumed anywhere).
//   - The TS type-inference scaffolding (AiSpanStartAttributes,
//     SchemaTelemetrySpan, ExactTelemetryAttributes, the generic starters'
//     const-attribute inference, and the pi-telemetry re-export type aliases)
//     is compile-time typing with no runtime counterpart and is not ported
//     (same decision as the telemetry package). startAiSpan/startHarnessSpan
//     keep pi's shape minus the generics; the callback is synchronous and
//     reports an error (pi: Result | Promise<Result> propagation through
//     startSpan's callback, see telemetry.TelemetryContext).
//   - pi re-exports the pi-telemetry type surface from this module; pig's
//     consumers import package telemetry directly, so no re-export aliases
//     are needed.
package harness

import (
	"github.com/ZoroSpace/pig/telemetry"
)

// TelemetryAttributeDefinition describes one span attribute (pi:
// TelemetryAttributeDefinition from pi-telemetry).
type TelemetryAttributeDefinition struct {
	// Type is the attribute value type: "string", "number", or "boolean".
	Type string `json:"type"`
	// Required marks attributes that must be present at span start.
	Required bool `json:"required,omitempty"`
	// Values enumerates the allowed values, when closed.
	Values []string `json:"values,omitempty"`
	// Description is the human-readable attribute description.
	Description string `json:"description,omitempty"`
	// Cardinality is the expected distinct-value volume: "low" or "high".
	Cardinality string `json:"cardinality,omitempty"`
}

// TelemetrySpanParents defines where a span may attach (pi: the `parents`
// member of TelemetrySpanDefinition).
type TelemetrySpanParents struct {
	// Kind is "any", "root_or_external", or "spans".
	Kind string `json:"kind"`
	// Spans names the allowed parent spans when Kind is "spans".
	Spans []string `json:"spans,omitempty"`
}

// TelemetryStatusDefinition declares the default span status and when it
// becomes an error (pi: the `status` member).
type TelemetryStatusDefinition struct {
	// Default is the default status kind ("ok").
	Default string `json:"default"`
	// ErrorWhen describes the condition that sets an error status.
	ErrorWhen string `json:"errorWhen"`
}

// TelemetrySchemaSpan defines one span (pi: TelemetrySpanDefinition).
type TelemetrySchemaSpan struct {
	Description     string                                  `json:"description"`
	Parents         TelemetrySpanParents                    `json:"parents"`
	StartAttributes map[string]TelemetryAttributeDefinition `json:"startAttributes"`
	EndAttributes   map[string]TelemetryAttributeDefinition `json:"endAttributes"`
	Status          TelemetryStatusDefinition               `json:"status"`
}

// TelemetrySchemaSpanEntry pairs a span name with its definition; the slice
// preserves pi's object key order.
type TelemetrySchemaSpanEntry struct {
	Name string
	Span TelemetrySchemaSpan
}

// TelemetrySchemaDefinition is one telemetry schema (pi:
// TelemetrySchemaDefinition).
type TelemetrySchemaDefinition struct {
	Version int                        `json:"version"`
	Spans   []TelemetrySchemaSpanEntry `json:"-"`
}

// SpanNames returns the span names in declaration order (pi:
// Object.keys(schema.spans)).
func (s *TelemetrySchemaDefinition) SpanNames() []string {
	names := make([]string, len(s.Spans))
	for i, entry := range s.Spans {
		names[i] = entry.Name
	}
	return names
}

// Span looks up one span definition by name.
func (s *TelemetrySchemaDefinition) Span(name string) (TelemetrySchemaSpan, bool) {
	for _, entry := range s.Spans {
		if entry.Name == name {
			return entry.Span, true
		}
	}
	return TelemetrySchemaSpan{}, false
}

// AiSpanName is the closed span-name vocabulary of AiTelemetrySchema (pi:
// AiSpanName). Names are declared as constants because Go has no string-literal
// types; consumers must pass one of these values.
const AiSpanName = "pi.ai.request"

// AI_TELEMETRY_SCHEMA is the AI-request telemetry schema (pi:
// AI_TELEMETRY_SCHEMA).
var AI_TELEMETRY_SCHEMA = &TelemetrySchemaDefinition{
	Version: 1,
	Spans: []TelemetrySchemaSpanEntry{
		{
			Name: "pi.ai.request",
			Span: TelemetrySchemaSpan{
				Description: "One logical request to an AI provider",
				Parents:     TelemetrySpanParents{Kind: "any"},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.ai.operation": {
						Type:        "string",
						Required:    true,
						Values:      []string{"stream", "fetch_deferred", "cancel_deferred", "generate_images"},
						Description: "Logical provider operation",
					},
					"pi.ai.provider": {
						Type:        "string",
						Required:    true,
						Description: "Selected provider id",
					},
					"pi.ai.model": {
						Type:        "string",
						Required:    true,
						Description: "Requested model id",
					},
					"pi.ai.api": {
						Type:        "string",
						Required:    true,
						Description: "Provider API id",
					},
					"pi.ai.streaming": {
						Type:        "boolean",
						Required:    true,
						Description: "Whether this operation returns a stream",
					},
					"pi.ai.deferred": {
						Type:        "boolean",
						Required:    false,
						Description: "Whether the operation requests or participates in deferred execution",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{
					"pi.ai.response.model": {Type: "string", Description: "Concrete response model"},
					"pi.ai.response.id": {
						Type:        "string",
						Cardinality: "high",
						Description: "Provider response id",
					},
					"pi.ai.response.stop_reason": {
						Type:        "string",
						Values:      []string{"stop", "length", "tool_use", "error", "aborted", "deferred"},
						Description: "Normalized terminal response reason",
					},
					"pi.ai.http.status_code":         {Type: "number", Description: "Final HTTP status"},
					"pi.ai.usage.input_tokens":       {Type: "number", Description: "Reported input tokens"},
					"pi.ai.usage.output_tokens":      {Type: "number", Description: "Reported output tokens"},
					"pi.ai.usage.cache_read_tokens":  {Type: "number", Description: "Reported cache-read tokens"},
					"pi.ai.usage.cache_write_tokens": {Type: "number", Description: "Reported cache-write tokens"},
					"pi.ai.usage.reasoning_tokens":   {Type: "number", Description: "Reported reasoning tokens"},
					"pi.ai.usage.total_tokens":       {Type: "number", Description: "Reported total tokens"},
					"pi.ai.usage.cost":               {Type: "number", Description: "Reported total cost"},
					"pi.ai.stream.chunk_count":       {Type: "number", Description: "Streamed update chunk count"},
					"pi.ai.stream.time_to_first_chunk_ms": {
						Type:        "number",
						Description: "Elapsed milliseconds to first update chunk",
					},
					"pi.ai.error.type": {
						Type:        "string",
						Cardinality: "low",
						Description: "Provider or transport error class",
					},
				},
				Status: TelemetryStatusDefinition{
					Default:   "ok",
					ErrorWhen: "The operation throws or returns an error result",
				},
			},
		},
	},
}

// HookName values reused by the harness telemetry schema (pi: HOOK_NAMES;
// the HookName union itself lives in agent_harness.go).
var HookNames = []string{
	"before_run",
	"before_resume",
	"before_run_end",
	"transform_context",
	"before_request",
	"before_payload",
	"after_response",
	"before_tool",
	"after_tool",
	"before_compaction",
	"before_navigation",
}

// EventTypes enumerates the passive harness event types (pi: EVENT_TYPES;
// the event vocabulary itself lives in events.ts).
var EventTypes = []string{
	"run_start",
	"run_resume",
	"run_suspend",
	"run_abort",
	"run_end",
	"fault",
	"handler_error",
	"turn_start",
	"turn_end",
	"retry_scheduled",
	"retry_start",
	"retry_end",
	"message_start",
	"message_update",
	"message_end",
	"tool_start",
	"tool_update",
	"tool_end",
	"entry_added",
	"write_pending",
	"queue_update",
	"fact_update",
	"config_update",
	"compaction_start",
	"compaction_end",
	"navigation_start",
	"navigation_end",
	"lane_created",
	"usage",
}

// operationStartAttributes are shared by run/compaction/navigation spans (pi:
// operationStartAttributes).
var operationStartAttributes = map[string]TelemetryAttributeDefinition{
	"pi.session.id": {
		Type:        "string",
		Required:    true,
		Cardinality: "high",
		Description: "Session id",
	},
	"pi.lane.name": {
		Type:        "string",
		Required:    true,
		Cardinality: "high",
		Description: "Lane name",
	},
	"pi.operation.id": {
		Type:        "string",
		Required:    true,
		Cardinality: "high",
		Description: "Durable operation id",
	},
	"pi.operation.recovery": {
		Type:        "boolean",
		Required:    true,
		Description: "Whether this invocation resumes durable work",
	},
}

// operationErrorAttributes are shared end attributes of operation spans (pi:
// operationErrorAttributes).
var operationErrorAttributes = map[string]TelemetryAttributeDefinition{
	"pi.error.code": {
		Type:        "string",
		Cardinality: "low",
		Description: "Stable operation error code",
	},
	"pi.error.type": {
		Type:        "string",
		Cardinality: "low",
		Description: "Low-cardinality operation error class",
	},
}

// HARNESS_TELEMETRY_SCHEMA is the harness telemetry schema (pi:
// HARNESS_TELEMETRY_SCHEMA). Span order matches pi's object literal.
var HARNESS_TELEMETRY_SCHEMA = &TelemetrySchemaDefinition{
	Version: 1,
	Spans: []TelemetrySchemaSpanEntry{
		{
			Name: "pi.harness.run",
			Span: TelemetrySchemaSpan{
				Description: "One admitted in-process run invocation",
				Parents:     TelemetrySpanParents{Kind: "root_or_external"},
				StartAttributes: mergeAttributes(operationStartAttributes, map[string]TelemetryAttributeDefinition{
					"pi.operation.kind": {
						Type:        "string",
						Required:    true,
						Values:      []string{"run"},
						Description: "Run operation kind",
					},
				}),
				EndAttributes: mergeAttributes(map[string]TelemetryAttributeDefinition{
					"pi.operation.outcome": {
						Type:        "string",
						Values:      []string{"completed", "aborted", "failed", "suspended"},
						Description: "Run invocation outcome",
					},
				}, operationErrorAttributes),
				Status: TelemetryStatusDefinition{Default: "ok", ErrorWhen: "The run fails or throws"},
			},
		},
		{
			Name: "pi.harness.compaction",
			Span: TelemetrySchemaSpan{
				Description: "One admitted in-process manual compaction invocation",
				Parents:     TelemetrySpanParents{Kind: "root_or_external"},
				StartAttributes: mergeAttributes(operationStartAttributes, map[string]TelemetryAttributeDefinition{
					"pi.operation.kind": {
						Type:        "string",
						Required:    true,
						Values:      []string{"compaction"},
						Description: "Compaction operation kind",
					},
				}),
				EndAttributes: mergeAttributes(map[string]TelemetryAttributeDefinition{
					"pi.operation.outcome": {
						Type:        "string",
						Values:      []string{"completed", "declined", "aborted", "failed"},
						Description: "Compaction invocation outcome",
					},
				}, operationErrorAttributes),
				Status: TelemetryStatusDefinition{Default: "ok", ErrorWhen: "The compaction fails or throws"},
			},
		},
		{
			Name: "pi.harness.navigation",
			Span: TelemetrySchemaSpan{
				Description: "One admitted in-process navigation invocation",
				Parents:     TelemetrySpanParents{Kind: "root_or_external"},
				StartAttributes: mergeAttributes(operationStartAttributes, map[string]TelemetryAttributeDefinition{
					"pi.operation.kind": {
						Type:        "string",
						Required:    true,
						Values:      []string{"navigation"},
						Description: "Navigation operation kind",
					},
				}),
				EndAttributes: mergeAttributes(map[string]TelemetryAttributeDefinition{
					"pi.operation.outcome": {
						Type:        "string",
						Values:      []string{"completed", "declined", "aborted", "failed"},
						Description: "Navigation invocation outcome",
					},
				}, operationErrorAttributes),
				Status: TelemetryStatusDefinition{Default: "ok", ErrorWhen: "The navigation fails or throws"},
			},
		},
		{
			Name: "pi.harness.checkpoint",
			Span: TelemetrySchemaSpan{
				Description: "One run checkpoint",
				Parents:     TelemetrySpanParents{Kind: "spans", Spans: []string{"pi.harness.run"}},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.lane.name": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Lane name",
					},
					"pi.operation.id": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Durable operation id",
					},
					"pi.checkpoint.kind": {
						Type:        "string",
						Required:    true,
						Values:      []string{"normal", "failure_drain", "abort_reconcile"},
						Description: "Checkpoint purpose",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{},
				Status:        TelemetryStatusDefinition{Default: "ok", ErrorWhen: "Checkpoint work throws"},
			},
		},
		{
			Name: "pi.harness.turn",
			Span: TelemetrySchemaSpan{
				Description: "One assistant response and its tool batch",
				Parents:     TelemetrySpanParents{Kind: "spans", Spans: []string{"pi.harness.run"}},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.lane.name": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Lane name",
					},
					"pi.operation.id": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Durable operation id",
					},
					"pi.turn.id": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Invocation-local turn id",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{},
				Status:        TelemetryStatusDefinition{Default: "ok", ErrorWhen: "Turn work throws"},
			},
		},
		{
			Name: "pi.harness.step",
			Span: TelemetrySchemaSpan{
				Description: "One durable retry attempt",
				Parents: TelemetrySpanParents{Kind: "spans", Spans: []string{
					"pi.harness.turn", "pi.harness.checkpoint", "pi.harness.compaction", "pi.harness.navigation",
				}},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.lane.name": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Lane name",
					},
					"pi.operation.id": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Durable operation id",
					},
					"pi.step.kind": {
						Type:        "string",
						Required:    true,
						Values:      []string{"assistant", "compaction", "branch_summary"},
						Description: "Retryable step kind",
					},
					"pi.step.attempt": {
						Type:        "number",
						Required:    true,
						Description: "One-based durable attempt number",
					},
					"pi.compaction.reason": {
						Type:        "string",
						Required:    false,
						Values:      []string{"manual", "threshold", "overflow"},
						Description: "Compaction trigger",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{
					"pi.step.outcome": {
						Type:        "string",
						Values:      []string{"succeeded", "retry", "failed", "aborted", "deferred", "overflow"},
						Description: "Attempt outcome",
					},
				},
				Status: TelemetryStatusDefinition{Default: "ok", ErrorWhen: "The attempt retries, fails, or throws"},
			},
		},
		{
			Name: "pi.harness.tool",
			Span: TelemetrySchemaSpan{
				Description: "One raw phase-2 tool execution",
				Parents:     TelemetrySpanParents{Kind: "spans", Spans: []string{"pi.harness.turn", "pi.harness.run"}},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.lane.name": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Lane name",
					},
					"pi.operation.id": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Durable operation id",
					},
					"pi.turn.id": {
						Type:        "string",
						Required:    false,
						Cardinality: "high",
						Description: "Invocation-local live turn id",
					},
					"pi.tool.name": {
						Type:        "string",
						Required:    true,
						Description: "Tool name",
					},
					"pi.tool.call_id": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Tool call id",
					},
					"pi.tool.replay": {
						Type:        "string",
						Required:    true,
						Values:      []string{"never", "safe"},
						Description: "Declared replay policy",
					},
					"pi.tool.recovery": {
						Type:        "boolean",
						Required:    true,
						Description: "Whether this is recovery execution",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{
					"pi.tool.is_error": {
						Type:        "boolean",
						Description: "Whether raw phase-2 execution returned an error",
					},
				},
				Status: TelemetryStatusDefinition{Default: "ok", ErrorWhen: "Raw phase-2 execution returns an error"},
			},
		},
		{
			Name: "pi.harness.hook",
			Span: TelemetrySchemaSpan{
				Description: "One registered hook handler invocation",
				Parents:     TelemetrySpanParents{Kind: "any"},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.lane.name": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Lane name",
					},
					"pi.operation.id": {
						Type:        "string",
						Required:    false,
						Cardinality: "high",
						Description: "Durable operation id when accepted",
					},
					"pi.hook.name": {
						Type:        "string",
						Required:    true,
						Values:      HookNames,
						Description: "Hook name",
					},
					"pi.hook.registration_id": {
						Type:        "string",
						Required:    false,
						Description: "Stable hook registration id",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{
					"pi.hook.outcome": {
						Type:        "string",
						Values:      []string{"completed", "skipped", "blocked", "failed"},
						Description: "Handler outcome",
					},
				},
				Status: TelemetryStatusDefinition{Default: "ok", ErrorWhen: "The handler throws"},
			},
		},
		{
			Name: "pi.harness.sleep",
			Span: TelemetrySchemaSpan{
				Description: "One retry delay",
				Parents:     TelemetrySpanParents{Kind: "spans", Spans: []string{"pi.harness.step", "pi.harness.run"}},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.operation.id": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Durable operation id",
					},
					"pi.sleep.delay_ms": {
						Type:        "number",
						Required:    true,
						Description: "Requested delay in milliseconds",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{
					"pi.sleep.outcome": {
						Type:        "string",
						Values:      []string{"elapsed", "aborted"},
						Description: "Delay outcome",
					},
				},
				Status: TelemetryStatusDefinition{Default: "ok", ErrorWhen: "Sleep work throws"},
			},
		},
		{
			Name: "pi.harness.event_handler",
			Span: TelemetrySchemaSpan{
				Description: "One passive event listener invocation",
				Parents:     TelemetrySpanParents{Kind: "any"},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.event.type": {
						Type:        "string",
						Required:    true,
						Cardinality: "low",
						Values:      EventTypes,
						Description: "Delivered harness event type",
					},
					"pi.lane.name": {
						Type:        "string",
						Required:    false,
						Cardinality: "high",
						Description: "Lane name for lane-scoped events",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{},
				Status:        TelemetryStatusDefinition{Default: "ok", ErrorWhen: "The listener throws"},
			},
		},
		{
			Name: "pi.session.write",
			Span: TelemetrySchemaSpan{
				Description: "One committed session mutation",
				Parents:     TelemetrySpanParents{Kind: "any"},
				StartAttributes: map[string]TelemetryAttributeDefinition{
					"pi.lane.name": {
						Type:        "string",
						Required:    true,
						Cardinality: "high",
						Description: "Lane name",
					},
					"pi.operation.id": {
						Type:        "string",
						Required:    false,
						Cardinality: "high",
						Description: "Durable operation id when accepted",
					},
					"pi.session.mutation": {
						Type:        "string",
						Required:    true,
						Values:      []string{"entry", "record", "lane", "fact"},
						Description: "Session mutation kind",
					},
					"pi.session.item_type": {
						Type:        "string",
						Required:    false,
						Description: "Entry, record, lane, or fact subtype",
					},
				},
				EndAttributes: map[string]TelemetryAttributeDefinition{
					"pi.session.seq": {
						Type:        "number",
						Description: "Committed session sequence when exposed",
					},
				},
				Status: TelemetryStatusDefinition{Default: "ok", ErrorWhen: "Storage rejects the mutation"},
			},
		},
	},
}

// mergeAttributes merges attribute maps; later entries win on key collision
// (pi: object spread semantics). Fresh maps are returned; the inputs are
// never mutated.
func mergeAttributes(base map[string]TelemetryAttributeDefinition, extra map[string]TelemetryAttributeDefinition) map[string]TelemetryAttributeDefinition {
	merged := make(map[string]TelemetryAttributeDefinition, len(base)+len(extra))
	for name, def := range base {
		merged[name] = def
	}
	for name, def := range extra {
		merged[name] = def
	}
	return merged
}

// AGENT_TELEMETRY_SCHEMAS is the combined typed span vocabulary for
// agent-owned AI-request and harness telemetry (pi: AGENT_TELEMETRY_SCHEMAS).
var AGENT_TELEMETRY_SCHEMAS = []*TelemetrySchemaDefinition{AI_TELEMETRY_SCHEMA, HARNESS_TELEMETRY_SCHEMA}

// StartAiSpan starts an AI-request span and runs callback inside it (pi:
// startAiSpan). pi's typed-span generics and ExactTelemetryAttributes
// checking are compile-time only; attributes pass through as-is.
func StartAiSpan(telemetryContext telemetry.TelemetryContext, name string, attributes telemetry.SpanAttributes, callback func(span telemetry.TelemetrySpan) error) error {
	return telemetryContext.StartSpan(telemetry.SpanOptions{Name: name, Attributes: attributes}, callback)
}

// StartHarnessSpan starts a harness span and runs callback inside it (pi:
// startHarnessSpan).
func StartHarnessSpan(telemetryContext telemetry.TelemetryContext, name string, attributes telemetry.SpanAttributes, callback func(span telemetry.TelemetrySpan) error) error {
	return telemetryContext.StartSpan(telemetry.SpanOptions{Name: name, Attributes: attributes}, callback)
}
