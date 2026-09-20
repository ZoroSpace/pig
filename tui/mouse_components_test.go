// tui/mouse_components_test.go (pi: packages/tui/test/mouse-components.test.ts)
package tui

import (
	"strings"
	"testing"
)

func mouseEvent(mouseType TuiMouseEventType, x int, y int, width int, height int) *TuiMouseEvent {
	event := &TuiMouseEvent{
		Type:    mouseType,
		Button:  MouseButtonLeft,
		X:       x,
		Y:       y,
		ScreenX: x,
		ScreenY: y,
		Width:   width,
		Height:  height,
	}
	if mouseType == MouseEventTypeClick {
		one := 1
		event.ClickCount = &one
	}
	return event
}

var mouseTestSelectTheme = SelectListTheme{
	SelectedPrefix: func(text string) string { return text },
	SelectedText:   func(text string) string { return text },
	Description:    func(text string) string { return text },
	ScrollInfo:     func(text string) string { return text },
	NoMatch:        func(text string) string { return text },
}

var mouseTestSettingsTheme = SettingsListTheme{
	Label:       func(text string, selected bool) string { return text },
	Value:       func(text string, selected bool) string { return text },
	Description: func(text string) string { return text },
	Cursor:      "> ",
	Hint:        func(text string) string { return text },
}

var mouseTestEditorTheme = EditorTheme{
	BorderColor: func(text string) string { return text },
	SelectList:  mouseTestSelectTheme,
}

func TestMousePositionsASingleLineInputCursorOnPress(t *testing.T) {
	input := NewInput()
	input.SetValue("hello")
	input.Render(20)

	result := input.HandleMouse(mouseEvent(MouseEventTypePress, 4, 0, 20, 1))
	if result == nil || !result.Handled {
		t.Fatal("expected handled press")
	}
	input.HandleInput("X")
	if got := input.GetValue(); got != "heXllo" {
		t.Fatalf("value = %q, want heXllo", got)
	}
}

func TestMouseSelectsAndActivatesListRows(t *testing.T) {
	list := NewSelectList([]SelectItem{
		{Value: "a", Label: "A"},
		{Value: "b", Label: "B"},
		{Value: "c", Label: "C"},
		{Value: "d", Label: "D"},
		{Value: "e", Label: "E"},
	}, 3, mouseTestSelectTheme, nil)
	selected := ""
	list.OnSelect = func(item SelectItem) { selected = item.Value }

	result := list.HandleMouse(mouseEvent(MouseEventTypePress, 1, 2, 40, 3))
	if result == nil || !result.Handled {
		t.Fatal("expected handled press")
	}
	if got := list.GetSelectedItem(); got == nil || got.Value != "c" {
		t.Fatalf("selected = %v, want c", got)
	}
	result = list.HandleMouse(mouseEvent(MouseEventTypeClick, 1, 2, 40, 3))
	if result == nil || !result.Handled {
		t.Fatal("expected handled click")
	}
	if selected != "c" {
		t.Fatalf("onSelect value = %q, want c", selected)
	}
}

func TestMouseActivatesSettingsRows(t *testing.T) {
	type change struct {
		id    string
		value string
	}
	var changes []change
	one := []string{"one", "two"}
	off := []string{"off", "on"}
	low := []string{"low", "high"}
	xy := []string{"x", "y"}
	list := NewSettingsList([]*SettingItem{
		{Id: "mode", Label: "Mode", CurrentValue: "one", Values: one},
		{Id: "other", Label: "Other", CurrentValue: "off", Values: off},
		{Id: "third", Label: "Third", CurrentValue: "low", Values: low},
		{Id: "fourth", Label: "Fourth", CurrentValue: "x", Values: xy},
	}, 3, mouseTestSettingsTheme, func(id string, value string) {
		changes = append(changes, change{id: id, value: value})
	}, func() {}, nil)

	list.HandleMouse(mouseEvent(MouseEventTypePress, 1, 2, 40, 5))
	list.HandleMouse(mouseEvent(MouseEventTypeClick, 1, 2, 40, 5))
	if len(changes) != 1 || changes[0].id != "third" || changes[0].value != "high" {
		t.Fatalf("changes = %v, want [{third high}]", changes)
	}
}

