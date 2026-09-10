// pi: core/sdk.ts — the programmatic entry point: createAgentSession wires
// model runtime, settings, resources, and the agent into one AgentSession.
package core

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/samber/lo"
)

func init() {
	// Preserve the pre-0.81 fallback for extensions that construct Agent
	// instances or invoke low-level agent loops without supplying streamFn.
	// Agent core remains provider-agnostic and does not import pi-ai/compat
	// itself. (pi: setDefaultStreamFn(streamSimple) at module load.)
	agent.SetDefaultStreamFn(ai.StreamSimple)
}

// CreateAgentSessionOptions are the createAgentSession inputs (pi:
// CreateAgentSessionOptions). pi's optional fields collapse per §3.4: nil
// pointers / empty values = undefined.
type CreateAgentSessionOptions struct {
	// Cwd is the working directory for project-local discovery. Default:
	// process.cwd().
	Cwd string
	// AgentDir is the global config directory. Default: ~/.pi/agent.
	AgentDir string
	// ModelRuntime is the canonical model/auth runtime. Defaults to a
	// runtime using agentDir/auth.json and models.json.
	ModelRuntime *ModelRuntime
	// Model is the model to use. Default: from settings, else first
	// available.
	Model *types.Model
	// ThinkingLevel is the thinking level. Default: from settings, else
	// "medium" (clamped to model capabilities). "" = undefined.
	ThinkingLevel agent.ThinkingLevel
	// ScopedModels are the models available for cycling (Ctrl+P in
	// interactive mode).
	ScopedModels []ScopedModel
	// NoTools is the default tool suppression mode when no explicit
	// allowlist is provided (pi: noTools?: "all" | "builtin"). "" =
	// undefined.
	//
	//   - "all": start with no tools enabled
	//   - "builtin": disable the default built-in tools (read, bash, edit,
	//     write) but keep extension/custom tools enabled
	NoTools string
	// Tools is the optional allowlist of tool names. When omitted, pig uses
	// the defaultTools setting for the initial built-in selection when
	// configured; otherwise it enables the default built-in tools (read,
	// bash, edit, write). Extension/custom tools remain enabled unless
	// NoTools changes that default. When provided, only the listed tool
	// names are enabled.
	Tools []string
	// ExcludeTools is the optional denylist of tool names to disable.
	// Applies after Tools when both are provided.
	ExcludeTools []string
	// CustomTools are custom tools to register (in addition to built-in
	// tools).
	CustomTools []*tools.ToolDefinition
	// ResourceLoader supplies resources. When nil, DefaultResourceLoader is
	// used.
	ResourceLoader ResourceLoader
	// SessionManager is the session manager. Default:
	// SessionManager.create(cwd).
	SessionManager *SessionManager
	// SettingsManager is the settings manager. Default:
	// SettingsManager.create(cwd, agentDir).
	SettingsManager *SettingsManager
	// SessionStartEvent is the session start event metadata for extension
	// runtime startup.
	SessionStartEvent *SessionStartEvent
}

// CreateAgentSessionResult is the createAgentSession result (pi:
// CreateAgentSessionResult).
type CreateAgentSessionResult struct {
	// Session is the created session.
	Session *AgentSession
	// ExtensionsResult carries the extensions result (for UI context setup
	// in interactive mode).
	ExtensionsResult *LoadExtensionsResult
	// ModelFallbackMessage is set when the session was restored with a
	// different model than saved. "" = none (pi: string | undefined).
	ModelFallbackMessage string
}

// getDefaultAgentDir resolves the default agent dir (pi:
// getDefaultAgentDir).
func getDefaultAgentDir() string {
	return codingagent.GetAgentDir()
}

// modelRuntimeResolver adapts ModelRuntime to the ModelResolverRuntime
// consumer interface (pi passes the ModelRuntime class directly; pig's
// GetModels carries the optional provider filter as a parameter, so the
// no-filter shape is bridged here).
//
// Consumers outside core use NewModelRuntimeResolver.
type modelRuntimeResolver struct {
	runtime *ModelRuntime
}

// NewModelRuntimeResolver adapts a ModelRuntime to the ModelResolverRuntime
// consumer interface (pi passes the ModelRuntime class directly to
// resolveCliModel/resolveModelScope; see modelRuntimeResolver above).
func NewModelRuntimeResolver(runtime *ModelRuntime) ModelResolverRuntime {
	return &modelRuntimeResolver{runtime: runtime}
}

