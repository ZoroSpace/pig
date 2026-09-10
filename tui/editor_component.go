// tui/editor_component.go — ported from pi: packages/tui/src/editor-component.ts
//
// Interface for custom editor components. This allows extensions to provide
// their own editor implementation (e.g. vim mode, emacs mode, custom
// keybindings) while maintaining compatibility with the core application.
//
// Declared deviations:
//   - TS optional interface members cannot be expressed in Go interfaces →
//     split into capability interfaces (repo rule); consumers type-assert.
//   - onSubmit/onChange (optional callback properties) have no interface
//     representation in Go — consumers set the fields on the concrete type;
//     documented here since they are part of the "required callbacks"
//     section of the pi interface.
//   - borderColor is a public property on pi's interface; Editor exposes it
//     as a struct field, not an interface member.

package tui

// EditorComponent is the required core of a custom editor component (pi:
// EditorComponent extends Component).
type EditorComponent interface {
	Component

	// =========================================================================
	// Core text access (required)
	// =========================================================================

	// GetText returns the current text content (pi: getText).
	GetText() string

	// SetText sets the text content (pi: setText).
	SetText(text string)

	// HandleInput handles raw terminal input (key presses, paste sequences,
	// etc.) (pi: handleInput).
	HandleInput(data string)
}

// EditorComponentHistory is the optional history capability (pi: optional
// addToHistory?).
type EditorComponentHistory interface {
	// AddToHistory adds text to history for up/down navigation.
	AddToHistory(text string)
}

// EditorComponentTextInsertion is the optional advanced text manipulation
// capability (pi: optional insertTextAtCursor?).
type EditorComponentTextInsertion interface {
	// InsertTextAtCursor inserts text at the current cursor position.
	InsertTextAtCursor(text string)
}

// EditorComponentExpandedText is the optional expanded-text capability (pi:
// optional getExpandedText? — falls back to getText() if not implemented).
type EditorComponentExpandedText interface {
	// GetExpandedText returns text with any markers expanded (e.g. paste
	// markers).
	GetExpandedText() string
}

// EditorComponentAutocomplete is the optional autocomplete capability (pi:
// optional setAutocompleteProvider?).
type EditorComponentAutocomplete interface {
	// SetAutocompleteProvider sets the autocomplete provider.
	SetAutocompleteProvider(provider AutocompleteProvider)
}

// EditorComponentAppearance is the optional appearance capability (pi:
// optional setPaddingX? / setAutocompleteMaxVisible?).
type EditorComponentAppearance interface {
	// SetPaddingX sets horizontal padding.
	SetPaddingX(padding int)

	// SetAutocompleteMaxVisible sets the max visible items in the
	// autocomplete dropdown.
	SetAutocompleteMaxVisible(maxVisible int)
}

// Compile-time assertions: the core Editor satisfies the full
// EditorComponent surface including every optional capability (pi: Editor
// implements EditorComponent).
var (
	_ EditorComponent              = (*Editor)(nil)
	_ EditorComponentHistory       = (*Editor)(nil)
	_ EditorComponentTextInsertion = (*Editor)(nil)
	_ EditorComponentExpandedText  = (*Editor)(nil)
	_ EditorComponentAutocomplete  = (*Editor)(nil)
	_ EditorComponentAppearance    = (*Editor)(nil)
	_ Focusable                    = (*Editor)(nil)
	_ FileCompletionTriggerer      = (*CombinedAutocompleteProvider)(nil)
	_ AutocompleteProvider         = (*CombinedAutocompleteProvider)(nil)
)
