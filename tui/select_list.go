// tui/select_list.go — ported from pi: packages/tui/src/components/select-list.ts
//
// Declared deviations:
//   - onSelect/onCancel/onSelectionChange optional properties → exported
//     func fields (nil = unset); item-or-null returns → *SelectItem.
//   - description? optional member → "" = absent (TS falsy-equivalent).

package tui

import (
	"fmt"
	"strings"
)

const (
	defaultPrimaryColumnWidth = 32
	primaryColumnGap          = 2
	minDescriptionWidth       = 10
)

func normalizeToSingleLine(text string) string {
	return strings.TrimSpace(replaceNewlineRuns(text))
}

// replaceNewlineRuns is pi's /[\r\n]+/g → " " replacement.
func replaceNewlineRuns(text string) string {
	var b strings.Builder
	inRun := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\r' || c == '\n' {
			if !inRun {
				b.WriteByte(' ')
				inRun = true
			}
			continue
		}
		inRun = false
		b.WriteByte(c)
	}
	return b.String()
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// SelectItem is one selectable entry (pi: SelectItem). Description is
// optional (pi: description?); "" means absent (TS falsy equivalent).
type SelectItem struct {
	Value       string
	Label       string
	Description string
}

// SelectListTheme colors the select list output (pi: SelectListTheme).
type SelectListTheme struct {
	SelectedPrefix func(text string) string
	SelectedText   func(text string) string
	Description    func(text string) string
	ScrollInfo     func(text string) string
	NoMatch        func(text string) string
}

// SelectListTruncatePrimaryContext is the argument of a custom
// truncatePrimary hook (pi: SelectListTruncatePrimaryContext).
type SelectListTruncatePrimaryContext struct {
	Text        string
	MaxWidth    int
	ColumnWidth int
	Item        SelectItem
	IsSelected  bool
}

// SelectListLayoutOptions configures the primary column layout (pi:
// SelectListLayoutOptions). Zero-value fields mean "not provided" (pi:
// optional members).
type SelectListLayoutOptions struct {
	MinPrimaryColumnWidth int
	MaxPrimaryColumnWidth int
	TruncatePrimary       func(context SelectListTruncatePrimaryContext) string
}

// SelectList is a scrollable list of selectable items (pi: SelectList).
// It implements Component and InputHandler.
type SelectList struct {
	items         []SelectItem
	filteredItems []SelectItem
	selectedIndex int
	maxVisible    int
	theme         SelectListTheme
	layout        SelectListLayoutOptions

	// OnSelect is called on confirm (pi: public onSelect?).
	OnSelect func(item SelectItem)
	// OnCancel is called on cancel (pi: public onCancel?).
	OnCancel func()
	// OnSelectionChange is called when the selection moves (pi: public
	// onSelectionChange?).
	OnSelectionChange func(item SelectItem)
}

// NewSelectList creates a select list (pi: constructor; layout optional —
// pass nil for defaults).
func NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme, layout *SelectListLayoutOptions) *SelectList {
	filtered := items
	if filtered == nil {
		filtered = []SelectItem{}
	}
	l := SelectListLayoutOptions{}
	if layout != nil {
		l = *layout
	}
	return &SelectList{
		items:         items,
		filteredItems: filtered,
		maxVisible:    maxVisible,
		theme:         theme,
		layout:        l,
	}
}

// SetFilter filters items by case-insensitive value prefix and resets the
// selection (pi: setFilter).
func (s *SelectList) SetFilter(filter string) {
	lowerFilter := strings.ToLower(filter)
	filtered := []SelectItem{}
	for _, item := range s.items {
		if strings.HasPrefix(strings.ToLower(item.Value), lowerFilter) {
			filtered = append(filtered, item)
		}
	}
	s.filteredItems = filtered
	// Reset selection when filter changes
	s.selectedIndex = 0
}

// SetSelectedIndex clamps and sets the selection (pi: setSelectedIndex).
func (s *SelectList) SetSelectedIndex(index int) {
	s.selectedIndex = clampInt(index, 0, len(s.filteredItems)-1)
}

// Invalidate clears cached render state (pi: invalidate — no cached state).
func (s *SelectList) Invalidate() {}

// Render implements Component (pi: render).
func (s *SelectList) Render(width int) []string {
	lines := []string{}

	// If no items match filter, show message
	if len(s.filteredItems) == 0 {
		lines = append(lines, s.theme.NoMatch("  No matching commands"))
		return lines
	}

	primaryColumnWidth := s.getPrimaryColumnWidth()

	// Calculate visible range with scrolling
	startIndex := clampInt(s.selectedIndex-s.maxVisible/2, 0, len(s.filteredItems)-s.maxVisible)
	endIndex := startIndex + s.maxVisible
	if endIndex > len(s.filteredItems) {
		endIndex = len(s.filteredItems)
	}

	// Render visible items
	for i := startIndex; i < endIndex; i++ {
		item := s.filteredItems[i]
		isSelected := i == s.selectedIndex
		descriptionSingleLine := ""
		if item.Description != "" {
			descriptionSingleLine = normalizeToSingleLine(item.Description)
		}
		lines = append(lines, s.renderItem(item, isSelected, width, descriptionSingleLine, primaryColumnWidth))
	}

	// Add scroll indicators if needed
	if startIndex > 0 || endIndex < len(s.filteredItems) {
		scrollText := fmt.Sprintf("  (%d/%d)", s.selectedIndex+1, len(s.filteredItems))
		// Truncate if too long for terminal
		lines = append(lines, s.theme.ScrollInfo(TruncateToWidth(scrollText, width-2, "", false)))
	}

	return lines
}

