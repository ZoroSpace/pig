// pi: src/harness/session/jsonl/codec.ts — JSONL v4 line codec: header and
// mutation parse/encode with schema validation.
//
// Wire-format rules (§3.4): field names and string literals are identical to
// pi. Concretely:
//   - header line: {"kind":"header","version":4,"id",...} with optional
//     parentSessionId/legacyParentSessionPath/metadata (undefined drops in
//     TS ≙ nil + omitempty in Go).
//   - mutation lines: {"kind":"entry","lane":...,...entryFields},
//     {"kind":"record",...recordFields}, {"kind":"lane",seq,lane,leafId}
//     (leafId null preserved), {"kind":"fact",...} (undefined name/label
//     dropped ≙ nil + conditional insert).
//
// Deviations:
//   - TS objects are dynamic: {kind, ...entry} keeps unknown entry
//     properties through a decode/encode round-trip. Go decodes into the
//     typed session.Entry/session.LaneRecord unions, so unknown properties
//     of *known* entry/record types are dropped on re-encode. Unknown types
//     and malformed required fields are rejected exactly as pi does. (pi v4
//     writers only emit known properties; a schema bump would change
//     `version`.)
//   - TS validates scalar types manually (requireString/requireSequence/...);
//     Go mirrors those checks explicitly, then re-decodes the line through
//     session.DecodeEntry/DecodeRecord for the typed value. Any residual
//     unmarshal error is reported as a "schema" decode error.
//   - Provisioned entries nested in queue_enqueued/write_deferred `target`
//     encode with "parentId":null (Go no-omit pointer) where TS drops the
//     undefined property; inert on both readers (storage overwrites parentId
//     on the deferred append).
//   - Key order inside a line is not preserved (encoding/json sorts map
//     keys); JSONL consumers parse per line, so this is semantically
//     transparent.
package jsonl

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/agent/harness/session"
)

// entryTypes is the accepted entry discriminant set (pi: ENTRY_TYPES).
var entryTypes = map[session.EntryType]struct{}{
	session.EntryTypeMessage:             {},
	session.EntryTypeModelChange:         {},
	session.EntryTypeThinkingLevelChange: {},
	session.EntryTypeActiveToolsChange:   {},
	session.EntryTypeCompaction:          {},
	session.EntryTypeBranchSummary:       {},
	session.EntryTypeCustom:              {},
}

// recordTypes is the accepted record discriminant set (pi: RECORD_TYPES).
var recordTypes = map[session.RecordType]struct{}{
	session.RecordTypeOperationStarted:  {},
	session.RecordTypeAbortRequested:    {},
	session.RecordTypeOperationFinished: {},
	session.RecordTypeStepAttempt:       {},
	session.RecordTypeToolStarted:       {},
	session.RecordTypeQueueEnqueued:     {},
	session.RecordTypeQueueCancelled:    {},
	session.RecordTypeWriteDeferred:     {},
	session.RecordTypeUsage:             {},
}

// operationKinds is the accepted intent kind set (pi: OPERATION_KINDS).
var operationKinds = map[session.OperationIntentKind]struct{}{
	session.OperationIntentRun:        {},
	session.OperationIntentCompaction: {},
	session.OperationIntentNavigation: {},
}

// parseObject parses one line as a JSON object, preserving raw field bytes
// (pi: parseObject + isObject).
func parseObject(line string) (map[string]json.RawMessage, error) {
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return nil, NewJsonlDecodeError("syntax", "is not valid JSON", err)
	}
	// pi's isObject rejects arrays and non-objects.
	if _, ok := value.(map[string]any); !ok {
		return nil, NewJsonlDecodeError("schema", "is not a JSON object", nil)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return nil, NewJsonlDecodeError("syntax", "is not valid JSON", err)
	}
	return fields, nil
}

// rawString reads a required string field (pi: requireString).
func rawString(fields map[string]json.RawMessage, field string) (string, error) {
	raw, ok := fields[field]
	if !ok {
		return "", NewJsonlDecodeError("schema", fmt.Sprintf("has invalid %s", field), nil)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", NewJsonlDecodeError("schema", fmt.Sprintf("has invalid %s", field), nil)
	}
	return value, nil
}

