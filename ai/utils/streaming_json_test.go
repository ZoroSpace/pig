package utils

import (
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func feedParser(t *testing.T, text string, chunk int) (any, bool) {
	t.Helper()
	parser := NewStreamingJSONParser()
	for index := 0; index < len(text); index += chunk {
		end := index + chunk
		if end > len(text) {
			end = len(text)
		}
		parser.Append(text[index:end])
	}
	value, has, _ := parser.Snapshot()
	return value, has
}

func TestStreamingJSONParserCompleteDocuments(t *testing.T) {
	documents := []string{
		`{}`,
		`[]`,
		`null`,
		`true`,
		`42`,
		`-0.5e2`,
		`"plain"`,
		`{"a":1,"b":[true,false,null],"c":{"d":"e"}}`,
		` {"a" : 1 , "b" : [ ] } `,
	}
	for _, document := range documents {
		var want any
		if err := json.Unmarshal([]byte(document), &want); err != nil {
			t.Fatalf("decode %q: %v", document, err)
		}
		for _, chunk := range []int{1, 2, 3, 7, len(document)} {
			if chunk == 0 {
				continue
			}
			got, has := feedParser(t, document, chunk)
			if !has {
				t.Fatalf("%q chunk %d: no value", document, chunk)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%q chunk %d = %#v, want %#v", document, chunk, got, want)
			}
		}
	}
}

func TestStreamingJSONParserSplitsEscapesAndSurrogates(t *testing.T) {
	documents := []struct {
		text string
		want any
	}{
		{`{"text":"a\u00e9b"}`, map[string]any{"text": "aéb"}},
		{`{"text":"\ud83d\ude00"}`, map[string]any{"text": "😀"}},
		{`{"text":"\ud83d\ude00 raw 😀"}`, map[string]any{"text": "😀 raw 😀"}},
		{`{"text":"a\\b\"c\/d\n"}`, map[string]any{"text": "a\\b\"c/d\n"}},
	}
	for _, document := range documents {
		for split := 0; split <= len(document.text); split++ {
			parser := NewStreamingJSONParser()
			parser.Append(document.text[:split])
			value, _, _ := parser.Snapshot()
			if split < len(document.text) {
				_ = value
			}
			parser.Append(document.text[split:])
			got, has, _ := parser.Snapshot()
			if !has || !reflect.DeepEqual(got, document.want) {
				t.Fatalf("%q split %d = %#v (has=%v), want %#v", document.text, split, got, has, document.want)
			}
		}
	}
}

func TestStreamingJSONParserKeepsTrailingWhitespaceAndControlCharacters(t *testing.T) {
	parser := NewStreamingJSONParser()
	parser.Append(`{"a":"x `)
	if value, _, _ := parser.Snapshot(); value.(map[string]any)["a"] != "x " {
		t.Fatalf("partial = %#v", value)
	}
	parser.Append("\x01")
	if value, _, _ := parser.Snapshot(); value.(map[string]any)["a"] != "x \x01" {
		t.Fatalf("control characters should stay visible: %#v", value)
	}
	parser.Append(`"}`)
	if value, _, _ := parser.Snapshot(); value.(map[string]any)["a"] != "x \x01" {
		t.Fatalf("final = %#v", value)
	}
}

func TestStreamingJSONParserKeepsInvalidEscapesLiteral(t *testing.T) {
	for _, text := range []string{`{"text":"a\d"}`, `{"text":"\u12"}`, `{"text":"\u12zz"}`} {
		parser := NewStreamingJSONParser()
		parser.Append(text)
		value, has, _ := parser.Snapshot()
		if !has {
			t.Fatalf("%q: no value", text)
		}
		if _, ok := value.(map[string]any)["text"]; !ok {
			t.Fatalf("%q dropped the field: %#v", text, value)
		}
	}
	parser := NewStreamingJSONParser()
	parser.Append(`{"text":"a\d"}`)
	if value, _, _ := parser.Snapshot(); value.(map[string]any)["text"] != `a\d` {
		t.Fatalf("invalid escape = %#v", value)
	}
}

func TestStreamingJSONParserSnapshotsNeverChange(t *testing.T) {
	parser := NewStreamingJSONParser()
	parser.Append(`{"a":{"b":[1,2`)
	first, _, fresh := parser.Snapshot()
	if !fresh {
		t.Fatal("first snapshot should be fresh")
	}
	firstCopy := deepCopyJSON(t, first)
	parser.Append(`,3]},"c":true}`)
	second, _, _ := parser.Snapshot()
	if reflect.DeepEqual(first, second) {
		t.Fatal("expected the snapshot to grow")
	}
	if !reflect.DeepEqual(first, firstCopy) {
		t.Fatalf("earlier snapshot changed: %#v, was %#v", first, firstCopy)
	}
}

func deepCopyJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var copyValue any
	if err := json.Unmarshal(data, &copyValue); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return copyValue
}

func TestStreamingJSONParserLenientMalformed(t *testing.T) {
	cases := map[string]any{
		`{"a" 1}`:                     map[string]any{"a": float64(1)},
		`{"a":1 "b":2}`:               map[string]any{"a": float64(1), "b": float64(2)},
		`[1,,2]`:                      []any{float64(1), float64(2)},
		`{"a":1} trailing garbage`:    map[string]any{"a": float64(1)},
		`}`:                           nil,
		`{"a":}`:                      map[string]any{},
		`{"a":1}{"b":2}`:              map[string]any{"a": float64(1)},
		`{"a":tru}`:                   map[string]any{},
		`{"a":1e}`:                    map[string]any{},
		`{"a":[],"b":{},"c":invalid}`: map[string]any{"a": []any{}, "b": map[string]any{}},
	}
	for text, want := range cases {
		value, has := feedParser(t, text, 1)
		if want == nil && !has {
			continue
		}
		if !reflect.DeepEqual(value, want) {
			t.Fatalf("%q = %#v, want %#v", text, value, want)
		}
	}
}

