// pi: src/main.ts + src/cli.ts — the coding agent CLI entry points. cli.ts
// sets process.title/env and configures the undici dispatcher (no pig
// counterpart: os env is set in Main, and net/http needs no dispatcher
// installation); main.ts handles CLI argument parsing and translates them
// into createAgentSession() options, with the SDK doing the heavy lifting. for the coding agent CLI. Handles CLI
// argument parsing and translates them into createAgentSession() options;
// the SDK does the heavy lifting.
//
// Batch scope (print CLI 第 7 批): the print/json runtime path, auth
// commands, --list-models, --help/--version, and the session resolution
// paths reachable without a TUI. Deferred segments, each marked inline:
//   - handlePackageCommand / handleConfigCommand (Phase 7 package manager)
//   - runMigrations (Phase 8 migrations batch)
//   - exportFromFile (--export, Phase 8 export-html batch)
//   - runRpcMode (Phase 5 RPC batch)
//   - InteractiveMode: the T5b shell subset is wired (regular mode,
//     transcript/footer/submit chain); selectors, showLoadedResources,
//     fullscreen mode, extension UI, and the session picker (--resume)
//     remain deferred (T5b-2/T6 batches)
//   - first-time setup / startup TUI (codingagent/cli startup-ui) are
//     delivered but not yet wired into main (first-run flow lands with T6)
//   - builtInExtensions / InlineExtension factories (Phase 7 extensions)
package main

import (
	"context"
	"encoding/json"
	"net/http"
	// blank import: registers pprof handlers on DefaultServeMux (diagnostic)
	"fmt"
	"io"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/cli"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/core/exporthtml"
	"github.com/ZoroSpace/pig/codingagent/modes"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive"
	"github.com/ZoroSpace/pig/codingagent/utils"
	pigutils "github.com/ZoroSpace/pig/utils"
)

// extensionLoadFailureHint mirrors EXTENSION_LOAD_FAILURE_HINT.
const extensionLoadFailureHint = "Hint: Start without extensions using \"pig -ne\"." // pig: was "pi -ne"

// readPipedStdin reads all content from piped stdin; ok=false when stdin is
// a TTY (interactive terminal) or the pipe is empty (pi: readPipedStdin —
// `data.trim() || undefined`).
func readPipedStdin() (string, bool) {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
		return "", false
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(data))
	return trimmed, trimmed != ""
}

// reportDiagnostics prints runtime diagnostics to stderr (pi:
// reportDiagnostics; chalk coloring dropped).
func reportDiagnostics(diagnostics []core.AgentSessionRuntimeDiagnostic) {
	for _, diagnostic := range diagnostics {
		prefix := ""
		if diagnostic.Type == "error" {
			prefix = "Error: "
		} else if diagnostic.Type == "warning" {
			prefix = "Warning: "
		}
		fmt.Fprintf(os.Stderr, "%s%s\n", prefix, diagnostic.Message)
	}
}

// isTruthyEnvFlag reports whether an env var counts as true (pi:
// isTruthyEnvFlag).
func isTruthyEnvFlag(value string) bool {
	if value == "" {
		return false
	}
	return value == "1" || strings.ToLower(value) == "true" || strings.ToLower(value) == "yes"
}

// appMode is the resolved application mode (pi: AppMode from
// core/project-trust).
type appMode = core.AppMode

// resolveAppMode picks the run mode from flags and TTY state (pi:
// resolveAppMode).
func resolveAppMode(parsed *cli.Args, stdinIsTTY bool, stdoutIsTTY bool) appMode {
	if parsed.Mode != nil && *parsed.Mode == cli.ModeRPC {
		return "rpc"
	}
	if parsed.Mode != nil && *parsed.Mode == cli.ModeJSON {
		return "json"
	}
	if (parsed.Print != nil && *parsed.Print) || !stdinIsTTY || !stdoutIsTTY {
		return "print"
	}
	return "interactive"
}

// toPrintOutputMode maps the app mode to the print output mode (pi:
// toPrintOutputMode).
func toPrintOutputMode(appMode appMode) string {
	if appMode == "json" {
		return cli.ModeJSON
	}
	return cli.ModeText
}

// isPlainRuntimeMetadataCommand reports whether the invocation only queries
// runtime metadata (pi: isPlainRuntimeMetadataCommand).
func isPlainRuntimeMetadataCommand(parsed *cli.Args) bool {
	return (parsed.Print == nil || !*parsed.Print) && parsed.Mode == nil &&
		((parsed.Help != nil && *parsed.Help) || parsed.ListModels != nil)
}

