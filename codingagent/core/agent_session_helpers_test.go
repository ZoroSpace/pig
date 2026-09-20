// Test helpers for the AgentSession tests (pi:
// test/model-runtime-test-utils.ts + the createSession/createTestResourceLoader
// helpers from test/utilities.ts, scoped to what the AgentSession batch needs).
//
// Segment deviations (declared):
//   - pig has no anthropic builtin provider or anthropic-messages adapter, so
//     the tests use a locally constructed mock model (provider "anthropic",
//     api "openai-completions") instead of getModel("anthropic",
//     "claude-sonnet-4-5"). Auth is configured against the same provider id,
//     and the stream function is always mocked, so no wire protocol is hit.
//   - The real-API e2e suites (compaction/tree-navigation) keep pi's
//     ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY skip gate and are explicitly
//     skipped in this batch (the anthropic-messages adapter is deferred).
package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	harness "github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// testModel is the mock model used across the AgentSession tests (pi:
// getModel("anthropic", "claude-sonnet-4-5"); see the header deviation).
func testModel() *types.Model {
	return &types.Model{
		ID:            "claude-sonnet-4-5",
		Name:          "Claude Sonnet 4.5",
		API:           types.ApiOpenAICompletions,
		Provider:      "anthropic",
		BaseURL:       "http://localhost:0",
		Reasoning:     true,
		ContextWindow: 200000,
		MaxTokens:     64000,
		Input:         []ai.ModelInput{ai.ModelInputText},
	}
}

// mockAssistantStream is pi's MockAssistantStream: an
// AssistantMessageEventStream whose producer pushes events synchronously (pi
// queues a microtask; pig pushes inline from the stream function goroutine —
// the agent loop consumes events asynchronously either way).
type mockAssistantStream struct {
	*types.AssistantMessageEventStream
}

func newMockAssistantStream() *mockAssistantStream {
	return &mockAssistantStream{types.NewAssistantMessageEventStream()}
}

// testAssistantMessage builds an assistant message (pi: createAssistantMessage
// in the retry/concurrent suites).
func testAssistantMessage(text string, overrides ...func(*types.AssistantMessage)) types.AssistantMessage {
	message := types.AssistantMessage{
		Role:     types.RoleAssistant,
		Content:  []types.Content{types.NewTextContent(text)},
		API:      "anthropic-messages",
		Provider: "anthropic",
		Model:    "mock",
		Usage: types.Usage{
			Cost: types.Cost{},
		},
		StopReason: types.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
	for _, override := range overrides {
		override(&message)
	}
	return message
}

func withStopReason(reason types.StopReason) func(*types.AssistantMessage) {
	return func(message *types.AssistantMessage) { message.StopReason = reason }
}

func withErrorMessage(message string) func(*types.AssistantMessage) {
	return func(m *types.AssistantMessage) { m.ErrorMessage = message }
}

func withModelRef(model *types.Model) func(*types.AssistantMessage) {
	return func(m *types.AssistantMessage) {
		m.API = model.API
		m.Provider = model.Provider
		m.Model = model.ID
	}
}

func withUsage(usage types.Usage) func(*types.AssistantMessage) {
	return func(m *types.AssistantMessage) { m.Usage = usage }
}

// createTestModelRegistry creates a registry/runtime pair without file-backed
// catalog locks (pi: createModelRegistry from model-runtime-test-utils.ts).
func createTestModelRegistry(t *testing.T, credentials auth.CredentialStore, modelsPath string) (*ModelRegistry, *ModelRuntime) {
	t.Helper()
	emptyPath := ""
	if modelsPath != "" {
		emptyPath = modelsPath
	}
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsPath:        &emptyPath,
		ModelsStore:       &ai.InMemoryModelsStore{},
		AllowModelNetwork: false,
	})
	return NewModelRegistry(runtime), runtime
}

// testResourceLoader is pi's createTestResourceLoader: an empty ResourceLoader.
type testResourceLoader struct {
	extensions *LoadExtensionsResult
	skills     []Skill
	prompts    []PromptTemplate
}

func createTestResourceLoader(options ...*LoadExtensionsResult) ResourceLoader {
	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{},
		Runtime:    CreateExtensionRuntime(),
	}
	if len(options) > 0 && options[0] != nil {
		extensionsResult = options[0]
	}
	return &testResourceLoader{extensions: extensionsResult}
}

func (l *testResourceLoader) GetExtensions() *LoadExtensionsResult {
	if l.extensions == nil {
		l.extensions = &LoadExtensionsResult{
			Extensions: []*Extension{},
			Runtime:    CreateExtensionRuntime(),
		}
	}
	return l.extensions
}

func (l *testResourceLoader) GetSkills() ([]Skill, []ResourceDiagnostic) {
	if l.skills != nil {
		return l.skills, nil
	}
	return []Skill{}, []ResourceDiagnostic{}
}