func (a *modelRuntimeResolver) GetModels() []ai.Model { return a.runtime.GetModels("") }
func (a *modelRuntimeResolver) HasConfiguredAuth(providerID string) bool {
	return a.runtime.HasConfiguredAuth(providerID)
}
func (a *modelRuntimeResolver) GetAvailable(ctx context.Context) ([]ai.Model, error) {
	return a.runtime.GetAvailable(ctx)
}
func (a *modelRuntimeResolver) GetAvailableSnapshot() []ai.Model {
	return a.runtime.GetAvailableSnapshot()
}
func (a *modelRuntimeResolver) GetModel(provider, modelID string) (ai.Model, bool) {
	return a.runtime.GetModel(provider, modelID)
}

// CreateAgentSession creates an AgentSession with the specified options (pi:
// createAgentSession).
func CreateAgentSession(ctx context.Context, options *CreateAgentSessionOptions) (*CreateAgentSessionResult, error) {
	if options == nil {
		options = &CreateAgentSessionOptions{}
	}
	cwd := options.Cwd
	if cwd == "" && options.SessionManager != nil {
		cwd = options.SessionManager.GetCwd()
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	cwd = utils.ResolvePath(cwd, "", nil)
	agentDir := options.AgentDir
	if agentDir != "" {
		agentDir = utils.ResolvePath(agentDir, "", nil)
	} else {
		agentDir = getDefaultAgentDir()
	}
	resourceLoader := options.ResourceLoader

	var authPath, modelsPath string
	if options.AgentDir != "" {
		authPath = filepath.Join(agentDir, "auth.json")
		modelsPath = filepath.Join(agentDir, "models.json")
	}
	modelRuntime := options.ModelRuntime
	if modelRuntime == nil {
		modelRuntime = CreateModelRuntime(ctx, &CreateModelRuntimeOptions{
			AuthPath:   authPath,
			ModelsPath: lo.ToPtr(modelsPath),
		})
	}

	settingsManager := options.SettingsManager
	if settingsManager == nil {
		settingsManager = CreateSettingsManager(cwd, agentDir, nil)
	}
	sessionManager := options.SessionManager
	if sessionManager == nil {
		sessionDir, err := GetDefaultSessionDir(cwd, agentDir)
		if err != nil {
			return nil, err
		}
		created, err := CreateSessionManager(cwd, sessionDir, nil)
		if err != nil {
			return nil, err
		}
		sessionManager = created
	}

	if resourceLoader == nil {
		loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
			Cwd:             cwd,
			AgentDir:        agentDir,
			SettingsManager: settingsManager,
		})
		if err := loader.Reload(nil); err != nil {
			return nil, err
		}
		Time("resourceLoader.reload", TimingMain)
		resourceLoader = loader
	}

	// Check if session has existing data to restore
	existingSession := sessionManager.BuildSessionContext()
	hasExistingSession := len(existingSession.Messages) > 0
	hasThinkingEntry := false
	for _, entry := range sessionManager.GetBranch("") {
		if entry.EntryType() == "thinking_level_change" {
			hasThinkingEntry = true
			break
		}
	}

	model := options.Model
	modelFallbackMessage := ""

	// If session has data, try to restore model from it
	if model == nil && hasExistingSession && existingSession.Model != nil {
		restoredModel, found := modelRuntime.GetModel(existingSession.Model.Provider, existingSession.Model.ModelID)
		if found && modelRuntime.HasConfiguredAuth(string(restoredModel.Provider)) {
			model = &restoredModel
		}
		if model == nil {
			modelFallbackMessage = "Could not restore model " + existingSession.Model.Provider + "/" + existingSession.Model.ModelID
		}
	}

	// If still no model, use findInitialModel (checks settings default, then
	// provider defaults)
	if model == nil {
		defaultProvider, _ := settingsManager.GetDefaultProvider()
		defaultModelID, _ := settingsManager.GetDefaultModel()
		defaultThinkingLevel, _ := settingsManager.GetDefaultThinkingLevel()
		result := FindInitialModel(FindInitialModelOptions{
			ScopedModels:         nil,
			IsContinuing:         hasExistingSession,
			DefaultProvider:      defaultProvider,
			DefaultModelID:       defaultModelID,
			DefaultThinkingLevel: defaultThinkingLevel,
			ModelThinkingLevels:  settingsManager.GetAllModelThinkingLevels(),
			ModelRuntime:         &modelRuntimeResolver{runtime: modelRuntime},
		})
		model = result.Model
		if model == nil {
			modelFallbackMessage = FormatNoModelsAvailableMessage()
		} else if modelFallbackMessage != "" {
			modelFallbackMessage += ". Using " + string(model.Provider) + "/" + model.ID
		}
	}

	thinkingLevel := options.ThinkingLevel

	// If session has data, restore thinking level from it
	if thinkingLevel == "" && hasExistingSession {
		if hasThinkingEntry {
			thinkingLevel = agent.ThinkingLevel(existingSession.ThinkingLevel)
		} else if level, ok := settingsManager.GetDefaultThinkingLevel(); ok {
			thinkingLevel = level
		} else {
			thinkingLevel = DefaultThinkingLevel
		}
	}

	// Fall back to per-model override, then global default
	if thinkingLevel == "" && model != nil {
		if perModel, ok := settingsManager.GetModelThinkingLevel(string(model.Provider), model.ID); ok {
			thinkingLevel = perModel
		}
	}
	if thinkingLevel == "" {
		if level, ok := settingsManager.GetDefaultThinkingLevel(); ok {
			thinkingLevel = level
		} else {
			thinkingLevel = DefaultThinkingLevel
		}
	}

	// Clamp to model capabilities
	if model == nil {
		thinkingLevel = "off"
	} else {
		thinkingLevel = ai.ClampThinkingLevel(model, ai.ModelThinkingLevel(thinkingLevel))
	}

	defaultActiveToolNames := []tools.ToolName{"read", "bash", "edit", "write"}
	configuredDefaultToolNames, hasConfiguredDefaultTools := settingsManager.GetDefaultTools()
	allowedToolNames := options.Tools
	var excludedToolNameSet map[string]bool
	if options.ExcludeTools != nil {
		excludedToolNameSet = make(map[string]bool, len(options.ExcludeTools))
		for _, name := range options.ExcludeTools {
			excludedToolNameSet[name] = true
		}
	}
	initialActiveToolNamesSource := options.Tools
	if initialActiveToolNamesSource == nil {
		if options.NoTools != "" {
			initialActiveToolNamesSource = []string{}
		} else if hasConfiguredDefaultTools {
			initialActiveToolNamesSource = configuredDefaultToolNames
		} else {
			initialActiveToolNamesSource = toolNamesToStrings(defaultActiveToolNames)
		}
	}
	var initialActiveToolNames []string
	for _, name := range initialActiveToolNamesSource {
		if excludedToolNameSet[name] {
			continue
		}
		initialActiveToolNames = append(initialActiveToolNames, name)
	}

	// Create convertToLlm wrapper that filters images if blockImages is
	// enabled (defense-in-depth)
	convertToLlmWithBlockImages := func(messages []agent.AgentMessage) []types.Message {
		converted := harness.ConvertToLlm(messages)
		// Check setting dynamically so mid-session changes take effect
		if !settingsManager.GetBlockImages() {
			return converted
		}
		// Filter out ImageContent from all messages, replacing with text
		// placeholder
		filtered := make([]types.Message, 0, len(converted))
		for _, msg := range converted {
			if user, ok := msg.(*types.UserMessage); ok {
				filtered = append(filtered, filterImagesInUserMessage(user))
				continue
			}
			if toolResult, ok := msg.(*types.ToolResultMessage); ok {
				filtered = append(filtered, filterImagesInToolResult(toolResult))
				continue
			}
			filtered = append(filtered, msg)
		}
		return filtered
	}

	extensionRunnerRef := &ExtensionRunnerRef{}

	agentOptions, err := buildSdkAgentOptions(buildSdkAgentOptionsInput{
		settingsManager:     settingsManager,
		extensionRunnerRef:  extensionRunnerRef,
		modelRuntime:        modelRuntime,
		sessionManager:      sessionManager,
		model:               model,
		thinkingLevel:       thinkingLevel,
		convertToLlmWrapper: convertToLlmWithBlockImages,
	})
	if err != nil {
		return nil, err
	}

	agentInstance, err := agent.NewAgent(*agentOptions)
	if err != nil {
		return nil, err
	}

	// Restore messages if session has existing data
	if hasExistingSession {
		agentInstance.State().Messages = existingSession.Messages
		if !hasThinkingEntry {
			_, _ = sessionManager.AppendThinkingLevelChange(string(thinkingLevel))
		}
	} else {
		// Save initial model and thinking level for new sessions so they can
		// be restored on resume
		if model != nil {
			_, _ = sessionManager.AppendModelChange(string(model.Provider), model.ID)
		}
		_, _ = sessionManager.AppendThinkingLevelChange(string(thinkingLevel))
	}

	session := NewAgentSession(AgentSessionConfig{
		Agent:                  agentInstance,
		SessionManager:         sessionManager,
		SettingsManager:        settingsManager,
		Cwd:                    cwd,
		ScopedModels:           options.ScopedModels,
		ResourceLoader:         resourceLoader,
		CustomTools:            options.CustomTools,
		ModelRuntime:           modelRuntime,
		InitialActiveToolNames: initialActiveToolNames,
		AllowedToolNames:       allowedToolNames,
		ExcludedToolNames:      options.ExcludeTools,
		ExtensionRunnerRef:     extensionRunnerRef,
		SessionStartEvent:      options.SessionStartEvent,
	})
	extensionsResult := resourceLoader.GetExtensions()

	return &CreateAgentSessionResult{
		Session:              session,
		ExtensionsResult:     extensionsResult,
		ModelFallbackMessage: modelFallbackMessage,
	}, nil
}

