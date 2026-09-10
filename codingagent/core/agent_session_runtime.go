// pi: core/agent-session-runtime.ts — owns the current AgentSession plus its
// cwd-bound services. Session replacement methods tear down the current
// runtime first, then create and apply the next runtime. If creation fails,
// the error is propagated to the caller; the caller is responsible for
// user-facing error handling.
package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
)

// CreateAgentSessionRuntimeResult is the result returned by runtime creation
// (pi: CreateAgentSessionRuntimeResult extends CreateAgentSessionResult).
type CreateAgentSessionRuntimeResult struct {
	CreateAgentSessionResult
	Services             *AgentSessionServices
	Diagnostics          []AgentSessionRuntimeDiagnostic
	ModelFallbackMessage string
}

// CreateAgentSessionRuntimeFactory creates a full runtime for a target cwd
// and session manager (pi: CreateAgentSessionRuntimeFactory).
type CreateAgentSessionRuntimeFactory = func(options CreateAgentSessionRuntimeFactoryOptions) (*CreateAgentSessionRuntimeResult, error)

// CreateAgentSessionRuntimeFactoryOptions are the factory inputs (pi's
// inline options object).
type CreateAgentSessionRuntimeFactoryOptions struct {
	Cwd               string
	AgentDir          string
	SessionManager    *SessionManager
	SessionStartEvent *SessionStartEvent
	// ProjectTrustContext is deferred with the extensions batch (pi:
	// projectTrustContext: ProjectTrustContext); the factory below feeds it
	// once the type lands.
	ProjectTrustContext any
}

// SessionImportFileNotFoundError is thrown when /import references a JSONL
// file path that does not exist (pi: SessionImportFileNotFoundError).
type SessionImportFileNotFoundError struct {
	FilePath string
}

func (e *SessionImportFileNotFoundError) Error() string {
	return fmt.Sprintf("File not found: %s", e.FilePath)
}

// extractUserMessageText flattens user message content to text (pi:
// extractUserMessageText).
func extractUserMessageText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	parts, ok := content.([]types.Content)
	if !ok {
		// pi filters {type,text} shapes; pig's decoded user content is
		// []types.Content or UserContent.String.
		if userContent, ok := content.(types.UserContent); ok {
			return userContent.String
		}
		return ""
	}
	var texts []string
	for _, part := range parts {
		if text, ok := part.(*types.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "")
}

// AgentSessionRuntime owns the current AgentSession plus its cwd-bound
// services (pi: AgentSessionRuntime).
type AgentSessionRuntime struct {
	rebindSession              func(session *AgentSession) error
	beforeSessionInvalidate    func()
	session                    *AgentSession
	services                   *AgentSessionServices
	createRuntime              CreateAgentSessionRuntimeFactory
	diagnostics                []AgentSessionRuntimeDiagnostic
	modelFallbackMessage       string
	rebindSessionSet           bool
	beforeSessionInvalidateSet bool
}

// NewAgentSessionRuntime builds the runtime host (pi: constructor).
func NewAgentSessionRuntime(
	session *AgentSession,
	services *AgentSessionServices,
	createRuntime CreateAgentSessionRuntimeFactory,
	diagnostics []AgentSessionRuntimeDiagnostic,
	modelFallbackMessage string,
) *AgentSessionRuntime {
	return &AgentSessionRuntime{
		session:              session,
		services:             services,
		createRuntime:        createRuntime,
		diagnostics:          diagnostics,
		modelFallbackMessage: modelFallbackMessage,
	}
}

// Services returns the cwd-bound services (pi: services getter).
func (r *AgentSessionRuntime) Services() *AgentSessionServices { return r.services }

// Session returns the current session (pi: session getter).
func (r *AgentSessionRuntime) Session() *AgentSession { return r.session }

// Cwd returns the effective cwd (pi: cwd getter).
func (r *AgentSessionRuntime) Cwd() string { return r.services.Cwd }

// Diagnostics returns the collected diagnostics (pi: diagnostics getter).
func (r *AgentSessionRuntime) Diagnostics() []AgentSessionRuntimeDiagnostic { return r.diagnostics }

// ModelFallbackMessage returns the model fallback warning, "" = none (pi:
// modelFallbackMessage getter).
func (r *AgentSessionRuntime) ModelFallbackMessage() string { return r.modelFallbackMessage }