func TestMouseIgnoresHoverAndClicksVisibleSelectListRowsAfterScrolling(t *testing.T) {
	for _, row := range []int{0, 4} {
		var items []SelectItem
		for i := 0; i < 12; i++ {
			items = append(items, SelectItem{Value: "item-" + itoa(i), Label: "Item " + itoa(i)})
		}
		list := NewSelectList(items, 5, mouseTestSelectTheme, nil)
		var changes []string
		selected := ""
		list.OnSelectionChange = func(item SelectItem) { changes = append(changes, item.Value) }
		list.OnSelect = func(item SelectItem) { selected = item.Value }
		list.SetSelectedIndex(5)

		wheel := mouseEvent(MouseEventTypeWheel, 1, row, 80, 10)
		one := 1
		wheel.WheelDelta = &one
		list.HandleMouse(wheel)
		if got := list.GetSelectedItem(); got == nil || got.Value != "item-6" {
			t.Fatalf("row %d: selected = %v, want item-6", row, got)
		}
		if !stringSlicesEqual(changes, []string{"item-6"}) {
			t.Fatalf("row %d: changes = %q", row, changes)
		}
		before := list.Render(80)
		want := "Item " + itoa(4+row)
		if !strings.HasSuffix(StripTerminalSequences(before[row]), want) {
			t.Fatalf("row %d: rendered row = %q, want suffix %q", row, before[row], want)
		}

		for _, y := range []int{0, 1, 2, 3, 4, row} {
			move := mouseEvent(MouseEventTypeMove, 1, y, 80, 10)
			move.Button = MouseButtonNone
			if result := list.HandleMouse(move); result != nil {
				t.Fatalf("row %d y %d: move was handled", row, y)
			}
			if after := list.Render(80); !stringSlicesEqual(after, before) {
				t.Fatalf("row %d y %d: render changed: %q", row, y, after)
			}
		}
		if got := list.GetSelectedItem(); got == nil || got.Value != "item-6" {
			t.Fatalf("row %d: selected after hover = %v, want item-6", row, got)
		}
		if !stringSlicesEqual(changes, []string{"item-6"}) {
			t.Fatalf("row %d: changes after hover = %q", row, changes)
		}
		if selected != "" {
			t.Fatalf("row %d: selected = %q, want none", row, selected)
		}

		list.HandleMouse(mouseEvent(MouseEventTypePress, 1, row, 80, 10))
		list.Render(80)
		list.HandleMouse(mouseEvent(MouseEventTypeClick, 1, row, 80, 10))
		if selected != "item-"+itoa(4+row) {
			t.Fatalf("row %d: selected = %q, want item-%d", row, selected, 4+row)
		}
		if !stringSlicesEqual(changes, []string{"item-6", "item-" + itoa(4+row)}) {
			t.Fatalf("row %d: final changes = %q", row, changes)
		}
	}
}

func TestMouseIgnoresHoverAndClicksVisibleSettingsRowsAfterScrolling(t *testing.T) {
	type change struct {
		id    string
		value string
	}
	for _, row := range []int{0, 4} {
		var changes []change
		var items []*SettingItem
		for i := 0; i < 12; i++ {
			items = append(items, &SettingItem{
				Id:           "item-" + itoa(i),
				Label:        "Item " + itoa(i),
				Description:  "Description " + itoa(i),
				CurrentValue: "off",
				Values:       []string{"off", "on"},
			})
		}
		list := NewSettingsList(items, 5, mouseTestSettingsTheme, func(id string, value string) {
			changes = append(changes, change{id: id, value: value})
		}, func() {}, &SettingsListOptions{EnableSearch: true})
		list.SelectItem("item-5")

		wheel := mouseEvent(MouseEventTypeWheel, 1, row+2, 80, 10)
		one := 1
		wheel.WheelDelta = &one
		list.HandleMouse(wheel)
		before := list.Render(80)
		if stripped := StripTerminalSequences(before[4]); !strings.HasPrefix(stripped, "> Item 6") {
			t.Fatalf("row %d: selected row = %q", row, stripped)
		}
		if stripped := StripTerminalSequences(before[row+2]); !strings.Contains(stripped, "Item "+itoa(4+row)+" ") {
			t.Fatalf("row %d: target row = %q", row, stripped)
		}

		for _, y := range []int{0, 1, 2, 3, 4, row} {
			move := mouseEvent(MouseEventTypeMove, 1, y+2, 80, 10)
			move.Button = MouseButtonNone
			if result := list.HandleMouse(move); result != nil {
				t.Fatalf("row %d y %d: move was handled", row, y)
			}
			if after := list.Render(80); !stringSlicesEqual(after, before) {
				t.Fatalf("row %d y %d: render changed", row, y)
			}
		}
		if len(changes) != 0 {
			t.Fatalf("row %d: changes = %v, want none", row, changes)
		}

		list.HandleMouse(mouseEvent(MouseEventTypePress, 1, row+2, 80, 10))
		list.Render(80)
		list.HandleMouse(mouseEvent(MouseEventTypeClick, 1, row+2, 80, 10))
		if len(changes) != 1 || changes[0].id != "item-"+itoa(4+row) || changes[0].value != "on" {
			t.Fatalf("row %d: changes = %v", row, changes)
		}
	}
}

