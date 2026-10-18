// pi: modes/rpc/rpc-mode.ts — RPC mode (headless operation with a JSON
// stdin/stdout protocol). Used for embedding the agent in other programs:
// commands arrive as JSON lines on stdin, responses and session events are
// emitted as JSON lines on stdout.
//
// Protocol:
//   - Commands: JSON objects with a `type` field, optional `id` for
//     correlation.
//   - Responses: { type: "response", command, success, data?/error? }.
//   - Events: AgentSessionEvent objects streamed as they occur.
//
// Extension UI: pi emits extension_ui_request and expects extension_ui_response
// round-trips. pig does not port the extension system (Phase 7 not done, and
// pig extensions are incompatible with pi's), so no request is ever emitted;
// incoming extension_ui_response lines are recognized and ignored. The
// get_commands "extension" source is likewise always empty.
//
// Go adaptation (declared): pi's runRpcMode is a never-resolving promise and
// writes through module-global output-guard/jsonl functions (mocked in tests).
// pig splits the entry into RunRpcMode (takes over stdout, uses os.Stdin) and
// an unexported runRpcMode that receives an rpcIO so tests can inject a
// writer and a reader. Each input line is handled in its own goroutine so
// steer/abort commands can run while a prompt is streaming (pi: `void
// handleInputLine`, whose async handlers run concurrently).
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// RpcRuntimeHost is the runtime host consumption surface (pi:
// AgentSessionRuntime's members used by runRpcMode). Mirrors
// modes.PrintRuntimeHost.
type RpcRuntimeHost interface {
	Session() *core.AgentSession
	NewSession(ctx context.Context, options *core.RuntimeNewSessionOptions) (cancelled bool, err error)
	Fork(ctx context.Context, entryID string, options *core.ForkOptions) (*core.ForkResult, error)
	SwitchSession(ctx context.Context, sessionPath string, options *core.SwitchSessionOptions) (cancelled bool, err error)
	Dispose() error
	SetRebindSession(rebindSession func(session *core.AgentSession) error)
}

// rpcIO carries the process IO so tests can substitute it (pig-only).
type rpcIO struct {
	write            func(string)
	waitBackpressure func()
	flush            func()
	input            io.Reader
}

// RunRpcMode runs in RPC mode (pi: runRpcMode). It takes over stdout and
// blocks until stdin reaches EOF or a termination signal arrives. Returns the
// process exit code.
func RunRpcMode(ctx context.Context, host RpcRuntimeHost) int {
	core.TakeOverStdout()
	return runRpcMode(ctx, host, rpcIO{
		write:            func(s string) { core.WriteRawStdout(s) },
		waitBackpressure: core.WaitForRawStdoutBackpressure,
		flush:            core.FlushRawStdout,
		input:            os.Stdin,
	})
}