// rawSequence reads the shared sequence (pi: requireSequence): a positive
// integer.
func rawSequence(fields map[string]json.RawMessage) (int64, error) {
	raw, ok := fields["seq"]
	if !ok {
		return 0, NewJsonlDecodeError("schema", "has invalid seq", nil)
	}
	number := json.Number(strings.TrimSpace(string(raw)))
	seq, err := number.Int64()
	if err != nil || seq <= 0 {
		return 0, NewJsonlDecodeError("schema", "has invalid seq", nil)
	}
	return seq, nil
}

// rawTimestamp reads a non-negative integer timestamp (pi: requireTimestamp).
func rawTimestamp(fields map[string]json.RawMessage, field string) (int64, error) {
	raw, ok := fields[field]
	if !ok {
		return 0, NewJsonlDecodeError("schema", fmt.Sprintf("has invalid %s", field), nil)
	}
	number := json.Number(strings.TrimSpace(string(raw)))
	ts, err := number.Int64()
	if err != nil || ts < 0 {
		return 0, NewJsonlDecodeError("schema", fmt.Sprintf("has invalid %s", field), nil)
	}
	return ts, nil
}

// rawNullableID reads a null-or-string id (pi: requireNullableId).
func rawNullableID(fields map[string]json.RawMessage, field string) error {
	raw, ok := fields[field]
	if !ok {
		return NewJsonlDecodeError("schema", fmt.Sprintf("has invalid %s", field), nil)
	}
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil {
		return NewJsonlDecodeError("schema", fmt.Sprintf("has invalid %s", field), nil)
	}
	return nil
}

// decodeHeader validates and decodes one header line (pi: decodeHeader).
func decodeHeader(line string) (JsonlV4Header, error) {
	fields, err := parseObject(line)
	if err != nil {
		return JsonlV4Header{}, err
	}
	var kind string
	if raw, ok := fields["kind"]; ok {
		if err := json.Unmarshal(raw, &kind); err != nil || kind != "header" {
			return JsonlV4Header{}, NewJsonlDecodeError("schema", "is not a header", nil)
		}
	} else {
		return JsonlV4Header{}, NewJsonlDecodeError("schema", "is not a header", nil)
	}
	var version int
	if raw, ok := fields["version"]; ok {
		if err := json.Unmarshal(raw, &version); err != nil || version != 4 {
			return JsonlV4Header{}, NewJsonlDecodeError("schema", "has unsupported session version", nil)
		}
	} else {
		return JsonlV4Header{}, NewJsonlDecodeError("schema", "has unsupported session version", nil)
	}

	var parentSessionID *string
	if raw, ok := fields["parentSessionId"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &parentSessionID); err != nil {
			return JsonlV4Header{}, NewJsonlDecodeError("schema", "has invalid parentSessionId", nil)
		}
		if parentSessionID == nil {
			return JsonlV4Header{}, NewJsonlDecodeError("schema", "has invalid parentSessionId", nil)
		}
	}
	var legacyParentSessionPath *string
	if raw, ok := fields["legacyParentSessionPath"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &legacyParentSessionPath); err != nil {
			return JsonlV4Header{}, NewJsonlDecodeError("schema", "has invalid legacyParentSessionPath", nil)
		}
		if legacyParentSessionPath == nil {
			return JsonlV4Header{}, NewJsonlDecodeError("schema", "has invalid legacyParentSessionPath", nil)
		}
	}
	if parentSessionID != nil && legacyParentSessionPath != nil {
		return JsonlV4Header{}, NewJsonlDecodeError("schema", "has both parentSessionId and legacyParentSessionPath", nil)
	}
	var metadata map[string]any
	if raw, ok := fields["metadata"]; ok {
		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return JsonlV4Header{}, NewJsonlDecodeError("schema", "has invalid metadata", nil)
		}
		obj, ok := probe.(map[string]any)
		if !ok {
			return JsonlV4Header{}, NewJsonlDecodeError("schema", "has invalid metadata", nil)
		}
		metadata = obj
	}

	id, err := rawString(fields, "id")
	if err != nil {
		return JsonlV4Header{}, err
	}
	createdAt, err := rawTimestamp(fields, "createdAt")
	if err != nil {
		return JsonlV4Header{}, err
	}
	cwd, err := rawString(fields, "cwd")
	if err != nil {
		return JsonlV4Header{}, err
	}
	return JsonlV4Header{
		Kind:                    "header",
		Version:                 4,
		ID:                      id,
		CreatedAt:               createdAt,
		Cwd:                     cwd,
		ParentSessionID:         parentSessionID,
		LegacyParentSessionPath: legacyParentSessionPath,
		Metadata:                metadata,
	}, nil
}

