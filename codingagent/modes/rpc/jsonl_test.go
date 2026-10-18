// Ported from pi's test/rpc-jsonl.test.ts.
package rpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

type lineCollector struct {
	mu    sync.Mutex
	lines []string
}

func (c *lineCollector) add(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
}

func (c *lineCollector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

func TestSerializeJsonLine(t *testing.T) {
	t.Run("serializes strict JSONL records without escaping Unicode separators", func(t *testing.T) {
		line, err := SerializeJsonLine(map[string]any{"text": "a\u2028b\u2029c"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(line, "a\u2028b\u2029c") {
			t.Fatalf("line = %q, want it to contain the raw Unicode separators", line)
		}
		if !strings.HasSuffix(line, "\n") {
			t.Fatalf("line = %q, want a trailing LF", line)
		}
		var parsed struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Text != "a\u2028b\u2029c" {
			t.Fatalf("parsed.Text = %q", parsed.Text)
		}
	})

	t.Run("does not unescape a literal backslash-u2028 sequence", func(t *testing.T) {
		line, err := SerializeJsonLine(map[string]any{"text": `\u2028`})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(line, `"\\u2028"`) {
			t.Fatalf("line = %q, want the literal sequence preserved as an escaped backslash", line)
		}
	})
}

func TestAttachJsonlLineReader(t *testing.T) {
	t.Run("splits on LF only and preserves U+2028/U+2029 inside payloads", func(t *testing.T) {
		collector := &lineCollector{}
		line, err := SerializeJsonLine(map[string]any{"text": "a\u2028b\u2029c"})
		if err != nil {
			t.Fatal(err)
		}
		_, done := AttachJsonlLineReader(bytes.NewReader([]byte(line)), collector.add)
		<-done

		lines := collector.snapshot()
		if len(lines) != 1 {
			t.Fatalf("lines = %d, want 1", len(lines))
		}
		var parsed struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Text != "a\u2028b\u2029c" {
			t.Fatalf("parsed.Text = %q", parsed.Text)
		}
	})

	t.Run("handles CRLF-delimited input", func(t *testing.T) {
		collector := &lineCollector{}
		_, done := AttachJsonlLineReader(bytes.NewReader([]byte("{\"a\":1}\r\n{\"b\":2}\r\n")), collector.add)
		<-done

		lines := collector.snapshot()
		if len(lines) != 2 || lines[0] != `{"a":1}` || lines[1] != `{"b":2}` {
			t.Fatalf("lines = %v", lines)
		}
	})

	t.Run("emits a final line without trailing LF", func(t *testing.T) {
		collector := &lineCollector{}
		_, done := AttachJsonlLineReader(bytes.NewReader([]byte(`{"a":1}`)), collector.add)
		<-done

		lines := collector.snapshot()
		if len(lines) != 1 || lines[0] != `{"a":1}` {
			t.Fatalf("lines = %v", lines)
		}
	})
}
