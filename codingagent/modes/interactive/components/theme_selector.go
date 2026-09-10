// theme_selector.go (pi: components/theme-selector.ts) — the theme picker
// list with live preview (extension export surface; interactive mode uses
// the settings ThemeSubmenu).
package components

import (
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// themeSelectListLayout is the fixed layout (pi: THEME_SELECT_LIST_LAYOUT).
var themeSelectListLayout = &tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: 12,
	MaxPrimaryColumnWidth: 32,
}

// ThemeSelectorComponent renders a theme selector (pi: ThemeSelectorComponent
// extends Container).
type ThemeSelectorComponent struct {
	tui.Container

	selectList *tui.SelectList
	onPreview  func(themeName string)
}

// NewThemeSelectorComponent builds the selector (pi: constructor).
func NewThemeSelectorComponent(
	currentTheme string,
	onSelect func(themeName string),
	onCancel func(),
	onPreview func(themeName string),
) *ThemeSelectorComponent {
	s := &ThemeSelectorComponent{
		Container: tui.Container{},
		onPreview: onPreview,
	}

	// Get available themes and create select items
	themes := theme.GetAvailableThemes()
	themeItems := make([]tui.SelectItem, 0, len(themes))
	for _, name := range themes {
		description := ""
		if name == currentTheme {
			description = "(current)"
		}
		themeItems = append(themeItems, tui.SelectItem{Value: name, Label: name, Description: description})
	}

	// Add top border
	s.AddChild(NewDynamicBorder(nil))

	// Create selector
	s.selectList = tui.NewSelectList(themeItems, 10, theme.GetSelectListTheme(), themeSelectListLayout)

	// Preselect current theme
	for i, name := range themes {
		if name == currentTheme {
			s.selectList.SetSelectedIndex(i)
			break
		}
	}

	s.selectList.OnSelect = func(item tui.SelectItem) {
		onSelect(item.Value)
	}

	s.selectList.OnCancel = func() {
		onCancel()
	}

	s.selectList.OnSelectionChange = func(item tui.SelectItem) {
		s.onPreview(item.Value)
	}

	s.AddChild(s.selectList)

	// Add bottom border
	s.AddChild(NewDynamicBorder(nil))
	return s
}

// GetSelectList exposes the inner list (pi: getSelectList).
func (s *ThemeSelectorComponent) GetSelectList() *tui.SelectList { return s.selectList }
