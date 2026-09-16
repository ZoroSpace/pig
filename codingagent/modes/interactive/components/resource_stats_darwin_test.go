//go:build darwin

// resource_stats_darwin_test.go — the /usr/bin/footprint sampler behind
// the footer resource segment's MEM value (Activity Monitor parity).
package components

import "testing"

const footprintSample = "======================================================================\n" +
	"footprobe [49970]: 64-bit    Footprint: 56164880 B (16384 bytes per page)\n" +
	"======================================================================\n"

// TestParseFootprintOutput locks the "Footprint: <N> B" parsing: the
// real summary shape (header rules + process line + page-size note),
// a bare single line, and the malformed shapes that must fail (missing
// line, non-numeric value, non-bytes unit) so the sampler falls back
// to ps RSS instead of publishing garbage.
func TestParseFootprintOutput(t *testing.T) {
	if b, ok := parseFootprintOutput([]byte(footprintSample)); !ok || b != 56164880 {
		t.Fatalf("sample output: got (%d, %v), want (56164880, true)", b, ok)
	}
	if b, ok := parseFootprintOutput([]byte("Footprint: 944 B (16384 bytes per page)\n")); !ok || b != 944 {
		t.Fatalf("bare line: got (%d, %v), want (944, true)", b, ok)
	}
	for _, bad := range []string{
		"no footprint marker here\n",
		"Footprint: abc B\n",
		"Footprint: 944 KB (16384 bytes per page)\n",
		"Footprint: 0 B\n",
	} {
		if b, ok := parseFootprintOutput([]byte(bad)); ok {
			t.Fatalf("malformed output %q parsed as (%d, true), want failure", bad, b)
		}
	}
}

// TestReadFootprintBytesSanity: the real tool yields a plausible byte
// count for our own pid without root.
func TestReadFootprintBytesSanity(t *testing.T) {
	b, ok := readFootprintBytes()
	if !ok {
		t.Skip("/usr/bin/footprint unavailable on this machine")
	}
	const mb = 1024 * 1024
	const gb = 1024 * mb
	if b < mb || b > 64*gb {
		t.Fatalf("implausible footprint %d bytes", b)
	}
}