// ParseHeader parses one header line (pi: parseHeader; TS Result → (T,
// error)).
func ParseHeader(line string) (JsonlV4Header, error) {
	header, err := decodeHeader(line)
	if err != nil {
		return JsonlV4Header{}, err
	}
	return header, nil
}

// EncodeHeader encodes one header line with trailing newline (pi:
// encodeHeader).
func EncodeHeader(header JsonlV4Header) string {
	encoded, _ := json.Marshal(header)
	return string(encoded) + "\n"
}

// MetadataFromHeader derives list metadata from a header (pi:
// metadataFromHeader).
func MetadataFromHeader(header JsonlV4Header, path string, modifiedAt int64) JsonlSessionMetadata {
	return JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{
			ID:              header.ID,
			CreatedAt:       header.CreatedAt,
			ParentSessionID: header.ParentSessionID,
		},
		Cwd:                     header.Cwd,
		Path:                    path,
		ModifiedAt:              modifiedAt,
		SourceFormat:            SourceFormatV4,
		LegacyParentSessionPath: header.LegacyParentSessionPath,
		Metadata:                header.Metadata,
	}
}

// parseEntryMutation validates and decodes an entry mutation line (pi:
// parseEntryMutation).
func parseEntryMutation(fields map[string]json.RawMessage) (session.SessionMutation, error) {
	// Note: the top-level SessionMutation.Seq stays zero for entry/record
	// kinds — their sequence lives on the payload (pi: {kind, record} has no
	// top-level seq; state reads the payload's).
	mutation := session.SessionMutation{Kind: session.MutationKindEntry}
	if raw, ok := fields["lane"]; ok {
		var lane string
		if err := json.Unmarshal(raw, &lane); err != nil {
			return mutation, NewJsonlDecodeError("schema", "has invalid lane", nil)
		}
		mutation.Lane = &lane
	}
	if _, err := rawString(fields, "id"); err != nil {
		return mutation, err
	}
	typeRaw, err := rawString(fields, "type")
	if err != nil {
		return mutation, err
	}
	if _, known := entryTypes[session.EntryType(typeRaw)]; !known {
		return mutation, NewJsonlDecodeError("schema", fmt.Sprintf("has unknown entry type %s", typeRaw), nil)
	}
	if err := rawNullableID(fields, "parentId"); err != nil {
		return mutation, err
	}
	if _, err := rawTimestamp(fields, "timestamp"); err != nil {
		return mutation, err
	}
	if session.EntryType(typeRaw) == session.EntryTypeCustom {
		if _, err := rawString(fields, "customType"); err != nil {
			return mutation, err
		}
	}
	entry, err := session.DecodeEntry(joinFields(fields))
	if err != nil {
		return mutation, NewJsonlDecodeError("schema", err.Error(), nil)
	}
	// pi rebuilds the entry with the line-level seq; the typed decode filled
	// the same field from the same line (EntryBase.Seq json:"seq").
	mutation.Entry = entry
	return mutation, nil
}

