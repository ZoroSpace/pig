// pi: core/agent-session-services.ts — cwd-bound runtime services for one
// effective session cwd. The extension half of applyExtensionFlagValues
// (registered flag validation against loaded extensions) is inert until the
// extension loader (Phase 7) populates extensionsResult.extensions; the
// unknown-flag diagnostics path is kept because it does not need handlers.
package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/utils"
	pigmcp "github.com/ZoroSpace/pig/integrations/mcp"
	"github.com/samber/lo"
)

// AgentSessionRuntimeDiagnostic is one user-facing runtime diagnostic (pi:
// AgentSessionRuntimeDiagnostic — canonical struct lives in
// settings_diagnostics.go per the settings batch's forward declaration).
// The Type constants are declared here, where pi defines the union.
const (
	AgentSessionRuntimeDiagnosticInfo    = "info"
	AgentSessionRuntimeDiagnosticWarning = "warning"
	AgentSessionRuntimeDiagnosticError   = "error"
)

// CreateAgentSessionServicesOptions are the inputs for creating cwd-bound
// runtime services (pi: CreateAgentSessionServicesOptions). pi's
// modelRuntimeSignal is dropped per the AbortSignal → ctx rule: the ctx
// passed to CreateAgentSessionServices carries cancellation.
type CreateAgentSessionServicesOptions struct {
	Cwd      string
	AgentDir string
	// SettingsManager is an explicit override; nil = create (pi:
	// settingsManager?).
	SettingsManager *SettingsManager
	// ModelRuntime is an explicit override; nil = create (pi:
	// modelRuntime?).
	ModelRuntime *ModelRuntime
	// ExtensionFlagValues maps "--name" → value for extension flags (pi:
	// extensionFlagValues). nil = none.
	ExtensionFlagValues map[string]any
	// ResourceLoaderOptions are the loader options minus cwd/agentDir/
	// settingsManager, which this function fixes (pi: resourceLoaderOptions:
	// Omit<DefaultResourceLoaderOptions, ...>).
	ResourceLoaderOptions *DefaultResourceLoaderOptions
	// ResourceLoaderReloadOptions are forwarded to loader.Reload (pi:
	// resourceLoaderReloadOptions).
	ResourceLoaderReloadOptions *ResourceLoaderReloadOptions
}

// CreateAgentSessionFromServicesOptions are the inputs for creating an
// AgentSession from already-created services (pi:
// CreateAgentSessionFromServicesOptions).
type CreateAgentSessionFromServicesOptions struct {
	Services          *AgentSessionServices
	SessionManager    *SessionManager
	SessionStartEvent *SessionStartEvent
	Model             *types.Model
	ThinkingLevel     agent.ThinkingLevel
	ScopedModels      []ScopedModel
	Tools             []string
	ExcludeTools      []string
	NoTools           string
	CustomTools       []*tools.ToolDefinition
}

// AgentSessionServices is the coherent cwd-bound runtime services for one
// effective session cwd (pi: AgentSessionServices). This is infrastructure
// only: the AgentSession itself is created separately so session options can
// be resolved against these services first.
type AgentSessionServices struct {
	Cwd             string
	AgentDir        string
	ModelRuntime    *ModelRuntime
	SettingsManager *SettingsManager
	ResourceLoader  ResourceLoader
	Diagnostics     []AgentSessionRuntimeDiagnostic
	// MCPManager owns the configured MCP servers; nil = none configured
	// (pig-only).
	MCPManager *pigmcp.Manager
}

// applyExtensionFlagValues validates CLI flag values against the flags
// registered by loaded extensions (pi: applyExtensionFlagValues). Without
// the extension loader every flag is unknown, which keeps the unknown-flag
// diagnostics behavior live.
func applyExtensionFlagValues(resourceLoader ResourceLoader, extensionFlagValues map[string]any) []AgentSessionRuntimeDiagnostic {
	if extensionFlagValues == nil {
		return nil
	}

	diagnostics := make([]AgentSessionRuntimeDiagnostic, 0)
	extensionsResult := resourceLoader.GetExtensions()
	registeredFlags := map[string]string{} // name → "boolean" | "string"
	for _, extension := range extensionsResult.Extensions {
		flagNames := make([]string, 0, len(extension.Flags))
		for name := range extension.Flags {
			flagNames = append(flagNames, name)
		}
		sort.Strings(flagNames)
		for _, name := range flagNames {
			registeredFlags[name] = extension.Flags[name].Type
		}
	}

	var unknownFlags []string
	flagNames := make([]string, 0, len(extensionFlagValues))
	for name := range extensionFlagValues {
		flagNames = append(flagNames, name)
	}
	sort.Strings(flagNames)
	for _, name := range flagNames {
		value := extensionFlagValues[name]
		flagType, ok := registeredFlags[name]
		if !ok {
			unknownFlags = append(unknownFlags, name)
			continue
		}
		if flagType == "boolean" {
			extensionsResult.Runtime.FlagValues[name] = true
			continue
		}
		if stringValue, isString := value.(string); isString {
			extensionsResult.Runtime.FlagValues[name] = stringValue
			continue
		}
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
			Type:    AgentSessionRuntimeDiagnosticError,
			Message: fmt.Sprintf("Extension flag \"--%s\" requires a value", name),
		})
	}

	if len(unknownFlags) > 0 {
		quoted := make([]string, 0, len(unknownFlags))
		for _, name := range unknownFlags {
			quoted = append(quoted, "--"+name)
		}
		plural := "s"
		if len(unknownFlags) == 1 {
			plural = ""
		}
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
			Type:    AgentSessionRuntimeDiagnosticError,
			Message: fmt.Sprintf("Unknown option%s: %s", plural, strings.Join(quoted, ", ")),
		})
	}

	return diagnostics
}

