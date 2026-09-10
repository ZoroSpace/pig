// pi: test/harness/session/jsonl-codec.test.ts — header and mutation line
// codec round trips and rejections.
package jsonl_test

import (
	"github.com/samber/lo"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
)

// expectHeaderRoundTrip encodes, re-parses, and compares via canonical JSON
// (pi: expectHeaderRoundTrip + vitest toEqual).
func expectHeaderRoundTrip(t *testing.T, header jsonl.JsonlV4Header) {
	t.Helper()
	encoded := jsonl.EncodeHeader(header)
	if encoded[len(encoded)-1] != '\n' {
		t.Fatal("encoded header must end with a newline")
	}
	parsed, err := jsonl.ParseHeader(trimNewline(encoded))
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, parsed, header)
}

// expectMutationRoundTrip encodes, re-parses, and compares via canonical JSON
// (pi: expectMutationRoundTrip).
func expectMutationRoundTrip(t *testing.T, mutation session.SessionMutation) {
	t.Helper()
	encoded := jsonl.EncodeMutation(mutation)
	if encoded[len(encoded)-1] != '\n' {
		t.Fatal("encoded mutation must end with a newline")
	}
	parsed, err := jsonl.ParseMutation(trimNewline(encoded))
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, parsed, mutation)
}

func trimNewline(encoded string) string {
	return encoded[:len(encoded)-1]
}

// assertParseErrorKind asserts a JsonlDecodeError with the given kind (pi:
// the result.error match on kind).
func assertParseErrorKind(t *testing.T, err error, kind string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s decode error, got success", kind)
	}
	decodeErr, ok := err.(*jsonl.JsonlDecodeError)
	if !ok {
		t.Fatalf("expected JsonlDecodeError, got %T", err)
	}
	if decodeErr.Kind != kind {
		t.Fatalf("decode error kind = %s, want %s", decodeErr.Kind, kind)
	}
}

func TestHeaderRoundTripResolvedParent(t *testing.T) {
	expectHeaderRoundTrip(t, jsonl.JsonlV4Header{
		Kind:            "header",
		Version:         4,
		ID:              "session",
		CreatedAt:       1_700_000_000_000,
		Cwd:             "/workspace/project",
		ParentSessionID: lo.ToPtr("parent"),
		Metadata: map[string]any{
			"owner":  "agent",
			"nested": map[string]any{"enabled": true},
			"values": []any{float64(1), nil, "two"},
		},
	})
}

func TestHeaderRoundTripLegacyParentPath(t *testing.T) {
	expectHeaderRoundTrip(t, jsonl.JsonlV4Header{
		Kind:                    "header",
		Version:                 4,
		ID:                      "legacy-child",
		CreatedAt:               1_700_000_000_001,
		Cwd:                     "/workspace/project",
		LegacyParentSessionPath: lo.ToPtr("/sessions/missing-parent.jsonl"),
	})
}

func TestMetadataFromHeaderProjectsHeaderAndFilesystemFields(t *testing.T) {
	header := jsonl.JsonlV4Header{
		Kind:                    "header",
		Version:                 4,
		ID:                      "session",
		CreatedAt:               1_700_000_000_000,
		Cwd:                     "/workspace/project",
		LegacyParentSessionPath: lo.ToPtr("/sessions/missing-parent.jsonl"),
		Metadata:                map[string]any{"owner": "agent"},
	}
	assertJSONEqual(t,
		jsonl.MetadataFromHeader(header, "/sessions/session.jsonl", 1_700_000_000_100),
		jsonl.JsonlSessionMetadata{
			SessionMetadata: session.SessionMetadata{
				ID:        "session",
				CreatedAt: 1_700_000_000_000,
			},
			Cwd:                     "/workspace/project",
			Path:                    "/sessions/session.jsonl",
			ModifiedAt:              1_700_000_000_100,
			SourceFormat:            jsonl.SourceFormatV4,
			LegacyParentSessionPath: lo.ToPtr("/sessions/missing-parent.jsonl"),
			Metadata:                map[string]any{"owner": "agent"},
		},
	)
}

func TestParseMutationSyntaxAndSchemaErrors(t *testing.T) {
	cases := []struct {
		line string
		kind string
	}{
		{"{", "syntax"},
		{`{"kind":"unknown","seq":1}`, "schema"},
	}
	for _, testCase := range cases {
		_, err := jsonl.ParseMutation(testCase.line)
		assertParseErrorKind(t, err, testCase.kind)
	}
}

func TestMutationRoundTripLaneBoundEntry(t *testing.T) {
	expectMutationRoundTrip(t, session.SessionMutation{
		Kind: session.MutationKindEntry,
		Lane: lo.ToPtr("main"),
		Entry: &session.CustomEntry{
			EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "entry-1", Seq: 1, Timestamp: 100},
			CustomType: "note",
			Data:       map[string]any{"text": "hello"},
		},
	})
}

func TestMutationRoundTripImportedEntryWithoutLane(t *testing.T) {
	expectMutationRoundTrip(t, session.SessionMutation{
		Kind: session.MutationKindEntry,
		Entry: &session.CustomEntry{
			EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "entry-1", Seq: 1, ParentID: nil, Timestamp: 100},
			CustomType: "note",
		},
	})
}

func TestMutationRoundTripRecord(t *testing.T) {
	expectMutationRoundTrip(t, session.SessionMutation{
		Kind: session.MutationKindRecord,
		Record: &session.OperationStartedRecord{
			RecordBase:   session.RecordBase{Type: session.RecordTypeOperationStarted, ID: "run-1", Seq: 1, Lane: "main", Timestamp: 100},
			SourceLeafID: nil,
			Intent: &session.RunIntent{
				Kind:            session.OperationIntentRun,
				OriginalPrompt:  []agent.AgentMessage{},
				InitialMessages: []session.Entry{},
			},
		},
	})
}

func TestMutationRoundTripLane(t *testing.T) {
	expectMutationRoundTrip(t, session.SessionMutation{
		Kind:   session.MutationKindLane,
		Seq:    1,
		Lane:   lo.ToPtr("thread"),
		LeafID: lo.ToPtr("entry-1"),
	})
}

func TestMutationRoundTripFacts(t *testing.T) {
	expectMutationRoundTrip(t, session.SessionMutation{
		Kind: session.MutationKindFact,
		Seq:  1,
		Fact: "name",
		Name: lo.ToPtr("Example"),
	})
	expectMutationRoundTrip(t, session.SessionMutation{
		Kind: session.MutationKindFact,
		Seq:  2,
		Fact: "name",
		Name: nil,
	})
	expectMutationRoundTrip(t, session.SessionMutation{
		Kind:     session.MutationKindFact,
		Seq:      3,
		Fact:     "label",
		TargetID: "entry-1",
		Label:    lo.ToPtr("checkpoint"),
	})
}

func TestParseMutationRejections(t *testing.T) {
	cases := []struct {
		name     string
		mutation string
	}{
		{
			name:     "a custom entry without customType",
			mutation: `{"kind":"entry","type":"custom","id":"entry","parentId":null,"seq":1,"timestamp":1}`,
		},
		{
			name:     "an operation_started record without intent",
			mutation: `{"kind":"record","type":"operation_started","id":"run","lane":"main","seq":1,"timestamp":1,"sourceLeafId":null}`,
		},
		{
			name:     "an operation_finished record without runId",
			mutation: `{"kind":"record","type":"operation_finished","id":"finish","lane":"main","seq":1,"timestamp":1,"outcome":"completed"}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := jsonl.ParseMutation(testCase.mutation); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