func toolNamesToStrings(names []tools.ToolName) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, string(name))
	}
	return out
}

func orEmptyHeaders(headers types.ProviderHeaders) types.ProviderHeaders {
	if headers == nil {
		return types.ProviderHeaders{}
	}
	return headers
}

// buildSdkAgentOptionsInput carries the closure captures of pi's inline
// `new Agent({...})` literal.
type buildSdkAgentOptionsInput struct {
	settingsManager     *SettingsManager
	extensionRunnerRef  *ExtensionRunnerRef
	modelRuntime        *ModelRuntime
	sessionManager      *SessionManager
	model               *types.Model
	thinkingLevel       agent.ThinkingLevel
	convertToLlmWrapper func(messages []agent.AgentMessage) []types.Message
}

// buildSdkAgentOptions assembles the agent options (pi: the new Agent({...})
// literal inside createAgentSession).
func buildSdkAgentOptions(input buildSdkAgentOptionsInput) (*agent.AgentOptions, error) {
	settingsManager := input.settingsManager
	extensionRunnerRef := input.extensionRunnerRef

	options := &agent.AgentOptions{
		InitialState: &agent.AgentState{
			SystemPrompt:  "",
			Model:         input.model,
			ThinkingLevel: input.thinkingLevel,
			Tools:         []agent.AgentTool{},
		},
		ConvertToLlm: input.convertToLlmWrapper,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			providerRetrySettings := settingsManager.GetProviderRetrySettings()
			httpIdleTimeoutMs, _ := settingsManager.GetHTTPIdleTimeoutMs()
			// SDKs treat timeout=0 as 0ms (immediate timeout), not "no
			// timeout". Use max int32 to effectively disable the timeout.
			effectiveTimeoutMs := httpIdleTimeoutMs
			if effectiveTimeoutMs == 0 {
				effectiveTimeoutMs = 2147483647
			}
			streamOptions := types.SimpleStreamOptions{}
			if options != nil {
				streamOptions = *options
			}
			// pi: options?.timeoutMs ?? settings ?? effective — the request
			// value wins whenever request options were supplied. pig's int64
			// TimeoutMs cannot distinguish explicit 0 from unset (declared
			// deviation: with non-nil request options the request TimeoutMs
			// is authoritative verbatim, 0 included).
			requestOptionsProvided := options != nil
			if !requestOptionsProvided {
				if providerRetrySettings.TimeoutMs != nil {
					streamOptions.TimeoutMs = *providerRetrySettings.TimeoutMs
				} else {
					streamOptions.TimeoutMs = effectiveTimeoutMs
				}
				if wsTimeout, err := settingsManager.GetWebSocketConnectTimeoutMs(); err == nil {
					streamOptions.WebsocketConnectTimeoutMs = wsTimeout
				}
			}
			if streamOptions.MaxRetries == nil && providerRetrySettings.MaxRetries != nil {
				streamOptions.MaxRetries = lo.ToPtr(int(*providerRetrySettings.MaxRetries))
			}
			if streamOptions.MaxRetryDelayMs == nil {
				delay := providerRetrySettings.MaxRetryDelayMs
				streamOptions.MaxRetryDelayMs = &delay
			}
			headerRunner := extensionRunnerRef.Current
			transformHeaders := func(requestHeaders types.ProviderHeaders) (types.ProviderHeaders, error) {
				headers := MergeProviderAttributionHeaders(model, settingsManager, streamOptions.SessionID, requestHeaders)
				if headerRunner != nil && headerRunner.HasHandlers("before_provider_headers") {
					mutated := headerRunner.EmitBeforeProviderHeaders(ctx, orEmptyHeaders(headers))
					return mutated, nil
				}
				return headers, nil
			}
			return input.modelRuntime.StreamSimple(ctx, model, llmContext, &ai.ModelsSimpleStreamOptions{
				SimpleStreamOptions: streamOptions,
				ModelsRequestTransforms: ai.ModelsRequestTransforms{
					TransformHeaders: transformHeaders,
				},
			})
		},
		OnPayload: func(payload any, _ *types.Model) any {
			runner := extensionRunnerRef.Current
			if runner == nil || !runner.HasHandlers("before_provider_request") {
				return payload
			}
			// pi's onPayload callback carries no signal; the handler dispatch
			// runs without cancellation (no-extension path short-circuits
			// above until Phase 7).
			return runner.EmitBeforeProviderRequest(context.Background(), payload)
		},
		OnResponse: func(response types.ProviderResponse, _ *types.Model) {
			runner := extensionRunnerRef.Current
			if runner == nil || !runner.HasHandlers("after_provider_response") {
				return
			}
			// See OnPayload: no signal rides this callback (pi: onResponse).
			_, _ = runner.Emit(context.Background(), &AfterProviderResponseEvent{
				Status:  response.Status,
				Headers: response.Headers,
			})
		},
		SessionID: input.sessionManager.GetSessionID(),
		TransformContext: func(ctx context.Context, messages []agent.AgentMessage) []agent.AgentMessage {
			runner := extensionRunnerRef.Current
			if runner == nil {
				return messages
			}
			return runner.EmitContext(ctx, messages)
		},
		SteeringMode: agent.QueueMode(settingsManager.GetSteeringMode()),
		FollowUpMode: agent.QueueMode(settingsManager.GetFollowUpMode()),
		Transport:    types.Transport(settingsManager.GetTransport()),
	}
	options.ThinkingBudgets = sdkThinkingBudgets(settingsManager)
	options.MaxRetryDelayMs = sdkProviderRetryMaxDelay(settingsManager)
	return options, nil
}