func runRpcMode(ctx context.Context, host RpcRuntimeHost, ioState rpcIO) int {
	var session *core.AgentSession
	var unsubscribe func()
	var unsubscribeBackpressure func()

	// shutdownRequested is set by the extension ShutdownHandler (pi:
	// shutdownHandler); never fires without extensions.
	var shutdownRequested atomic.Bool
	var shuttingDown atomic.Bool
	// handlers tracks in-flight per-line command goroutines. Go's scheduler
	// can read EOF before a fast handler's response is written, whereas Node
	// drains microtasks before the stdin "end" event; the EOF path waits
	// (bounded) for handlers so `echo '{...}' | pig --mode rpc` reliably
	// emits its responses (pig-only robustness; pi exits immediately).
	var handlers sync.WaitGroup
	done := make(chan int, 1)
	var signalCleanupHandlers []func()
	detachInput := func() {}

	output := func(obj any) {
		line, err := SerializeJsonLine(obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return
		}
		ioState.write(line)
	}

	success := func(id string, command string, data any) RpcResponse {
		return RpcResponse{ID: id, Type: "response", Command: command, Success: true, Data: data}
	}
	failure := func(id string, command string, message string) RpcResponse {
		return RpcResponse{ID: id, Type: "response", Command: command, Success: false, Error: message}
	}

	shutdown := func(exitCode int, flush bool) {
		if !shuttingDown.CompareAndSwap(false, true) {
			return
		}
		for _, cleanup := range signalCleanupHandlers {
			cleanup()
		}
		if unsubscribe != nil {
			unsubscribe()
		}
		if unsubscribeBackpressure != nil {
			unsubscribeBackpressure()
		}
		_ = host.Dispose()
		detachInput()
		if flush {
			ioState.flush()
		}
		done <- exitCode
	}

	checkShutdownRequested := func() {
		if shutdownRequested.Load() {
			shutdown(0, true)
		}
	}

	rebindSession := func() error {
		session = host.Session()

		bindErr := session.BindExtensions(ctx, &core.ExtensionBindings{
			// UIContext is intentionally nil: the extension system is not
			// ported, so no UI request is ever emitted.
			Mode: core.ExtensionModeRPC,
			CommandContextActions: &core.ExtensionCommandContextActions{
				WaitForIdle: func(ctx context.Context) error { return session.WaitForIdle(ctx) },
				NewSession: func(ctx context.Context, newSessionOptions *core.NewSessionCommandOptions) (bool, error) {
					return host.NewSession(ctx, &core.RuntimeNewSessionOptions{
						ParentSession: newSessionOptions.ParentSession,
						Setup: func(sessionManager *core.SessionManager) error {
							if newSessionOptions.Setup == nil {
								return nil
							}
							return newSessionOptions.Setup(ctx, sessionManager)
						},
						WithSession: func(sessionCtx *core.ReplacedSessionContext) error {
							if newSessionOptions.WithSession == nil {
								return nil
							}
							return newSessionOptions.WithSession(ctx, sessionCtx)
						},
					})
				},
				Fork: func(ctx context.Context, entryID string, forkOptions *core.ForkCommandOptions) (bool, error) {
					var withSession func(sessionCtx *core.ReplacedSessionContext) error
					if forkOptions.WithSession != nil {
						withSession = func(sessionCtx *core.ReplacedSessionContext) error {
							return forkOptions.WithSession(ctx, sessionCtx)
						}
					}
					result, err := host.Fork(ctx, entryID, &core.ForkOptions{
						Position:    forkOptions.Position,
						WithSession: withSession,
					})
					if err != nil {
						return false, err
					}
					return result.Cancelled, nil
				},
				NavigateTree: func(ctx context.Context, targetID string, navigateOptions *core.NavigateTreeCommandOptions) (bool, error) {
					result, err := session.NavigateTree(ctx, targetID, &core.NavigateTreeOptions{
						Summarize:           navigateOptions.Summarize,
						CustomInstructions:  navigateOptions.CustomInstructions,
						ReplaceInstructions: navigateOptions.ReplaceInstructions,
						Label:               navigateOptions.Label,
					})
					if err != nil {
						return false, err
					}
					return result.Cancelled, nil
				},
				SwitchSession: func(ctx context.Context, sessionPath string, switchOptions *core.SwitchSessionCommandOptions) (bool, error) {
					var withSession func(sessionCtx *core.ReplacedSessionContext) error
					if switchOptions.WithSession != nil {
						withSession = func(sessionCtx *core.ReplacedSessionContext) error {
							return switchOptions.WithSession(ctx, sessionCtx)
						}
					}
					return host.SwitchSession(ctx, sessionPath, &core.SwitchSessionOptions{
						WithSession: withSession,
					})
				},
				Reload: func(ctx context.Context) error {
					return session.Reload(ctx, nil)
				},
			},
			ShutdownHandler: func() {
				shutdownRequested.Store(true)
			},
			OnError: func(err core.ExtensionError) {
				output(map[string]any{
					"type":          "extension_error",
					"extensionPath": err.ExtensionPath,
					"event":         err.Event,
					"error":         err.Error,
				})
			},
		})
		if bindErr != nil {
			return bindErr
		}

		if unsubscribe != nil {
			unsubscribe()
		}
		if unsubscribeBackpressure != nil {
			unsubscribeBackpressure()
		}
		unsubscribe = session.Subscribe(func(event *core.AgentSessionEvent) {
			jsonEvent, err := modes.ToJsonEvent(*event)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				return
			}
			data, err := modes.MarshalJsonEvent(jsonEvent)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				return
			}
			ioState.write(string(data) + "\n")
			if event.Type == core.AgentSessionEventAgentSettled {
				go checkShutdownRequested()
			}
		})
		unsubscribeBackpressure = session.Agent.Subscribe(func(ctx context.Context, event agent.AgentEvent) {
			ioState.waitBackpressure()
		})
		return nil
	}

	registerSignalHandlers := func() {
		signals := []os.Signal{syscall.SIGTERM}
		if runtime.GOOS != "windows" {
			signals = append(signals, syscall.SIGHUP)
		}
		for _, sig := range signals {
			signalCh := make(chan os.Signal, 1)
			signal.Notify(signalCh, sig)
			signalCleanupHandlers = append(signalCleanupHandlers, func() { signal.Stop(signalCh) })
			go func(sig os.Signal, signalCh chan os.Signal) {
				if _, ok := <-signalCh; !ok {
					return
				}
				utils.KillTrackedDetachedChildren()
				if sig == syscall.SIGHUP {
					shutdown(129, true)
				} else {
					shutdown(143, true)
				}
			}(sig, signalCh)
		}
	}

	host.SetRebindSession(func(session *core.AgentSession) error {
		return rebindSession()
	})

	if err := rebindSession(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	registerSignalHandlers()

	// handleCommand handles a single command (pi: handleCommand). The boolean
	// reports whether the caller should emit the returned response; prompt
	// returns false because its response is emitted from the preflight
	// callback.
	handleCommand := func(cmd RpcCommand) (RpcResponse, bool) {
		id := cmd.ID

		switch cmd.Type {
		// =================================================================
		// Prompting
		// =================================================================

		case "prompt":
			// Start prompt handling immediately, but emit the authoritative
			// response only after prompt preflight succeeds. Queued and
			// immediately handled prompts also count as success.
			preflightSucceeded := false
			err := session.Prompt(ctx, cmd.Message, &core.PromptOptions{
				Images:            cmd.Images,
				StreamingBehavior: cmd.StreamingBehavior,
				Source:            core.InputSourceRPC,
				PreflightResult: func(disposition core.PromptDisposition) {
					preflightSucceeded = true
					output(success(id, "prompt", rpcDispositionData{Disposition: disposition}))
				},
			})
			if err != nil && !preflightSucceeded {
				output(failure(id, "prompt", err.Error()))
			}
			return RpcResponse{}, false

		case "steer":
			disposition, err := session.Steer(ctx, cmd.Message, cmd.Images, &core.QueueUserInputOptions{Source: core.InputSourceRPC})
			if err != nil {
				return failure(id, "steer", err.Error()), true
			}
			return success(id, "steer", rpcDispositionData{Disposition: disposition}), true

		case "follow_up":
			disposition, err := session.FollowUp(ctx, cmd.Message, cmd.Images, &core.QueueUserInputOptions{Source: core.InputSourceRPC})
			if err != nil {
				return failure(id, "follow_up", err.Error()), true
			}
			return success(id, "follow_up", rpcDispositionData{Disposition: disposition}), true

		case "abort":
			if err := session.Abort(ctx); err != nil {
				return failure(id, "abort", err.Error()), true
			}
			return success(id, "abort", nil), true

		case "clear_queue":
			steering, followUp := session.ClearQueue()
			return success(id, "clear_queue", rpcClearQueueData{Steering: steering, FollowUp: followUp}), true

		case "new_session":
			var options *core.RuntimeNewSessionOptions
			if cmd.ParentSession != "" {
				options = &core.RuntimeNewSessionOptions{ParentSession: cmd.ParentSession}
			}
			cancelled, err := host.NewSession(ctx, options)
			if err != nil {
				return failure(id, "new_session", err.Error()), true
			}
			if !cancelled {
				if err := rebindSession(); err != nil {
					return failure(id, "new_session", err.Error()), true
				}
			}
			return success(id, "new_session", rpcNewSessionData{Cancelled: cancelled}), true

		// =================================================================
		// State
		// =================================================================

		case "get_state":
			state := RpcSessionState{
				Model:                 session.Model(),
				ThinkingLevel:         session.ThinkingLevel(),
				IsStreaming:           session.IsStreaming(),
				IsCompacting:          session.IsCompacting(),
				SteeringMode:          string(session.SteeringMode()),
				FollowUpMode:          string(session.FollowUpMode()),
				SessionFile:           session.SessionFile(),
				SessionID:             session.SessionID(),
				AutoCompactionEnabled: session.AutoCompactionEnabled(),
				MessageCount:          len(session.Messages()),
				PendingMessageCount:   session.PendingMessageCount(),
			}
			if name, ok := session.SessionName(); ok {
				state.SessionName = name
			}
			return success(id, "get_state", state), true

		// =================================================================
		// Model
		// =================================================================

		case "set_model":
			models := session.GetModelRuntime().GetAvailableSnapshot()
			var model *types.Model
			for i := range models {
				if string(models[i].Provider) == cmd.Provider && models[i].ID == cmd.ModelID {
					model = &models[i]
					break
				}
			}
			if model == nil {
				return failure(id, "set_model", "Model not found: "+cmd.Provider+"/"+cmd.ModelID), true
			}
			if err := session.SetModel(ctx, model, nil); err != nil {
				return failure(id, "set_model", err.Error()), true
			}
			return success(id, "set_model", model), true

		case "cycle_model":
			result, err := session.CycleModel(ctx, "", nil)
			if err != nil {
				return failure(id, "cycle_model", err.Error()), true
			}
			if result == nil {
				return success(id, "cycle_model", marshalNull), true
			}
			return success(id, "cycle_model", rpcCycleModelData{
				Model:         result.Model,
				ThinkingLevel: result.ThinkingLevel,
				IsScoped:      result.IsScoped,
			}), true

		case "get_available_models":
			return success(id, "get_available_models", rpcModelsData{
				Models: session.GetModelRuntime().GetAvailableSnapshot(),
			}), true

		// =================================================================
		// Thinking
		// =================================================================

		case "set_thinking_level":
			session.SetThinkingLevel(cmd.Level, nil)
			return success(id, "set_thinking_level", nil), true

		case "cycle_thinking_level":
			level := session.CycleThinkingLevel(nil)
			if level == "" {
				return success(id, "cycle_thinking_level", marshalNull), true
			}
			return success(id, "cycle_thinking_level", rpcLevelData{Level: level}), true

		case "get_available_thinking_levels":
			return success(id, "get_available_thinking_levels", rpcLevelsData{
				Levels: session.GetAvailableThinkingLevels(),
			}), true

		// =================================================================
		// Queue Modes
		// =================================================================

		case "set_steering_mode":
			session.SetSteeringMode(cmd.Mode)
			return success(id, "set_steering_mode", nil), true

		case "set_follow_up_mode":
			session.SetFollowUpMode(cmd.Mode)
			return success(id, "set_follow_up_mode", nil), true

		// =================================================================
		// Compaction
		// =================================================================

		case "compact":
			result, err := session.Compact(ctx, cmd.CustomInstructions)
			if err != nil {
				return failure(id, "compact", err.Error()), true
			}
			return success(id, "compact", result), true

		case "set_auto_compaction":
			session.SetAutoCompactionEnabled(lo.FromPtr(cmd.Enabled))
			return success(id, "set_auto_compaction", nil), true

		// =================================================================
		// Retry
		// =================================================================

		case "set_auto_retry":
			session.SetAutoRetryEnabled(lo.FromPtr(cmd.Enabled))
			return success(id, "set_auto_retry", nil), true

		case "abort_retry":
			session.AbortRetry()
			return success(id, "abort_retry", nil), true

		// =================================================================
		// Bash
		// =================================================================

		case "bash":
			excludeFromContext := lo.FromPtr(cmd.ExcludeFromContext)
			eventResult, err := session.GetExtensionRunner().EmitUserBash(ctx, &core.UserBashEvent{
				Type:               "user_bash",
				Command:            cmd.Command,
				ExcludeFromContext: excludeFromContext,
				Cwd:                session.SessionManager.GetCwd(),
			})
			if err != nil {
				return failure(id, "bash", err.Error()), true
			}
			if eventResult != nil && eventResult.Result != nil {
				session.RecordBashResult(cmd.Command, *eventResult.Result, excludeFromContext)
				return success(id, "bash", eventResult.Result), true
			}
			result, err := session.ExecuteBash(ctx, cmd.Command, nil, bashOptionsForRpc(id, excludeFromContext, eventResult))
			if err != nil {
				return failure(id, "bash", err.Error()), true
			}
			return success(id, "bash", result), true

		case "abort_bash":
			session.AbortBash()
			return success(id, "abort_bash", nil), true

		// =================================================================
		// Session
		// =================================================================

		case "get_session_stats":
			return success(id, "get_session_stats", session.GetSessionStats()), true

		case "export_html":
			path, err := session.ExportToHtml(cmd.OutputPath, "")
			if err != nil {
				return failure(id, "export_html", err.Error()), true
			}
			return success(id, "export_html", rpcExportHTMLData{Path: path}), true

		case "switch_session":
			cancelled, err := host.SwitchSession(ctx, cmd.SessionPath, nil)
			if err != nil {
				return failure(id, "switch_session", err.Error()), true
			}
			if !cancelled {
				if err := rebindSession(); err != nil {
					return failure(id, "switch_session", err.Error()), true
				}
			}
			return success(id, "switch_session", rpcNewSessionData{Cancelled: cancelled}), true

		case "fork":
			result, err := host.Fork(ctx, cmd.EntryID, nil)
			if err != nil {
				return failure(id, "fork", err.Error()), true
			}
			if !result.Cancelled {
				if err := rebindSession(); err != nil {
					return failure(id, "fork", err.Error()), true
				}
			}
			return success(id, "fork", rpcForkData{Text: result.SelectedText, Cancelled: result.Cancelled}), true

		case "clone":
			leafID := session.SessionManager.GetLeafID()
			if leafID == nil {
				return failure(id, "clone", "Cannot clone session: no current entry selected"), true
			}
			result, err := host.Fork(ctx, *leafID, &core.ForkOptions{Position: "at"})
			if err != nil {
				return failure(id, "clone", err.Error()), true
			}
			if !result.Cancelled {
				if err := rebindSession(); err != nil {
					return failure(id, "clone", err.Error()), true
				}
			}
			return success(id, "clone", rpcNewSessionData{Cancelled: result.Cancelled}), true

		case "get_fork_messages":
			userMessages := session.GetUserMessagesForForking()
			messages := make([]rpcForkMessage, 0, len(userMessages))
			for _, message := range userMessages {
				messages = append(messages, rpcForkMessage{EntryID: message.EntryID, Text: message.Text})
			}
			return success(id, "get_fork_messages", rpcForkMessagesData{Messages: messages}), true

		case "get_entries":
			entries := session.SessionManager.GetEntries()
			if cmd.Since != "" {
				sinceIndex := -1
				for i, entry := range entries {
					if entry.Base().ID == cmd.Since {
						sinceIndex = i
						break
					}
				}
				if sinceIndex == -1 {
					return failure(id, "get_entries", "Entry not found: "+cmd.Since), true
				}
				entries = entries[sinceIndex+1:]
			}
			return success(id, "get_entries", rpcEntriesData{
				Entries: entries,
				LeafID:  session.SessionManager.GetLeafID(),
			}), true

		case "get_tree":
			return success(id, "get_tree", rpcTreeData{
				Tree:   session.SessionManager.GetTree(),
				LeafID: session.SessionManager.GetLeafID(),
			}), true

		case "get_last_assistant_text":
			var text *string
			if last := session.GetLastAssistantText(); last != "" {
				text = &last
			}
			return success(id, "get_last_assistant_text", rpcTextData{Text: text}), true

		case "set_session_name":
			name := strings.TrimSpace(cmd.Name)
			if name == "" {
				return failure(id, "set_session_name", "Session name cannot be empty"), true
			}
			session.SetSessionName(name)
			return success(id, "set_session_name", nil), true

		// =================================================================
		// Messages
		// =================================================================

		case "get_messages":
			return success(id, "get_messages", rpcMessagesData{Messages: session.Messages()}), true

		// =================================================================
		// Commands (available for invocation via prompt)
		// =================================================================

		case "get_commands":
			commands := make([]RpcSlashCommand, 0)

			// Extensions are not ported, so this loop is always empty.
			for _, command := range session.GetExtensionRunner().GetRegisteredCommands() {
				commands = append(commands, RpcSlashCommand{
					Name:        command.InvocationName,
					Description: command.Description,
					Source:      "extension",
					SourceInfo:  command.SourceInfo,
				})
			}

			for _, template := range session.PromptTemplates() {
				commands = append(commands, RpcSlashCommand{
					Name:        template.Name,
					Description: template.Description,
					Source:      "prompt",
					SourceInfo:  template.SourceInfo,
				})
			}

			skills, _ := session.GetResourceLoader().GetSkills()
			for _, skill := range skills {
				commands = append(commands, RpcSlashCommand{
					Name:        "skill:" + skill.Name,
					Description: skill.Description,
					Source:      "skill",
					SourceInfo:  skill.SourceInfo,
				})
			}

			return success(id, "get_commands", rpcCommandsData{Commands: commands}), true

		default:
			return failure(id, cmd.Type, "Unknown command: "+cmd.Type), true
		}
	}

	handleInputLine := func(line string) {
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(line), &envelope); err != nil {
				output(failure("", "parse", "Failed to parse command: "+err.Error()))
				ioState.waitBackpressure()
				return
			}

			// Extension UI responses have no consumer in pig (extensions are
			// not ported); recognize and discard them.
			if envelope.Type == "extension_ui_response" {
				return
			}

			var cmd RpcCommand
			if err := json.Unmarshal([]byte(line), &cmd); err != nil {
				output(failure("", "parse", "Failed to parse command: "+err.Error()))
				ioState.waitBackpressure()
				return
			}

			// pi wraps handleCommand in try/catch and reports a thrown error
			// with the command id/type; recover is the Go equivalent
			// (panic-resilience: never let a handler panic kill the process).
			response, hasResponse := func() (response RpcResponse, hasResponse bool) {
				defer func() {
					if recovered := recover(); recovered != nil {
						output(failure(cmd.ID, cmd.Type, fmt.Sprint(recovered)))
						ioState.waitBackpressure()
						response, hasResponse = RpcResponse{}, false
					}
				}()
				return handleCommand(cmd)
			}()
			if hasResponse {
				output(response)
				ioState.waitBackpressure()
			}
			checkShutdownRequested()
		}()
	}

	detach, readerDone := AttachJsonlLineReader(ioState.input, handleInputLine)
	detachInput = detach

	// pi registers an "end" handler on stdin and shuts down; in Go the line
	// reader closes readerDone at EOF, which is the same edge.
	go func() {
		<-readerDone
		// Let in-flight handlers emit their immediate responses before
		// exiting (bounded so a streaming prompt/bash cannot stall exit).
		drained := make(chan struct{})
		go func() {
			handlers.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(time.Second):
		}
		shutdown(0, true)
	}()

	// Keep running until stdin ends or a signal arrives.
	return <-done
}

// bashOptionsForRpc builds the ExecuteBash options, forwarding extension
// operations when the extension runner supplied them (pi: executeBash's
// `operations: eventResult?.operations`). The runner always returns nil
// without extensions, so this is structural fidelity only.
func bashOptionsForRpc(id string, excludeFromContext bool, eventResult *core.UserBashEventResult) *core.BashExecuteOptions {
	options := &core.BashExecuteOptions{ExcludeFromContext: excludeFromContext, ID: id}
	if eventResult != nil && eventResult.Operations != nil {
		options.Operations = *eventResult.Operations
		options.HasOperations = true
	}
	return options
}