// SetRebindSession sets the session rebind callback (pi: setRebindSession).
// nil = unset.
func (r *AgentSessionRuntime) SetRebindSession(rebindSession func(session *AgentSession) error) {
	r.rebindSession = rebindSession
	r.rebindSessionSet = rebindSession != nil
}

// SetBeforeSessionInvalidate sets a synchronous callback that runs after
// session_shutdown handlers finish but before the current session is
// invalidated (pi: setBeforeSessionInvalidate).
//
// This is for host-owned UI teardown that must not yield to the event loop,
// such as detaching extension-provided TUI components before the old
// extension context becomes stale.
func (r *AgentSessionRuntime) SetBeforeSessionInvalidate(beforeSessionInvalidate func()) {
	r.beforeSessionInvalidate = beforeSessionInvalidate
	r.beforeSessionInvalidateSet = beforeSessionInvalidate != nil
}

func (r *AgentSessionRuntime) emitBeforeSwitch(ctx context.Context, reason string, targetSessionFile string) (bool, error) {
	runner := r.session.GetExtensionRunner()
	if !runner.HasHandlers("session_before_switch") {
		return false, nil
	}

	result, err := runner.Emit(ctx, &SessionBeforeSwitchEvent{
		Type:              "session_before_switch",
		Reason:            reason,
		TargetSessionFile: targetSessionFile,
	})
	if err != nil {
		return false, err
	}
	if switchResult, ok := result.(*SessionBeforeSwitchResult); ok && switchResult != nil {
		return switchResult.Cancel, nil
	}
	return false, nil
}

func (r *AgentSessionRuntime) emitBeforeFork(ctx context.Context, entryID string, position string) (bool, error) {
	runner := r.session.GetExtensionRunner()
	if !runner.HasHandlers("session_before_fork") {
		return false, nil
	}

	result, err := runner.Emit(ctx, &SessionBeforeForkEvent{
		Type:     "session_before_fork",
		EntryID:  entryID,
		Position: position,
	})
	if err != nil {
		return false, err
	}
	if forkResult, ok := result.(*SessionBeforeForkResult); ok && forkResult != nil {
		return forkResult.Cancel, nil
	}
	return false, nil
}

func (r *AgentSessionRuntime) teardownCurrent(ctx context.Context, reason string, targetSessionFile string) error {
	// Settle any active response first so the aborted turn (including tool
	// results) is persisted to the outgoing session before it is replaced.
	if err := r.session.Abort(ctx); err != nil {
		return err
	}
	if _, err := EmitSessionShutdownEvent(ctx, r.session.GetExtensionRunner(), &SessionShutdownEvent{
		Type:              "session_shutdown",
		Reason:            reason,
		TargetSessionFile: targetSessionFile,
	}); err != nil {
		return err
	}
	if r.beforeSessionInvalidateSet {
		r.beforeSessionInvalidate()
	}
	r.session.Dispose()
	return nil
}

func (r *AgentSessionRuntime) apply(result *CreateAgentSessionRuntimeResult) {
	r.session = result.Session
	r.services = result.Services
	r.diagnostics = result.Diagnostics
	r.modelFallbackMessage = result.ModelFallbackMessage
}

func (r *AgentSessionRuntime) finishSessionReplacement(ctx context.Context, withSession func(replacedCtx *ReplacedSessionContext) error) error {
	if r.rebindSessionSet {
		if err := r.rebindSession(r.session); err != nil {
			return err
		}
	}
	if withSession != nil {
		return withSession(r.session.CreateReplacedSessionContext())
	}
	return nil
}

// SwitchSessionOptions are the switchSession inputs (pi's inline options).
type SwitchSessionOptions struct {
	// CwdOverride forces the session cwd (pi: cwdOverride; nil = none).
	CwdOverride *string
	// WithSession runs with the replacement session context (pi:
	// withSession).
	WithSession func(replacedCtx *ReplacedSessionContext) error
	// ProjectTrustContextFactory resolves trust for the target cwd (pi:
	// projectTrustContextFactory).
	ProjectTrustContextFactory func(cwd string) any
}