func sdkProviderRetryMaxDelay(settingsManager *SettingsManager) *int64 {
	delay := settingsManager.GetProviderRetrySettings().MaxRetryDelayMs
	return &delay
}

func sdkThinkingBudgets(settingsManager *SettingsManager) *types.ThinkingBudgets {
	if budgets, ok := settingsManager.GetThinkingBudgets(); ok && budgets != nil {
		return &types.ThinkingBudgets{
			Minimal: floatBudget(budgets.Minimal),
			Low:     floatBudget(budgets.Low),
			Medium:  floatBudget(budgets.Medium),
			High:    floatBudget(budgets.High),
		}
	}
	return nil
}

func floatBudget(v *float64) *int64 {
	if v == nil {
		return nil
	}
	return lo.ToPtr(int64(*v))
}

// filterImagesInUserMessage replaces images with a text placeholder (pi's
// blockImages map branch for user messages).
func filterImagesInUserMessage(message *types.UserMessage) *types.UserMessage {
	hasImages := false
	for _, c := range message.Content.Parts {
		if c.ContentKind() == types.KindImage {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return message
	}
	copied := *message
	copied.Content.Parts = filterImageContent(message.Content.Parts)
	return &copied
}

// filterImagesInToolResult replaces images with a text placeholder (pi's
// blockImages map branch for tool results).
func filterImagesInToolResult(message *types.ToolResultMessage) *types.ToolResultMessage {
	hasImages := false
	for _, c := range message.Content {
		if c.ContentKind() == types.KindImage {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return message
	}
	copied := *message
	copied.Content = filterImageContent(message.Content)
	return &copied
}

// filterImageContent replaces images and dedupes consecutive placeholders
// (pi: the map + filter chain).
func filterImageContent(content []types.Content) []types.Content {
	var newContent []types.Content
	for _, c := range content {
		if c.ContentKind() == types.KindImage {
			newContent = append(newContent, &types.TextContent{
				Kind: types.KindText,
				Text: "Image reading is disabled.",
			})
			continue
		}
		// Dedupe consecutive "Image reading is disabled." texts
		if text, ok := c.(*types.TextContent); ok &&
			text.Text == "Image reading is disabled." &&
			len(newContent) > 0 {
			if prev, ok := newContent[len(newContent)-1].(*types.TextContent); ok &&
				prev.Text == "Image reading is disabled." {
				continue
			}
		}
		newContent = append(newContent, c)
	}
	return newContent
}
