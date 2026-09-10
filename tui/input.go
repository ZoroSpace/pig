// tui/input.go — ported from pi: packages/tui/src/components/input.ts
//
// Declared deviations:
//   - onSubmit/onEscape optional properties → exported func fields (nil =
//     unset).
//   - focused public property → Focused field + GetFocused/SetFocused methods
//     (Focusable interface, same adaptation as Editor).
//   - Control-character rejection iterates runes (pi: [...data] code points,
//     ch.charCodeAt(0) = first UTF-16 unit — equivalent for C0/DEL/C1).
//   - All cursor offsets are byte offsets (pi: UTF-16 code units; repo
//     convention). char.length → len(char) bytes.

package tui

import (
	"fmt"
	"strings"
)

// inputState is the undo snapshot of the Input component (pi: InputState).
type inputState struct {
	Value  string
	Cursor int
}

// cloneInputState deep-clones an input state (pi: structuredClone — strings
// are immutable, so a field copy is exact for this shape).
func cloneInputState(s inputState) inputState {
	return inputState{Value: s.Value, Cursor: s.Cursor}
}

// Input is a single-line text input with horizontal scrolling (pi: Input).
// It implements Component, InputHandler and Focusable.
type Input struct {
	value  string
	cursor int // Cursor position in the value (byte offset)

	// OnSubmit is called when the user submits (pi: public onSubmit?).
	OnSubmit func(value string)
	// OnEscape is called on escape (pi: public onEscape?).
	OnEscape func()

	// Focused is the Focusable flag - set by TUI when focus changes (pi:
	// focused public property).
	Focused bool

	// Bracketed paste mode buffering
	pasteBuffer string
	isInPaste   bool

	// killRing for Emacs-style kill/yank operations
	killRing   KillRing
	lastAction string // "kill" | "yank" | "type-word" | ""

	// undoStack supports undo/redo
	undoStack *UndoStack[inputState]
}

// NewInput creates an input component (pi: constructor with field
// initializers).
func NewInput() *Input {
	return &Input{
		undoStack: NewUndoStack(cloneInputState),
	}
}

// GetValue returns the current value (pi: getValue).
func (i *Input) GetValue() string {
	return i.value
}

// SetValue replaces the value, clamping the cursor (pi: setValue).
func (i *Input) SetValue(value string) {
	i.value = value
	if i.cursor > len(value) {
		i.cursor = len(value)
	}
}

