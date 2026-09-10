package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	codingagent "github.com/ZoroSpace/pig/codingagent"
)

// Port of test/theme-export.test.ts (getThemeExportColors).

type themeFile struct {
	Name   string         `json:"name"`
	Vars   map[string]any `json:"vars,omitempty"`
	Colors map[string]any `json:"colors"`
	Export map[string]any `json:"export,omitempty"`
}

func readDarkThemeFixture(t *testing.T) themeFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "theme", "dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme themeFile
	if err := json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	return theme
}

func TestGetThemeExportColors(t *testing.T) {
	t.Run("resolves export variable references using the same syntax as colors", func(t *testing.T) {
		tempRoot := t.TempDir()
		agentDir := filepath.Join(tempRoot, "agent")
		t.Setenv(codingagent.EnvAgentDir, agentDir)
		if err := os.MkdirAll(filepath.Join(agentDir, "themes"), 0o755); err != nil {
			t.Fatal(err)
		}

		darkTheme := readDarkThemeFixture(t)
		vars := map[string]any{}
		for k, v := range darkTheme.Vars {
			vars[k] = v
		}
		vars["pageBgVar"] = "#112233"
		vars["pageBgAlias"] = "pageBgVar"
		vars["infoBgVar"] = "#445566"
		vars["cardBgVar"] = "#223344"

		customTheme := themeFile{
			Name:   "custom-export-vars",
			Vars:   vars,
			Colors: darkTheme.Colors,
			Export: map[string]any{
				"pageBg": "pageBgAlias",
				"cardBg": "cardBgVar",
				"infoBg": "infoBgVar",
			},
		}

		data, err := json.MarshalIndent(customTheme, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "themes", "custom-export-vars.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}

		colors := GetThemeExportColors("custom-export-vars")
		if colors.PageBg != "#112233" || colors.CardBg != "#223344" || colors.InfoBg != "#445566" {
			t.Fatalf("got %+v", colors)
		}
	})

	t.Run("resolves recursive vars and converts 256-color export values to hex", func(t *testing.T) {
		tempRoot := t.TempDir()
		agentDir := filepath.Join(tempRoot, "agent")
		t.Setenv(codingagent.EnvAgentDir, agentDir)
		if err := os.MkdirAll(filepath.Join(agentDir, "themes"), 0o755); err != nil {
			t.Fatal(err)
		}

		darkTheme := readDarkThemeFixture(t)
		vars := map[string]any{}
		for k, v := range darkTheme.Vars {
			vars[k] = v
		}
		vars["deepPageBg"] = "#abcdef"
		vars["pageBgAlias"] = "deepPageBg"
		vars["cardBgAnsi"] = 24

		customTheme := themeFile{
			Name:   "custom-export-recursive",
			Vars:   vars,
			Colors: darkTheme.Colors,
			Export: map[string]any{
				"pageBg": "pageBgAlias",
				"cardBg": "cardBgAnsi",
				"infoBg": "",
			},
		}

		data, err := json.MarshalIndent(customTheme, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "themes", "custom-export-recursive.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}

		colors := GetThemeExportColors("custom-export-recursive")
		if colors.PageBg != "#abcdef" || colors.CardBg != "#005f87" || colors.InfoBg != "" {
			t.Fatalf("got %+v", colors)
		}
	})
}
