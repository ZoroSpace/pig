// user_message_selector.go (pi: components/user-message-selector.ts) — the
// /fork user message picker: a custom scrolling list of past user messages
// with wrap-around navigation.
package components

import (
	"strings"
	"time"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// UserMessageItem is one selectable user message (pi: UserMessageItem).
type UserMessageItem struct {
	// ID is the entry ID in the session.
	ID string
	// Text is the message text.
	Text string
	// Timestamp is optional (pi: timestamp?; currently unused by the
	// renderer, kept for shape fidelity).
	Timestamp string
}

// userMessageList is the custom user message list component with selection
// (pi: UserMessageList implements Component).
type userMessageList struct {
	messages      []UserMessageItem
	selectedIndex int
	// OnSelect fires with the selected entry ID (pi: public onSelect?).
	OnSelect func(entryID string)
	// OnCancel fires on escape (pi: public onCancel?).
	OnCancel   func()
	maxVisible int // Max messages visible
}

// newUserMessageList builds the list (pi: constructor).
func newUserMessageList(messages []UserMessageItem, initialSelectedID string) *userMessageList {
	l := &userMessageList{
		// Store messages in chronological order (oldest to newest)
		messages:   messages,
		maxVisible: 10,
	}
	initialIndex := -1
	if initialSelectedID != "" {
		for i, message := range messages {
			if message.ID == initialSelectedID {
				initialIndex = i
				break
			}
		}
	}
	// Start with selected message if provided, else default to the most recent
	if initialIndex >= 0 {
		l.selectedIndex = initialIndex
	} else {
		l.selectedIndex = maxInt(0, len(messages)-1)
	}
	return l
}

// Invalidate implements Component (pi: invalidate — no cached state).
func (l *userMessageList) Invalidate() {}

// Render implements Component (pi: render).
func (l *userMessageList) Render(width int) []string {
	var lines []string
	currentTheme := theme.Current()

	if len(l.messages) == 0 {
		lines = append(lines, currentTheme.Fg("muted", "  No user messages found"))
		return lines
	}

	// Calculate visible range with scrolling
	startIndex := maxInt(0, minInt(l.selectedIndex-l.maxVisible/2, len(l.messages)-l.maxVisible))
	endIndex := minInt(startIndex+l.maxVisible, len(l.messages))

	// Render visible messages (2 lines per message + blank line)
	for i := startIndex; i < endIndex; i++ {
		message := l.messages[i]
		isSelected := i == l.selectedIndex

		// Normalize message to single line
		normalizedMessage := strings.TrimSpace(strings.ReplaceAll(message.Text, "\n", " "))

		// First line: cursor + message
		cursor := "  "
		if isSelected {
			cursor = currentTheme.Fg("accent", "› ")
		}
		maxMsgWidth := width - 2 // Account for cursor (2 chars)
		truncatedMsg := tui.TruncateToWidth(normalizedMessage, maxMsgWidth, "", false)
		if isSelected {
			truncatedMsg = currentTheme.Bold(truncatedMsg)
		}
		lines = append(lines, cursor+truncatedMsg)

		// Second line: metadata (position in history)
		position := i + 1
		metadata := "  Message " + itoa(position) + " of " + itoa(len(l.messages))
		lines = append(lines, currentTheme.Fg("muted", metadata))
		lines = append(lines, "") // Blank line between messages
	}

	// Add scroll indicator if needed
	if startIndex > 0 || endIndex < len(l.messages) {
		scrollInfo := currentTheme.Fg("muted", "  ("+itoa(l.selectedIndex+1)+"/"+itoa(len(l.messages))+")")
		lines = append(lines, scrollInfo)
	}

	return lines
}

// HandleInput implements InputHandler (pi: handleInput).
func (l *userMessageList) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	// Up arrow - go to previous (older) message, wrap to bottom when at top
	case kb.Matches(keyData, "tui.select.up"):
		if l.selectedIndex == 0 {
			l.selectedIndex = len(l.messages) - 1
		} else {
			l.selectedIndex--
		}
	// Down arrow - go to next (newer) message, wrap to top when at bottom
	case kb.Matches(keyData, "tui.select.down"):
		if l.selectedIndex == len(l.messages)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
	// Enter - select message and branch
	case kb.Matches(keyData, "tui.select.confirm"):
		if l.selectedIndex < len(l.messages) {
			selected := l.messages[l.selectedIndex]
			if selected.ID != "" && l.OnSelect != nil {
				l.OnSelect(selected.ID)
			}
		}
	// Escape - cancel
	case kb.Matches(keyData, "tui.select.cancel"):
		if l.OnCancel != nil {
			l.OnCancel()
		}
	}
}

// UserMessageSelectorComponent renders a user message selector for branching
// (pi: UserMessageSelectorComponent extends Container).
type UserMessageSelectorComponent struct {
	tui.Container

	messageList *userMessageList
}

// NewUserMessageSelectorComponent builds the selector (pi: constructor).
// initialSelectedID optional — "" for none.
func NewUserMessageSelectorComponent(
	messages []UserMessageItem,
	onSelect func(entryID string),
	onCancel func(),
	initialSelectedID string,
) *UserMessageSelectorComponent {
	s := &UserMessageSelectorComponent{}
	currentTheme := theme.Current()

	// Add header
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(tui.NewText(currentTheme.Bold("Fork from Message"), 1, 0))
	s.AddChild(tui.NewText(
		currentTheme.Fg("muted", "Select a user message to copy the active path up to that point into a new session"),
		1, 0,
	))
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(NewDynamicBorder(nil))
	s.AddChild(tui.NewSpacer(1))

	// Create message list
	s.messageList = newUserMessageList(messages, initialSelectedID)
	s.messageList.OnSelect = onSelect
	s.messageList.OnCancel = onCancel

	s.AddChild(s.messageList)

	// Add bottom border
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(NewDynamicBorder(nil))

	// Auto-cancel if no messages
	if len(messages) == 0 {
		time.AfterFunc(100*time.Millisecond, func() {
			if onCancel != nil {
				onCancel()
			}
		})
	}
	return s
}

// GetMessageList returns the message list (pi: getMessageList).
func (s *UserMessageSelectorComponent) GetMessageList() *userMessageList {
	return s.messageList
}
