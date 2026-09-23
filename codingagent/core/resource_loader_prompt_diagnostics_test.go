// pi: test/resource-loader.test.ts — the prompt-frontmatter diagnostics half
// (#9830); pig's DefaultResourceLoader suite otherwise rides on the sdk /
// runtime tests, so only the new diagnostics consumption is ported here.
package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pi: "should report invalid prompt frontmatter while loading valid
// siblings" — regression test for #9354 / #9830.
func TestDefaultResourceLoaderReportsInvalidPromptFrontmatter(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	promptsDir := filepath.Join(agentDir, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	invalidPromptPath := filepath.Join(promptsDir, "invalid.md")
	if err := os.WriteFile(invalidPromptPath, []byte("---\ndescription: Broken: unquoted colon\n---\nDo something.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "valid.md"), []byte("Valid prompt content."), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: InMemorySettingsManager(nil, nil),
		NoExtensions:    true,
		NoSkills:        true,
		NoThemes:        true,
		NoContextFiles:  true,
	})
	if err := loader.Reload(nil); err != nil {
		t.Fatal(err)
	}

	prompts, diagnostics := loader.GetPrompts()
	names := []string{}
	for _, prompt := range prompts {
		names = append(names, prompt.Name)
	}
	if len(names) != 1 || names[0] != "valid" {
		t.Fatalf("prompt names = %q, want [valid]", names)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", diagnostics)
	}
	diagnostic := diagnostics[0]
	// pi asserts message contains "line 1, column 14" (npm yaml position
	// format); yaml.v3's scanner error for this shape carries no position,
	// so pig pins the yaml-flavored substring instead.
	if diagnostic.Type != "warning" || diagnostic.Path != invalidPromptPath || !strings.Contains(diagnostic.Message, "mapping values") {
		t.Fatalf("diagnostic = %+v, want a frontmatter warning for %q", diagnostic, invalidPromptPath)
	}
}
