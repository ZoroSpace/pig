// pi: modes/print-mode.ts — print mode (single-shot): send prompts, output
// result, exit. Used for `pig -p "prompt"` (text output) and
// `pig --mode json "prompt"` (JSON event stream).
//
// pig declares a PrintRuntimeHost interface instead of depending on the
// concrete core.AgentSessionRuntime (pi: runtimeHost: AgentSessionRuntime) —
// the method set used here is exactly pi's consumption surface, and the
// interface keeps the mode testable without the runtime constructor.
package modes

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// PrintRuntimeHost is the runtime host consumption surface (pi:
// AgentSessionRuntime's members used by runPrintMode).
type PrintRuntimeHost interface {
	Session() *core.AgentSession
	NewSession(ctx context.Context, options *core.RuntimeNewSessionOptions) (cancelled bool, err error)
	Fork(ctx context.Context, entryID string, options *core.ForkOptions) (*core.ForkResult, error)
	SwitchSession(ctx context.Context, sessionPath string, options *core.SwitchSessionOptions) (cancelled bool, err error)
	Dispose() error
	SetRebindSession(rebindSession func(session *core.AgentSession) error)
}

// PrintModeOptions are the print mode options (pi: PrintModeOptions).
type PrintModeOptions struct {
	// Mode is the output mode: "text" for final response only, "json" for
	// all events.
	Mode string
	// Messages are additional prompts to send after InitialMessage.
	Messages []string
	// InitialMessage is the first message to send (may contain @file
	// content); "" = none.
	InitialMessage string
	// InitialImages are images attached to the initial message.
	InitialImages []types.ImageContent
}

// RunPrintMode runs in print (single-shot) mode: sends prompts to the agent
// and outputs the result (pi: runPrintMode). Returns the process exit code.
func RunPrintMode(ctx context.Context, runtimeHost PrintRuntimeHost, options PrintModeOptions) int {
	mode := options.Mode
	messages := options.Messages
	exitCode := 0
	disposed := false
	var unsubscribe func()
	var unsubscribeBackpressure func()
	var signalCleanupHandlers []func()

	disposeRuntime := func() {
		if disposed {
			return
		}
		disposed = true
		if unsubscribe != nil {
			unsubscribe()
		}
		if unsubscribeBackpressure != nil {
			unsubscribeBackpressure()
		}
		_ = runtimeHost.Dispose()
	}

	// pi registers SIGTERM/SIGHUP handlers that kill detached children,
	// dispose the runtime, and exit; pig keeps the same behavior with
	// signal.Notify. Cleanup handlers remove them on normal completion.
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
				disposeRuntime()
				if sig == syscall.SIGHUP {
					os.Exit(129)
				}
				os.Exit(143)
			}(sig, signalCh)
		}
	}

	registerSignalHandlers()

	var rebindSession func() error
	rebindSession = func() error {
		session := runtimeHost.Session()

		extensionMode := core.ExtensionModePrint
		if mode == "json" {
			extensionMode = core.ExtensionModeJSON
		}
		bindErr := session.BindExtensions(ctx, &core.ExtensionBindings{
			Mode: extensionMode,
			CommandContextActions: &core.ExtensionCommandContextActions{
				WaitForIdle: func(ctx context.Context) error { return session.WaitForIdle(ctx) },
				NewSession: func(ctx context.Context, newSessionOptions *core.NewSessionCommandOptions) (bool, error) {
					return runtimeHost.NewSession(ctx, &core.RuntimeNewSessionOptions{
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
					result, err := runtimeHost.Fork(ctx, entryID, &core.ForkOptions{
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
					return runtimeHost.SwitchSession(ctx, sessionPath, &core.SwitchSessionOptions{
						WithSession: withSession,
					})
				},
				Reload: func(ctx context.Context) error {
					return session.Reload(ctx, nil)
				},
			},
			OnError: func(err core.ExtensionError) {
				fmt.Fprintf(os.Stderr, "Extension error (%s): %s\n", err.ExtensionPath, err.Error)
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
			if extensionMode == core.ExtensionModeJSON {
				jsonEvent, err := ToJsonEvent(*event)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					return
				}
				data, err := MarshalJsonEvent(jsonEvent)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					return
				}
				core.WriteRawStdout(string(data) + "\n")
			}
		})
		if extensionMode == core.ExtensionModeJSON {
			unsubscribeBackpressure = session.Agent.Subscribe(func(ctx context.Context, event agent.AgentEvent) {
				core.WaitForRawStdoutBackpressure()
			})
		}
		return nil
	}

	runtimeHost.SetRebindSession(func(session *core.AgentSession) error {
		return rebindSession()
	})

	func() {
		session := runtimeHost.Session()

		if mode == "json" {
			header := session.SessionManager.GetHeader()
			if header != nil {
				data, err := MarshalJsonEvent(header)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					exitCode = 1
					return
				}
				core.WriteRawStdout(string(data) + "\n")
			}
		}

		if err := rebindSession(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			exitCode = 1
			return
		}

		if options.InitialMessage != "" {
			if err := session.Prompt(ctx, options.InitialMessage, &core.PromptOptions{Images: options.InitialImages}); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				exitCode = 1
				return
			}
		}

		for _, message := range messages {
			if err := session.Prompt(ctx, message, nil); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				exitCode = 1
				return
			}
		}

		if mode == "text" {
			state := session.State()
			msgs := state.Messages
			if len(msgs) > 0 {
				lastMessage := msgs[len(msgs)-1]
				assistantMsg, ok := lastMessage.(*types.AssistantMessage)
				if !ok {
					if value, isValue := lastMessage.(types.AssistantMessage); isValue {
						assistantMsg = &value
						ok = true
					}
				}
				if ok {
					if assistantMsg.StopReason == types.StopReasonError || assistantMsg.StopReason == types.StopReasonAborted {
						reason := string(assistantMsg.StopReason)
						if assistantMsg.ErrorMessage != "" {
							fmt.Fprintf(os.Stderr, "%s\n", assistantMsg.ErrorMessage)
						} else {
							fmt.Fprintf(os.Stderr, "Request %s\n", reason)
						}
						exitCode = 1
					} else {
						for _, content := range assistantMsg.Content {
							switch text := content.(type) {
							case *types.TextContent:
								core.WriteRawStdout(text.Text + "\n")
							case types.TextContent:
								core.WriteRawStdout(text.Text + "\n")
							}
						}
					}
				}
			}
		}
	}()

	for _, cleanup := range signalCleanupHandlers {
		cleanup()
	}
	disposeRuntime()
	core.FlushRawStdout()
	return exitCode
}
