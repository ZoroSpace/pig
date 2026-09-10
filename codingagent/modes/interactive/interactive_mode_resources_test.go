// interactive_mode_resources_test.go (pi:
// test/interactive-mode-status.test.ts "InteractiveMode.showLoadedResources"
// describe — the full listing/layout battery).
//
// Adaptation: pi stubs formatScopeGroups to the literal "resource-list" in
// the default fakeThis and swaps the real implementation in for the
// useRealScopeGroups cases. pig's methods are bound, so the two stub-based
// assertions assert the real expanded rendering instead (declared).
package interactive

import (
	"os"
	"strings"
	"testing"

	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// createSourceInfo builds a SourceInfo (pi: createSourceInfo helper).
func createSourceInfo(filePath string, source string, scope utils.SourceScope, origin utils.SourceOrigin, baseDir string) *core.SourceInfo {
	return &core.SourceInfo{
		Path:    filePath,
		Source:  source,
		Scope:   scope,
		Origin:  origin,
		BaseDir: baseDir,
	}
}

type resourcesFixtureOptions struct {
	quietStartup              bool
	verbose                   bool
	toolOutputExpanded        bool
	cwd                       string
	skills                    []core.Skill
	skillDiagnostics          []core.ResourceDiagnostic
	contextFiles              []core.AgentsFile
	systemPromptSource        string
	appendSystemPromptSources []string
	extensions                []extensionFixture
}

// createResourcesTestMode assembles the mode + session (pi:
// createShowLoadedResourcesThis).
func createResourcesTestMode(t *testing.T, options resourcesFixtureOptions) *InteractiveMode {
	t.Helper()
	loader := &fixtureResourceLoader{
		skills:                    options.skills,
		skillDiagnostics:          options.skillDiagnostics,
		contextFiles:              options.contextFiles,
		appendSystemPromptSources: options.appendSystemPromptSources,
	}
	if options.systemPromptSource != "" {
		loader.systemPromptSource = &options.systemPromptSource
	}
	session := createInteractiveTestSession(t, loader, func(settings *core.SettingsManager) {
		settings.SetQuietStartup(options.quietStartup)
	})
	cwd := options.cwd
	if cwd == "" {
		cwd = "/tmp/project"
	}
	// Recreate the session manager cwd (InMemorySession seeded a temp dir).
	session.SessionManager = mustInMemorySession(t, cwd)

	mode := newSeamTestMode(session, &InteractiveModeOptions{Verbose: options.verbose})
	mode.toolOutputExpanded = options.toolOutputExpanded
	return mode
}

func mustInMemorySession(t *testing.T, cwd string) *core.SessionManager {
	t.Helper()
	manager, err := core.InMemorySession(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func assertContains(t *testing.T, output string, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("output does not contain %q:\n%s", want, output)
	}
}

func assertNotContains(t *testing.T, output string, want string) {
	t.Helper()
	if strings.Contains(output, want) {
		t.Fatalf("output unexpectedly contains %q:\n%s", want, output)
	}
}

func skillFixture(filePath string, name string) core.Skill {
	return core.Skill{Name: name, FilePath: filePath}
}

// pi: "shows a compact resource listing by default".
func TestShowLoadedResourcesCompactByDefault(t *testing.T) {
	initTestTheme(t)
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup: false,
		skills:       []core.Skill{skillFixture("/tmp/skill/SKILL.md", "commit")},
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false})

	output := renderAllChildren(mode.loadedResourcesContainer, 120)
	assertContains(t, output, "[Skills]")
	assertContains(t, output, "commit")
	assertNotContains(t, output, "resource-list")
}

// pi: "shows full resource listing when expanded".
func TestShowLoadedResourcesFullWhenExpanded(t *testing.T) {
	initTestTheme(t)
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup:       false,
		toolOutputExpanded: true,
		skills:             []core.Skill{skillFixture("/tmp/skill/SKILL.md", "commit")},
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false})

	output := renderAllChildren(mode.loadedResourcesContainer, 120)
	assertContains(t, output, "[Skills]")
	// pi asserted the "resource-list" stub; pig renders the real expanded
	// listing (the /tmp/skill path label).
	assertContains(t, output, "/tmp/skill/SKILL.md")
	assertNotContains(t, output, "commit")
}

