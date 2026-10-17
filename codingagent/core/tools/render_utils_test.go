// render_utils_test.go — regression coverage for the shared read line-range
// label (pi: test/read-line-range-display.test.ts, #9887/#10938). String
// offset/limit from raw JSON args must render the same range as numbers, and
// malformed ranges must be omitted rather than printed as garbage.
package tools

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

func TestFormatReadLineRange(t *testing.T) {
	cases := []struct {
		name   string
		offset any
		limit  any
		want   string
	}{
		{"numbers", 25.0, 13.0, ":25-37"},
		{"numeric strings", "25", "13", ":25-37"},
		{"null offset", nil, "13", ":1-13"},
		{"null limit", "25", nil, ":25"},
		{"all null", nil, nil, ""},
		{"non-numeric offset", "abc", 13.0, ""},
		{"empty limit", 25.0, "", ""},
		{"infinite limit", 25.0, "Infinity", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatReadLineRange(tc.offset, tc.limit); got != tc.want {
				t.Fatalf("FormatReadLineRange(%#v, %#v) = %q, want %q", tc.offset, tc.limit, got, tc.want)
			}
		})
	}
}

// String offset/limit (as decoded from raw JSON args) must show the range in
// the read call row, matching the numeric case.
func TestReadRenderCallStringLineRange(t *testing.T) {
	theme.InitTheme("dark", false)
	args := map[string]any{"path": "notes.txt", "offset": "25", "limit": "13"}
	component := ReadRenderers.RenderCall(args, theme.Current(), ToolRenderContext{
		Args: args,
		Cwd:  "/tmp",
	})
	body := utils.StripAnsi(strings.Join(component.Render(120), "\n"))
	if !strings.Contains(body, "read notes.txt:25-37") {
		t.Fatalf("read call = %q, want it to contain %q", body, "read notes.txt:25-37")
	}
}
