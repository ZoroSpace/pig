// show_images_selector.go (pi: components/show-images-selector.ts) — the
// image display toggle selector with borders.
package components

import (
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

var showImagesSelectListLayout = tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: 12,
	MaxPrimaryColumnWidth: 32,
}

// ShowImagesSelectorComponent renders a show images selector with borders
// (pi: ShowImagesSelectorComponent extends Container).
type ShowImagesSelectorComponent struct {
	tui.Container

	selectList *tui.SelectList
}

// NewShowImagesSelectorComponent builds the selector (pi: constructor).
func NewShowImagesSelectorComponent(currentValue bool, onSelect func(show bool), onCancel func()) *ShowImagesSelectorComponent {
	s := &ShowImagesSelectorComponent{}

	items := []tui.SelectItem{
		{Value: "yes", Label: "Yes", Description: "Show images inline in terminal"},
		{Value: "no", Label: "No", Description: "Show text placeholder instead"},
	}

	// Add top border
	s.AddChild(NewDynamicBorder(nil))

	// Create selector
	s.selectList = tui.NewSelectList(items, 5, theme.GetSelectListTheme(), &showImagesSelectListLayout)

	// Preselect current value
	if currentValue {
		s.selectList.SetSelectedIndex(0)
	} else {
		s.selectList.SetSelectedIndex(1)
	}

	s.selectList.OnSelect = func(item tui.SelectItem) {
		onSelect(item.Value == "yes")
	}

	s.selectList.OnCancel = func() {
		onCancel()
	}

	s.AddChild(s.selectList)

	// Add bottom border
	s.AddChild(NewDynamicBorder(nil))
	return s
}

// GetSelectList returns the underlying select list (pi: getSelectList).
func (s *ShowImagesSelectorComponent) GetSelectList() *tui.SelectList {
	return s.selectList
}