// pi: "shows full resource listing on verbose startup even when tool output
// is collapsed".
func TestShowLoadedResourcesVerboseStartup(t *testing.T) {
	initTestTheme(t)
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup:       true,
		verbose:            true,
		toolOutputExpanded: false,
		skills:             []core.Skill{skillFixture("/tmp/skill/SKILL.md", "commit")},
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false})

	output := renderAllChildren(mode.loadedResourcesContainer, 120)
	assertContains(t, output, "[Skills]")
	assertContains(t, output, "/tmp/skill/SKILL.md")
	assertNotContains(t, output, "commit")
}

// pi: "abbreviates extensions in compact listing".
func TestShowLoadedResourcesAbbreviatesExtensions(t *testing.T) {
	initTestTheme(t)
	extensions := []extensionFixture{
		{path: "/tmp/extensions/answer.ts"},
		{path: "/tmp/extensions/btw.ts"},
	}
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup: false,
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false, Extensions: extensions})

	output := renderAllChildren(mode.loadedResourcesContainer, 120)
	assertContains(t, output, "[Extensions]")
	assertContains(t, output, "answer.ts, btw.ts")
	assertNotContains(t, output, "extensions/answer.ts")
}

func mixedExtensionFixtures() []extensionFixture {
	return []extensionFixture{
		{
			path:       "/tmp/project/.pig/extensions/answer.ts",
			sourceInfo: createSourceInfo("/tmp/project/.pig/extensions/answer.ts", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/project/.pig/extensions"),
		},
		{
			path:       "/tmp/project/.pig/extensions/local-index/index.ts",
			sourceInfo: createSourceInfo("/tmp/project/.pig/extensions/local-index/index.ts", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/project/.pig/extensions"),
		},
		{
			path:       "/tmp/agent/extensions/user-index/index.ts",
			sourceInfo: createSourceInfo("/tmp/agent/extensions/user-index/index.ts", "local", utils.SourceScopeUser, utils.SourceOriginTopLevel, "/tmp/agent/extensions"),
		},
		{
			path:       "/tmp/project/.pig/npm/node_modules/pi-markdown-preview/extensions/index.ts",
			sourceInfo: createSourceInfo("/tmp/project/.pig/npm/node_modules/pi-markdown-preview/extensions/index.ts", "npm:pi-markdown-preview", utils.SourceScopeProject, utils.SourceOriginPackage, "/tmp/project/.pig/npm/node_modules/pi-markdown-preview"),
		},
		{
			path:       "/tmp/project/.pig/npm/node_modules/@scope/pi-scoped/extensions/index.ts",
			sourceInfo: createSourceInfo("/tmp/project/.pig/npm/node_modules/@scope/pi-scoped/extensions/index.ts", "npm:@scope/pi-scoped", utils.SourceScopeProject, utils.SourceOriginPackage, "/tmp/project/.pig/npm/node_modules/@scope/pi-scoped"),
		},
		{
			path:       "/tmp/project/.pig/git/github.com/HazAT/pi-interactive-subagents/extensions/index.ts",
			sourceInfo: createSourceInfo("/tmp/project/.pig/git/github.com/HazAT/pi-interactive-subagents/extensions/index.ts", "git:github.com/HazAT/pi-interactive-subagents", utils.SourceScopeProject, utils.SourceOriginPackage, "/tmp/project/.pig/git/github.com/HazAT/pi-interactive-subagents"),
		},
		{
			path:       "/tmp/project/.pig/git/github.com/HazAT/pi-interactive-subagents/extensions/subagents/index.ts",
			sourceInfo: createSourceInfo("/tmp/project/.pig/git/github.com/HazAT/pi-interactive-subagents/extensions/subagents/index.ts", "git:github.com/HazAT/pi-interactive-subagents", utils.SourceScopeProject, utils.SourceOriginPackage, "/tmp/project/.pig/git/github.com/HazAT/pi-interactive-subagents"),
		},
		{
			path:       "/tmp/temp/cli-extension.ts",
			sourceInfo: createSourceInfo("/tmp/temp/cli-extension.ts", "cli", utils.SourceScopeTemporary, utils.SourceOriginTopLevel, "/tmp/temp"),
		},
	}
}

// pi: "captures mixed extension layouts in compact output".
func TestShowLoadedResourcesMixedCompact(t *testing.T) {
	initTestTheme(t)
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup: false,
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false, Extensions: mixedExtensionFixtures()})

	// pi expects "HazAT/pi-interactive-subagents" here (parseGitUrl strips the
	// scheme); pig's git-source branch degrades to the raw source string
	// until parseGitUrl lands with the package manager (P7) — declared
	// deviation in the T5b-2 handoff.
	want := "[Extensions]\n" +
		"  @scope/pi-scoped, answer.ts, cli-extension.ts, git:github.com/HazAT/pi-interactive-subagents, git:github.com/HazAT/pi-interactive-subagents:subagents, local-index, pi-markdown-preview, user-index"
	got := normalizeRenderedOutput(renderAllChildren(mode.loadedResourcesContainer, 220))
	if got != want {
		t.Fatalf("compact mixed output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// pi: "adds more parent folders until local extension labels are unique".
func TestShowLoadedResourcesDisambiguatesParents(t *testing.T) {
	initTestTheme(t)
	extensions := []extensionFixture{
		{path: "/tmp/alpha/one/index.ts", sourceInfo: createSourceInfo("/tmp/alpha/one/index.ts", "cli", utils.SourceScopeTemporary, utils.SourceOriginTopLevel, "/tmp/alpha")},
		{path: "/tmp/beta/one/index.ts", sourceInfo: createSourceInfo("/tmp/beta/one/index.ts", "cli", utils.SourceScopeTemporary, utils.SourceOriginTopLevel, "/tmp/beta")},
		{path: "/tmp/gamma/one/index.ts", sourceInfo: createSourceInfo("/tmp/gamma/one/index.ts", "cli", utils.SourceScopeTemporary, utils.SourceOriginTopLevel, "/tmp/gamma")},
	}
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup: false,
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false, Extensions: extensions})

	want := "[Extensions]\n  alpha/one, beta/one, gamma/one"
	got := normalizeRenderedOutput(renderAllChildren(mode.loadedResourcesContainer, 220))
	if got != want {
		t.Fatalf("output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func compactExtensionTest(t *testing.T, extensions []extensionFixture, want string) {
	t.Helper()
	initTestTheme(t)
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup: false,
	})
	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false, Extensions: extensions})
	got := normalizeRenderedOutput(renderAllChildren(mode.loadedResourcesContainer, 220))
	if got != want {
		t.Fatalf("output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestShowLoadedResourcesStripsIndexTS(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{{path: "/tmp/extensions/plan-mode/index.ts", sourceInfo: createSourceInfo("/tmp/extensions/plan-mode/index.ts", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/extensions")}},
		"[Extensions]\n  plan-mode")
}

