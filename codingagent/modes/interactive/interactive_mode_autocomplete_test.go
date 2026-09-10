// interactive_mode_autocomplete_test.go (pi:
// test/interactive-mode-status.test.ts — the
// createExtensionUIContext.addAutocompleteProvider / setupAutocompleteProvider
// / createBaseAutocompleteProvider describes).
//
// The login-arguments case (pi: "matches login command arguments by provider
// id and name") lands with T6's /login selector (getLoginProviderOptions).
package interactive

import (
	"context"
	"strings"
	"testing"

	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// trackingProvider records calls and forwards to the wrapped provider (pi:
// the wrap1/wrap2 object literals).
type trackingProvider struct {
	wrapped tui.AutocompleteProvider
	tag     string
	calls   *[]string
}

func (p *trackingProvider) GetSuggestions(ctx context.Context, lines []string, cursorLine, cursorCol int, options tui.AutocompleteGetSuggestionsOptions) *tui.AutocompleteSuggestions {
	*p.calls = append(*p.calls, "getSuggestions:"+p.tag)
	return p.wrapped.GetSuggestions(ctx, lines, cursorLine, cursorCol, options)
}

func (p *trackingProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item tui.AutocompleteItem, prefix string) tui.AutocompleteApplyResult {
	*p.calls = append(*p.calls, "applyCompletion:"+p.tag)
	return p.wrapped.ApplyCompletion(lines, cursorLine, cursorCol, item, prefix)
}

// passThroughTriggerProvider forwards and exposes trigger characters (pi:
// the passThrough factory in the triggerCharacters test).
type passThroughTriggerProvider struct {
	tui.AutocompleteProvider
	triggerCharacters []string
}

func (p *passThroughTriggerProvider) TriggerCharacters() []string { return p.triggerCharacters }

func (p *passThroughTriggerProvider) SetTriggerCharacters(chars []string) {
	p.triggerCharacters = chars
}

// pi: "stores wrapper factories and rebuilds autocomplete immediately".
func TestAddAutocompleteProviderStoresAndRebuilds(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	mode.defaultEditor = components.NewCustomEditor(mode.ui, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)
	mode.editor = mode.defaultEditor

	wrapper := core.AutocompleteProviderFactory(func(current tui.AutocompleteProvider) tui.AutocompleteProvider {
		return current
	})

	uiContext := mode.createExtensionUIContext()
	uiContext.AddAutocompleteProvider(context.Background(), wrapper)

	if len(mode.autocompleteProviderWrappers) != 1 {
		t.Fatalf("expected 1 wrapper, got %d", len(mode.autocompleteProviderWrappers))
	}
	if mode.autocompleteProvider == nil {
		t.Fatal("expected the provider to be rebuilt immediately")
	}
	if mode.defaultEditor.GetAutocompleteProvider() != mode.autocompleteProvider {
		t.Fatal("expected the editor to carry the rebuilt provider")
	}
}

// pi: "stacks wrapper factories over a fresh base provider".
func TestSetupAutocompleteProviderStacksWrappers(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	mode.defaultEditor = components.NewCustomEditor(mode.ui, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)
	mode.editor = mode.defaultEditor

	calls := []string{}
	wrap1 := core.AutocompleteProviderFactory(func(current tui.AutocompleteProvider) tui.AutocompleteProvider {
		return &trackingProvider{wrapped: current, tag: "wrap1", calls: &calls}
	})
	wrap2 := core.AutocompleteProviderFactory(func(current tui.AutocompleteProvider) tui.AutocompleteProvider {
		return &trackingProvider{wrapped: current, tag: "wrap2", calls: &calls}
	})
	mode.autocompleteProviderWrappers = []core.AutocompleteProviderFactory{wrap1, wrap2}

	mode.setupAutocompleteProvider()

	// pi additionally asserts provider.shouldTriggerFileCompletion; pig keeps
	// the stacking-order assertions.
	tp := mode.autocompleteProvider.(*trackingProvider)
	if _, ok := tp.wrapped.(*trackingProvider); !ok {
		t.Fatal("expected wrap2 to wrap wrap1")
	}
	if tp.tag != "wrap2" {
		t.Fatalf("expected outermost wrapper wrap2, got %s", tp.tag)
	}
	if inner := tp.wrapped.(*trackingProvider); inner.tag != "wrap1" {
		t.Fatalf("expected inner wrapper wrap1, got %s", inner.tag)
	}
}

// pi: "merges triggerCharacters from wrapper factories".
func TestSetupAutocompleteProviderMergesTriggerCharacters(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	mode.defaultEditor = components.NewCustomEditor(mode.ui, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)
	mode.editor = mode.defaultEditor

	passThrough := func(triggerCharacters []string) core.AutocompleteProviderFactory {
		return func(current tui.AutocompleteProvider) tui.AutocompleteProvider {
			return &passThroughTriggerProvider{AutocompleteProvider: current, triggerCharacters: triggerCharacters}
		}
	}
	mode.autocompleteProviderWrappers = []core.AutocompleteProviderFactory{
		passThrough([]string{"$"}),
		passThrough([]string{"!"}),
	}

	mode.setupAutocompleteProvider()

	setter, ok := mode.autocompleteProvider.(tui.TriggerCharactersSetter)
	if !ok {
		t.Fatal("expected the merged provider to accept trigger characters")
	}
	_ = setter
	// setupAutocompleteProvider collects ["$"] then ["!"] in wrapper order,
	// dedupes, and pushes the merged slice back onto the outermost provider
	// (pi asserts the merged ["$", "!"]).
	merged := mode.autocompleteProvider.(*passThroughTriggerProvider).TriggerCharacters()
	if len(merged) != 2 || merged[0] != "$" || merged[1] != "!" {
		t.Fatalf("expected merged [\"$\" \"!\"], got %v", merged)
	}
}

// pi: "matches model command arguments across provider/model order".
func TestCreateBaseAutocompleteProviderModelArguments(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	registerTestProvider(t, session, "openai-codex", []core.ProviderConfigInputModel{{
		ID: "gpt-5.5", Name: "GPT-5.5", API: "openai-completions", BaseURL: "http://localhost:0",
	}})
	registerTestProvider(t, session, "github-copilot", []core.ProviderConfigInputModel{{
		ID: "gpt-5.2-codex", Name: "GPT-5.2 Codex", API: "openai-completions", BaseURL: "http://localhost:0",
	}})

	mode := newSeamTestMode(session, nil)
	mode.fdPath = ""

	provider := mode.createBaseAutocompleteProvider()
	line := "/model codexgpt"
	suggestions := provider.GetSuggestions(context.Background(), []string{line}, 0, len(line), tui.AutocompleteGetSuggestionsOptions{})
	if suggestions == nil {
		t.Fatal("expected suggestions")
	}
	var values []string
	for _, item := range suggestions.Items {
		values = append(values, item.Value)
	}
	want := []string{"openai-codex/gpt-5.5", "github-copilot/gpt-5.2-codex"}
	if strings.Join(values, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %v, got %v", want, values)
	}
}
