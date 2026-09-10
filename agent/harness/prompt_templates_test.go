// pi: test/harness/prompt-templates.test.ts — template loading over the
// node execution env plus argument substitution.
package harness_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent/harness"
)

func TestLoadsMarkdownTemplatesNonRecursivelyFromOneOrMoreDirs(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustCreateDir(t, e, "a/nested")
	mustCreateDir(t, e, "b")
	mustWriteFile(t, e, "a/one.md", "---\ndescription: One template\n---\nHello $1")
	mustWriteFile(t, e, "a/nested/ignored.md", "Ignored")
	mustWriteFile(t, e, "b/two.md", "First line description\nBody")

	templates, diagnostics := harness.LoadPromptTemplates(e, []string{"a", "b"})

	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want empty", diagnostics)
	}
	want := []harness.PromptTemplate{
		{Name: "one", Description: lo.ToPtr("One template"), Content: "Hello $1"},
		{Name: "two", Description: lo.ToPtr("First line description"), Content: "First line description\nBody"},
	}
	if len(templates) != len(want) {
		t.Fatalf("templates = %v, want %v", templates, want)
	}
	for i, w := range want {
		if !promptTemplateEqual(templates[i], w) {
			t.Fatalf("templates[%d] = %+v, want %+v", i, templates[i], w)
		}
	}
}

func TestPreservesSourceInfoForSourcedPromptTemplates(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustCreateDir(t, e, "prompts")
	mustWriteFile(t, e, "prompts/example.md", "---\ndescription: Example\n---\nExample body")

	templates, diagnostics := harness.LoadSourcedPromptTemplates(e, []harness.SourcedInput[testSource]{
		{Path: "prompts", Source: testSource{Type: "project"}},
	}, nil)

	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want empty", diagnostics)
	}
	if len(templates) != 1 {
		t.Fatalf("templates = %v, want 1 entry", templates)
	}
	want := harness.PromptTemplate{Name: "example", Description: lo.ToPtr("Example"), Content: "Example body"}
	if !promptTemplateEqual(templates[0].PromptTemplate, want) {
		t.Fatalf("templates[0].PromptTemplate = %+v, want %+v", templates[0].PromptTemplate, want)
	}
	if templates[0].Source != (testSource{Type: "project"}) {
		t.Fatalf("templates[0].Source = %+v, want project", templates[0].Source)
	}
}

func TestAttachesSourceInfoToTemplateDiagnostics(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustWriteFile(t, e, "broken.md", "---\ndescription: [unterminated\n---\nBody")

	templates, diagnostics := harness.LoadSourcedPromptTemplates(e, []harness.SourcedInput[testSource]{
		{Path: "broken.md", Source: testSource{Type: "user"}},
	}, nil)

	if len(templates) != 0 {
		t.Fatalf("templates = %v, want empty", templates)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want 1 entry", diagnostics)
	}
	diag := diagnostics[0]
	if diag.Type != "warning" || diag.Code != harness.PromptTemplateDiagParseFailed {
		t.Fatalf("diagnostic type/code = %s/%s, want warning/parse_failed", diag.Type, diag.Code)
	}
	if diag.Path != filepath.Join(root, "broken.md") {
		t.Fatalf("diagnostic path = %s, want %s", diag.Path, filepath.Join(root, "broken.md"))
	}
	if diag.Source != (testSource{Type: "user"}) {
		t.Fatalf("diagnostic source = %+v, want user", diag.Source)
	}
}

func TestLoadsExplicitMarkdownFilesAndSymlinkedFiles(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustWriteFile(t, e, "target.md", "---\ndescription: Target\n---\nTarget body")
	if err := os.Symlink(filepath.Join(root, "target.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	templates, _ := harness.LoadPromptTemplates(e, []string{"target.md", "link.md"})

	want := []harness.PromptTemplate{
		{Name: "target", Description: lo.ToPtr("Target"), Content: "Target body"},
		{Name: "link", Description: lo.ToPtr("Target"), Content: "Target body"},
	}
	if len(templates) != len(want) {
		t.Fatalf("templates = %v, want %v", templates, want)
	}
	for i, w := range want {
		if !promptTemplateEqual(templates[i], w) {
			t.Fatalf("templates[%d] = %+v, want %+v", i, templates[i], w)
		}
	}
}

func TestSubstitutesCommandArguments(t *testing.T) {
	content := "$1 $" + "{@:2} $ARGUMENTS"
	got := harness.FormatPromptTemplateInvocation(harness.PromptTemplate{Name: "one", Content: content}, []string{"hello world", "test"})
	want := "hello world test hello world test"
	if got != want {
		t.Fatalf("FormatPromptTemplateInvocation = %q, want %q", got, want)
	}
}