// parseRecordMutation validates and decodes a record mutation line (pi:
// parseRecordMutation).
func parseRecordMutation(fields map[string]json.RawMessage) (session.SessionMutation, error) {
	// See parseEntryMutation: top-level Seq stays zero for record kinds.
	mutation := session.SessionMutation{Kind: session.MutationKindRecord}
	if _, err := rawString(fields, "id"); err != nil {
		return mutation, err
	}
	lane, err := rawString(fields, "lane")
	if err != nil {
		return mutation, err
	}
	// pi validates lane here but keeps the record payload's own lane field
	// (the spread retains record.lane); the typed decode below does the same.
	_ = lane
	typeRaw, err := rawString(fields, "type")
	if err != nil {
		return mutation, err
	}
	if _, known := recordTypes[session.RecordType(typeRaw)]; !known {
		return mutation, NewJsonlDecodeError("schema", fmt.Sprintf("has unknown record type %s", typeRaw), nil)
	}
	if _, err := rawTimestamp(fields, "timestamp"); err != nil {
		return mutation, err
	}
	if session.RecordType(typeRaw) == session.RecordTypeOperationStarted {
		intentRaw, ok := fields["intent"]
		if !ok {
			return mutation, NewJsonlDecodeError("schema", "has invalid intent", nil)
		}
		var intentFields map[string]json.RawMessage
		if err := json.Unmarshal(intentRaw, &intentFields); err != nil {
			return mutation, NewJsonlDecodeError("schema", "has invalid intent", nil)
		}
		if intentFields == nil {
			return mutation, NewJsonlDecodeError("schema", "has invalid intent", nil)
		}
		kindRaw, err := rawString(intentFields, "kind")
		if err != nil {
			return mutation, NewJsonlDecodeError("schema", "has invalid operation kind", nil)
		}
		if _, known := operationKinds[session.OperationIntentKind(kindRaw)]; !known {
			return mutation, NewJsonlDecodeError("schema", fmt.Sprintf("has unknown operation kind %s", kindRaw), nil)
		}
	}
	if session.RecordType(typeRaw) == session.RecordTypeOperationFinished {
		if _, err := rawString(fields, "runId"); err != nil {
			return mutation, err
		}
	}
	record, err := session.DecodeRecord(joinFields(fields))
	if err != nil {
		return mutation, NewJsonlDecodeError("schema", err.Error(), nil)
	}
	mutation.Record = record
	return mutation, nil
}

// parseLaneMutation validates and decodes a lane mutation line (pi:
// parseLaneMutation).
func parseLaneMutation(fields map[string]json.RawMessage, seq int64) (session.SessionMutation, error) {
	lane, err := rawString(fields, "lane")
	if err != nil {
		return session.SessionMutation{}, err
	}
	if err := rawNullableID(fields, "leafId"); err != nil {
		return session.SessionMutation{}, err
	}
	mutation := session.SessionMutation{
		Kind: session.MutationKindLane,
		Seq:  seq,
		Lane: &lane,
	}
	if raw, ok := fields["leafId"]; ok && string(raw) != "null" {
		var leafID string
		if err := json.Unmarshal(raw, &leafID); err != nil {
			return session.SessionMutation{}, NewJsonlDecodeError("schema", "has invalid leafId", nil)
		}
		mutation.LeafID = &leafID
	}
	return mutation, nil
}

// parseFactMutation validates and decodes a fact mutation line (pi:
// parseFactMutation).
func parseFactMutation(fields map[string]json.RawMessage, seq int64) (session.SessionMutation, error) {
	fact, err := rawString(fields, "fact")
	if err != nil {
		return session.SessionMutation{}, err
	}
	mutation := session.SessionMutation{Kind: session.MutationKindFact, Seq: seq, Fact: fact}
	switch fact {
	case "name":
		if raw, ok := fields["name"]; ok {
			var name string
			if err := json.Unmarshal(raw, &name); err != nil {
				return session.SessionMutation{}, NewJsonlDecodeError("schema", "has invalid name", nil)
			}
			mutation.Name = &name
		}
		return mutation, nil
	case "label":
		targetID, err := rawString(fields, "targetId")
		if err != nil {
			return session.SessionMutation{}, err
		}
		mutation.TargetID = targetID
		if raw, ok := fields["label"]; ok {
			var label string
			if err := json.Unmarshal(raw, &label); err != nil {
				return session.SessionMutation{}, NewJsonlDecodeError("schema", "has invalid label", nil)
			}
			mutation.Label = &label
		}
		return mutation, nil
	default:
		return session.SessionMutation{}, NewJsonlDecodeError("schema", "has unknown fact type", nil)
	}
}

