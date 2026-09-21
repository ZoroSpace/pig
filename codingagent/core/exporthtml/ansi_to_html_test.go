// ansi_to_html_test.go — table-driven ports of pi's ansi-to-html behavior
// (pi has no dedicated test file; P8-EH1 builds the coverage pig-side).
package exporthtml

import (
	"strings"
	"testing"
)

func TestAnsiToHtmlTable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text unchanged except escaping", "hello <world> & \"friends\"", "hello &lt;world&gt; &amp; &quot;friends&quot;"},
		{"empty", "", ""},
		{"standard fg red", "\x1b[31mred\x1b[0m", `<span style="color:#800000">red</span>`},
		{"bright fg red", "\x1b[91mbright\x1b[0m", `<span style="color:#ff0000">bright</span>`},
		{"standard bg blue", "\x1b[44mon blue\x1b[0m", `<span style="background-color:#000080">on blue</span>`},
		{"bright bg cyan", "\x1b[106mon cyan\x1b[0m", `<span style="background-color:#00ffff">on cyan</span>`},
		{"bold", "\x1b[1mbold\x1b[0m", `<span style="font-weight:bold">bold</span>`},
		{"dim", "\x1b[2mdim\x1b[0m", `<span style="opacity:0.6">dim</span>`},
		{"italic", "\x1b[3mitalic\x1b[0m", `<span style="font-style:italic">italic</span>`},
		{"underline", "\x1b[4munder\x1b[0m", `<span style="text-decoration:underline">under</span>`},
		{"combined codes in one sequence", "\x1b[1;31mbold red\x1b[0m", `<span style="color:#800000;font-weight:bold">bold red</span>`},
		{
			"22 resets bold and dim only",
			"\x1b[1;2;4mx\x1b[22my\x1b[0m",
			`<span style="font-weight:bold;opacity:0.6;text-decoration:underline">x</span><span style="text-decoration:underline">y</span>`,
		},
		{"23 clears italic", "\x1b[3mi\x1b[23mn\x1b[0m", `<span style="font-style:italic">i</span>n`},
		{"24 clears underline", "\x1b[4mu\x1b[24mn\x1b[0m", `<span style="text-decoration:underline">u</span>n`},
		{"39 default fg", "\x1b[31;1mred\x1b[39mplain\x1b[0m", `<span style="color:#800000;font-weight:bold">red</span><span style="font-weight:bold">plain</span>`},
		{"49 default bg", "\x1b[44mblue\x1b[49mplain\x1b[0m", `<span style="background-color:#000080">blue</span>plain`},
		{"256 color fg", "\x1b[38;5;196mred256\x1b[0m", `<span style="color:#ff0000">red256</span>`},
		{"256 color bg", "\x1b[48;5;21mblue256\x1b[0m", `<span style="background-color:#0000ff">blue256</span>`},
		{"rgb fg", "\x1b[38;2;12;34;56mrgb\x1b[0m", `<span style="color:rgb(12,34,56)">rgb</span>`},
		{"rgb bg", "\x1b[48;2;255;0;128mrgb\x1b[0m", `<span style="background-color:rgb(255,0,128)">rgb</span>`},
		{"empty params means reset", "\x1b[31mred\x1b[mplain", `<span style="color:#800000">red</span>plain`},
		{"unrecognized codes ignored", "\x1b[7;99mtext\x1b[0m", "text"},
		{"truncated extended sequence consumed", "\x1b[38;5mtext\x1b[0m", "text"},
		// pi: params [48,2,1,2] — 48 no-op (len guard), then 2=dim, 1=bold.
		{"truncated rgb consumed", "\x1b[48;2;1;2mtext\x1b[0m", `<span style="font-weight:bold;opacity:0.6">text</span>`},
		{"unclosed span closed at end", "\x1b[31mno reset", `<span style="color:#800000">no reset</span>`},
		{"text between sequences escapes", "\x1b[31ma<b\x1b[32mc>d\x1b[0m", `<span style="color:#800000">a&lt;b</span><span style="color:#008000">c&gt;d</span>`},
		{"leading text before sequence", "plain\x1b[1mbold", "plain<span style=\"font-weight:bold\">bold</span>"},
		{"js parseInt leniency", "\x1b[031mred\x1b[0m", `<span style="color:#800000">red</span>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AnsiToHtml(tt.in); got != tt.want {
				t.Fatalf("AnsiToHtml(%q) =\n%s\nwant:\n%s", tt.in, got, tt.want)
			}
		})
	}
}

func TestColor256ToHexTable(t *testing.T) {
	tests := []struct {
		index int
		want  string
	}{
		{0, "#000000"},
		{7, "#c0c0c0"},
		{15, "#ffffff"},
		{16, "#000000"},  // cube start: 0,0,0
		{17, "#00005f"},  // b=1 → 55+40=95=0x5f
		{196, "#ff0000"}, // r=5, g=0, b=0
		{231, "#ffffff"}, // cube end
		{232, "#080808"}, // grayscale start: 8
		{255, "#eeeeee"}, // 8+23*10=238=0xee
	}
	for _, tt := range tests {
		if got := color256ToHex(tt.index); got != tt.want {
			t.Errorf("color256ToHex(%d) = %s, want %s", tt.index, got, tt.want)
		}
	}
}

func TestAnsiLinesToHtml(t *testing.T) {
	lines := []string{"\x1b[31mred\x1b[0m", "", "plain"}
	want := `<div class="ansi-line"><span style="color:#800000">red</span></div>` +
		`<div class="ansi-line">&nbsp;</div>` +
		`<div class="ansi-line">plain</div>`
	if got := AnsiLinesToHtml(lines); got != want {
		t.Fatalf("AnsiLinesToHtml =\n%s\nwant:\n%s", got, want)
	}
	if got := AnsiLinesToHtml(nil); got != "" {
		t.Fatalf("AnsiLinesToHtml(nil) = %q, want empty", got)
	}
}

func TestAnsiLinesToHtmlWhitespaceLineIsNotNbsp(t *testing.T) {
	// pi: ansiToHtml(line) || "&nbsp;" — a line of spaces escapes to " "
	// (truthy), so it stays as-is; only a fully EMPTY html gets &nbsp;.
	got := AnsiLinesToHtml([]string{" "})
	if !strings.Contains(got, `> </div>`) {
		t.Fatalf("whitespace-only line should keep its spaces, got %q", got)
	}
}