// SwitchSession opens another session file and replaces the runtime (pi:
// switchSession).
func (r *AgentSessionRuntime) SwitchSession(ctx context.Context, sessionPath string, options *SwitchSessionOptions) (bool, error) {
	if options == nil {
		options = &SwitchSessionOptions{}
	}
	cancelled, err := r.emitBeforeSwitch(ctx, "resume", sessionPath)
	if err != nil {
		return false, err
	}
	if cancelled {
		return true, nil
	}

	previousSessionFile := r.session.SessionFile()
	var cwdOverride *string
	if options.CwdOverride != nil {
		cwdOverride = options.CwdOverride
	}
	sessionManager, err := OpenSession(sessionPath, "", cwdOverride)
	if err != nil {
		return false, err
	}
	if err := AssertSessionCwdExists(sessionManager, r.services.Cwd); err != nil {
		return false, err
	}
	if err := r.teardownCurrent(ctx, "resume", sessionManager.GetSessionFile()); err != nil {
		return false, err
	}
	var projectTrustContext any
	if options.ProjectTrustContextFactory != nil {
		projectTrustContext = options.ProjectTrustContextFactory(sessionManager.GetCwd())
	}
	result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
		Cwd:                 sessionManager.GetCwd(),
		AgentDir:            r.services.AgentDir,
		SessionManager:      sessionManager,
		SessionStartEvent:   &SessionStartEvent{Type: "session_start", Reason: "resume", PreviousSessionFile: previousSessionFile},
		ProjectTrustContext: projectTrustContext,
	})
	if err != nil {
		return false, err
	}
	r.apply(result)
	if err := r.finishSessionReplacement(ctx, options.WithSession); err != nil {
		return false, err
	}
	return false, nil
}

// RuntimeNewSessionOptions are the newSession inputs (pi's inline options;
// named to avoid the SessionManager's NewSessionOptions).
type RuntimeNewSessionOptions struct {
	// ParentSession is the parent session file for the new session (pi:
	// parentSession).
	ParentSession string
	// Setup runs on the new session manager before the context is rebuilt
	// (pi: setup).
	Setup func(sessionManager *SessionManager) error
	// WithSession runs with the replacement session context (pi:
	// withSession).
	WithSession func(replacedCtx *ReplacedSessionContext) error
}

// NewSession replaces the runtime with a fresh session (pi: newSession).
func (r *AgentSessionRuntime) NewSession(ctx context.Context, options *RuntimeNewSessionOptions) (bool, error) {
	if options == nil {
		options = &RuntimeNewSessionOptions{}
	}
	cancelled, err := r.emitBeforeSwitch(ctx, "new", "")
	if err != nil {
		return false, err
	}
	if cancelled {
		return true, nil
	}

	previousSessionFile := r.session.SessionFile()
	sessionDir := r.session.SessionManager.GetSessionDir()
	var sessionManager *SessionManager
	if r.session.SessionManager.IsPersisted() {
		sessionManager, err = CreateSessionManager(r.Cwd(), sessionDir, nil)
	} else {
		sessionManager, err = InMemorySession(r.Cwd(), nil)
	}
	_ = options
	if err != nil {
		return false, err
	}
	if options.ParentSession != "" {
		parent := options.ParentSession
		if _, err := sessionManager.NewSession(&NewSessionOptions{ParentSession: &parent}); err != nil {
			return false, err
		}
	}

	if err := r.teardownCurrent(ctx, "new", sessionManager.GetSessionFile()); err != nil {
		return false, err
	}
	result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
		Cwd:               r.Cwd(),
		AgentDir:          r.services.AgentDir,
		SessionManager:    sessionManager,
		SessionStartEvent: &SessionStartEvent{Type: "session_start", Reason: "new", PreviousSessionFile: previousSessionFile},
	})
	if err != nil {
		return false, err
	}
	r.apply(result)
	if options.Setup != nil {
		if err := options.Setup(r.session.SessionManager); err != nil {
			return false, err
		}
		r.session.Agent.State().Messages = r.session.SessionManager.BuildSessionContext().Messages
	}
	if err := r.finishSessionReplacement(ctx, options.WithSession); err != nil {
		return false, err
	}
	return false, nil
}

// ForkOptions are the fork inputs (pi's inline options).
type ForkOptions struct {
	// Position is "before" (default) or "at" (pi: position).
	Position string
	// WithSession runs with the replacement session context (pi:
	// withSession).
	WithSession func(replacedCtx *ReplacedSessionContext) error
}

// ForkResult is the fork outcome (pi's { cancelled, selectedText? }).
type ForkResult struct {
	Cancelled    bool
	SelectedText string
}