// decodeMutation decodes one mutation line (pi: decodeMutation).
func decodeMutation(line string) (session.SessionMutation, error) {
	fields, err := parseObject(line)
	if err != nil {
		return session.SessionMutation{}, err
	}
	seq, err := rawSequence(fields)
	if err != nil {
		return session.SessionMutation{}, err
	}
	kind, err := rawString(fields, "kind")
	if err != nil {
		return session.SessionMutation{}, NewJsonlDecodeError("schema", "has unknown mutation kind", nil)
	}
	switch kind {
	case "entry":
		return parseEntryMutation(fields)
	case "record":
		return parseRecordMutation(fields)
	case "lane":
		return parseLaneMutation(fields, seq)
	case "fact":
		return parseFactMutation(fields, seq)
	default:
		return session.SessionMutation{}, NewJsonlDecodeError("schema", "has unknown mutation kind", nil)
	}
}

// ParseMutation parses one mutation line (pi: parseMutation; TS Result →
// (T, error)).
func ParseMutation(line string) (session.SessionMutation, error) {
	mutation, err := decodeMutation(line)
	if err != nil {
		return session.SessionMutation{}, err
	}
	return mutation, nil
}

// EncodeMutation encodes one mutation line with trailing newline (pi:
// encodeMutation).
func EncodeMutation(mutation session.SessionMutation) string {
	var encoded []byte
	switch mutation.Kind {
	case session.MutationKindEntry:
		fields := objectFields(mutation.Entry)
		fields["kind"] = json.RawMessage(`"entry"`)
		if mutation.Lane != nil {
			fields["lane"] = mustMarshal(*mutation.Lane)
		}
		encoded = mustMarshal(fields)
	case session.MutationKindRecord:
		fields := objectFields(mutation.Record)
		fields["kind"] = json.RawMessage(`"record"`)
		encoded = mustMarshal(fields)
	case session.MutationKindLane:
		// pi stringifies { kind, seq, lane, leafId } — leafId null survives.
		fields := map[string]json.RawMessage{
			"kind":   json.RawMessage(`"lane"`),
			"seq":    mustMarshal(mutation.Seq),
			"lane":   mustMarshal(derefString(mutation.Lane)),
			"leafId": json.RawMessage("null"),
		}
		if mutation.LeafID != nil {
			fields["leafId"] = mustMarshal(*mutation.LeafID)
		}
		encoded = mustMarshal(fields)
	case session.MutationKindFact:
		fields := map[string]json.RawMessage{
			"kind": json.RawMessage(`"fact"`),
			"seq":  mustMarshal(mutation.Seq),
			"fact": mustMarshal(mutation.Fact),
		}
		switch mutation.Fact {
		case "name":
			if mutation.Name != nil {
				fields["name"] = mustMarshal(*mutation.Name)
			}
		case "label":
			fields["targetId"] = mustMarshal(mutation.TargetID)
			if mutation.Label != nil {
				fields["label"] = mustMarshal(*mutation.Label)
			}
		}
		encoded = mustMarshal(fields)
	}
	return string(encoded) + "\n"
}

// joinFields re-marshals a raw field map (used to feed the typed union
// decoders the original line bytes).
func joinFields(fields map[string]json.RawMessage) json.RawMessage {
	return mustMarshal(fields)
}

// objectFields flattens a typed union value into raw fields (pi: the
// {kind, ...entry} spread). See the file-header note on unknown-property
// drop.
func objectFields(value any) map[string]json.RawMessage {
	raw := mustMarshal(value)
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		// Typed union values always marshal to objects; unreachable.
		panic(fmt.Sprintf("jsonl: union value did not marshal to an object: %v", err))
	}
	return fields
}

func mustMarshal(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("jsonl: marshal failure: %v", err))
	}
	return encoded
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