func (l *testResourceLoader) GetPrompts() ([]PromptTemplate, []ResourceDiagnostic) {
	if l.prompts != nil {
		return l.prompts, nil
	}
	return []PromptTemplate{}, []ResourceDiagnostic{}
}

func (l *testResourceLoader) GetAgentsFiles() []AgentsFile { return []AgentsFile{} }

func (l *testResourceLoader) GetSystemPrompt() (string, bool) { return "", false }

func (l *testResourceLoader) GetSystemPromptSource() (string, bool) { return "", false }

func (l *testResourceLoader) GetAppendSystemPrompt() []string { return []string{} }

func (l *testResourceLoader) GetAppendSystemPromptSources() []string { return []string{} }

func (l *testResourceLoader) GetThemes() ([]any, []ResourceDiagnostic) { return nil, nil }

func (l *testResourceLoader) ExtendResources(ResourceExtensionPaths) {}

func (l *testResourceLoader) Reload(*ResourceLoaderReloadOptions) error { return nil }

// createAgentTestSession assembles an AgentSession with in-memory session and
// settings (pi: the createSession helpers in each agent-session test file).
func createAgentTestSession(t *testing.T, options *createAgentTestSessionOptions) (*AgentSession, *SessionManager, *SettingsManager) {
	t.Helper()
	cfg := options
	if cfg == nil {
		cfg = &createAgentTestSessionOptions{}
	}
	model := testModel()
	if cfg.Model != nil {
		model = cfg.Model
	}

	streamFn := cfg.StreamFn
	if streamFn == nil {
		streamFn = defaultStreamFn
	}
	agentOptions := agent.AgentOptions{
		StreamFn: streamFn,
		// pi's createHarness runs through the SDK assembly, which installs
		// the coding-agent convertToLlm (custom/compaction/branch-summary
		// roles project to user messages); defaultConvertToLlm would drop
		// them (pi: defaultConvertToLlm filters the same three roles, and
		// createAgentSession overrides it — mirror that here).

		ConvertToLlm: harness.ConvertToLlm,
		InitialState: &agent.AgentState{Model: model, SystemPrompt: "Test", ThinkingLevel: cfg.ThinkingLevel},
	}
	if cfg.GetAPIKey {
		agentOptions.GetApiKey = func(types.ProviderID) string { return "test-key" }
	}
	agentInstance, err := agent.NewAgent(agentOptions)
	if err != nil {
		t.Fatal(err)
	}

	sessionManager, err := InMemorySession(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	settingsManager := InMemorySettingsManager(cfg.SettingsOverrides, nil)
	authStorage := NewInMemoryAuthStorage(authStorageData{})
	if _, err := authStorage.Modify(context.Background(), "anthropic", func(credential auth.Credential) (auth.Credential, error) {
		return &auth.ApiKeyCredential{Key: "test-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	registry, _ := createTestModelRegistry(t, authStorage, "")

	cwd := cfg.Cwd
	if cwd == "" {
		cwd = t.TempDir()
	}
	session := NewAgentSession(AgentSessionConfig{
		Agent:             agentInstance,
		SessionManager:    sessionManager,
		SettingsManager:   settingsManager,
		Cwd:               cwd,
		ModelRuntime:      registry.GetModelRuntime(),
		ResourceLoader:    createTestResourceLoader(cfg.ExtensionsResult...),
		BaseToolsOverride: cfg.BaseToolsOverride,
	})
	t.Cleanup(session.Dispose)
	return session, sessionManager, settingsManager
}

// createAgentTestSessionOptions parameterizes createAgentTestSession.
type createAgentTestSessionOptions struct {
	Model             *types.Model
	StreamFn          agent.StreamFn
	ThinkingLevel     agent.ThinkingLevel
	GetAPIKey         bool
	SettingsOverrides Settings
	ExtensionsResult  []*LoadExtensionsResult
	// BaseToolsOverride overrides base tools (pi: baseToolsOverride).
	BaseToolsOverride map[string]agent.AgentTool
	// Cwd overrides the session working directory.
	Cwd string
}

// GetModelRuntime returns the runtime behind the registry (pi:
// getModelRuntime(modelRegistry); pig stores the backreference explicitly in
// tests instead of the WeakMap).
func (r *ModelRegistry) GetModelRuntime() *ModelRuntime { return r.runtime }

// rawJSON builds a json.RawMessage literal (test helper for tool schemas).
func rawJSON(value string) json.RawMessage { return json.RawMessage(value) }

// aiStreamSimpleForE2E returns the compat-level stream function (pi:
// streamSimple from pi-ai/compat).
func aiStreamSimpleForE2E() agent.StreamFn { return defaultStreamFn }

// splitWords splits text on double spaces for the MONKEY phrase check (test
// helper; pi uses toContain on the phrase).
func splitWords(text string) []string {
	return strings.Split(text, "  ")
}