func TestStreamingJSONParserChanges(t *testing.T) {
	parser := NewStreamingJSONParser()
	changes := parser.Append(`{"a":"x`)
	if len(changes) < 2 {
		t.Fatalf("changes = %#v", changes)
	}
	if changes[0].Kind != "set" || len(changes[0].Path) != 0 {
		t.Fatalf("first change = %#v", changes[0])
	}
	last := changes[len(changes)-1]
	if last.Kind != "set" || last.Value != "x" || !reflect.DeepEqual(last.Path, JSONPath{"a"}) {
		t.Fatalf("string change = %#v", last)
	}
	changes = parser.Append(`y"}`)
	last = changes[len(changes)-1]
	if last.Kind != "append" || last.Text != "y" {
		t.Fatalf("continued string change = %#v", last)
	}
}

func TestStreamingJSONParserDeepNesting(t *testing.T) {
	depth := 2000
	var builder strings.Builder
	for index := 0; index < depth; index++ {
		builder.WriteByte('[')
	}
	for index := 0; index < depth; index++ {
		builder.WriteByte(']')
	}
	value, has := feedParser(t, builder.String(), 1)
	if !has {
		t.Fatal("no value")
	}
	current := value
	for index := 0; index < depth-1; index++ {
		next, ok := current.([]any)
		if !ok || len(next) != 1 {
			t.Fatalf("depth %d = %#v", index, current)
		}
		current = next[0]
	}
	if innermost, ok := current.([]any); !ok || len(innermost) != 0 {
		t.Fatalf("innermost = %#v", current)
	}
}

func TestStreamingJSONParserNeverPanicsOnCorruption(t *testing.T) {
	documents := []string{`{"a":1,"b":[true,null,"x"]}`, `[1,2,{"c":"d"}]`, `"text"`}
	random := rand.New(rand.NewSource(42))
	const alphabet = "{}[],:\"\\ abcdef0123456789-+.eEtrufalsn\x01\xff"
	for _, document := range documents {
		for iteration := 0; iteration < 500; iteration++ {
			data := []byte(document)
			for edits := 0; edits < 1+random.Intn(4); edits++ {
				position := random.Intn(len(data) + 1)
				if random.Intn(2) == 0 && position < len(data) {
					data = append(data[:position], data[position+1:]...)
				} else {
					data = append(data[:position], append([]byte{alphabet[random.Intn(len(alphabet))]}, data[position:]...)...)
				}
			}
			feedParser(t, string(data), 1)
			feedParser(t, string(data), 2)
		}
	}
}

func TestJSONTestSuiteDocuments(t *testing.T) {
	raw, err := os.ReadFile("testdata/json-test-suite.json")
	if err != nil {
		t.Fatalf("read test data: %v", err)
	}
	var suite struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatalf("decode test data: %v", err)
	}
	checked := 0
	for name, encoded := range suite.Files {
		document, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// pi parses JS strings; Go strings can carry invalid UTF-8 that the
		// upstream decoder would already have replaced. Skip those documents.
		if !utf8.Valid(document) {
			continue
		}
		var want any
		parseErr := json.Unmarshal(document, &want)
		for _, chunk := range []int{1, 2, 5, 13} {
			got, has := feedParser(t, string(document), chunk)
			if parseErr == nil {
				if !has || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s chunk %d = %#v, want %#v", name, chunk, got, want)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no documents checked")
	}
}

func TestToolCallArgumentsStream(t *testing.T) {
	stream := NewToolCallArgumentsStream()
	if got := stream.Append(`{"path":"/tmp/`); got["path"] != "/tmp/" {
		t.Fatalf("partial = %#v", got)
	}
	if got := stream.Append(`file.txt"}`); got["path"] != "/tmp/file.txt" {
		t.Fatalf("partial = %#v", got)
	}
	arguments, argumentsError := stream.Finish()
	if argumentsError != "" || arguments["path"] != "/tmp/file.txt" {
		t.Fatalf("finish = %#v, %q", arguments, argumentsError)
	}
	if stream.Text() != `{"path":"/tmp/file.txt"}` {
		t.Fatalf("text = %q", stream.Text())
	}

	nonObject := NewToolCallArgumentsStream()
	if got := nonObject.Append(`[1,2`); len(got) != 0 {
		t.Fatalf("non-object root = %#v", got)
	}

	malformed := NewToolCallArgumentsStream()
	malformed.Append(`{"a":`)
	arguments, argumentsError = malformed.Finish()
	if argumentsError == "" || len(arguments) != 0 {
		t.Fatalf("malformed finish = %#v, %q", arguments, argumentsError)
	}
}

func TestToolCallArgumentsStreamReplace(t *testing.T) {
	stream := NewToolCallArgumentsStream()
	stream.Append(`{"a":1`)
	delta, hasDelta, arguments := stream.Replace(`{"a":1,"b":2}`)
	if !hasDelta || delta != `,"b":2}` || arguments["b"] != float64(2) {
		t.Fatalf("replace = %q %v %#v", delta, hasDelta, arguments)
	}
	delta, hasDelta, arguments = stream.Replace(`{"a":1,"b":2}`)
	if hasDelta || delta != "" || arguments["a"] != float64(1) {
		t.Fatalf("same text = %q %v %#v", delta, hasDelta, arguments)
	}
	delta, hasDelta, arguments = stream.Replace(`{"c":3}`)
	if hasDelta || arguments["c"] != float64(3) {
		t.Fatalf("restart = %q %v %#v", delta, hasDelta, arguments)
	}
}