// mouseInputOverlay is a delegating overlay container (pi: InputOverlay).
type mouseInputOverlay struct {
	Container
	input *Input
}

func newMouseInputOverlay() *mouseInputOverlay {
	o := &mouseInputOverlay{input: NewInput()}
	o.AddChild(o.input)
	return o
}

func (o *mouseInputOverlay) HandleInput(data string) {
	o.input.HandleInput(data)
}

func TestMouseKeepsADelegatingOverlayFocusedWhenItsNestedInputIsClicked(t *testing.T) {
	terminal := NewVirtualTerminal(20, 4)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	overlay := newMouseInputOverlay()
	overlay.input.SetValue("hi")
	tui.Start()
	tui.ShowOverlay(overlay, &OverlayOptions{Anchor: OverlayAnchorTopLeft, Width: 20})
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;5;1M")
	terminal.SendInput("\x1b[<0;5;1m")
	terminal.SendInput("!")
	terminal.WaitForRender()

	if got := overlay.input.GetValue(); got != "hi!" {
		t.Fatalf("input value = %q, want hi!", got)
	}
	if got := tui.GetFocusedComponent(); got != Component(overlay) {
		t.Fatalf("focused = %v, want overlay", got)
	}
	tui.Stop(nil)
}

func TestMousePositionsAndFocusesTheMultilineEditorThroughAlternateScreenDispatch(t *testing.T) {
	terminal := NewVirtualTerminal(20, 6)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	editor := NewEditor(tui, mouseTestEditorTheme, nil)
	editor.SetText("hello")
	tui.AddChild(editor)
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;3;2M")
	terminal.SendInput("\x1b[<0;3;2m")
	terminal.SendInput("X")
	terminal.WaitForRender()

	if got := editor.GetText(); got != "heXllo" {
		t.Fatalf("text = %q, want heXllo", got)
	}
	if got := tui.GetFocusedComponent(); got != Component(editor) {
		t.Fatalf("focused = %v, want editor", got)
	}
	tui.Stop(nil)
}

func TestMouseSelectsAndCopiesEditorTextOnDragInsteadOfMovingTheCursor(t *testing.T) {
	terminal := NewVirtualTerminal(20, 6)
	var copied []string
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		CopySelection: func(text string) CopySelectionResult {
			copied = append(copied, text)
			return CopySelectionResult{Ok: true}
		},
	})
	editor := NewEditor(tui, mouseTestEditorTheme, nil)
	editor.SetText("hello world")
	tui.AddChild(editor)
	tui.Start()
	terminal.WaitForRender()
	cursorBefore := editor.GetCursor()

	terminal.SendInput("\x1b[<0;1;2M")
	terminal.SendInput("\x1b[<32;5;2M")
	terminal.SendInput("\x1b[<0;5;2m")
	terminal.WaitForRender()

	if !stringSlicesEqual(copied, []string{"hello"}) {
		t.Fatalf("copied = %q, want [hello]", copied)
	}
	if got := editor.GetCursor(); got != cursorBefore {
		t.Fatalf("cursor = %+v, want %+v", got, cursorBefore)
	}
	tui.Stop(nil)
}

// pi: "does not vertically redispatch misses through horizontal layout
// containers"
func TestMouseDoesNotVerticallyRedispatchMissesThroughHorizontalLayoutContainers(t *testing.T) {
	terminal := NewVirtualTerminal(20, 2)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	selections := 0
	list := NewSelectList([]SelectItem{
		{Value: "a", Label: "A"},
		{Value: "b", Label: "B"},
	}, 2, mouseTestSelectTheme, nil)
	list.OnSelect = func(item SelectItem) { selections++ }
	tui.SetLayoutRoot(NewHStack([]StackChild{
		StackEntryOf(list, &StackEntryOptions{Basis: 10}),
		StackEntryOf(NewText("plain", 0, 0), &StackEntryOptions{Basis: 10}),
	}, nil))
	tui.Start()
	terminal.WaitForRender()

	// A press+release in the "plain" column must not fall through to the
	// sibling list (plain Containers skip mouse dispatch inside layouts).
	terminal.SendInput("\x1b[<0;15;1M")
	terminal.SendInput("\x1b[<0;15;1m")
	terminal.WaitForRender()
	if selections != 0 {
		t.Fatalf("selections = %d, want 0", selections)
	}
	tui.Stop(nil)
}
