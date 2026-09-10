// tui/settings_list.go (pi: packages/tui/src/components/settings-list.ts)
package tui

import "strings"

// SettingItem is one row of a SettingsList (pi: SettingItem).
type SettingItem struct {
	// Id is the unique identifier for this setting.
	Id string
	// Label is the display label (left side).
	Label string
	// Description is shown when selected (empty = none).
	Description string
	// CurrentValue is the value to display (right side).
	CurrentValue string
	// Values cycle on Enter/Space when non-empty.
	Values []string
	// Submenu opens a submenu on Enter when non-nil. The done callback
	// accepts an optional selectedValue and an optional navigateTo id to
	// move the cursor after close (pi: done(selectedValue?, options?)).
	Submenu func(currentValue string, done func(selectedValue string, options *SettingsSubmenuDoneOptions)) Component
}

// SettingsSubmenuDoneOptions carries pi's done() options (undefined → nil).
type SettingsSubmenuDoneOptions struct {
	// NavigateTo moves the cursor to the item with this id after close.
	NavigateTo string
}

// SettingsListTheme styles a SettingsList (pi: SettingsListTheme).
type SettingsListTheme struct {
	Label       func(text string, selected bool) string
	Value       func(text string, selected bool) string
	Description func(text string) string
	Cursor      string
	Hint        func(text string) string
}

// SettingsListOptions carries pi's SettingsListOptions.
type SettingsListOptions struct {
	EnableSearch bool
}

// SettingsList is a scrollable list of settings with optional cycling values,
// submenus, and search (pi: SettingsList implements Component).
type SettingsList struct {
	items         []*SettingItem
	filteredItems []*SettingItem
	theme         SettingsListTheme
	selectedIndex int
	maxVisible    int
	onChange      func(id string, newValue string)
	onCancel      func()
	searchInput   *Input
	searchEnabled bool

	// Submenu state
	submenuComponent    Component
	submenuItemIndex    int
	hasSubmenuItemIndex bool
	navigateAfterClose  string
}

// NewSettingsList creates a SettingsList.
func NewSettingsList(
	items []*SettingItem,
	maxVisible int,
	theme SettingsListTheme,
	onChange func(id string, newValue string),
	onCancel func(),
	options *SettingsListOptions,
) *SettingsList {
	l := &SettingsList{
		items:         items,
		filteredItems: items,
		maxVisible:    maxVisible,
		theme:         theme,
		onChange:      onChange,
		onCancel:      onCancel,
	}
	if options != nil {
		l.searchEnabled = options.EnableSearch
	}
	if l.searchEnabled {
		l.searchInput = NewInput()
	}
	return l
}

// UpdateValue updates an item's currentValue (pi: updateValue).
func (l *SettingsList) UpdateValue(id string, newValue string) {
	for _, item := range l.items {
		if item.Id == id {
			item.CurrentValue = newValue
			return
		}
	}
}

// SelectItem moves selection to the item with the given id (no-op if not
// found) (pi: selectItem).
func (l *SettingsList) SelectItem(id string) {
	items := l.displayItems()
	for i, item := range items {
		if item.Id == id {
			l.selectedIndex = i
			return
		}
	}
}

// displayItems mirrors pi's `this.searchEnabled ? this.filteredItems :
// this.items` selection.
func (l *SettingsList) displayItems() []*SettingItem {
	if l.searchEnabled {
		return l.filteredItems
	}
	return l.items
}

// Invalidate implements Component.
func (l *SettingsList) Invalidate() {
	if inv, ok := l.submenuComponent.(interface{ Invalidate() }); ok {
		inv.Invalidate()
	}
}

// Render implements Component (pi: render).
func (l *SettingsList) Render(width int) []string {
	// If submenu is active, render it instead
	if l.submenuComponent != nil {
		return l.submenuComponent.Render(width)
	}
	return l.renderMainList(width)
}

func (l *SettingsList) renderMainList(width int) []string {
	var lines []string

	if l.searchEnabled && l.searchInput != nil {
		lines = append(lines, l.searchInput.Render(width)...)
		lines = append(lines, "")
	}

	if len(l.items) == 0 {
		lines = append(lines, l.theme.Hint("  No settings available"))
		if l.searchEnabled {
			lines = l.addHintLine(lines, width)
		}
		return lines
	}

	displayItems := l.displayItems()
	if len(displayItems) == 0 {
		lines = append(lines, TruncateToWidth(l.theme.Hint("  No matching settings"), width, "", false))
		lines = l.addHintLine(lines, width)
		return lines
	}

	// Calculate visible range with scrolling
	startIndex := maxInt(0, minInt(l.selectedIndex-l.maxVisible/2, len(displayItems)-l.maxVisible))
	endIndex := minInt(startIndex+l.maxVisible, len(displayItems))

	// Calculate max label width for alignment
	maxLabelWidth := 0
	for _, item := range l.items {
		if w := VisibleWidth(item.Label); w > maxLabelWidth {
			maxLabelWidth = w
		}
	}
	maxLabelWidth = minInt(36, maxLabelWidth)

	// Render visible items
	for i := startIndex; i < endIndex; i++ {
		item := displayItems[i]
		if item == nil {
			continue
		}
		isSelected := i == l.selectedIndex
		prefix := "  "
		if isSelected {
			prefix = l.theme.Cursor
		}
		prefixWidth := VisibleWidth(prefix)

		// Pad label to align values
		padding := maxInt(0, maxLabelWidth-VisibleWidth(item.Label))
		labelPadded := item.Label + strings.Repeat(" ", padding)
		labelText := l.theme.Label(labelPadded, isSelected)

		// Calculate space for value
		separator := "  "
		usedWidth := prefixWidth + maxLabelWidth + VisibleWidth(separator)
		valueMaxWidth := width - usedWidth - 2

		valueText := l.theme.Value(TruncateToWidth(item.CurrentValue, valueMaxWidth, "", false), isSelected)

		lines = append(lines, TruncateToWidth(prefix+labelText+separator+valueText, width, "", false))
	}

	// Add scroll indicator if needed
	if startIndex > 0 || endIndex < len(displayItems) {
		scrollText := "  (" + itoa(l.selectedIndex+1) + "/" + itoa(len(displayItems)) + ")"
		lines = append(lines, l.theme.Hint(TruncateToWidth(scrollText, width-2, "", false)))
	}

	// Add description for selected item
	if l.selectedIndex < len(displayItems) {
		selectedItem := displayItems[l.selectedIndex]
		if selectedItem != nil && selectedItem.Description != "" {
			lines = append(lines, "")
			wrappedDesc := WrapTextWithAnsi(selectedItem.Description, width-4)
			for _, line := range wrappedDesc {
				lines = append(lines, l.theme.Description("  "+line))
			}
		}
	}

	// Add hint
	lines = l.addHintLine(lines, width)

	return lines
}