// Fork forks the session at (or before) an entry and replaces the runtime
// (pi: fork).
func (r *AgentSessionRuntime) Fork(ctx context.Context, entryID string, options *ForkOptions) (*ForkResult, error) {
	position := "before"
	if options != nil && options.Position != "" {
		position = options.Position
	}
	cancelled, err := r.emitBeforeFork(ctx, entryID, position)
	if err != nil {
		return nil, err
	}
	if cancelled {
		return &ForkResult{Cancelled: true}, nil
	}
	var targetLeafID string
	selectedText := ""

	selectedEntry := r.session.SessionManager.GetEntry(entryID)
	if selectedEntry == nil {
		return nil, fmt.Errorf("Invalid entry ID for forking")
	}

	if position == "at" {
		targetLeafID = selectedEntry.Base().ID
	} else {
		messageEntry, ok := selectedEntry.(*SessionMessageEntry)
		if !ok || messageEntry.Message.MessageRole() != types.RoleUser {
			return nil, fmt.Errorf("Invalid entry ID for forking")
		}
		if messageEntry.Base().ParentID != nil {
			targetLeafID = *messageEntry.Base().ParentID
		}
		if userMessage, ok := messageEntry.Message.(*types.UserMessage); ok {
			selectedText = extractUserMessageText(userMessage.Content)
		} else {
			selectedText = extractUserMessageText(messageEntry.Message)
		}
	}

	previousSessionFile := r.session.SessionFile()
	if r.session.SessionManager.IsPersisted() {
		currentSessionFile := r.session.SessionFile()
		if currentSessionFile == "" {
			return nil, fmt.Errorf("Persisted session is missing a session file")
		}
		sessionDir := r.session.SessionManager.GetSessionDir()
		if targetLeafID == "" {
			sessionManager, err := CreateSessionManager(r.Cwd(), sessionDir, nil)
			if err != nil {
				return nil, err
			}
			parent := currentSessionFile
			if _, err := sessionManager.NewSession(&NewSessionOptions{ParentSession: &parent}); err != nil {
				return nil, err
			}
			if err := r.teardownCurrent(ctx, "fork", sessionManager.GetSessionFile()); err != nil {
				return nil, err
			}
			result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
				Cwd:               r.Cwd(),
				AgentDir:          r.services.AgentDir,
				SessionManager:    sessionManager,
				SessionStartEvent: &SessionStartEvent{Type: "session_start", Reason: "fork", PreviousSessionFile: previousSessionFile},
			})
			if err != nil {
				return nil, err
			}
			r.apply(result)
			if err := r.finishSessionReplacement(ctx, options.WithSession); err != nil {
				return nil, err
			}
			return &ForkResult{SelectedText: selectedText}, nil
		}

		if _, err := os.Stat(currentSessionFile); err != nil {
			return nil, fmt.Errorf(
				"This session has not been saved yet. Wait for the first assistant response before cloning or forking it.",
			)
		}
		sessionManager, err := OpenSession(currentSessionFile, sessionDir, nil)
		if err != nil {
			return nil, err
		}
		forkedSessionPath, err := sessionManager.CreateBranchedSession(targetLeafID)
		if err != nil {
			return nil, err
		}
		if forkedSessionPath == "" {
			return nil, fmt.Errorf("Failed to create forked session")
		}
		if err := r.teardownCurrent(ctx, "fork", sessionManager.GetSessionFile()); err != nil {
			return nil, err
		}
		result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
			Cwd:               sessionManager.GetCwd(),
			AgentDir:          r.services.AgentDir,
			SessionManager:    sessionManager,
			SessionStartEvent: &SessionStartEvent{Type: "session_start", Reason: "fork", PreviousSessionFile: previousSessionFile},
		})
		if err != nil {
			return nil, err
		}
		r.apply(result)
		if err := r.finishSessionReplacement(ctx, options.WithSession); err != nil {
			return nil, err
		}
		return &ForkResult{SelectedText: selectedText}, nil
	}

	sessionManager := r.session.SessionManager
	if targetLeafID == "" {
		parent := r.session.SessionFile()
		var parentPtr *string
		if parent != "" {
			parentPtr = &parent
		}
		if _, err := sessionManager.NewSession(&NewSessionOptions{ParentSession: parentPtr}); err != nil {
			return nil, err
		}
	} else {
		if _, err := sessionManager.CreateBranchedSession(targetLeafID); err != nil {
			return nil, err
		}
	}
	if err := r.teardownCurrent(ctx, "fork", sessionManager.GetSessionFile()); err != nil {
		return nil, err
	}
	result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
		Cwd:               r.Cwd(),
		AgentDir:          r.services.AgentDir,
		SessionManager:    sessionManager,
		SessionStartEvent: &SessionStartEvent{Type: "session_start", Reason: "fork", PreviousSessionFile: previousSessionFile},
	})
	if err != nil {
		return nil, err
	}
	r.apply(result)
	if err := r.finishSessionReplacement(ctx, options.WithSession); err != nil {
		return nil, err
	}
	return &ForkResult{SelectedText: selectedText}, nil
}