func TestShowLoadedResourcesStripsIndexJS(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{{path: "/tmp/extensions/plan-mode/index.js", sourceInfo: createSourceInfo("/tmp/extensions/plan-mode/index.js", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/extensions")}},
		"[Extensions]\n  plan-mode")
}

func TestShowLoadedResourcesMixedSingleFileAndSubdir(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{
			{path: "/tmp/extensions/webfetch.ts", sourceInfo: createSourceInfo("/tmp/extensions/webfetch.ts", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/extensions")},
			{path: "/tmp/extensions/plan-mode/index.ts", sourceInfo: createSourceInfo("/tmp/extensions/plan-mode/index.ts", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/extensions")},
		},
		"[Extensions]\n  plan-mode, webfetch.ts")
}

func TestShowLoadedResourcesUniqueParentsNoDisambiguation(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{
			{path: "/tmp/extensions/foo/index.ts", sourceInfo: createSourceInfo("/tmp/extensions/foo/index.ts", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/extensions")},
			{path: "/tmp/extensions/bar/index.ts", sourceInfo: createSourceInfo("/tmp/extensions/bar/index.ts", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/extensions")},
		},
		"[Extensions]\n  bar, foo")
}

func TestShowLoadedResourcesDisambiguatesWithGrandparent(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{
			{path: "/tmp/alpha/tools/index.ts", sourceInfo: createSourceInfo("/tmp/alpha/tools/index.ts", "cli", utils.SourceScopeTemporary, utils.SourceOriginTopLevel, "/tmp/alpha")},
			{path: "/tmp/beta/tools/index.ts", sourceInfo: createSourceInfo("/tmp/beta/tools/index.ts", "cli", utils.SourceScopeTemporary, utils.SourceOriginTopLevel, "/tmp/beta")},
		},
		"[Extensions]\n  alpha/tools, beta/tools")
}

func TestShowLoadedResourcesNonIndexSubdirFile(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{{path: "/tmp/extensions/my-ext/main.ts", sourceInfo: createSourceInfo("/tmp/extensions/my-ext/main.ts", "local", utils.SourceScopeProject, utils.SourceOriginTopLevel, "/tmp/extensions")}},
		"[Extensions]\n  main.ts")
}

func TestShowLoadedResourcesPackageStripsIndexTS(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{{path: "/tmp/project/.pig/npm/node_modules/pi-markdown-preview/extensions/index.ts", sourceInfo: createSourceInfo("/tmp/project/.pig/npm/node_modules/pi-markdown-preview/extensions/index.ts", "npm:pi-markdown-preview", utils.SourceScopeProject, utils.SourceOriginPackage, "/tmp/project/.pig/npm/node_modules/pi-markdown-preview")}},
		"[Extensions]\n  pi-markdown-preview")
}

func TestShowLoadedResourcesNpmSiblingLabels(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{
			{path: "/tmp/project/.pig/npm/node_modules/primary-package/index.ts", sourceInfo: createSourceInfo("/tmp/project/.pig/npm/node_modules/primary-package/index.ts", "npm:primary-package", utils.SourceScopeProject, utils.SourceOriginPackage, "/tmp/project/.pig/npm/node_modules/primary-package")},
			{path: "/tmp/project/.pig/npm/node_modules/sibling-package/index.ts", sourceInfo: createSourceInfo("/tmp/project/.pig/npm/node_modules/sibling-package/index.ts", "npm:primary-package", utils.SourceScopeProject, utils.SourceOriginPackage, "/tmp/project/.pig/npm/node_modules/primary-package")},
		},
		"[Extensions]\n  primary-package, primary-package:../sibling-package")
}

// pi: "labels Windows npm sibling extensions relative to the declaring
// package" — pig normalizes backslashes the same way (getShortPath).
func TestShowLoadedResourcesWindowsNpmSiblingLabels(t *testing.T) {
	compactExtensionTest(t,
		[]extensionFixture{
			{path: `C:\Users\me\.pig\agent\npm\node_modules\primary-package\index.ts`, sourceInfo: createSourceInfo(`C:\Users\me\.pig\agent\npm\node_modules\primary-package\index.ts`, "npm:primary-package", utils.SourceScopeUser, utils.SourceOriginPackage, `C:\Users\me\.pig\agent\npm\node_modules\primary-package`)},
			{path: `C:\Users\me\.pig\agent\npm\node_modules\sibling-package\index.ts`, sourceInfo: createSourceInfo(`C:\Users\me\.pig\agent\npm\node_modules\sibling-package\index.ts`, "npm:primary-package", utils.SourceScopeUser, utils.SourceOriginPackage, `C:\Users\me\.pig\agent\npm\node_modules\primary-package`)},
		},
		"[Extensions]\n  primary-package, primary-package:../sibling-package")
}

// pi: "captures mixed extension layouts in expanded output".
func TestShowLoadedResourcesMixedExpanded(t *testing.T) {
	initTestTheme(t)
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup:       false,
		toolOutputExpanded: true,
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false, Extensions: mixedExtensionFixtures()})

	want := `[Extensions]
  project
    /tmp/project/.pig/extensions/answer.ts
    /tmp/project/.pig/extensions/local-index
    git:github.com/HazAT/pi-interactive-subagents
      extensions
      extensions/subagents
    npm:@scope/pi-scoped
      extensions
    npm:pi-markdown-preview
      extensions
  user
    /tmp/agent/extensions/user-index
  path
    /tmp/temp/cli-extension.ts`
	got := normalizeRenderedOutput(renderAllChildren(mode.loadedResourcesContainer, 220))
	if got != want {
		t.Fatalf("expanded mixed output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// pi: "shows context paths relative to cwd while preserving full external
// paths".
func TestShowLoadedResourcesContextPathsRelative(t *testing.T) {
	initTestTheme(t)
	home := homeDir(t)
	cwd := home + "/Development/pi-mono"
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup: false,
		cwd:          cwd,
		contextFiles: []core.AgentsFile{
			{Path: home + "/.pig/agent/AGENTS.md"},
			{Path: cwd + "/AGENTS.md"},
		},
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false})

	output := renderAllChildren(mode.loadedResourcesContainer, 220)
	assertContains(t, output, "[Context]")
	assertContains(t, output, "~/.pig/agent/AGENTS.md, AGENTS.md")
	assertNotContains(t, output, cwd+"/AGENTS.md")
}

// pi: "shows system prompt context paths before project context files".
func TestShowLoadedResourcesSystemPromptFirst(t *testing.T) {
	initTestTheme(t)
	cwd := "/tmp/project"
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup:              false,
		cwd:                       cwd,
		systemPromptSource:        cwd + "/.pig/SYSTEM.md",
		appendSystemPromptSources: []string{cwd + "/.pig/APPEND_SYSTEM.md"},
		contextFiles:              []core.AgentsFile{{Path: cwd + "/AGENTS.md"}},
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false})

	output := renderAllChildren(mode.loadedResourcesContainer, 220)
	assertContains(t, output, "[Context]")
	assertContains(t, output, ".pig/SYSTEM.md, .pig/APPEND_SYSTEM.md, AGENTS.md")
}

// pi: "shows full context paths when expanded".
func TestShowLoadedResourcesContextPathsExpanded(t *testing.T) {
	initTestTheme(t)
	home := homeDir(t)
	cwd := home + "/Development/pi-mono"
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup:       false,
		toolOutputExpanded: true,
		cwd:                cwd,
		contextFiles: []core.AgentsFile{
			{Path: home + "/.pig/agent/AGENTS.md"},
			{Path: cwd + "/AGENTS.md"},
		},
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false})

	output := renderAllChildren(mode.loadedResourcesContainer, 220)
	assertContains(t, output, "[Context]")
	assertContains(t, output, "~/.pig/agent/AGENTS.md")
	assertContains(t, output, "~/Development/pi-mono/AGENTS.md")
	assertNotContains(t, output, "~/.pig/agent/AGENTS.md, AGENTS.md")
}

