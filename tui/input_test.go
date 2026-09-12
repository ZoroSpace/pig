// tui/input_test.go — ported from pi: packages/tui/test/input.test.ts
//
// Declared deviations:
//   - CJK word-deletion expectations are byte conversions of pi's UTF-16
//     values; the CJK dictionary grouping (pi: ICU, pig: gse — see
//     cjk_segmenter.go) reproduces pi's exact deletion counts, and fullwidth
//     punctuation stays an independent boundary.

package tui

import (
	"github.com/samber/lo"
	"strings"
	"testing"
)

func TestInputSubmit(t *testing.T) {
	t.Run("submits value including backslash on Enter", func(t *testing.T) {
		input := NewInput()
		var submitted string

		input.OnSubmit = func(value string) {
			submitted = value
		}

		// Type hello, then backslash, then Enter
		for _, ch := range []string{"h", "e", "l", "l", "o", "\\", "\r"} {
			input.HandleInput(ch)
		}

		// Input is single-line, no backslash+Enter workaround
		if submitted != "hello\\" {
			t.Errorf("submitted = %q, want %q", submitted, "hello\\")
		}
	})

	t.Run("inserts backslash as regular character", func(t *testing.T) {
		input := NewInput()

		input.HandleInput("\\")
		input.HandleInput("x")

		if input.GetValue() != "\\x" {
			t.Errorf("value = %q, want %q", input.GetValue(), "\\x")
		}
	})
}

func TestInputRender(t *testing.T) {
	t.Run("supports a custom prompt and styled placeholder", func(t *testing.T) {
		input := NewInput(&InputOptions{
			Prompt:           lo.ToPtr(""),
			Placeholder:      "Find transcript",
			PlaceholderStyle: func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
		})
		input.Focused = true

		empty := input.Render(20)[0]
		if !strings.Contains(empty, "\x1b[2m") {
			t.Errorf("placeholder not styled: %q", empty)
		}
		if got := strings.TrimRight(StripTerminalSequences(empty), " "); got != "Find transcript" {
			t.Errorf("placeholder text = %q, want Find transcript", got)
		}

		input.HandleInput("n")
		populated := input.Render(20)[0]
		if got := strings.TrimRight(StripTerminalSequences(populated), " "); got != "n" {
			t.Errorf("populated text = %q, want n", got)
		}
	})

	t.Run("does not overflow with wide CJK and fullwidth text", func(t *testing.T) {
		width := 93
		cases := []string{
			"가나다라마바사아자차카타파하 한글 텍스트가 터미널 너비를 초과하면 크래시가 발생합니다 이것은 재현용 테스트입니다",
			"これはテスト文章です。日本語のテキストが正しく表示されるかどうかを確認するためのサンプルテキストです。あいうえお",
			"这是一段测试文本，用于验证中文字符在终端中的显示宽度是否被正确计算，如果不正确就会导致用户界面崩溃的问题",
			"ＡＢＣＤＥＦＧＨＩＪＫＬＭＮＯＰＱＲＳＴＵＶＷＸＹＺ０１２３４５６７８９ａｂｃｄｅｆｇｈｉｊｋｌｍ",
		}
		type cursorMove struct {
			label string
			move  func(input *Input)
		}
		cursorPositions := []cursorMove{
			{label: "start", move: func(input *Input) {}},
			{label: "middle", move: func(input *Input) {
				for i := 0; i < 10; i++ {
					input.HandleInput("\x1b[C")
				}
			}},
			{label: "end", move: func(input *Input) { input.HandleInput("\x05") }},
		}

		for _, text := range cases {
			for _, cp := range cursorPositions {
				input := NewInput()
				input.SetValue(text)
				input.Focused = true
				cp.move(input)

				lines := input.Render(width)
				if len(lines) == 0 {
					t.Fatalf("no rendered line for %q at %s", text, cp.label)
				}
				if got := VisibleWidth(lines[0]); got > width {
					t.Errorf("rendered line overflowed (%d > %d) for %q at %s", got, width, text, cp.label)
				}
			}
		}
	})

	t.Run("keeps the cursor visible when horizontally scrolling wide text", func(t *testing.T) {
		input := NewInput()
		width := 20
		text := "가나다라마바사아자차카타파하"
		input.SetValue(text)
		input.Focused = true
		input.HandleInput("\x01")
		for i := 0; i < 5; i++ {
			input.HandleInput("\x1b[C")
		}

		lines := input.Render(width)
		if len(lines) == 0 {
			t.Fatal("no rendered line")
		}
		if got := VisibleWidth(lines[0]); got > width {
			t.Errorf("rendered line overflowed (%d > %d)", got, width)
		}
	})
}