// runAuthCommand handles the `auth` subcommands; handled=false means the
// args are not an auth command (pi: runAuthCommand). The returned code is
// the process exit code; ok=false keeps it unset (pi: process.exitCode).
func runAuthCommand(ctx context.Context, args []string) (handled bool, exitCode int, ok bool) {
	if cli.IsAuthCommandHelp(args) {
		cli.PrintAuthCommandHelp()
		return true, 0, false
	}

	command, err := cli.ParseAuthCommand(args)
	if err != nil {
		message := "Failed to parse auth command"
		var authErr *cli.AuthCommandError
		if asAuthCommandError(err, &authErr) {
			message = authErr.Message
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		return true, 1, true
	}
	if command == nil {
		return false, 0, false
	}

	parsed := cli.ParseArgs(command.Args)
	if len(parsed.UnknownFlags) > 0 {
		option := parsed.UnknownFlagKeys[0]
		fmt.Fprintf(os.Stderr, "Error: Unknown option --%s for %q.\n", option, cli.GetAuthCommandName(command.Kind))
		fmt.Fprintf(os.Stderr, "Use %q or %q.\n", codingagent.AppName+" --help", cli.GetAuthCommandUsage(command.Kind))
		return true, 1, true
	}
	if len(parsed.Diagnostics) > 0 {
		messages := make([]string, len(parsed.Diagnostics))
		for i, diagnostic := range parsed.Diagnostics {
			messages[i] = diagnostic.Message
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", strings.Join(messages, "\n"))
		return true, 1, true
	}
	if command.Kind != cli.AuthCommandCheck {
		// pi wraps the print commands in AbortSignal.timeout(15_000); pig
		// mirrors that with a context deadline.
		signalCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		modelRuntime := core.CreateModelRuntime(signalCtx, &core.CreateModelRuntimeOptions{
			AllowModelNetwork: false,
		})
		credential, err := cli.ResolveCredentialForPrint(signalCtx, &parsed, modelRuntime, command.Kind, command.MinExpiryMs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", authCommandErrorMessage(err))
			return true, 1, true
		}
		fmt.Printf("%s\n", credential)
		return true, 0, false
	}

	requestedAuth, err := cli.ValidateAuthCommandArgs(&parsed, command.Kind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", authCommandErrorMessage(err))
		return true, 2, true
	}
	var result cli.AuthCheckResult
	var credential string
	func() {
		defer func() {
			if r := recover(); r != nil {
				result = cli.AuthCheckResult{
					Status:   cli.AuthCheckInvalid,
					Provider: firstNonEmpty(requestedAuth.Provider, requestedAuth.Model),
					Reason:   cli.AuthCheckInvalidState,
				}
			}
		}()
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var credentials auth.CredentialStore
		if command.NoRefresh {
			credentials = core.NewReadOnlyAuthStorage(codingagent.GetAuthPath())
		} else {
			credentials = core.CreateAuthStorage(codingagent.GetAuthPath())
		}
		modelRuntime := cli.CreateAuthCheckModelRuntime(credentials)
		result, err = cli.CheckProviderAuth(checkCtx, &parsed, modelRuntime, &cli.CheckProviderAuthOptions{Refresh: !command.NoRefresh})
		if err != nil {
			result = cli.AuthCheckResult{
				Status:   cli.AuthCheckInvalid,
				Provider: firstNonEmpty(requestedAuth.Provider, requestedAuth.Model),
				Reason:   cli.AuthCheckInvalidState,
			}
			return
		}
		if command.Credential && result.Status == cli.AuthCheckReady {
			value, ok, credErr := cli.GetProviderCredential(checkCtx, result.Provider, modelRuntime, credentials,
				&cli.GetProviderCredentialOptions{Refresh: !command.NoRefresh})
			if credErr != nil || !ok {
				result = cli.AuthCheckResult{Status: cli.AuthCheckNotReady, Provider: result.Provider, Reason: cli.AuthCheckCredentialNotAvailable}
				return
			}
			credential = value
		}
	}()
	output := result.Status
	if command.JSON {
		payload := map[string]any{"status": result.Status, "provider": result.Provider}
		if result.Reason != "" {
			payload["reason"] = result.Reason
		}
		if result.AuthType != "" {
			payload["authType"] = result.AuthType
		}
		if credential != "" {
			payload["credentials"] = credential
		}
		output = marshalJSONCompact(payload)
	} else if credential != "" {
		output = credential
	}
	fmt.Printf("%s\n", output)
	switch result.Status {
	case cli.AuthCheckReady:
		return true, 0, false
	case cli.AuthCheckNotReady:
		return true, 1, true
	default:
		return true, 2, true
	}
}

// resolvedSession is the outcome of session-argument resolution (pi:
// ResolvedSession union).
type resolvedSession struct {
	typ  string // "path" | "local" | "global" | "not_found"
	path string
	cwd  string
	arg  string
}

// findLocalSessionByExactId finds an exact session id in the current
// project (pi: findLocalSessionByExactId). v0.86.0 routes through
// SessionManager.findById: exact IDs only require reading session headers
// (#9440), no full metadata listing.
func findLocalSessionByExactId(sessionID string, cwd string, sessionDir string) *resolvedSession {
	if path := core.FindSessionById(cwd, sessionID, sessionDir); path != "" {
		return &resolvedSession{typ: "local", path: path}
	}
	return nil
}

// resolveSessionPath resolves a session argument to a file path (pi:
// resolveSessionPath).
func resolveSessionPath(sessionArg string, cwd string, sessionDir string) resolvedSession {
	// If it looks like a file path, resolve it before handing it to the
	// session manager.
	if strings.Contains(sessionArg, "/") || strings.Contains(sessionArg, "\\") || strings.HasSuffix(sessionArg, ".jsonl") {
		return resolvedSession{typ: "path", path: utils.ResolvePath(sessionArg, cwd, nil)}
	}

	// Exact IDs only require reading session headers. Fall back to the full
	// metadata listing for prefix matches (pi v0.86.0, #9440).
	if exactLocalMatch := findLocalSessionByExactId(sessionArg, cwd, sessionDir); exactLocalMatch != nil {
		return *exactLocalMatch
	}

	localSessions := core.ListSessions(context.Background(), cwd, sessionDir, nil)
	var localMatch *core.SessionInfo
	for i := range localSessions {
		if strings.HasPrefix(localSessions[i].ID, sessionArg) {
			localMatch = &localSessions[i]
			break
		}
	}
	if localMatch != nil {
		return resolvedSession{typ: "local", path: localMatch.Path}
	}

	// Try global search across all projects
	allSessions := core.ListAllSessions(context.Background(), sessionDir, nil)
	var globalMatch *core.SessionInfo
	for i := range allSessions {
		if allSessions[i].ID == sessionArg {
			globalMatch = &allSessions[i]
			break
		}
	}
	if globalMatch == nil {
		for i := range allSessions {
			if strings.HasPrefix(allSessions[i].ID, sessionArg) {
				globalMatch = &allSessions[i]
				break
			}
		}
	}
	if globalMatch != nil {
		return resolvedSession{typ: "global", path: globalMatch.Path, cwd: globalMatch.Cwd}
	}

	return resolvedSession{typ: "not_found", arg: sessionArg}
}

// promptConfirm prompts the user for yes/no confirmation (pi:
// promptConfirm).
func promptConfirm(message string) bool {
	fmt.Printf("%s [y/N] ", message)
	var answer string
	_, _ = fmt.Fscanln(os.Stdin, &answer)
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// validateForkFlags rejects --fork combined with conflicting flags (pi:
// validateForkFlags).
func validateForkFlags(parsed *cli.Args) {
	if parsed.Fork == nil {
		return
	}
	var conflicting []string
	if parsed.Session != nil {
		conflicting = append(conflicting, "--session")
	}
	if parsed.Continue != nil && *parsed.Continue {
		conflicting = append(conflicting, "--continue")
	}
	if parsed.Resume != nil && *parsed.Resume {
		conflicting = append(conflicting, "--resume")
	}
	if parsed.NoSession != nil && *parsed.NoSession {
		conflicting = append(conflicting, "--no-session")
	}
	if len(conflicting) > 0 {
		fmt.Fprintf(os.Stderr, "Error: --fork cannot be combined with %s\n", strings.Join(conflicting, ", "))
		os.Exit(1)
	}
}

// validateSessionIdFlags rejects --session-id combined with conflicting
// flags and validates the id (pi: validateSessionIdFlags).
func validateSessionIdFlags(parsed *cli.Args) {
	if parsed.SessionID == nil {
		return
	}
	var conflicting []string
	if parsed.Session != nil {
		conflicting = append(conflicting, "--session")
	}
	if parsed.Continue != nil && *parsed.Continue {
		conflicting = append(conflicting, "--continue")
	}
	if parsed.Resume != nil && *parsed.Resume {
		conflicting = append(conflicting, "--resume")
	}
	if len(conflicting) > 0 {
		fmt.Fprintf(os.Stderr, "Error: --session-id cannot be combined with %s\n", strings.Join(conflicting, ", "))
		os.Exit(1)
	}
	if err := core.AssertValidSessionID(*parsed.SessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
}

// openSessionOrExit opens a session file, exiting on failure (pi:
// openSessionOrExit).
func openSessionOrExit(path string, sessionDir string) *core.SessionManager {
	sessionManager, err := core.OpenSession(path, sessionDir, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
	return sessionManager
}

// forkSessionOrExit forks a session file, exiting on failure (pi:
// forkSessionOrExit).
func forkSessionOrExit(sourcePath string, cwd string, sessionDir string, sessionID *string) *core.SessionManager {
	sessionManager, err := core.ForkSession(sourcePath, cwd, sessionDir, &core.NewSessionOptions{ID: sessionID})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
	return sessionManager
}

// createSessionManager resolves the target session from CLI flags (pi:
// createSessionManager).
func createSessionManager(ctx context.Context, parsed *cli.Args, cwd string, sessionDir string, settingsManager *core.SettingsManager) *core.SessionManager {
	if (parsed.NoSession != nil && *parsed.NoSession) || (parsed.Help != nil && *parsed.Help) || parsed.ListModels != nil {
		var options *core.NewSessionOptions
		if parsed.SessionID != nil {
			options = &core.NewSessionOptions{ID: parsed.SessionID}
		}
		sessionManager, err := core.InMemorySession(cwd, options)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
			os.Exit(1)
		}
		return sessionManager
	}

	if parsed.Fork != nil {
		if parsed.SessionID != nil {
			existingTarget := findLocalSessionByExactId(*parsed.SessionID, cwd, sessionDir)
			if existingTarget != nil {
				fmt.Fprintf(os.Stderr, "Error: Session already exists with id '%s'\n", *parsed.SessionID)
				os.Exit(1)
			}
		}

		resolved := resolveSessionPath(*parsed.Fork, cwd, sessionDir)
		switch resolved.typ {
		case "path", "local", "global":
			return forkSessionOrExit(resolved.path, cwd, sessionDir, parsed.SessionID)
		default:
			fmt.Fprintf(os.Stderr, "Error: No session found matching '%s'\n", resolved.arg)
			os.Exit(1)
		}
	}

	if parsed.Session != nil {
		resolved := resolveSessionPath(*parsed.Session, cwd, sessionDir)
		switch resolved.typ {
		case "path", "local":
			return openSessionOrExit(resolved.path, sessionDir)
		case "global":
			fmt.Printf("Session found in different project: %s\n", resolved.cwd)
			shouldFork := promptConfirm("Fork this session into current directory?")
			if !shouldFork {
				fmt.Println("Aborted.")
				os.Exit(0)
			}
			return forkSessionOrExit(resolved.path, cwd, sessionDir, nil)
		default:
			fmt.Fprintf(os.Stderr, "Error: No session found matching '%s'\n", resolved.arg)
			os.Exit(1)
		}
	}

	if parsed.Resume != nil {
		// pi delegates --resume to the TUI session picker (cli/session-picker.ts
		// + startup-ui), which runs before interactive mode mounts.
		selectedPath, ok := cli.SelectSession(
			func(ctx context.Context, onProgress core.SessionListProgress) []core.SessionInfo {
				return core.ListSessions(ctx, cwd, sessionDir, onProgress)
			},
			func(ctx context.Context, onProgress core.SessionListProgress) []core.SessionInfo {
				return core.ListAllSessions(ctx, sessionDir, onProgress)
			},
			settingsManager,
		)
		if !ok {
			fmt.Println("No session selected")
			os.Exit(0)
		}
		return openSessionOrExit(selectedPath, sessionDir)
	}

	if parsed.Continue != nil && *parsed.Continue {
		sessionManager, err := core.ContinueRecentSession(cwd, sessionDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
			os.Exit(1)
		}
		return sessionManager
	}

	if parsed.SessionID != nil {
		existingSession := findLocalSessionByExactId(*parsed.SessionID, cwd, sessionDir)
		if existingSession != nil {
			return openSessionOrExit(existingSession.path, sessionDir)
		}
		fmt.Fprintf(os.Stderr, "Warning: No project session found with id '%s'; creating a new session with that id.\n", *parsed.SessionID)
	}

	sessionManager, err := core.CreateSessionManager(cwd, sessionDir, &core.NewSessionOptions{ID: parsed.SessionID})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
	return sessionManager
}

// buildSessionOptionsResult carries buildSessionOptions' outcome (pi: the
// inline return object).
type buildSessionOptionsResult struct {
	options              core.CreateAgentSessionOptions
	cliThinkingFromModel bool
	diagnostics          []core.AgentSessionRuntimeDiagnostic
}

// buildSessionOptions translates parsed CLI flags into session options (pi:
// buildSessionOptions).
func buildSessionOptions(parsed *cli.Args, scopedModels []core.ScopedModel, hasExistingSession bool, modelRuntime *core.ModelRuntime, settingsManager *core.SettingsManager) buildSessionOptionsResult {
	result := buildSessionOptionsResult{}

	// Model from CLI
	// - supports --provider <name> --model <pattern>
	// - supports --model <provider>/<pattern>
	if parsed.Model != nil {
		resolved := core.ResolveCliModel(core.ResolveCliModelOptions{
			CliProvider:  lo.FromPtr(parsed.Provider),
			CliModel:     *parsed.Model,
			CliThinking:  lo.FromPtr(parsed.Thinking),
			ModelRuntime: core.NewModelRuntimeResolver(modelRuntime),
		})
		if resolved.Warning != "" {
			result.diagnostics = append(result.diagnostics, core.AgentSessionRuntimeDiagnostic{Type: "warning", Message: resolved.Warning})
		}
		if resolved.Error != "" {
			result.diagnostics = append(result.diagnostics, core.AgentSessionRuntimeDiagnostic{Type: "error", Message: resolved.Error})
		}
		if resolved.Model != nil {
			result.options.Model = resolved.Model
			// Allow "--model <pattern>:<thinking>" as a shorthand.
			// Explicit --thinking still takes precedence (applied later).
			if parsed.Thinking == nil && resolved.ThinkingLevel != "" {
				result.options.ThinkingLevel = resolved.ThinkingLevel
				result.cliThinkingFromModel = true
			}
		}
	}

	if result.options.Model == nil && len(scopedModels) > 0 && !hasExistingSession {
		// Check if saved default is in scoped models - use it if so, otherwise first scoped model
		savedProvider, hasProvider := settingsManager.GetDefaultProvider()
		savedModelID, hasModel := settingsManager.GetDefaultModel()
		var savedModel *ai.Model
		if hasProvider && hasModel {
			if model, ok := modelRuntime.GetModel(savedProvider, savedModelID); ok {
				savedModel = &model
			}
		}
		var savedInScope *core.ScopedModel
		if savedModel != nil {
			for i := range scopedModels {
				if ai.ModelsAreEqual(&scopedModels[i].Model, savedModel) {
					savedInScope = &scopedModels[i]
					break
				}
			}
		}

		if savedInScope != nil {
			result.options.Model = &savedInScope.Model
			// Use thinking level from scoped model config if explicitly set
			if parsed.Thinking == nil && savedInScope.ThinkingLevel != "" {
				result.options.ThinkingLevel = savedInScope.ThinkingLevel
			}
		} else {
			result.options.Model = &scopedModels[0].Model
			// Use thinking level from first scoped model if explicitly set
			if parsed.Thinking == nil && scopedModels[0].ThinkingLevel != "" {
				result.options.ThinkingLevel = scopedModels[0].ThinkingLevel
			}
		}
	}

	// Thinking level from CLI (takes precedence over scoped model thinking levels set above)
	if parsed.Thinking != nil {
		result.options.ThinkingLevel = *parsed.Thinking
	}

	// Scoped models for Ctrl+P cycling
	// Keep thinking level undefined when not explicitly set in the model pattern.
	// Undefined means "inherit current session thinking level" during cycling.
	if len(scopedModels) > 0 {
		result.options.ScopedModels = make([]core.ScopedModel, len(scopedModels))
		for i, sm := range scopedModels {
			result.options.ScopedModels[i] = core.ScopedModel{Model: sm.Model, ThinkingLevel: sm.ThinkingLevel}
		}
	}

	// API key from CLI - set as a non-persistent runtime override
	// (handled by caller before createAgentSession)

	// Tools
	if parsed.NoTools != nil && *parsed.NoTools {
		result.options.NoTools = "all"
	} else if parsed.NoBuiltinTools != nil && *parsed.NoBuiltinTools {
		result.options.NoTools = "builtin"
	}
	if parsed.Tools != nil {
		result.options.Tools = append([]string(nil), parsed.Tools...)
	}
	if parsed.ExcludeTools != nil {
		result.options.ExcludeTools = append([]string(nil), parsed.ExcludeTools...)
	}

	return result
}

// resolveCliPaths resolves local CLI resource paths against cwd (pi:
// resolveCliPaths).
func resolveCliPaths(cwd string, paths []string) []string {
	if paths == nil {
		return nil
	}
	resolved := make([]string, len(paths))
	for i, value := range paths {
		if utils.IsLocalPath(value) {
			resolved[i] = utils.ResolvePath(value, cwd, nil)
		} else {
			resolved[i] = value
		}
	}
	return resolved
}

// mainRuntimeResult carries the assembled runtime plus the values main
// consumes afterwards (pi: the destructured runtime return).
type mainRuntimeResult struct {
	runtime              *core.AgentSessionRuntime
	settingsManager      *core.SettingsManager
	modelRuntime         *core.ModelRuntime
	resourceLoader       core.ResourceLoader
	modelFallbackMessage string
}

// createRuntimeFactory is main's CreateAgentSessionRuntimeFactory (pi: the
// createRuntime closure). projectTrustByCwd caches trust decisions across
// factory invocations.
func createRuntimeFactory(
	ctx context.Context,
	parsed *cli.Args,
	cwd string,
	agentDir string,
	trustStore *core.ProjectTrustStore,
	startupSettingsManager *core.SettingsManager,
	startupDefaultProjectTrust core.DefaultProjectTrust,
	trustPromptMode appMode,
	appModeValue appMode,
	projectTrustByCwd map[string]bool,
	resolvedExtensionPaths []string,
	resolvedSkillPaths []string,
	resolvedPromptTemplatePaths []string,
	resolvedThemePaths []string,
) core.CreateAgentSessionRuntimeFactory {
	return func(options core.CreateAgentSessionRuntimeFactoryOptions) (*core.CreateAgentSessionRuntimeResult, error) {
		isInitialRuntime := options.SessionStartEvent == nil
		var projectTrustDiagnostics []core.AgentSessionRuntimeDiagnostic

		cachedProjectTrust, hasCachedTrust := projectTrustByCwd[options.Cwd]
		hasTrustRequiringResources := core.HasTrustRequiringProjectResources(options.Cwd)
		shouldResolveProjectTrust := parsed.ProjectTrustOverride == nil && !hasCachedTrust && hasTrustRequiringResources
		projectTrusted := !hasTrustRequiringResources || func() bool {
			decision, err := trustStore.Get(options.Cwd)
			if err != nil {
				return false
			}
			return decision != nil && *decision
		}()
		if hasCachedTrust {
			projectTrusted = cachedProjectTrust
		} else if parsed.ProjectTrustOverride != nil {
			projectTrusted = *parsed.ProjectTrustOverride
		}

		runtimeSettingsManager := core.CreateSettingsManager(options.Cwd, agentDir, &core.SettingsManagerCreateOptions{ProjectTrusted: lo.ToPtr(projectTrusted)})
		servicesOptions := core.CreateAgentSessionServicesOptions{
			Cwd:                 options.Cwd,
			AgentDir:            agentDir,
			SettingsManager:     runtimeSettingsManager,
			ExtensionFlagValues: parsed.UnknownFlags,
		}
		if shouldResolveProjectTrust {
			servicesOptions.ResourceLoaderReloadOptions = &core.ResourceLoaderReloadOptions{
				ResolveProjectTrust: func(extensionsResult *core.LoadExtensionsResult) (bool, error) {
					// pi's projectTrustContext ?? createProjectTrustContext(...)
					// carries a UI face for interactive prompts; the TUI batch
					// (Phase 6) lands that type, so only the hasUI half is
					// resolved here (print mode behaves identically).
					trusted, err := core.ResolveProjectTrusted(core.ResolveProjectTrustedOptions{
						Cwd:                 options.Cwd,
						TrustStore:          trustStore,
						TrustOverride:       parsed.ProjectTrustOverride,
						DefaultProjectTrust: startupDefaultProjectTrust,
						ExtensionsResult:    extensionsResult,
						ProjectTrustHasUI:   isInitialRuntime && trustPromptMode == "interactive",
						OnExtensionError: func(message string) {
							projectTrustDiagnostics = append(projectTrustDiagnostics, core.AgentSessionRuntimeDiagnostic{Type: "warning", Message: message})
						},
					})
					if err != nil {
						return false, err
					}
					projectTrustByCwd[options.Cwd] = trusted
					return trusted, nil
				},
			}
		}
		servicesOptions.ResourceLoaderOptions = &core.DefaultResourceLoaderOptions{
			AdditionalExtensionPaths:      resolvedExtensionPaths,
			AdditionalSkillPaths:          resolvedSkillPaths,
			AdditionalPromptTemplatePaths: resolvedPromptTemplatePaths,
			AdditionalThemePaths:          resolvedThemePaths,
			NoExtensions:                  lo.FromPtr(parsed.NoExtensions),
			NoSkills:                      lo.FromPtr(parsed.NoSkills),
			NoPromptTemplates:             lo.FromPtr(parsed.NoPromptTemplates),
			NoThemes:                      lo.FromPtr(parsed.NoThemes),
			NoContextFiles:                lo.FromPtr(parsed.NoContextFiles),
			SystemPrompt:                  lo.FromPtr(parsed.SystemPrompt),
			AppendSystemPrompt:            append([]string(nil), parsed.AppendSystemPrompt...),
		}
		services, err := core.CreateAgentSessionServices(ctx, servicesOptions)
		if err != nil {
			return nil, err
		}

		var diagnostics []core.AgentSessionRuntimeDiagnostic
		diagnostics = append(diagnostics, projectTrustDiagnostics...)
		diagnostics = append(diagnostics, services.Diagnostics...)
		diagnostics = append(diagnostics, core.CollectSettingsDiagnostics(services.SettingsManager)...)
		extensionsResult := services.ResourceLoader.GetExtensions()
		for _, loadError := range extensionsResult.Errors {
			diagnostics = append(diagnostics, core.AgentSessionRuntimeDiagnostic{
				Type:    "error",
				Message: fmt.Sprintf("Failed to load extension %q: %s", loadError.Path, loadError.Error),
			})
		}

		modelPatterns := parsed.Models
		if modelPatterns == nil {
			if enabled, ok := services.SettingsManager.GetEnabledModels(); ok {
				modelPatterns = enabled
			}
		}
		var scopedModels []core.ScopedModel
		if len(modelPatterns) > 0 {
			scopeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			scopedModels, err = core.ResolveModelScope(scopeCtx, modelPatterns, core.NewModelRuntimeResolver(services.ModelRuntime))
			cancel()
			if err != nil {
				return nil, err
			}
		}
		// pi reads sessionManager.buildSessionContext().messages.length here.
		built := buildSessionOptions(parsed, scopedModels, len(options.SessionManager.BuildSessionContext().Messages) > 0, services.ModelRuntime, services.SettingsManager)
		diagnostics = append(diagnostics, built.diagnostics...)

		if parsed.APIKey != nil {
			if built.options.Model == nil {
				diagnostics = append(diagnostics, core.AgentSessionRuntimeDiagnostic{
					Type:    "error",
					Message: "--api-key requires a model to be specified via --model, --provider/--model, or --models",
				})
			} else if err := services.ModelRuntime.SetRuntimeApiKey(ctx, string(built.options.Model.Provider), *parsed.APIKey); err != nil {
				return nil, err
			}
		}

		created, err := core.CreateAgentSessionFromServices(ctx, core.CreateAgentSessionFromServicesOptions{
			Services:          services,
			SessionManager:    options.SessionManager,
			SessionStartEvent: options.SessionStartEvent,
			Model:             built.options.Model,
			ThinkingLevel:     built.options.ThinkingLevel,
			ScopedModels:      built.options.ScopedModels,
			Tools:             built.options.Tools,
			ExcludeTools:      built.options.ExcludeTools,
			NoTools:           built.options.NoTools,
			CustomTools:       built.options.CustomTools,
		})
		if err != nil {
			return nil, err
		}
		cliThinkingOverride := parsed.Thinking != nil || built.cliThinkingFromModel
		if created.Session.Model() != nil && cliThinkingOverride {
			created.Session.SetThinkingLevel(created.Session.ThinkingLevel(), nil)
		}

		return &core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *created,
			Services:                 services,
			Diagnostics:              diagnostics,
		}, nil
	}
}

// Main is the coding agent CLI entry point (pi: main). It returns the
// process exit code.
// startPprofEndpointIfRequested starts a net/http/pprof endpoint when
// PIG_PPROF is set (e.g. PIG_PPROF=127.0.0.1:18924). Diagnostic aid for
// live-process heap/goroutine inspection; no-op unless the env is set.
func startPprofEndpointIfRequested() {
	addr := os.Getenv("PIG_PPROF")
	if addr == "" {
		return
	}
	go func() {
		// never crash the TUI over a diagnostic endpoint
		defer func() { _ = recover() }()
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "pig: pprof endpoint failed: %v\n", err)
		}
	}()
}

func Main(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startPprofEndpointIfRequested()
	core.ResetTimings(core.TimingMain)
	// pi spreads builtInExtensions + options.extensionFactories here; the
	// built-in extensions and inline factories arrive with the extensions
	// batch (Phase 7), and pig has no JS extension factories.
	offlineMode := containsString(args, "--offline") || isTruthyEnvFlag(os.Getenv("PI_OFFLINE"))
	if offlineMode {
		_ = os.Setenv("PI_OFFLINE", "1")
		_ = os.Setenv("PI_SKIP_VERSION_CHECK", "1")
	}

	if handled, exitCode, ok := runAuthCommand(ctx, args); handled {
		if ok {
			return exitCode
		}
		return 0
	}

	// pi runs cleanupWindowsSelfUpdateQuarantine + cleanupManagedInstall
	// here (npm/pnpm/yarn/bun self-update machinery); pig ships as a static
	// binary and both are deferred with the release batch (Phase 8).
	// handlePackageCommand / handleConfigCommand likewise arrive with the
	// package-manager batch (Phase 7); until then these spellings fall
	// through to ordinary message parsing exactly as pi does for unknown
	// words.

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		return 1
	}
	agentDir := codingagent.GetAgentDir()
	bootstrapSettingsManager := core.CreateSettingsManager(cwd, agentDir, &core.SettingsManagerCreateOptions{ProjectTrusted: lo.ToPtr(false)})
	if proxy, ok := bootstrapSettingsManager.GetGlobalSettings()["httpProxy"].(string); ok {
		core.ApplyHTTPProxySettings(proxy)
	}
	// pi re-runs configureHttpDispatcher() here; pig's net/http needs no
	// dispatcher installation (core/http_dispatcher.go header declaration).

	parsed := cli.ParseArgs(args)
	if len(parsed.Diagnostics) > 0 {
		for _, d := range parsed.Diagnostics {
			label := "Warning"
			if d.Type == cli.ArgDiagnosticError {
				label = "Error"
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", label, d.Message)
		}
		for _, d := range parsed.Diagnostics {
			if d.Type == cli.ArgDiagnosticError {
				return 1
			}
		}
	}
	core.Time("parseArgs", core.TimingMain)

	if parsed.Version != nil && *parsed.Version {
		fmt.Println(codingagent.Version)
		return 0
	}

	if parsed.Export != nil {
		// pi: exportFromFile(parsed.export, outputPath) where outputPath is
		// the first positional message (main.ts --export branch).
		outputPath := ""
		if len(parsed.Messages) > 0 {
			outputPath = parsed.Messages[0]
		}
		result, err := core.ExportFromFile(*parsed.Export, exporthtml.ExportOptions{OutputPath: outputPath})
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: "+err.Error())
			return 1
		}
		fmt.Println("Exported to: " + result)
		return 0
	}

	stdinIsTTY := isCharDevice(os.Stdin)
	stdoutIsTTY := isCharDevice(os.Stdout)
	appMode := resolveAppMode(&parsed, stdinIsTTY, stdoutIsTTY)
	shouldTakeOverStdout := appMode != "interactive" && !isPlainRuntimeMetadataCommand(&parsed)
	if shouldTakeOverStdout {
		core.TakeOverStdout()
	}

	if parsed.Mode != nil && *parsed.Mode == cli.ModeRPC && len(parsed.FileArgs) > 0 {
		fmt.Fprintln(os.Stderr, "Error: @file arguments are not supported in RPC mode")
		return 1
	}

	validateForkFlags(&parsed)
	validateSessionIdFlags(&parsed)

	// pi runs runMigrations(cwd) here (auth/models.json migrations +
	// deprecation warnings collected for interactive mode).
	migrationsResult := core.RunMigrations(cwd)
	core.Time("runMigrations", core.TimingMain)

	startupSettingsManager := core.CreateSettingsManager(cwd, agentDir, nil)
	startupSettingsDiagnostics := core.CollectSettingsDiagnostics(startupSettingsManager)

	// pi shows the experimental first-time setup TUI here (interactive only);
	// it arrives with the TUI batch (Phase 6).
	// pi applies parsed.useTheme via applyOverrides for interactive mode;
	// theme loading is the theme batch, so the override is deferred with it.

	// Decide the final runtime cwd before creating cwd-bound runtime services.
	// --session and --resume may select a session from another project, so project-local
	// settings, resources, provider registrations, and models must be resolved only after
	// the target session cwd is known. The startup-cwd settings manager is used only for
	// sessionDir lookup during session selection.
	envSessionDir := os.Getenv(codingagent.EnvSessionDir)
	var sessionDir string
	if parsed.SessionDir != nil {
		sessionDir = utils.NormalizePath(*parsed.SessionDir, nil)
	} else if envSessionDir != "" {
		sessionDir = codingagent.ExpandTildePath(envSessionDir)
	} else if dir, ok := startupSettingsManager.GetSessionDir(); ok {
		sessionDir = dir
	}
	sessionManager := createSessionManager(ctx, &parsed, cwd, sessionDir, startupSettingsManager)
	if issue := core.GetMissingSessionCwdIssue(sessionManager, cwd); issue != nil {
		if appMode == "interactive" {
			// pi prompts via the startup TUI selector (promptForMissingSessionCwd);
			// the TUI arrives with Phase 6, so interactive is rejected below
			// anyway — the print branch's error is the reachable path.
			fmt.Fprintf(os.Stderr, "Error: %s\n", core.NewMissingSessionCwdError(*issue).Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", core.NewMissingSessionCwdError(*issue).Error())
		return 1
	}
	if parsed.Name != nil {
		name, ok := cli.NormalizeSessionName(*parsed.Name)
		if !ok {
			fmt.Fprintln(os.Stderr, "Error: --name requires a non-empty value")
			return 1
		}
		if _, err := sessionManager.AppendSessionInfo(name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
			return 1
		}
	}
	core.Time("createSessionManager", core.TimingMain)

	trustStore := core.NewProjectTrustStore(agentDir)
	// pi computes autoTrustOnReloadCwd from sessionCwd here (interactive-mode
	// trust flow on extension reloads); the extensions runtime arrives with
	// Phase 7, and CreateAgentSessionRuntime re-derives the cwd from the
	// session manager below.
	_ = sessionManager.GetCwd()
	trustPromptMode := appMode
	if parsed.Help != nil && *parsed.Help || parsed.ListModels != nil {
		trustPromptMode = "print"
	}
	projectTrustByCwd := map[string]bool{}

	resolvedExtensionPaths := resolveCliPaths(cwd, parsed.Extensions)
	resolvedSkillPaths := resolveCliPaths(cwd, parsed.Skills)
	resolvedPromptTemplatePaths := resolveCliPaths(cwd, parsed.PromptTemplates)
	resolvedThemePaths := resolveCliPaths(cwd, parsed.Themes)
	startupDefaultProjectTrust := startupSettingsManager.GetDefaultProjectTrust()
	factory := createRuntimeFactory(ctx, &parsed, cwd, agentDir, trustStore, startupSettingsManager,
		startupDefaultProjectTrust, trustPromptMode, appMode, projectTrustByCwd,
		resolvedExtensionPaths, resolvedSkillPaths, resolvedPromptTemplatePaths, resolvedThemePaths)
	core.Time("createRuntime", core.TimingMain)
	runtime, err := core.CreateAgentSessionRuntime(factory, core.CreateAgentSessionRuntimeFactoryOptions{
		Cwd:            sessionManager.GetCwd(),
		AgentDir:       agentDir,
		SessionManager: sessionManager,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		return 1
	}
	core.Time("createAgentSessionRuntime", core.TimingMain)
	services := runtime.Services()
	settingsManager := services.SettingsManager
	modelRuntime := services.ModelRuntime
	resourceLoader := services.ResourceLoader
	if proxy, ok := settingsManager.GetGlobalSettings()["httpProxy"].(string); ok {
		core.ApplyHTTPProxySettings(proxy)
	}
	// pi re-runs configureHttpDispatcher(settingsManager.getHttpIdleTimeoutMs())
	// here; pig's net/http needs no dispatcher installation.
	if idleTimeoutMs, err := settingsManager.GetHTTPIdleTimeoutMs(); err == nil {
		_ = idleTimeoutMs // consumed by the undici dispatcher in pi; no Go equivalent
	}

	if parsed.Help != nil && *parsed.Help {
		reportDiagnostics(startupSettingsDiagnostics)
		var extensionFlags []core.ExtensionFlag
		for _, extension := range resourceLoader.GetExtensions().Extensions {
			names := make([]string, 0, len(extension.Flags))
			for name := range extension.Flags {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				extensionFlags = append(extensionFlags, extension.Flags[name])
			}
		}
		cli.PrintHelp(extensionFlags)
		return 0
	}

	if parsed.ListModels != nil {
		reportDiagnostics(startupSettingsDiagnostics)
		searchPattern := ""
		if s, ok := parsed.ListModels.(string); ok {
			searchPattern = s
		}
		listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := cli.ListModels(listCtx, modelRuntime, searchPattern); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
			return 1
		}
		return 0
	}

	// Read piped stdin content (if any) - skip for RPC mode which uses stdin for JSON-RPC
	var stdinContent *string
	if appMode != "rpc" {
		if content, ok := readPipedStdin(); ok {
			stdinContent = &content
			if appMode == "interactive" {
				appMode = "print"
			}
		}
	}
	core.Time("readPipedStdin", core.TimingMain)

	initialMessage, initialImages := prepareInitialMessage(&parsed, stdinContent)
	core.Time("prepareInitialMessage", core.TimingMain)
	// pi calls initTheme(...) here (main-level, before the deprecation
	// warnings); pig initializes the theme inside InteractiveMode, so the
	// warnings render before theme init relative to pi.
	// pi shows deprecation warnings in interactive mode (runMigrations
	// half).
	if appMode == "interactive" && len(migrationsResult.DeprecationWarnings) > 0 {
		core.ShowDeprecationWarnings(migrationsResult.DeprecationWarnings)
	}
	core.Time("resolveModelScope", core.TimingMain)

	startupDiagnostics := core.DeduplicateDiagnostics(append(append([]core.AgentSessionRuntimeDiagnostic(nil), startupSettingsDiagnostics...), runtime.Diagnostics()...))
	hasRuntimeErrors := false
	for _, diagnostic := range runtime.Diagnostics() {
		if diagnostic.Type == "error" {
			hasRuntimeErrors = true
			break
		}
	}
	if appMode != "interactive" || hasRuntimeErrors {
		reportDiagnostics(startupDiagnostics)
	}
	if hasRuntimeErrors {
		extensionFailure := false
		for _, diagnostic := range runtime.Diagnostics() {
			if diagnostic.Type == "error" && strings.Contains(diagnostic.Message, "Failed to load extension") {
				extensionFailure = true
				break
			}
		}
		if extensionFailure {
			fmt.Fprintf(os.Stderr, "%s\n", extensionLoadFailureHint)
		}
		return 1
	}
	core.Time("createAgentSession", core.TimingMain)

	session := runtime.Session()
	if appMode != "interactive" && session.Model() == nil {
		fmt.Fprintf(os.Stderr, "%s\n", core.FormatNoModelsAvailableMessage())
		return 1
	}

	// pi supports PI_STARTUP_BENCHMARK (interactive only); the TUI lands
	// with Phase 6, so the flag is a no-op here.
	// pi refreshes model catalogs in the background for RPC mode; runRpcMode
	// arrives with the RPC batch (Phase 5).

	switch appMode {
	case "rpc":
		// runRpcMode is the Phase 5 batch.
		fmt.Fprintln(os.Stderr, "Error: RPC mode is not yet available (lands in a later phase).")
		return 1
	case "interactive":
		interactiveMode := interactive.NewInteractiveMode(runtime, interactive.InteractiveModeOptions{
			StartupDiagnostics:   runtime.Diagnostics(),
			ModelFallbackMessage: runtime.ModelFallbackMessage(),
			MigratedProviders:    migrationsResult.MigratedAuthProviders,
			InitialMessage:       lo.FromPtr(initialMessage),
			InitialImages:        initialImages,
			InitialMessages:      parsed.Messages,
			Verbose:              lo.FromPtr(parsed.Verbose),
			TuiMode:              lo.FromPtr(parsed.TuiMode),
			InitialThemeSetting:  lo.FromPtr(parsed.UseTheme),
		})
		core.PrintTimings()
		if err := interactiveMode.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
			return 1
		}
		return 0
	default:
		core.PrintTimings()
		exitCode := modes.RunPrintMode(ctx, runtime, modes.PrintModeOptions{
			Mode:           toPrintOutputMode(appMode),
			Messages:       parsed.Messages,
			InitialMessage: lo.FromPtr(initialMessage),
			InitialImages:  initialImages,
		})
		// pi calls stopThemeWatcher() here (theme batch).
		core.RestoreStdout()
		return exitCode
	}
}

// prepareInitialMessage combines stdin, @file args, and the first CLI
// message (pi: prepareInitialMessage). pi's signature lost the
// autoResizeImages parameter in v0.87.0 (#9631): AgentSession resizes these
// after extension hooks select the request model, so @file attachments defer
// resizing to the prompt path.
func prepareInitialMessage(parsed *cli.Args, stdinContent *string) (*string, []ai.ImageContent) {
	if len(parsed.FileArgs) == 0 {
		return cli.BuildInitialMessage(cli.InitialMessageInput{
			Parsed:       parsed,
			StdinContent: stdinContent,
		}).InitialMessage, nil
	}
	processed := cli.ProcessFileArguments(parsed.FileArgs, &cli.ProcessFileOptions{AutoResizeImages: lo.ToPtr(false)})
	result := cli.BuildInitialMessage(cli.InitialMessageInput{
		Parsed:       parsed,
		FileText:     processed.Text,
		FileImages:   processed.Images,
		StdinContent: stdinContent,
	})
	return result.InitialMessage, result.InitialImages
}

// --- small shared helpers -------------------------------------------------

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func isCharDevice(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// authCommandErrorMessage extracts the display message from an auth command
// error (pi: error instanceof AuthCommandError ? error.message : fallback).
func authCommandErrorMessage(err error) string {
	var authErr *cli.AuthCommandError
	if asAuthCommandError(err, &authErr) {
		return authErr.Message
	}
	return "Failed to resolve credential"
}

func asAuthCommandError(err error, target **cli.AuthCommandError) bool {
	if e, ok := err.(*cli.AuthCommandError); ok {
		*target = e
		return true
	}
	return false
}

func marshalJSONCompact(payload map[string]any) string {
	// pi stringifies the check result with JSON.stringify (insertion order);
	// key order is not load-bearing for consumers, so encoding/json's sorted
	// keys are accepted (declared).
	bytes, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

// main is the process entry (pi: cli.ts bottom-half + the `main(...)` call).
func main() {
	// pi sets process.title = APP_NAME and the PI_CODING_AGENT / AI_AGENT env
	// markers in cli.ts; process title has no Go equivalent, the env markers
	// are set here.
	_ = os.Setenv("PI_CODING_AGENT", "true")
	_ = os.Setenv("AI_AGENT", "pi")
	// pi suppresses process.emitWarning here; Go has no direct equivalent.

	// pig panic containment, process-level last resort: hot-path panics
	// (render, input dispatch, session listeners, worker goroutines) are
	// contained deeper and never reach here; this catches a panic on the
	// main goroutine, restores the terminal, and points at the panic log
	// instead of letting the runtime scroll a raw stack over the TUI.
	// See docs/feat/PANIC-RESILIENCE.md.
	exitCode := 0
	logPath, panicked := pigutils.Guard("main", func() { exitCode = Main(os.Args[1:]) })
	if panicked {
		core.RestoreStdout()
		if logPath != "" {
			fmt.Fprintf(os.Stderr, "pig: internal error (recovered, exiting) — panic log: %s\n", logPath)
		} else {
			fmt.Fprintln(os.Stderr, "pig: internal error (recovered, exiting)")
		}
		exitCode = 1
	}
	os.Exit(exitCode)
}