// HandleInput implements InputHandler (pi: handleInput).
func (i *Input) HandleInput(data string) {
	// Handle bracketed paste mode
	// Start of paste: \x1b[200~
	// End of paste: \x1b[201~

	// Check if we're starting a bracketed paste
	if strings.Contains(data, "\x1b[200~") {
		i.isInPaste = true
		i.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}

	// If we're in a paste, buffer the data
	if i.isInPaste {
		// Check if this chunk contains the end marker
		i.pasteBuffer += data

		endIndex := strings.Index(i.pasteBuffer, "\x1b[201~")
		if endIndex != -1 {
			// Extract the pasted content
			pasteContent := i.pasteBuffer[:endIndex]

			// Process the complete paste
			i.handlePaste(pasteContent)

			// Reset paste state
			i.isInPaste = false

			// Handle any remaining input after the paste marker
			remaining := i.pasteBuffer[endIndex+6:] // 6 = length of \x1b[201~
			i.pasteBuffer = ""
			if remaining != "" {
				i.HandleInput(remaining)
			}
		}
		return
	}

	kb := GetKeybindings()

	// Escape/Cancel
	if kb.Matches(data, "tui.select.cancel") {
		if i.OnEscape != nil {
			i.OnEscape()
		}
		return
	}

	// Undo
	if kb.Matches(data, "tui.editor.undo") {
		i.undo()
		return
	}

	// Submit
	if kb.Matches(data, "tui.input.submit") || data == "\n" {
		if i.OnSubmit != nil {
			i.OnSubmit(i.value)
		}
		return
	}

	// Deletion
	if kb.Matches(data, "tui.editor.deleteCharBackward") {
		i.handleBackspace()
		return
	}

	if kb.Matches(data, "tui.editor.deleteCharForward") {
		i.handleForwardDelete()
		return
	}

	if kb.Matches(data, "tui.editor.deleteWordBackward") {
		i.deleteWordBackwards()
		return
	}

	if kb.Matches(data, "tui.editor.deleteWordForward") {
		i.deleteWordForward()
		return
	}

	if kb.Matches(data, "tui.editor.deleteToLineStart") {
		i.deleteToLineStart()
		return
	}

	if kb.Matches(data, "tui.editor.deleteToLineEnd") {
		i.deleteToLineEnd()
		return
	}

	// Kill ring actions
	if kb.Matches(data, "tui.editor.yank") {
		i.yank()
		return
	}
	if kb.Matches(data, "tui.editor.yankPop") {
		i.yankPop()
		return
	}

	// Cursor movement
	if kb.Matches(data, "tui.editor.cursorLeft") {
		i.lastAction = ""
		if i.cursor > 0 {
			beforeCursor := i.value[:i.cursor]
			graphemes := graphemeSegmentData(beforeCursor)
			graphemeLength := 1
			if len(graphemes) > 0 {
				graphemeLength = len(graphemes[len(graphemes)-1].Segment)
			}
			i.cursor -= graphemeLength
		}
		return
	}

	if kb.Matches(data, "tui.editor.cursorRight") {
		i.lastAction = ""
		if i.cursor < len(i.value) {
			afterCursor := i.value[i.cursor:]
			graphemes := graphemeSegmentData(afterCursor)
			graphemeLength := 1
			if len(graphemes) > 0 {
				graphemeLength = len(graphemes[0].Segment)
			}
			i.cursor += graphemeLength
		}
		return
	}

	if kb.Matches(data, "tui.editor.cursorLineStart") {
		i.lastAction = ""
		i.cursor = 0
		return
	}

	if kb.Matches(data, "tui.editor.cursorLineEnd") {
		i.lastAction = ""
		i.cursor = len(i.value)
		return
	}

	if kb.Matches(data, "tui.editor.cursorWordLeft") {
		i.moveWordBackwards()
		return
	}

	if kb.Matches(data, "tui.editor.cursorWordRight") {
		i.moveWordForwards()
		return
	}

	// Kitty CSI-u printable character (e.g. \x1b[97u for 'a').
	// Terminals with Kitty protocol flag 1 (disambiguate) send CSI-u for all
	// keys, including plain printable characters. Decode before the
	// control-char check since CSI-u sequences contain \x1b which would be
	// rejected.
	if kittyPrintable, ok := DecodeKittyPrintable(data); ok {
		i.insertCharacter(kittyPrintable)
		return
	}

	// Regular character input - accept printable characters including
	// Unicode, but reject control characters (C0: 0x00-0x1F, DEL: 0x7F,
	// C1: 0x80-0x9F)
	hasControlChars := false
	for _, ch := range data {
		if ch < 32 || ch == 0x7f || (ch >= 0x80 && ch <= 0x9f) {
			hasControlChars = true
			break
		}
	}
	if !hasControlChars {
		i.insertCharacter(data)
	}
}

// insertCharacter inserts a character at the cursor (pi: insertCharacter).
func (i *Input) insertCharacter(char string) {
	// Undo coalescing: consecutive word chars coalesce into one undo unit
	if IsWhitespaceChar(char) || i.lastAction != "type-word" {
		i.pushUndo()
	}
	i.lastAction = "type-word"

	i.value = i.value[:i.cursor] + char + i.value[i.cursor:]
	i.cursor += len(char)
}

// handleBackspace deletes the grapheme before the cursor (pi:
// handleBackspace).
func (i *Input) handleBackspace() {
	i.lastAction = ""
	if i.cursor > 0 {
		i.pushUndo()
		beforeCursor := i.value[:i.cursor]
		graphemes := graphemeSegmentData(beforeCursor)
		graphemeLength := 1
		if len(graphemes) > 0 {
			graphemeLength = len(graphemes[len(graphemes)-1].Segment)
		}
		i.value = i.value[:i.cursor-graphemeLength] + i.value[i.cursor:]
		i.cursor -= graphemeLength
	}
}

// handleForwardDelete deletes the grapheme at the cursor (pi:
// handleForwardDelete).
func (i *Input) handleForwardDelete() {
	i.lastAction = ""
	if i.cursor < len(i.value) {
		i.pushUndo()
		afterCursor := i.value[i.cursor:]
		graphemes := graphemeSegmentData(afterCursor)
		graphemeLength := 1
		if len(graphemes) > 0 {
			graphemeLength = len(graphemes[0].Segment)
		}
		i.value = i.value[:i.cursor] + i.value[i.cursor+graphemeLength:]
	}
}