// HandleInput implements InputHandler (pi: handleInput).
func (l *SettingsList) HandleInput(data string) {
	// If submenu is active, delegate all input to it
	// The submenu's onCancel (triggered by escape) will call done() which closes it
	if l.submenuComponent != nil {
		if handler, ok := l.submenuComponent.(InputHandler); ok {
			handler.HandleInput(data)
		}
		return
	}

	// Main list input handling
	kb := GetKeybindings()
	displayItems := l.displayItems()
	switch {
	case kb.Matches(data, "tui.select.up"):
		if len(displayItems) == 0 {
			return
		}
		if l.selectedIndex == 0 {
			l.selectedIndex = len(displayItems) - 1
		} else {
			l.selectedIndex--
		}
	case kb.Matches(data, "tui.select.down"):
		if len(displayItems) == 0 {
			return
		}
		if l.selectedIndex == len(displayItems)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
	case kb.Matches(data, "tui.select.confirm") ||
		(data == " " && (!l.searchEnabled || l.searchInput.GetValue() == "")):
		l.activateItem()
	case kb.Matches(data, "tui.select.cancel"):
		l.onCancel()
	case l.searchEnabled && l.searchInput != nil:
		l.searchInput.HandleInput(data)
		l.applyFilter(l.searchInput.GetValue())
	}
}

func (l *SettingsList) activateItem() {
	displayItems := l.displayItems()
	if l.selectedIndex >= len(displayItems) {
		return
	}
	item := displayItems[l.selectedIndex]
	if item == nil {
		return
	}

	if item.Submenu != nil {
		// Open submenu, passing current value so it can pre-select correctly
		l.submenuItemIndex = l.selectedIndex
		l.hasSubmenuItemIndex = true
		l.submenuComponent = item.Submenu(item.CurrentValue, func(selectedValue string, options *SettingsSubmenuDoneOptions) {
			if selectedValue != "" {
				// pi: selectedValue !== undefined — pig collapses undefined to
				// the empty string, so an explicit "" selection also skips the
				// update (the pi submenu never calls done with "").
				item.CurrentValue = selectedValue
				l.onChange(item.Id, selectedValue)
			}
			if options != nil && options.NavigateTo != "" {
				l.navigateAfterClose = options.NavigateTo
			}
			l.closeSubmenu()
		})
	} else if len(item.Values) > 0 {
		// Cycle through values
		currentIndex := -1
		for i, v := range item.Values {
			if v == item.CurrentValue {
				currentIndex = i
				break
			}
		}
		nextIndex := (currentIndex + 1) % len(item.Values)
		newValue := item.Values[nextIndex]
		item.CurrentValue = newValue
		l.onChange(item.Id, newValue)
	}
}

func (l *SettingsList) closeSubmenu() {
	l.submenuComponent = nil
	if l.navigateAfterClose != "" {
		id := l.navigateAfterClose
		l.navigateAfterClose = ""
		l.submenuItemIndex = 0
		l.hasSubmenuItemIndex = false
		l.SelectItem(id)
		// Open the target item's submenu automatically
		l.activateItem()
	} else if l.hasSubmenuItemIndex {
		// Restore selection to the item that opened the submenu
		l.selectedIndex = l.submenuItemIndex
		l.submenuItemIndex = 0
		l.hasSubmenuItemIndex = false
	}
}

func (l *SettingsList) applyFilter(query string) {
	l.filteredItems = FuzzyFilter(l.items, query, func(item *SettingItem) string { return item.Label })
	l.selectedIndex = 0
}

func (l *SettingsList) addHintLine(lines []string, width int) []string {
	hint := "  Enter/Space to change · Esc to cancel"
	if l.searchEnabled {
		hint = "  Type to search · Enter/Space to change · Esc to cancel"
	}
	lines = append(lines, "")
	lines = append(lines, TruncateToWidth(l.theme.Hint(hint), width, "", false))
	return lines
}
