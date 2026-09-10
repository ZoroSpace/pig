// test_session_seam_test.go — test seams for the interactive-mode tests
// (pi: the createSession/createTestResourceLoader helpers in
// test/utilities.ts + the fakeThis `session` stubs in
// test/interactive-mode-status.test.ts).
//
// pig's methods are bound to the concrete InteractiveMode struct, so the
// tests build a real AgentSession with in-memory stores and a fixture
// ResourceLoader instead of stubbing `this` (declared adaptation).
package interactive

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

// fixtureResourceLoader is the configurable ResourceLoader for the
// showLoadedResources / autocomplete tests (pi: the resourceLoader stub in
// createShowLoadedResourcesThis + createTestResourceLoader).
type fixtureResourceLoader struct {
	extensions *core.LoadExtensionsResult
	skills     []core.Skill
	prompts    []core.PromptTemplate
	// skillDiagnostics feed the [Skill conflicts] section.
	skillDiagnostics          []core.ResourceDiagnostic
	contextFiles              []core.AgentsFile
	systemPromptSource        *string
	appendSystemPromptSources []string
}

func (l *fixtureResourceLoader) GetExtensions() *core.LoadExtensionsResult {
	if l.extensions == nil {
		l.extensions = &core.LoadExtensionsResult{
			Extensions: []*core.Extension{},
			Runtime:    core.CreateExtensionRuntime(),
		}
	}
	return l.extensions
}

func (l *fixtureResourceLoader) GetSkills() ([]core.Skill, []core.ResourceDiagnostic) {
	return l.skills, l.skillDiagnostics
}

func (l *fixtureResourceLoader) GetPrompts() ([]core.PromptTemplate, []core.ResourceDiagnostic) {
	return l.prompts, nil
}

func (l *fixtureResourceLoader) GetAgentsFiles() []core.AgentsFile { return l.contextFiles }

func (l *fixtureResourceLoader) GetSystemPrompt() (string, bool) { return "", false }

func (l *fixtureResourceLoader) GetSystemPromptSource() (string, bool) {
	if l.systemPromptSource == nil {
		return "", false
	}
	return *l.systemPromptSource, true
}

func (l *fixtureResourceLoader) GetAppendSystemPromptSources() []string {
	return l.appendSystemPromptSources
}

func (l *fixtureResourceLoader) GetAppendSystemPrompt() []string { return []string{} }

func (l *fixtureResourceLoader) GetThemes() ([]any, []core.ResourceDiagnostic) { return nil, nil }

func (l *fixtureResourceLoader) ExtendResources(core.ResourceExtensionPaths) {}

func (l *fixtureResourceLoader) Reload(*core.ResourceLoaderReloadOptions) error { return nil }

// createInteractiveTestSession assembles an AgentSession with in-memory
// stores and the fixture loader (pi: createSession helper).
func createInteractiveTestSession(t *testing.T, loader core.ResourceLoader, configureSettings func(*core.SettingsManager)) *core.AgentSession {
	t.Helper()

	agentInstance, err := agent.NewAgent(agent.AgentOptions{
		StreamFn: func(context.Context, *types.Model, *types.Context, *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			return types.NewAssistantMessageEventStream()
		},
		InitialState: &agent.AgentState{SystemPrompt: "Test"},
	})
	if err != nil {
		t.Fatal(err)
	}

	sessionManager, err := core.InMemorySession(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	settingsManager := core.InMemorySettingsManager(core.Settings{}, nil)
	if configureSettings != nil {
		configureSettings(settingsManager)
	}

	emptyModelsPath := ""
	runtime := core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials:       core.NewInMemoryAuthStorageWithCredentials(nil),
		ModelsPath:        &emptyModelsPath,
		ModelsStore:       &ai.InMemoryModelsStore{},
		AllowModelNetwork: false,
	})

	session := core.NewAgentSession(core.AgentSessionConfig{
		Agent:           agentInstance,
		SessionManager:  sessionManager,
		SettingsManager: settingsManager,
		Cwd:             sessionManager.GetCwd(),
		ModelRuntime:    runtime,
		ResourceLoader:  loader,
	})
	t.Cleanup(session.Dispose)
	return session
}

// newSeamTestMode builds a partially-constructed InteractiveMode around a
// test session (same-package construction; only the fields the tested
// methods touch are populated).
func newSeamTestMode(session *core.AgentSession, options *InteractiveModeOptions) *InteractiveMode {
	mode := &InteractiveMode{
		runtimeHost:              core.NewAgentSessionRuntime(session, nil, nil, nil, ""),
		loadedResourcesContainer: &tui.Container{},
		chatContainer:            &tui.Container{},
		headerContainer:          &tui.Container{},
		documentContainer:        &tui.Container{},
		pendingMessagesContainer: &tui.Container{},
		statusContainer:          &tui.Container{},
		widgetContainerAbove:     &tui.Container{},
		widgetContainerBelow:     &tui.Container{},
		editorContainer:          &tui.Container{},
		extensionWidgetsAbove:    map[string]extensionWidget{},
		extensionWidgetsBelow:    map[string]extensionWidget{},
	}
	mode.ui = &stubRequestRenderTUI{}
	if options != nil {
		mode.options = *options
	}
	return mode
}

// registerTestProvider registers a dynamic provider with models so
// GetAvailableSnapshot returns them (pi: the fake modelRuntime snapshot in
// the createBaseAutocompleteProvider tests).
func registerTestProvider(t *testing.T, session *core.AgentSession, providerID string, models []core.ProviderConfigInputModel) {
	t.Helper()
	if err := session.GetModelRuntime().RegisterProvider(providerID, &core.ProviderConfigInput{
		Name:    providerID,
		BaseURL: "http://localhost:0",
		APIKey:  "test-key",
		API:     types.ApiOpenAICompletions,
		Models:  models,
	}); err != nil {
		t.Fatal(err)
	}
}