// deleteToLineStart deletes from line start to the cursor (pi:
// deleteToLineStart).
func (i *Input) deleteToLineStart() {
	if i.cursor == 0 {
		return
	}
	i.pushUndo()
	deletedText := i.value[:i.cursor]
	i.killRing.Push(deletedText, KillRingPushOptions{Prepend: true, Accumulate: i.lastAction == "kill"})
	i.lastAction = "kill"
	i.value = i.value[i.cursor:]
	i.cursor = 0
}

// deleteToLineEnd deletes from the cursor to line end (pi: deleteToLineEnd).
func (i *Input) deleteToLineEnd() {
	if i.cursor >= len(i.value) {
		return
	}
	i.pushUndo()
	deletedText := i.value[i.cursor:]
	i.killRing.Push(deletedText, KillRingPushOptions{Prepend: false, Accumulate: i.lastAction == "kill"})
	i.lastAction = "kill"
	i.value = i.value[:i.cursor]
}

// deleteWordBackwards deletes one word backward (pi: deleteWordBackwards).
func (i *Input) deleteWordBackwards() {
	if i.cursor == 0 {
		return
	}

	// Save lastAction before cursor movement (moveWordBackwards resets it)
	wasKill := i.lastAction == "kill"

	i.pushUndo()

	oldCursor := i.cursor
	i.moveWordBackwards()
	deleteFrom := i.cursor
	i.cursor = oldCursor

	deletedText := i.value[deleteFrom:i.cursor]
	i.killRing.Push(deletedText, KillRingPushOptions{Prepend: true, Accumulate: wasKill})
	i.lastAction = "kill"

	i.value = i.value[:deleteFrom] + i.value[i.cursor:]
	i.cursor = deleteFrom
}

// deleteWordForward deletes one word forward (pi: deleteWordForward).
func (i *Input) deleteWordForward() {
	if i.cursor >= len(i.value) {
		return
	}

	// Save lastAction before cursor movement (moveWordForwards resets it)
	wasKill := i.lastAction == "kill"

	i.pushUndo()

	oldCursor := i.cursor
	i.moveWordForwards()
	deleteTo := i.cursor
	i.cursor = oldCursor

	deletedText := i.value[i.cursor:deleteTo]
	i.killRing.Push(deletedText, KillRingPushOptions{Prepend: false, Accumulate: wasKill})
	i.lastAction = "kill"

	i.value = i.value[:i.cursor] + i.value[deleteTo:]
}

// yank yanks the most recent kill-ring entry (pi: yank).
func (i *Input) yank() {
	text, ok := i.killRing.Peek()
	if !ok || text == "" {
		return
	}

	i.pushUndo()

	i.value = i.value[:i.cursor] + text + i.value[i.cursor:]
	i.cursor += len(text)
	i.lastAction = "yank"
}

// yankPop cycles the kill ring after a yank (pi: yankPop).
func (i *Input) yankPop() {
	if i.lastAction != "yank" || i.killRing.Len() <= 1 {
		return
	}

	i.pushUndo()

	// Delete the previously yanked text (still at end of ring before
	// rotation)
	prevText, _ := i.killRing.Peek()
	i.value = i.value[:i.cursor-len(prevText)] + i.value[i.cursor:]
	i.cursor -= len(prevText)

	// Rotate and insert new entry
	i.killRing.Rotate()
	text, _ := i.killRing.Peek()
	i.value = i.value[:i.cursor] + text + i.value[i.cursor:]
	i.cursor += len(text)
	i.lastAction = "yank"
}

// pushUndo snapshots the current state (pi: pushUndo).
func (i *Input) pushUndo() {
	i.undoStack.Push(inputState{Value: i.value, Cursor: i.cursor})
}

// undo restores the previous snapshot (pi: undo).
func (i *Input) undo() {
	snapshot, ok := i.undoStack.Pop()
	if !ok {
		return
	}
	i.value = snapshot.Value
	i.cursor = snapshot.Cursor
	i.lastAction = ""
}