// CreateAgentSessionServices creates cwd-bound runtime services (pi:
// createAgentSessionServices). Returns services plus diagnostics; it does
// not create an AgentSession.
func CreateAgentSessionServices(ctx context.Context, options CreateAgentSessionServicesOptions) (*AgentSessionServices, error) {
	cwd := utils.ResolvePath(options.Cwd, "", nil)
	agentDir := options.AgentDir
	if agentDir != "" {
		agentDir = utils.ResolvePath(agentDir, "", nil)
	} else {
		agentDir = codingagent.GetAgentDir()
	}
	modelRuntime := options.ModelRuntime
	if modelRuntime == nil {
		modelRuntime = CreateModelRuntime(ctx, &CreateModelRuntimeOptions{
			AuthPath:   filepath.Join(agentDir, "auth.json"),
			ModelsPath: lo.ToPtr(filepath.Join(agentDir, "models.json")),
		})
	}
	settingsManager := options.SettingsManager
	if settingsManager == nil {
		settingsManager = CreateSettingsManager(cwd, agentDir, nil)
	}
	loaderOptions := DefaultResourceLoaderOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: settingsManager,
	}
	if options.ResourceLoaderOptions != nil {
		loaderOptions = *options.ResourceLoaderOptions
		loaderOptions.Cwd = cwd
		loaderOptions.AgentDir = agentDir
		loaderOptions.SettingsManager = settingsManager
	}
	resourceLoader := NewDefaultResourceLoader(loaderOptions)
	if err := resourceLoader.Reload(options.ResourceLoaderReloadOptions); err != nil {
		return nil, err
	}

	var diagnostics []AgentSessionRuntimeDiagnostic
	extensionsResult := resourceLoader.GetExtensions()
	// pi iterates extensionsResult.runtime.pendingProviderRegistrations and
	// pendingNativeProviderRegistrations here — those queues arrive with the
	// extension batch (Phase 7; pi: runtime.pendingProviderRegistrations).
	// Until then there is nothing pending and ModelRuntime registration
	// cannot be fed from extensions.
	_ = extensionsResult
	modelRuntime.Refresh(ctx, &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false)})
	diagnostics = append(diagnostics, applyExtensionFlagValues(resourceLoader, options.ExtensionFlagValues)...)

	// pig: native MCP — build the session's MCP manager after Reload, when
	// project trust is final (ResolveProjectTrust may have set it).
	mcpManager, mcpDiagnostics := createMCPManager(cwd, agentDir, settingsManager.IsProjectTrusted(),
		// S12-4: per-request token for `auth.provider` servers (pi:
		// modelRegistry?.getApiKeyForProvider).
		func(ctx context.Context, provider string) (string, error) {
			key := NewModelRegistry(modelRuntime).GetApiKeyForProvider(ctx, provider)
			if key == "" {
				return "", fmt.Errorf("no auth found for provider %q", provider)
			}
			return key, nil
		})
	diagnostics = append(diagnostics, mcpDiagnostics...)

	return &AgentSessionServices{
		Cwd:             cwd,
		AgentDir:        agentDir,
		ModelRuntime:    modelRuntime,
		SettingsManager: settingsManager,
		ResourceLoader:  resourceLoader,
		Diagnostics:     diagnostics,
		MCPManager:      mcpManager,
	}, nil
}

// CreateAgentSessionFromServices creates an AgentSession from previously
// created services (pi: createAgentSessionFromServices). This keeps session
// creation separate from service creation so callers can resolve model,
// thinking, tools, and other session inputs against the target cwd before
// constructing the session.
func CreateAgentSessionFromServices(ctx context.Context, options CreateAgentSessionFromServicesOptions) (*CreateAgentSessionResult, error) {
	return CreateAgentSession(ctx, &CreateAgentSessionOptions{
		Cwd:               options.Services.Cwd,
		AgentDir:          options.Services.AgentDir,
		ModelRuntime:      options.Services.ModelRuntime,
		SettingsManager:   options.Services.SettingsManager,
		ResourceLoader:    options.Services.ResourceLoader,
		MCPManager:        options.Services.MCPManager,
		SessionManager:    options.SessionManager,
		Model:             options.Model,
		ThinkingLevel:     options.ThinkingLevel,
		ScopedModels:      options.ScopedModels,
		Tools:             options.Tools,
		ExcludeTools:      options.ExcludeTools,
		NoTools:           options.NoTools,
		CustomTools:       options.CustomTools,
		SessionStartEvent: options.SessionStartEvent,
	})
}