func TestInputKillRing(t *testing.T) {
	t.Run("Ctrl+W saves deleted text to kill ring and Ctrl+Y yanks it", func(t *testing.T) {
		input := NewInput()

		input.SetValue("foo bar baz")
		// Move cursor to end
		input.HandleInput("\x05") // Ctrl+E

		input.HandleInput("\x17") // Ctrl+W - deletes "baz"
		if input.GetValue() != "foo bar " {
			t.Errorf("value = %q", input.GetValue())
		}

		// Move to beginning and yank
		input.HandleInput("\x01") // Ctrl+A
		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "bazfoo bar " {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Ctrl+W preserves ASCII punctuation boundaries", func(t *testing.T) {
		input := NewInput()

		input.SetValue("foo.bar")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W - deletes "bar"
		if input.GetValue() != "foo." {
			t.Errorf("value = %q, want foo.", input.GetValue())
		}

		input.SetValue("foo:bar")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W - deletes "bar"
		if input.GetValue() != "foo:" {
			t.Errorf("value = %q, want foo:", input.GetValue())
		}
	})

	t.Run("Ctrl+W handles Unicode word boundaries", func(t *testing.T) {
		// pi (ICU dictionary): "你好世界。你好，世界" segments as
		// 你好|世界|。|你好|，|世界 — 6 deletions, reproduced via the gse
		// dictionary (cjk_segmenter.go). Fullwidth punctuation stays an
		// independent boundary.
		requireCJKDict(t)
		input := NewInput()

		input.SetValue("你好世界。你好，世界")
		input.HandleInput("\x05") // Ctrl+E
		want := []string{
			"你好世界。你好，",
			"你好世界。你好",
			"你好世界。",
			"你好世界",
			"你好",
			"",
		}
		for _, w := range want {
			input.HandleInput("\x17") // Ctrl+W
			if input.GetValue() != w {
				t.Fatalf("after Ctrl+W value = %q, want %q", input.GetValue(), w)
			}
		}
	})

	t.Run("Ctrl+U saves deleted text to kill ring", func(t *testing.T) {
		input := NewInput()

		input.SetValue("hello world")
		// Move cursor to after "hello "
		input.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			input.HandleInput("\x1b[C")
		}

		input.HandleInput("\x15") // Ctrl+U - deletes "hello "
		if input.GetValue() != "world" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "hello world" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Ctrl+K saves deleted text to kill ring", func(t *testing.T) {
		input := NewInput()

		input.SetValue("hello world")
		input.HandleInput("\x01") // Ctrl+A
		input.HandleInput("\x0b") // Ctrl+K - deletes "hello world"

		if input.GetValue() != "" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "hello world" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Ctrl+Y does nothing when kill ring is empty", func(t *testing.T) {
		input := NewInput()

		input.SetValue("test")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "test" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Alt+Y cycles through kill ring after Ctrl+Y", func(t *testing.T) {
		input := NewInput()

		// Create kill ring with multiple entries
		for _, v := range []string{"first", "second", "third"} {
			input.SetValue(v)
			input.HandleInput("\x05") // Ctrl+E
			input.HandleInput("\x17") // Ctrl+W
		}

		if input.GetValue() != "" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x19") // Ctrl+Y - yanks "third"
		if input.GetValue() != "third" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1by") // Alt+Y - cycles to "second"
		if input.GetValue() != "second" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1by") // Alt+Y - cycles to "first"
		if input.GetValue() != "first" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1by") // Alt+Y - cycles back to "third"
		if input.GetValue() != "third" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Alt+Y does nothing if not preceded by yank", func(t *testing.T) {
		input := NewInput()

		input.SetValue("test")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W - deletes "test"
		input.SetValue("other")
		input.HandleInput("\x05") // Ctrl+E

		// Type something to break the yank chain
		input.HandleInput("x")
		if input.GetValue() != "otherx" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1by") // Alt+Y - should do nothing
		if input.GetValue() != "otherx" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Alt+Y does nothing if kill ring has one entry", func(t *testing.T) {
		input := NewInput()

		input.SetValue("only")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W - deletes "only"

		input.HandleInput("\x19") // Ctrl+Y - yanks "only"
		if input.GetValue() != "only" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1by") // Alt+Y - should do nothing
		if input.GetValue() != "only" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("consecutive Ctrl+W accumulates into one kill ring entry", func(t *testing.T) {
		input := NewInput()

		input.SetValue("one two three")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W - deletes "three"
		input.HandleInput("\x17") // Ctrl+W - deletes "two "
		input.HandleInput("\x17") // Ctrl+W - deletes "one "

		if input.GetValue() != "" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "one two three" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("non-delete actions break kill accumulation", func(t *testing.T) {
		input := NewInput()

		input.SetValue("foo bar baz")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W - deletes "baz"
		if input.GetValue() != "foo bar " {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("x") // Typing breaks accumulation
		if input.GetValue() != "foo bar x" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x17") // Ctrl+W - deletes "x" (separate entry)
		if input.GetValue() != "foo bar " {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x19") // Ctrl+Y - most recent is "x"
		if input.GetValue() != "foo bar x" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1by") // Alt+Y - cycle to "baz"
		if input.GetValue() != "foo bar baz" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("non-yank actions break Alt+Y chain", func(t *testing.T) {
		input := NewInput()

		input.SetValue("first")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W
		input.SetValue("second")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W
		input.SetValue("")

		input.HandleInput("\x19") // Ctrl+Y - yanks "second"
		if input.GetValue() != "second" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("x") // Breaks yank chain
		if input.GetValue() != "secondx" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1by") // Alt+Y - should do nothing
		if input.GetValue() != "secondx" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("kill ring rotation persists after cycling", func(t *testing.T) {
		input := NewInput()

		for _, v := range []string{"first", "second", "third"} {
			input.SetValue(v)
			input.HandleInput("\x05") // Ctrl+E
			input.HandleInput("\x17") // deletes value
		}
		input.SetValue("")

		input.HandleInput("\x19")  // Ctrl+Y - yanks "third"
		input.HandleInput("\x1by") // Alt+Y - cycles to "second"
		if input.GetValue() != "second" {
			t.Errorf("value = %q", input.GetValue())
		}

		// Break chain and start fresh
		input.HandleInput("x")
		input.SetValue("")

		// New yank should get "second" (now at end after rotation)
		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "second" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("backward deletions prepend, forward deletions append during accumulation", func(t *testing.T) {
		input := NewInput()

		input.SetValue("prefix|suffix")
		// Position cursor at "|"
		input.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			input.HandleInput("\x1b[C") // Move right 6
		}

		input.HandleInput("\x0b") // Ctrl+K - deletes "|suffix" (forward)
		if input.GetValue() != "prefix" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "prefix|suffix" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Alt+D deletes word forward and saves to kill ring", func(t *testing.T) {
		input := NewInput()

		input.SetValue("hello world test")
		input.HandleInput("\x01") // Ctrl+A

		input.HandleInput("\x1bd") // Alt+D - deletes "hello"
		if input.GetValue() != " world test" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1bd") // Alt+D - deletes " world"
		if input.GetValue() != " test" {
			t.Errorf("value = %q", input.GetValue())
		}

		// Yank should get accumulated text
		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "hello world test" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Alt+D preserves ASCII punctuation boundaries", func(t *testing.T) {
		input := NewInput()

		input.SetValue("foo.bar baz")
		input.HandleInput("\x01")  // Ctrl+A
		input.HandleInput("\x1bd") // Alt+D - deletes "foo"
		if input.GetValue() != ".bar baz" {
			t.Errorf("value = %q", input.GetValue())
		}
		input.HandleInput("\x1bd") // Alt+D - deletes "."
		if input.GetValue() != "bar baz" {
			t.Errorf("value = %q", input.GetValue())
		}
		input.HandleInput("\x1bd") // Alt+D - deletes "bar"
		if input.GetValue() != " baz" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("Alt+D handles Unicode word boundaries", func(t *testing.T) {
		// pi (ICU dictionary): 你好|世界|。|你好|，|世界 — 6 deletions,
		// reproduced via the gse dictionary (cjk_segmenter.go).
		requireCJKDict(t)
		input := NewInput()

		input.SetValue("你好世界。你好，世界")
		input.HandleInput("\x01") // Ctrl+A
		want := []string{
			"世界。你好，世界",
			"。你好，世界",
			"你好，世界",
			"，世界",
			"世界",
			"",
		}
		for _, w := range want {
			input.HandleInput("\x1bd") // Alt+D
			if input.GetValue() != w {
				t.Fatalf("after Alt+D value = %q, want %q", input.GetValue(), w)
			}
		}
	})

	t.Run("handles yank in middle of text", func(t *testing.T) {
		input := NewInput()

		input.SetValue("word")
		input.HandleInput("\x05") // Ctrl+E
		input.HandleInput("\x17") // Ctrl+W - deletes "word"
		input.SetValue("hello world")
		// Move to middle (after "hello ")
		input.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			input.HandleInput("\x1b[C")
		}

		input.HandleInput("\x19") // Ctrl+Y
		if input.GetValue() != "hello wordworld" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("handles yank-pop in middle of text", func(t *testing.T) {
		input := NewInput()

		// Create two kill ring entries
		for _, v := range []string{"FIRST", "SECOND"} {
			input.SetValue(v)
			input.HandleInput("\x05") // Ctrl+E
			input.HandleInput("\x17") // Ctrl+W - deletes value
		}

		// Set up "hello world" and position cursor after "hello "
		input.SetValue("hello world")
		input.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			input.HandleInput("\x1b[C")
		}

		input.HandleInput("\x19") // Ctrl+Y - yanks "SECOND"
		if input.GetValue() != "hello SECONDworld" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput("\x1by") // Alt+Y - replaces with "FIRST"
		if input.GetValue() != "hello FIRSTworld" {
			t.Errorf("value = %q", input.GetValue())
		}
	})
}

func TestInputUndo(t *testing.T) {
	undoKey := "\x1b[45;5u" // Ctrl+- (undo)

	t.Run("does nothing when undo stack is empty", func(t *testing.T) {
		input := NewInput()

		input.HandleInput(undoKey)
		if input.GetValue() != "" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("coalesces consecutive word characters into one undo unit", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			input.HandleInput(ch)
		}
		if input.GetValue() != "hello world" {
			t.Errorf("value = %q", input.GetValue())
		}

		// Undo removes " world"
		input.HandleInput(undoKey)
		if input.GetValue() != "hello" {
			t.Errorf("value = %q", input.GetValue())
		}

		// Undo removes "hello"
		input.HandleInput(undoKey)
		if input.GetValue() != "" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes spaces one at a time", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", " "} {
			input.HandleInput(ch)
		}
		if input.GetValue() != "hello  " {
			t.Errorf("value = %q", input.GetValue())
		}

		// Undo removes second " "
		input.HandleInput(undoKey)
		if input.GetValue() != "hello " {
			t.Errorf("value = %q", input.GetValue())
		}

		// Undo removes first " "
		input.HandleInput(undoKey)
		if input.GetValue() != "hello" {
			t.Errorf("value = %q", input.GetValue())
		}

		// Undo removes "hello"
		input.HandleInput(undoKey)
		if input.GetValue() != "" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes backspace", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"h", "e", "l", "l", "o"} {
			input.HandleInput(ch)
		}
		input.HandleInput("\x7f") // Backspace
		if input.GetValue() != "hell" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput(undoKey)
		if input.GetValue() != "hello" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes forward delete", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"h", "e", "l", "l", "o"} {
			input.HandleInput(ch)
		}
		input.HandleInput("\x01")    // Ctrl+A - go to start
		input.HandleInput("\x1b[C")  // Right arrow
		input.HandleInput("\x1b[3~") // Delete key
		if input.GetValue() != "hllo" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput(undoKey)
		if input.GetValue() != "hello" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes Ctrl+W (delete word backward)", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			input.HandleInput(ch)
		}
		input.HandleInput("\x17") // Ctrl+W
		if input.GetValue() != "hello " {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput(undoKey)
		if input.GetValue() != "hello world" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes Ctrl+K (delete to line end)", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			input.HandleInput(ch)
		}
		input.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			input.HandleInput("\x1b[C")
		}

		input.HandleInput("\x0b") // Ctrl+K
		if input.GetValue() != "hello " {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput(undoKey)
		if input.GetValue() != "hello world" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes Ctrl+U (delete to line start)", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
			input.HandleInput(ch)
		}
		input.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 6; i++ {
			input.HandleInput("\x1b[C")
		}

		input.HandleInput("\x15") // Ctrl+U
		if input.GetValue() != "world" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput(undoKey)
		if input.GetValue() != "hello world" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes yank", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"h", "e", "l", "l", "o", " "} {
			input.HandleInput(ch)
		}
		input.HandleInput("\x17") // Ctrl+W - delete "hello "
		input.HandleInput("\x19") // Ctrl+Y - yank
		if input.GetValue() != "hello " {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput(undoKey)
		if input.GetValue() != "" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes paste atomically", func(t *testing.T) {
		input := NewInput()

		input.SetValue("hello world")
		input.HandleInput("\x01") // Ctrl+A
		for i := 0; i < 5; i++ {
			input.HandleInput("\x1b[C")
		}

		// Simulate bracketed paste
		input.HandleInput("\x1b[200~beep boop\x1b[201~")
		if input.GetValue() != "hellobeep boop world" {
			t.Errorf("value = %q", input.GetValue())
		}

		// Single undo should restore entire pre-paste state
		input.HandleInput(undoKey)
		if input.GetValue() != "hello world" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("undoes Alt+D (delete word forward)", func(t *testing.T) {
		input := NewInput()

		input.SetValue("hello world")
		input.HandleInput("\x01") // Ctrl+A

		input.HandleInput("\x1bd") // Alt+D - deletes "hello"
		if input.GetValue() != " world" {
			t.Errorf("value = %q", input.GetValue())
		}

		input.HandleInput(undoKey)
		if input.GetValue() != "hello world" {
			t.Errorf("value = %q", input.GetValue())
		}
	})

	t.Run("cursor movement starts new undo unit", func(t *testing.T) {
		input := NewInput()

		for _, ch := range []string{"a", "b", "c"} {
			input.HandleInput(ch)
		}
		input.HandleInput("\x01") // Ctrl+A - movement breaks coalescing
		input.HandleInput("\x05") // Ctrl+E
		for _, ch := range []string{"d", "e"} {
			input.HandleInput(ch)
		}
		if input.GetValue() != "abcde" {
			t.Errorf("value = %q", input.GetValue())
		}

		// Undo removes "de" (typed after movement)
		input.HandleInput(undoKey)
		if input.GetValue() != "abc" {
			t.Errorf("value = %q", input.GetValue())
		}

		// Undo removes "abc"
		input.HandleInput(undoKey)
		if input.GetValue() != "" {
			t.Errorf("value = %q", input.GetValue())
		}
	})
}