// ImportFromJsonl imports a session JSONL file and switches runtime state to
// the imported session (pi: importFromJsonl). Returns cancelled=true when
// cancelled by session_before_switch. Errors:
//   - SessionImportFileNotFoundError when the input path does not exist.
//   - MissingSessionCwdError when the imported session cwd cannot be
//     resolved and no override is provided.
func (r *AgentSessionRuntime) ImportFromJsonl(ctx context.Context, inputPath string, cwdOverride *string) (bool, error) {
	resolvedPath := inputPath
	if !filepath.IsAbs(resolvedPath) {
		if wd, err := os.Getwd(); err == nil {
			resolvedPath = filepath.Join(wd, resolvedPath)
		}
	}
	resolvedPath = filepath.Clean(resolvedPath)
	if _, err := os.Stat(resolvedPath); err != nil {
		return false, &SessionImportFileNotFoundError{FilePath: resolvedPath}
	}

	sessionDir := r.session.SessionManager.GetSessionDir()
	if _, err := os.Stat(sessionDir); err != nil {
		if err := os.MkdirAll(sessionDir, 0o755); err != nil {
			return false, err
		}
	}

	destinationPath := filepath.Join(sessionDir, filepath.Base(resolvedPath))
	cancelled, err := r.emitBeforeSwitch(ctx, "resume", destinationPath)
	if err != nil {
		return false, err
	}
	if cancelled {
		return true, nil
	}

	previousSessionFile := r.session.SessionFile()
	if filepath.Clean(destinationPath) != resolvedPath {
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			return false, err
		}
		if err := os.WriteFile(destinationPath, data, 0o644); err != nil {
			return false, err
		}
	}

	sessionManager, err := OpenSession(destinationPath, sessionDir, cwdOverride)
	if err != nil {
		return false, err
	}
	if err := AssertSessionCwdExists(sessionManager, r.services.Cwd); err != nil {
		return false, err
	}
	if err := r.teardownCurrent(ctx, "resume", sessionManager.GetSessionFile()); err != nil {
		return false, err
	}
	result, err := r.createRuntime(CreateAgentSessionRuntimeFactoryOptions{
		Cwd:               sessionManager.GetCwd(),
		AgentDir:          r.services.AgentDir,
		SessionManager:    sessionManager,
		SessionStartEvent: &SessionStartEvent{Type: "session_start", Reason: "resume", PreviousSessionFile: previousSessionFile},
	})
	if err != nil {
		return false, err
	}
	r.apply(result)
	if err := r.finishSessionReplacement(ctx, nil); err != nil {
		return false, err
	}
	return false, nil
}

// Dispose emits session_shutdown(quit) and tears down the session (pi:
// dispose).
func (r *AgentSessionRuntime) Dispose() error {
	if _, err := EmitSessionShutdownEvent(context.Background(), r.session.GetExtensionRunner(), &SessionShutdownEvent{
		Type:   "session_shutdown",
		Reason: "quit",
	}); err != nil {
		return err
	}
	if r.beforeSessionInvalidateSet {
		r.beforeSessionInvalidate()
	}
	r.session.Dispose()
	return nil
}

// CreateAgentSessionRuntime creates the initial runtime from a runtime
// factory and initial session target (pi: createAgentSessionRuntime). The
// same factory is stored on the returned AgentSessionRuntime and reused for
// later /new, /resume, /fork, and import flows.
func CreateAgentSessionRuntime(
	createRuntime CreateAgentSessionRuntimeFactory,
	options CreateAgentSessionRuntimeFactoryOptions,
) (*AgentSessionRuntime, error) {
	if err := AssertSessionCwdExists(options.SessionManager, options.Cwd); err != nil {
		return nil, err
	}
	result, err := createRuntime(options)
	if err != nil {
		return nil, err
	}
	return NewAgentSessionRuntime(
		result.Session,
		result.Services,
		createRuntime,
		result.Diagnostics,
		result.ModelFallbackMessage,
	), nil
}