// HandleInput implements InputHandler (pi: handleInput).
func (s *SelectList) HandleInput(keyData string) {
	kb := GetKeybindings()
	// Up arrow - wrap to bottom when at top
	if kb.Matches(keyData, "tui.select.up") {
		if s.selectedIndex == 0 {
			s.selectedIndex = len(s.filteredItems) - 1
		} else {
			s.selectedIndex--
		}
		s.notifySelectionChange()
	} else if kb.Matches(keyData, "tui.select.down") {
		// Down arrow - wrap to top when at bottom
		if s.selectedIndex == len(s.filteredItems)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		s.notifySelectionChange()
	} else if kb.Matches(keyData, "tui.select.confirm") {
		// Enter
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.filteredItems) {
			selectedItem := s.filteredItems[s.selectedIndex]
			if s.OnSelect != nil {
				s.OnSelect(selectedItem)
			}
		}
	} else if kb.Matches(keyData, "tui.select.cancel") {
		// Escape or Ctrl+C
		if s.OnCancel != nil {
			s.OnCancel()
		}
	}
}

// renderItem renders a single item row (pi: private renderItem).
func (s *SelectList) renderItem(item SelectItem, isSelected bool, width int, descriptionSingleLine string, primaryColumnWidth int) string {
	prefix := "  "
	if isSelected {
		prefix = "→ "
	}
	prefixWidth := VisibleWidth(prefix)

	if descriptionSingleLine != "" && width > 40 {
		effectivePrimaryColumnWidth := clampInt(primaryColumnWidth, 1, width-prefixWidth-4)
		maxPrimaryWidth := max(1, effectivePrimaryColumnWidth-primaryColumnGap)
		truncatedValue := s.truncatePrimary(item, isSelected, maxPrimaryWidth, effectivePrimaryColumnWidth)
		truncatedValueWidth := VisibleWidth(truncatedValue)
		spacing := strings.Repeat(" ", max(1, effectivePrimaryColumnWidth-truncatedValueWidth))
		descriptionStart := prefixWidth + truncatedValueWidth + len(spacing)
		remainingWidth := width - descriptionStart - 2 // -2 for safety

		if remainingWidth > minDescriptionWidth {
			truncatedDesc := TruncateToWidth(descriptionSingleLine, remainingWidth, "", false)
			if isSelected {
				return s.theme.SelectedText(fmt.Sprintf("%s%s%s%s", prefix, truncatedValue, spacing, truncatedDesc))
			}

			descText := s.theme.Description(spacing + truncatedDesc)
			return prefix + truncatedValue + descText
		}
	}

	maxWidth := width - prefixWidth - 2
	truncatedValue := s.truncatePrimary(item, isSelected, maxWidth, maxWidth)
	if isSelected {
		return s.theme.SelectedText(fmt.Sprintf("%s%s", prefix, truncatedValue))
	}

	return prefix + truncatedValue
}

// getPrimaryColumnWidth computes the effective primary column width (pi:
// private getPrimaryColumnWidth).
func (s *SelectList) getPrimaryColumnWidth() int {
	bounds := s.getPrimaryColumnBounds()
	widestPrimary := 0
	for _, item := range s.filteredItems {
		w := VisibleWidth(s.getDisplayValue(item)) + primaryColumnGap
		if w > widestPrimary {
			widestPrimary = w
		}
	}

	return clampInt(widestPrimary, bounds.min, bounds.max)
}

// primaryColumnBounds is the { min, max } result of getPrimaryColumnBounds.
type primaryColumnBounds struct {
	min int
	max int
}

// getPrimaryColumnBounds resolves the configured bounds (pi: private
// getPrimaryColumnBounds).
func (s *SelectList) getPrimaryColumnBounds() primaryColumnBounds {
	rawMin := s.layout.MinPrimaryColumnWidth
	if rawMin == 0 {
		rawMin = s.layout.MaxPrimaryColumnWidth
	}
	if rawMin == 0 {
		rawMin = defaultPrimaryColumnWidth
	}
	rawMax := s.layout.MaxPrimaryColumnWidth
	if rawMax == 0 {
		rawMax = s.layout.MinPrimaryColumnWidth
	}
	if rawMax == 0 {
		rawMax = defaultPrimaryColumnWidth
	}

	return primaryColumnBounds{
		min: max(1, min(rawMin, rawMax)),
		max: max(1, max(rawMin, rawMax)),
	}
}

// truncatePrimary truncates the primary text (pi: private truncatePrimary).
func (s *SelectList) truncatePrimary(item SelectItem, isSelected bool, maxWidth int, columnWidth int) string {
	displayValue := s.getDisplayValue(item)
	truncatedValue := TruncateToWidth(displayValue, maxWidth, "", false)
	if s.layout.TruncatePrimary != nil {
		truncatedValue = s.layout.TruncatePrimary(SelectListTruncatePrimaryContext{
			Text:        displayValue,
			MaxWidth:    maxWidth,
			ColumnWidth: columnWidth,
			Item:        item,
			IsSelected:  isSelected,
		})
	}

	return TruncateToWidth(truncatedValue, maxWidth, "", false)
}

// getDisplayValue returns the display text of an item (pi: private
// getDisplayValue).
func (s *SelectList) getDisplayValue(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

// notifySelectionChange fires the selection-change callback (pi: private
// notifySelectionChange).
func (s *SelectList) notifySelectionChange() {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.filteredItems) {
		return
	}
	selectedItem := s.filteredItems[s.selectedIndex]
	if s.OnSelectionChange != nil {
		s.OnSelectionChange(selectedItem)
	}
}

// GetSelectedItem returns the selected item or nil (pi: getSelectedItem —
// item || null, i.e. undefined when out of range).
func (s *SelectList) GetSelectedItem() *SelectItem {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.filteredItems) {
		return nil
	}
	item := s.filteredItems[s.selectedIndex]
	return &item
}