// pi: "does not show verbose listing on quiet startup during reload".
func TestShowLoadedResourcesQuietReloadNoListing(t *testing.T) {
	initTestTheme(t)
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup: true,
		skills:       []core.Skill{skillFixture("/tmp/skill/SKILL.md", "commit")},
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{
		Extensions:               []extensionFixture{{path: "/tmp/ext/index.ts"}},
		Force:                    false,
		ShowDiagnosticsWhenQuiet: true,
	})

	if len(mode.loadedResourcesContainer.Children) != 0 {
		t.Fatalf("expected no children on quiet reload, got:\n%s",
			renderAllChildren(mode.loadedResourcesContainer, 220))
	}
}

// pi: "still shows diagnostics on quiet startup when requested".
func TestShowLoadedResourcesQuietDiagnostics(t *testing.T) {
	initTestTheme(t)
	mode := createResourcesTestMode(t, resourcesFixtureOptions{
		quietStartup: true,
		skills:       []core.Skill{skillFixture("/tmp/skill/SKILL.md", "commit")},
		skillDiagnostics: []core.ResourceDiagnostic{
			{Type: "warning", Message: "duplicate skill name"},
		},
	})

	mode.showLoadedResources(&showLoadedResourcesOptions{Force: false, ShowDiagnosticsWhenQuiet: true})

	output := renderAllChildren(mode.loadedResourcesContainer, 220)
	assertContains(t, output, "[Skill conflicts]")
	assertNotContains(t, output, "[Skills]")
}

// homeDir returns the user home directory (pi: homedir()).
func homeDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return home
}
