// render_path_args_test.go — regression tests for rawPathFromArgs's
// str() parity (docs/fix/2026-09-11-toolcall-title-invalid-arg.md).
//
// pi's str() maps undefined (missing key / absent args object — the normal
// state while streaming partial tool-call args) and null to "" which renders
// as the emptyFallback ("..."), and only a present-but-non-string value to
// null → "[invalid arg]". rawPathFromArgs used to map a missing key to nil
// as well, so write/edit/read/ls call rows flashed or stuck on
// "[invalid arg]" during streaming; pi shows "..." until the path arrives.
package tools

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/samber/lo"
)

func TestRawPathFromArgsStrParity(t *testing.T) {
	theme.InitTheme("dark", false)
	th := theme.Current()

	cases := []struct {
		name string
		args any
		want *string // "" = absent (emptyFallback), set = path, nil = invalid
	}{
		{"nil args (streaming start)", nil, lo.ToPtr("")},
		{"non-map args", "garbage", lo.ToPtr("")},
		{"empty args object", map[string]any{}, lo.ToPtr("")},
		{"missing path key", map[string]any{"content": "x"}, lo.ToPtr("")},
		{"null path", map[string]any{"path": nil}, lo.ToPtr("")},
		{"string path", map[string]any{"path": "/tmp/a.md"}, lo.ToPtr("/tmp/a.md")},
		{"file_path preferred over path", map[string]any{"file_path": "/tmp/b.md", "path": "/tmp/a.md"}, lo.ToPtr("/tmp/b.md")},
		{"null file_path falls back to path", map[string]any{"file_path": nil, "path": "/tmp/c.md"}, lo.ToPtr("/tmp/c.md")},
		{"non-string path is invalid", map[string]any{"path": 123}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rawPathFromArgs(tc.args)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("got %q, want nil (invalid)", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("got nil, want %q", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Fatalf("got %q, want %q", *got, *tc.want)
			}
		})
	}

	// Render-level lock: absent args render the "..." fallback, not
	// "[invalid arg]" — the visible symptom.
	if got := RenderToolPath(rawPathFromArgs(nil), th, "/tmp", ""); strings.Contains(got, "[invalid arg]") {
		t.Fatalf("nil args rendered %q, want the ... fallback", got)
	}
	if got := RenderToolPath(rawPathFromArgs(map[string]any{}), th, "/tmp", "."); strings.Contains(got, "[invalid arg]") {
		t.Fatalf("empty args rendered %q, want the emptyFallback", got)
	}
	if got := RenderToolPath(rawPathFromArgs(map[string]any{"path": 123}), th, "/tmp", ""); !strings.Contains(got, "[invalid arg]") {
		t.Fatalf("non-string path rendered %q, want [invalid arg]", got)
	}
}
