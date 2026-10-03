package theme

import (
	"testing"
)

// The built-in theme set is pi's dark / light only. The pig-only promoted
// palettes (one-dark-pro / github-light, 2026-10-03) were removed the same
// day by user decision; themes without the optional pig-only rule tokens
// fall back through FgOptional (locked below).

func TestBuiltinThemeNamesAreDarkAndLight(t *testing.T) {
	want := []string{"dark", "light"}
	if len(BuiltinThemeNames) != len(want) {
		t.Fatalf("BuiltinThemeNames = %v, want %v", BuiltinThemeNames, want)
	}
	for i, name := range want {
		if BuiltinThemeNames[i] != name {
			t.Fatalf("BuiltinThemeNames = %v, want %v", BuiltinThemeNames, want)
		}
	}
}

func TestBuiltinThemesStillLoad(t *testing.T) {
	for _, name := range BuiltinThemeNames {
		theme := GetThemeByName(name)
		if theme == nil {
			t.Fatalf("%s theme not found", name)
		}
	}
	// The removed palettes must no longer resolve.
	for _, name := range []string{"one-dark-pro", "github-light"} {
		if theme := GetThemeByName(name); theme != nil {
			t.Fatalf("%s should not be a built-in theme anymore", name)
		}
	}
}

func TestFgOptionalFallsBackWhenTokenUndefined(t *testing.T) {
	theme := GetThemeByName("dark")
	if theme == nil {
		t.Fatal("dark theme not found")
	}
	if theme.HasFgToken("userMessageRule") {
		t.Fatal("dark should not define userMessageRule")
	}
	want := theme.Fg("text", "x")
	if got := theme.FgOptional("userMessageRule", "text", "x"); got != want {
		t.Fatalf("FgOptional fallback = %q, want %q", got, want)
	}
	wantDefined := theme.Fg("dim", "x")
	if got := theme.FgOptional("dim", "text", "x"); got != wantDefined {
		t.Fatalf("FgOptional defined = %q, want %q", got, wantDefined)
	}
}
