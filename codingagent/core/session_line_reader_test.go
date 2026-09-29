// session_line_reader_test.go — guards for the chunked line reader
// (session_manager.go). Long lines are the interesting shape: the reader
// re-scans its pending buffer on every refill, so the scan primitive decides
// whether large sessions cost milliseconds or tens of seconds (see
// docs/feat/TEST-SUITE-RUNTIME.md).

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionLineReaderSplitsLinesLargerThanTheReadBuffer(t *testing.T) {
	long := strings.Repeat("a", 4<<20) // 4 MiB: many refills for a single line
	file := filepath.Join(t.TempDir(), "long-line.jsonl")
	if err := os.WriteFile(file, []byte(long+"\ntail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	reader := newSessionLineReader(f, 64*1024)
	line, ok := reader.next()
	if !ok || line != long {
		t.Fatalf("first line: ok=%v len=%d want %d", ok, len(line), len(long))
	}
	line, ok = reader.next()
	if !ok || line != "tail" {
		t.Fatalf("second line: ok=%v %q", ok, line)
	}
	if _, ok = reader.next(); ok {
		t.Fatal("expected EOF after the trailing newline")
	}
	if reader.hadTrailing {
		t.Fatal("hadTrailing must stay false for a newline-terminated file")
	}
}

// BenchmarkSessionLineReaderLongLine pins the scan cost. The reader re-scans
// pending from index 0 on every refill, so a hand-rolled per-byte scan (the
// pre-optimization shape) is ~3 orders of magnitude slower here and turns the
// 512MB session fixture into a ~35s test under -race.
func BenchmarkSessionLineReaderLongLine(b *testing.B) {
	long := strings.Repeat("a", 4<<20)
	file := filepath.Join(b.TempDir(), "long-line.jsonl")
	if err := os.WriteFile(file, []byte(long+"\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(long)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := os.Open(file)
		if err != nil {
			b.Fatal(err)
		}
		reader := newSessionLineReader(f, 64*1024)
		if line, ok := reader.next(); !ok || line != long {
			f.Close()
			b.Fatalf("read failed: ok=%v len=%d want %d", ok, len(line), len(long))
		}
		f.Close()
	}
}