// moveWordBackwards moves the cursor one word backward (pi:
// moveWordBackwards).
func (i *Input) moveWordBackwards() {
	if i.cursor == 0 {
		return
	}
	i.lastAction = ""
	// pi: findWordBackward(value, cursor) with the default grapheme segmenter
	// (no atomic segments).
	i.cursor = FindWordBackward(i.value, i.cursor, nil)
}

// moveWordForwards moves the cursor one word forward (pi: moveWordForwards).
func (i *Input) moveWordForwards() {
	if i.cursor >= len(i.value) {
		return
	}
	i.lastAction = ""
	i.cursor = FindWordForward(i.value, i.cursor, nil)
}

// handlePaste inserts pasted text at the cursor (pi: handlePaste).
func (i *Input) handlePaste(pastedText string) {
	i.lastAction = ""
	i.pushUndo()

	// Clean the pasted text - remove newlines and carriage returns
	cleanText := pastedText
	cleanText = strings.ReplaceAll(cleanText, "\r\n", "")
	cleanText = strings.ReplaceAll(cleanText, "\r", "")
	cleanText = strings.ReplaceAll(cleanText, "\n", "")
	cleanText = strings.ReplaceAll(cleanText, "\t", "    ")

	// Insert at cursor position
	i.value = i.value[:i.cursor] + cleanText + i.value[i.cursor:]
	i.cursor += len(cleanText)
}

// Invalidate clears cached render state (pi: invalidate — no cached state).
func (i *Input) Invalidate() {}

// GetFocused reports the current focus state (pi: Focusable.focused).
func (i *Input) GetFocused() bool { return i.Focused }

// SetFocused is called by the TUI when focus changes (pi: Focusable.focused).
func (i *Input) SetFocused(focused bool) { i.Focused = focused }

// Render implements Component (pi: render).
func (i *Input) Render(width int) []string {
	// Calculate visible window
	prompt := "> "
	availableWidth := width - len(prompt)

	if availableWidth <= 0 {
		return []string{prompt}
	}

	visibleText := ""
	cursorDisplay := i.cursor
	totalWidth := VisibleWidth(i.value)

	if totalWidth < availableWidth {
		// Everything fits (leave room for cursor at end)
		visibleText = i.value
	} else {
		// Need horizontal scrolling
		// Reserve one column for cursor if it's at the end
		scrollWidth := availableWidth
		if i.cursor == len(i.value) {
			scrollWidth = availableWidth - 1
		}
		cursorCol := VisibleWidth(i.value[:i.cursor])

		if scrollWidth > 0 {
			halfWidth := scrollWidth / 2
			startCol := 0

			if cursorCol < halfWidth {
				// Cursor near start
				startCol = 0
			} else if cursorCol > totalWidth-halfWidth {
				// Cursor near end
				startCol = max(0, totalWidth-scrollWidth)
			} else {
				// Cursor in middle
				startCol = max(0, cursorCol-halfWidth)
			}

			visibleText = SliceByColumn(i.value, startCol, scrollWidth, true)
			beforeCursor := SliceByColumn(i.value, startCol, max(0, cursorCol-startCol), true)
			cursorDisplay = len(beforeCursor)
		} else {
			visibleText = ""
			cursorDisplay = 0
		}
	}

	// Build line with fake cursor
	// Insert cursor character at cursor position
	graphemes := graphemeSegmentData(visibleText[cursorDisplay:])
	atCursor := " " // Character at cursor, or space if at end
	if len(graphemes) > 0 {
		atCursor = graphemes[0].Segment
	}

	beforeCursor := visibleText[:cursorDisplay]
	after := cursorDisplay + len(atCursor)
	if after > len(visibleText) {
		// TS: slice beyond length returns "" (cursor at end of visible text,
		// atCursor is the fallback space).
		after = len(visibleText)
	}
	afterCursor := visibleText[after:]

	// Hardware cursor marker (zero-width, emitted before fake cursor for IME
	// positioning)
	marker := ""
	if i.Focused {
		marker = CursorMarker
	}

	// Use inverse video to show cursor
	cursorChar := fmt.Sprintf("\x1b[7m%s\x1b[27m", atCursor) // ESC[7m = reverse video, ESC[27m = normal
	textWithCursor := beforeCursor + marker + cursorChar + afterCursor

	// Calculate visual width
	visualLength := VisibleWidth(textWithCursor)
	padding := strings.Repeat(" ", max(0, availableWidth-visualLength))
	line := prompt + textWithCursor + padding

	return []string{line}
}
