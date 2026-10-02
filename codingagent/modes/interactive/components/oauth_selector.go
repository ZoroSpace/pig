// oauth_selector.go (pi: components/oauth-selector.ts) — the /login and
// /logout provider selector: searchable provider list with auth-type labels
// and per-provider configuration status indicators.
package components

import (
	"regexp"

	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// AuthSelectorProvider is one selectable provider entry (pi:
// AuthSelectorProvider). Method is pi's `method?: ApiKeyAuth | OAuthAuth` —
// the concrete auth method (auth.ApiKeyAuth or auth.OAuthAuth), nil when
// unset; Status is pi's `status?: AuthCheck` — nil when unconfigured.
type AuthSelectorProvider struct {
	ID       string
	Name     string
	AuthType auth.AuthType // "oauth" | "api_key"
	Method   any
	Status   *auth.AuthCheck
}

// AuthSelectorMethodName extracts pi's `method?.name ?? ""`.
func AuthSelectorMethodName(method any) string {
	switch m := method.(type) {
	case auth.ApiKeyAuth:
		return m.Name()
	case auth.OAuthAuth:
		return m.Name()
	default:
		return ""
	}
}

// FormatAuthSelectorProviderType renders the auth-type label (pi:
// formatAuthSelectorProviderType).
func FormatAuthSelectorProviderType(authType auth.AuthType) string {
	if authType == auth.AuthTypeOAuth {
		return "subscription"
	}
	return "API key"
}

// envSourcePattern matches pi's /^[A-Z][A-Z0-9_]*(?:, [A-Z][A-Z0-9_]*)*$/
// (all-caps env-var lists).
var envSourcePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(?:, [A-Z][A-Z0-9_]*)*$`)

// OAuthSelectorComponent renders an auth provider selector (pi:
// OAuthSelectorComponent extends Container implements Focusable).
type OAuthSelectorComponent struct {
	tui.Container

	searchInput *tui.Input

	listContainer      *tui.Container
	allProviders       []AuthSelectorProvider
	filteredProviders  []AuthSelectorProvider
	selectedIndex      int
	mode               string // "login" | "logout"
	onSelectCallback   func(providerID string, authType auth.AuthType)
	onCancelCallback   func()
	showAuthTypeLabels bool

	// Focusable implementation - propagate to search input for IME cursor
	// positioning (pi: _focused/focused getter+setter).
	focused bool
}

// NewOAuthSelectorComponent builds the selector (pi: constructor).
// initialSearchInput optional — "" for none.
func NewOAuthSelectorComponent(
	mode string,
	providers []AuthSelectorProvider,
	onSelect func(providerID string, authType auth.AuthType),
	onCancel func(),
	initialSearchInput string,
) *OAuthSelectorComponent {
	s := &OAuthSelectorComponent{
		mode:              mode,
		allProviders:      providers,
		filteredProviders: providers,
		onSelectCallback:  onSelect,
		onCancelCallback:  onCancel,
	}
	seen := map[auth.AuthType]bool{}
	for _, provider := range providers {
		seen[provider.AuthType] = true
	}
	s.showAuthTypeLabels = len(seen) > 1

	currentTheme := theme.Current()

	// Add top border
	s.AddChild(NewDynamicBorder(nil))
	s.AddChild(tui.NewSpacer(1))

	// Add title
	title := "Select provider to logout:"
	if mode == "login" {
		title = "Select provider to configure:"
	}
	s.AddChild(tui.NewTruncatedText(currentTheme.Fg("accent", currentTheme.Bold(title)), 1, 0))
	s.AddChild(tui.NewSpacer(1))

	s.searchInput = tui.NewInput()
	if initialSearchInput != "" {
		s.searchInput.SetValue(initialSearchInput)
	}
	s.searchInput.OnSubmit = func(string) {
		if s.selectedIndex < len(s.filteredProviders) {
			selectedProvider := s.filteredProviders[s.selectedIndex]
			if s.onSelectCallback != nil {
				s.onSelectCallback(selectedProvider.ID, selectedProvider.AuthType)
			}
		}
	}
	s.AddChild(s.searchInput)
	s.AddChild(tui.NewSpacer(1))

	// Create list container
	s.listContainer = &tui.Container{}
	s.AddChild(s.listContainer)

	s.AddChild(tui.NewSpacer(1))

	// Add bottom border
	s.AddChild(NewDynamicBorder(nil))

	// Initial render
	query := initialSearchInput
	s.filterProviders(query)
	return s
}

// GetFocused implements Focusable.
func (s *OAuthSelectorComponent) GetFocused() bool { return s.focused }

// SetFocused implements Focusable.
func (s *OAuthSelectorComponent) SetFocused(value bool) {
	s.focused = value
	s.searchInput.SetFocused(value)
}

func (s *OAuthSelectorComponent) filterProviders(query string) {
	if query != "" {
		s.filteredProviders = tui.FuzzyFilter(s.allProviders, query, func(provider AuthSelectorProvider) string {
			return provider.Name + " " + provider.ID + " " + string(provider.AuthType) + " " + AuthSelectorMethodName(provider.Method)
		})
	} else {
		s.filteredProviders = s.allProviders
	}
	s.selectedIndex = maxInt(0, minInt(s.selectedIndex, maxInt(0, len(s.filteredProviders)-1)))
	s.updateList()
}

func (s *OAuthSelectorComponent) updateList() {
	s.listContainer.Clear()
	currentTheme := theme.Current()

	maxVisible := 8
	startIndex := maxInt(0, minInt(s.selectedIndex-maxVisible/2, len(s.filteredProviders)-maxVisible))
	endIndex := minInt(startIndex+maxVisible, len(s.filteredProviders))

	for i := startIndex; i < endIndex; i++ {
		provider := s.filteredProviders[i]

		isSelected := i == s.selectedIndex

		statusIndicator := s.formatStatusIndicator(provider)
		authTypeLabel := ""
		if s.showAuthTypeLabels {
			authTypeLabel = currentTheme.Fg("muted", " ["+FormatAuthSelectorProviderType(provider.AuthType)+"]")
		}
		var line string
		if isSelected {
			prefix := currentTheme.Fg("accent", "→ ")
			text := currentTheme.Fg("accent", provider.Name)
			line = prefix + text + authTypeLabel + statusIndicator
		} else {
			text := "  " + currentTheme.Fg("text", provider.Name)
			line = text + authTypeLabel + statusIndicator
		}

		s.listContainer.AddChild(tui.NewTruncatedText(line, 1, 0))
	}

	if startIndex > 0 || endIndex < len(s.filteredProviders) {
		scrollInfo := currentTheme.Fg("muted", "  ("+itoa(s.selectedIndex+1)+"/"+itoa(len(s.filteredProviders))+")")
		s.listContainer.AddChild(tui.NewTruncatedText(scrollInfo, 1, 0))
	}

	// Show "no providers" if empty
	if len(s.filteredProviders) == 0 {
		message := "No matching providers"
		if len(s.allProviders) == 0 {
			if s.mode == "login" {
				message = "No providers available"
			} else {
				message = "No providers logged in. Use /login first."
			}
		}
		s.listContainer.AddChild(tui.NewTruncatedText(currentTheme.Fg("muted", "  "+message), 1, 0))
	}
}

func (s *OAuthSelectorComponent) formatStatusIndicator(provider AuthSelectorProvider) string {
	currentTheme := theme.Current()
	if provider.Status == nil {
		return currentTheme.Fg("muted", " • not configured")
	}
	if provider.Status.Type != provider.AuthType {
		label := "API key configured"
		if provider.Status.Type == auth.AuthTypeOAuth {
			label = "subscription configured"
		}
		return currentTheme.Fg("muted", " • ") + currentTheme.Fg("warning", label)
	}
	if provider.Status.Source == "" ||
		provider.Status.Source == "OAuth" ||
		provider.Status.Source == "stored credential" {
		return currentTheme.Fg("success", " ✓ configured")
	}
	source := provider.Status.Source
	if envSourcePattern.MatchString(provider.Status.Source) {
		source = "env: " + provider.Status.Source
	}
	return currentTheme.Fg("success", " ✓ "+source)
}

// HandleInput implements InputHandler (pi: handleInput).
func (s *OAuthSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	// Up arrow
	case kb.Matches(keyData, "tui.select.up"):
		if len(s.filteredProviders) == 0 {
			return
		}
		s.selectedIndex = maxInt(0, s.selectedIndex-1)
		s.updateList()
	// Down arrow
	case kb.Matches(keyData, "tui.select.down"):
		if len(s.filteredProviders) == 0 {
			return
		}
		s.selectedIndex = minInt(s.selectedIndex+1, len(s.filteredProviders)-1)
		s.updateList()
	// Enter
	case kb.Matches(keyData, "tui.select.confirm"):
		if s.selectedIndex < len(s.filteredProviders) {
			selectedProvider := s.filteredProviders[s.selectedIndex]
			if s.onSelectCallback != nil {
				s.onSelectCallback(selectedProvider.ID, selectedProvider.AuthType)
			}
		}
	// Escape or Ctrl+C
	case kb.Matches(keyData, "tui.select.cancel"):
		if s.onCancelCallback != nil {
			s.onCancelCallback()
		}
	// Pass everything else to search input
	default:
		s.searchInput.HandleInput(keyData)
		s.filterProviders(s.searchInput.GetValue())
	}
}
