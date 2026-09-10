// pi: src/core/tools/bash.ts — the coding-agent shell tool (shared by bash
// and powershell via createShellToolDefinition).
//
// Segment deviations:
//   - The render functions and BashResultRenderComponent depend on
//     Theme/pi-tui and arrive with the theme batch.
//   - signal wiring → context.Context; the abort-listener pattern collapses
//     to ctx cancellation, and the aborted/timeout error protocol keeps the
//     exact TS message strings ("aborted", "timeout:<seconds>") since the
//     execute body branches on them.
//   - resolveSpawnContext reads the session identity from the
//     ToolExecuteContext slice (pi: ctx.sessionManager).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/agent"
	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

const maxTimeoutMs = 2_147_483_647

// resolveTimeoutMs validates the timeout in seconds and converts to millis
// (pi: resolveTimeoutMs; throws).
func resolveTimeoutMs(timeout *float64) (float64, error) {
	if timeout == nil {
		return 0, nil
	}
	t := *timeout
	if t != t || t <= 0 || t > 1.7976931348623157e308 {
		return 0, fmt.Errorf("Invalid timeout: must be a finite number of seconds")
	}
	timeoutMs := t * 1000
	if timeoutMs > maxTimeoutMs {
		return 0, fmt.Errorf("Invalid timeout: maximum is %v seconds", maxTimeoutMs/1000)
	}
	return timeoutMs, nil
}

// bashSchemaJSON is the shell tool's parameter schema (pi: bashSchema).
const bashSchemaJSON = `{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "Shell command to execute"
    },
    "timeout": {
      "type": "number",
      "description": "Timeout in seconds (optional, no default timeout)"
    }
  },
  "required": ["command"]
}`

// BashToolSystemPromptContribution is the bash tool's system prompt metadata
// (pi: bashToolSystemPromptContribution).
var BashToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Execute bash commands (ls, grep, find, etc.)",
	Guidelines: []string{"You can inspect PI_* environment variables for current model and session details."},
}

// BashToolInput is the shell tool's parameter shape (pi: BashToolInput).
type BashToolInput struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitempty"`
}

// BashToolDetails carries the truncation state and spill path (pi:
// BashToolDetails).
type BashToolDetails struct {
	Truncation     *harnessutils.TruncationResult `json:"truncation,omitempty"`
	FullOutputPath string                         `json:"fullOutputPath,omitempty"`
}

// BashExecOptions carry the per-execution options of BashOperations.exec
// (pi: the options argument).
type BashExecOptions struct {
	// OnData receives raw output chunks from stdout and stderr (pi: onData
	// Buffer).
	OnData func(data []byte)
	// Timeout is the command timeout in seconds; nil = none (pi: timeout).
	Timeout *float64
	// Env is the child environment (pi: env NodeJS.ProcessEnv).
	Env []string
}

// BashOperations are pluggable operations for the shell tool; override these
// to delegate command execution to remote systems (pi: BashOperations).
type BashOperations struct {
	// Exec executes a command and streams output (pi: exec). exitCode is
	// nil-ptr when the process was killed by a signal (pi: null).
	Exec func(ctx context.Context, command string, cwd string, options *BashExecOptions) (exitCode *int, err error)
}

// CreateLocalShellOperations builds the shared process execution used by the
// built-in shell tools (pi: createLocalShellOperations; the shell cwd is
// captured for relative shell-path resolution).
func CreateLocalShellOperations(shellName string, cwd string, resolveShellConfig func() (codingagentutils.ShellConfig, error)) BashOperations {
	return BashOperations{
		Exec: func(ctx context.Context, command string, cwd string, options *BashExecOptions) (*int, error) {
			var timeout *float64
			var onData func([]byte)
			if options != nil {
				timeout = options.Timeout
				onData = options.OnData
			}
			if _, err := resolveTimeoutMs(timeout); err != nil {
				return nil, err
			}
			if ctx.Err() != nil {
				return nil, fmt.Errorf("aborted")
			}
			shellConfig, err := resolveShellConfig()
			if err != nil {
				return nil, err
			}
			if !runtimeCwdExists(cwd) {
				return nil, fmt.Errorf("Working directory does not exist: %s\nCannot execute %s commands.", cwd, shellName)
			}

			commandFromStdin := shellConfig.CommandTransport == "stdin"
			args := shellConfig.Args
			if !commandFromStdin {
				args = append(append([]string{}, shellConfig.Args...), command)
			}
			env := codingagentutils.GetShellEnv()
			if options != nil && options.Env != nil {
				env = options.Env
			}
			cmd, pipes, err := codingagentutils.SpawnProcess(shellConfig.Shell, args, cwd, env)
			if err != nil {
				return nil, err
			}

			// pi: child.stdin?.on("error", () => {}) — write errors
			// ignored. The stdin pipe is created before Start (os/exec
			// requirement) and written after it (pi's ordering).
			var stdinPipe io.WriteCloser
			if commandFromStdin {
				stdinPipe, err = cmd.StdinPipe()
				if err != nil {
					pipes.CloseWriteEnds()
					pipes.CloseReadEnds()
					return nil, err
				}
			}

			waiter, err := codingagentutils.StartChild(cmd, pipes)
			if err != nil {
				return nil, err
			}
			if commandFromStdin {
				go func() {
					_, _ = stdinPipe.Write([]byte(command))
					stdinPipe.Close()
				}()
			}
			codingagentutils.TrackDetachedChildPid(cmd.Process.Pid)
			defer codingagentutils.UntrackDetachedChildPid(cmd.Process.Pid)

			var timedOut atomicBool
			if timeout != nil {
				timeoutMs, _ := resolveTimeoutMs(timeout)
				timer := time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
					timedOut.Store(true)
					codingagentutils.KillProcessTree(cmd.Process.Pid)
				})
				defer timer.Stop()
			}

			// pi: abort listener kills the tree; ctx cancellation does the
			// same.
			abortDone := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					codingagentutils.KillProcessTree(cmd.Process.Pid)
				case <-abortDone:
				}
			}()
			defer close(abortDone)

			var onDataMu sync.Mutex
			onDataOnce := func(chunk []byte) {
				if onData == nil {
					return
				}
				onDataMu.Lock()
				defer onDataMu.Unlock()
				onData(chunk)
			}
			if err := codingagentutils.AwaitChildWaiter(cmd, pipes, ctx, waiter, onDataOnce); err != nil {
				return nil, err
			}
			if ctx.Err() != nil {
				return nil, fmt.Errorf("aborted")
			}
			if timedOut.Load() {
				timeoutText := ""
				if timeout != nil {
					timeoutText = fmt.Sprintf("%v", *timeout)
				}
				return nil, fmt.Errorf("timeout:%s", timeoutText)
			}
			// pi: waitForChildProcess resolves null on signal kill → exitCode
			// nil. AwaitChild surfaces that through the ProcessState.
			if cmd.ProcessState != nil && !cmd.ProcessState.Exited() {
				return nil, nil
			}
			code := cmd.ProcessState.ExitCode()
			return &code, nil
		},
	}
}

// shellUpdateThrottle coalesces streamed output updates (pi: the closure
// state updateTimer/updateDirty/lastUpdateAt).
type shellUpdateThrottle struct {
	mu         sync.Mutex
	timer      *time.Timer
	dirtyFlag  bool
	lastUpdate time.Time
}

func newShellUpdateThrottle() *shellUpdateThrottle { return &shellUpdateThrottle{} }

func (t *shellUpdateThrottle) dirty() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dirtyFlag
}

func (t *shellUpdateThrottle) clearDirty() {
	t.mu.Lock()
	t.dirtyFlag = false
	t.lastUpdate = time.Now()
	t.mu.Unlock()
}

func (t *shellUpdateThrottle) clearTimer() {
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()
}

func (t *shellUpdateThrottle) schedule(emit func()) {
	t.mu.Lock()
	t.dirtyFlag = true
	delay := bashUpdateThrottleMs - time.Since(t.lastUpdate).Milliseconds()
	if delay <= 0 {
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		t.mu.Unlock()
		emit()
		return
	}
	if t.timer == nil {
		t.timer = time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
			t.mu.Lock()
			t.timer = nil
			t.mu.Unlock()
			emit()
		})
	}
	t.mu.Unlock()
}

// atomicBool is a tiny flag wrapper (pi: the closure's timedOut boolean is
// event-loop serial; pig's timer goroutine is not).
type atomicBool struct {
	mu    sync.Mutex
	value bool
}

func (b *atomicBool) Store(v bool) {
	b.mu.Lock()
	b.value = v
	b.mu.Unlock()
}

func (b *atomicBool) Load() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.value
}

// CreateLocalBashOperations builds bash operations using pi's built-in local
// shell execution backend (pi: createLocalBashOperations).
func CreateLocalBashOperations(cwd string, options *BashLocalOptions) BashOperations {
	shellPath := ""
	if options != nil {
		shellPath = options.ShellPath
	}
	return CreateLocalShellOperations("bash", cwd, func() (codingagentutils.ShellConfig, error) {
		return codingagentutils.GetShellConfig(cwd, shellPath)
	})
}

// BashLocalOptions parameterize CreateLocalBashOperations (pi: the options
// argument).
type BashLocalOptions struct {
	// ShellPath is an optional explicit shell path.
	ShellPath string
}

// BashSpawnContext is the resolved spawn description (pi: BashSpawnContext).
type BashSpawnContext struct {
	Command string
	Cwd     string
	Env     []string
}

// BashSpawnHook adjusts command, cwd, or env before execution (pi:
// BashSpawnHook).
type BashSpawnHook func(context BashSpawnContext) BashSpawnContext

// resolveSpawnContext builds the spawn env, stripping the PI_* session vars
// and re-exposing them when requested (pi: resolveSpawnContext).
func resolveSpawnContext(command string, cwd string, spawnHook BashSpawnHook, exposeSessionEnvironment bool, ctx ToolExecuteContext) BashSpawnContext {
	env := envMapFromSlice(codingagentutils.GetShellEnv())
	delete(env, "PI_SESSION_ID")
	delete(env, "PI_SESSION_FILE")
	delete(env, "PI_PROVIDER")
	delete(env, "PI_MODEL")
	delete(env, "PI_REASONING_LEVEL")
	if exposeSessionEnvironment && ctx != nil {
		model := ctx.GetModel()
		env["PI_SESSION_ID"] = ctx.GetSessionID()
		if sessionFile := ctx.GetSessionFile(); sessionFile != nil {
			env["PI_SESSION_FILE"] = *sessionFile
		}
		if model != nil {
			env["PI_PROVIDER"] = string(model.Provider)
			env["PI_MODEL"] = model.ID
		}
		if thinkingLevel := ctx.GetThinkingLevel(); thinkingLevel != "" {
			env["PI_REASONING_LEVEL"] = thinkingLevel
		}
	}
	baseContext := BashSpawnContext{Command: command, Cwd: cwd, Env: envSliceFromMap(env)}
	if spawnHook != nil {
		return spawnHook(baseContext)
	}
	return baseContext
}

// envMapFromSlice/sliceFromMap convert between env representations.
func envMapFromSlice(env []string) map[string]string {
	m := map[string]string{}
	for _, entry := range env {
		if idx := strings.Index(entry, "="); idx >= 0 {
			m[entry[:idx]] = entry[idx+1:]
		}
	}
	return m
}

func envSliceFromMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for key, value := range m {
		out = append(out, key+"="+value)
	}
	return out
}

// BashToolOptions parameterize the shell tools (pi: BashToolOptions).
type BashToolOptions struct {
	// Operations customizes command execution. Default: local shell.
	Operations *BashOperations
	// CommandPrefix is prepended to every command (for example shell setup
	// commands).
	CommandPrefix string
	// ShellPath is an optional explicit shell path from settings.
	ShellPath string
	// ExposeSessionEnvironment exposes current Pi session metadata as PI_*
	// environment variables. Default: true.
	ExposeSessionEnvironment *bool
	// SpawnHook adjusts command, cwd, or env before execution.
	SpawnHook BashSpawnHook
}

const (
	bashPreviewLines     = 5
	bashUpdateThrottleMs = 100
)

// ShellToolConfig names the shell tool variant (pi: ShellToolConfig).
type ShellToolConfig struct {
	Name             string
	Label            string
	ShellName        string
	Prompt           string
	PromptSnippet    string
	PromptGuidelines []string
	TempFilePrefix   string
}

// formatDuration renders seconds with one decimal (pi: formatDuration).
func formatDuration(ms float64) string {
	return fmt.Sprintf("%.1fs", ms/1000)
}

// CreateShellToolDefinition builds the shared shell tool definition (pi:
// createShellToolDefinition; renderCall/renderResult arrive with the theme
// ============================================================================
// Render segment (pi: bash.ts render declarations)
// ============================================================================

// BashRenderState is the call-row renderer state (pi: BashRenderState;
// timestamps are ms, 0 = undefined; interval is the 1s elapsed ticker pi
// creates with setInterval).
type BashRenderState struct {
	StartedAt int64
	EndedAt   int64
	Interval  *time.Ticker
}

// BashResultRenderState is the result-row cache state (pi:
// BashResultRenderState).
type BashResultRenderState struct {
	CachedWidth   int
	CachedLines   []string
	CachedSkipped int
	// Cached is true once CachedLines holds a render for CachedWidth (pi:
	// undefined checks).
	Cached bool
}

// BashResultRenderComponent is the result render component (pi: class
// BashResultRenderComponent extends Container).
type BashResultRenderComponent struct {
	tui.Container
	state BashResultRenderState
}

// bashPreviewComponent is the inline collapsed-preview renderer (pi: the
// object literal { render, invalidate } with width-aware caching).
type bashPreviewComponent struct {
	theme         UITheme
	styledOutput  string
	cachedWidth   int
	cachedLines   []string
	cachedSkipped int
	cached        bool
	state         *BashResultRenderState
}

func (b *bashPreviewComponent) Render(width int) []string {
	if !b.cached || b.cachedWidth != width {
		preview := TruncateToVisualLines(b.styledOutput, bashPreviewLines, width, 0)
		b.cachedLines = preview.VisualLines
		b.cachedSkipped = preview.SkippedCount
		b.cachedWidth = width
		b.cached = true
		b.state.CachedWidth = width
		b.state.CachedLines = preview.VisualLines
		b.state.CachedSkipped = preview.SkippedCount
		b.state.Cached = true
	}
	if b.state.CachedSkipped > 0 {
		hint := b.theme.Fg("muted", fmt.Sprintf("... (%d earlier lines,", b.state.CachedSkipped)) +
			" " + KeyHint("app.tools.expand", "to expand") + b.theme.Fg("muted", ")")
		lines := []string{"", tui.TruncateToWidth(hint, width, "...", false)}
		return append(lines, b.state.CachedLines...)
	}
	return append([]string{""}, b.state.CachedLines...)
}

func (b *bashPreviewComponent) Invalidate() {
	b.cached = false
	b.state.Cached = false
	b.state.CachedLines = nil
	b.state.CachedSkipped = 0
}

// formatShellCall renders the call row (pi: formatShellCall; theme comes
// from the renderer argument instead of the global).
func formatShellCall(theme UITheme, args map[string]any, prompt string) string {
	command, commandOk := Str(args["command"])
	timeout, _ := args["timeout"].(float64)
	timeoutSuffix := ""
	if timeout != 0 {
		timeoutSuffix = theme.Fg("muted", fmt.Sprintf(" (timeout %gs)", timeout))
	}
	commandDisplay := theme.Fg("toolOutput", "...")
	if !commandOk {
		commandDisplay = InvalidArgText(theme)
	} else if command != "" {
		commandDisplay = command
	}
	return theme.Fg("toolTitle", theme.Bold(prompt+" "+commandDisplay)) + timeoutSuffix
}

// rebuildBashResultRenderComponent repopulates the result component (pi:
// rebuildBashResultRenderComponent).
func rebuildBashResultRenderComponent(
	theme UITheme,
	component *BashResultRenderComponent,
	result ToolRenderResult,
	options ToolRenderResultOptions,
	showImages bool,
	startedAt int64,
	endedAt int64,
) {
	state := &component.state
	component.Clear()

	output := strings.Trim(GetTextOutput(&result, showImages), " \t\n\r")
	var details *BashToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*BashToolDetails)
	}
	var truncation *harnessutils.TruncationResult
	fullOutputPath := ""
	if details != nil {
		truncation = details.Truncation
		fullOutputPath = details.FullOutputPath
	}
	if !options.IsPartial && truncation != nil && truncation.Truncated && fullOutputPath != "" && strings.HasSuffix(output, "]") {
		footerStart := strings.LastIndex(output, "\n\n[")
		if footerStart != -1 && strings.Contains(output[footerStart:], fullOutputPath) {
			output = strings.TrimRight(output[:footerStart], " \t\n\r")
		}
	}

	if output != "" {
		styledLines := strings.Split(output, "\n")
		for j, line := range styledLines {
			styledLines[j] = theme.Fg("toolOutput", line)
		}
		styledOutput := strings.Join(styledLines, "\n")

		if options.Expanded {
			component.AddChild(tui.NewText("\n"+styledOutput, 0, 0))
		} else {
			component.AddChild(&bashPreviewComponent{theme: theme, styledOutput: styledOutput, state: state})
		}
	}

	if (truncation != nil && truncation.Truncated) || fullOutputPath != "" {
		var warnings []string
		if fullOutputPath != "" {
			warnings = append(warnings, "Full output: "+fullOutputPath)
		}
		if truncation != nil && truncation.Truncated {
			if truncation.TruncatedBy == "lines" {
				warnings = append(warnings, fmt.Sprintf("Truncated: showing %d of %d lines", truncation.OutputLines, truncation.TotalLines))
			} else {
				maxBytes := int64(harnessutils.DefaultMaxBytes)
				if truncation.MaxBytes != 0 {
					maxBytes = int64(truncation.MaxBytes)
				}
				warnings = append(warnings, fmt.Sprintf("Truncated: %d lines shown (%s limit)", truncation.OutputLines, harnessutils.FormatSize(maxBytes)))
			}
		}
		component.AddChild(tui.NewText("\n"+theme.Fg("warning", "["+strings.Join(warnings, ". ")+"]"), 0, 0))
	}

	if startedAt != 0 {
		label := "Took"
		if options.IsPartial {
			label = "Elapsed"
		}
		endTime := endedAt
		if endTime == 0 {
			endTime = time.Now().UnixMilli()
		}
		component.AddChild(tui.NewText("\n"+theme.Fg("muted", fmt.Sprintf("%s %s", label, formatDuration(float64(endTime-startedAt)))), 0, 0))
	}
}

// batch).
func CreateShellToolDefinition(cwd string, config ShellToolConfig, options *BashToolOptions) *ToolDefinition {
	localOps := CreateLocalBashOperations(cwd, &BashLocalOptions{ShellPath: optionsShellPath(options)})
	ops := localOps
	if options != nil && options.Operations != nil {
		ops = *options.Operations
	}
	commandPrefix := ""
	if options != nil {
		commandPrefix = options.CommandPrefix
	}
	exposeSessionEnvironment := options == nil || options.ExposeSessionEnvironment == nil || *options.ExposeSessionEnvironment
	var spawnHook BashSpawnHook
	if options != nil {
		spawnHook = options.SpawnHook
	}
	return &ToolDefinition{
		Name:             config.Name,
		Label:            config.Label,
		Description:      fmt.Sprintf("Execute a %s command in the current working directory. Returns stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.", config.ShellName, harnessutils.DefaultMaxLines, harnessutils.DefaultMaxBytes/1024),
		PromptSnippet:    config.PromptSnippet,
		PromptGuidelines: promptGuidelinesCopy(exposeSessionEnvironment, config.PromptGuidelines),
		Parameters:       json.RawMessage(bashSchemaJSON),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeToolParams[BashToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			resolvedCommand := input.Command
			if commandPrefix != "" {
				resolvedCommand = commandPrefix + "\n" + input.Command
			}
			spawnContext := resolveSpawnContext(resolvedCommand, cwd, spawnHook, exposeSessionEnvironment, toolCtx)
			output := NewOutputAccumulator(&OutputAccumulatorOptions{TempFilePrefix: config.TempFilePrefix})
			var acceptingOutput atomicBool
			acceptingOutput.Store(true)

			throttle := newShellUpdateThrottle()

			emitOutputUpdate := func() {
				if onUpdate == nil || !throttle.dirty() {
					return
				}
				throttle.clearDirty()
				snapshot := output.Snapshot(true)
				onUpdate(agent.AgentToolResult{
					Content: []types.Content{asTextContent(snapshot.Content)},
					Details: &BashToolDetails{
						Truncation:     truncationOrNil(snapshot),
						FullOutputPath: snapshot.FullOutputPath,
					},
				})
			}

			if onUpdate != nil {
				onUpdate(agent.AgentToolResult{Content: []types.Content{}})
			}

			handleData := func(data []byte) {
				if !acceptingOutput.Load() {
					return
				}
				_ = output.Append(data)
				throttle.schedule(emitOutputUpdate)
			}

			finishOutput := func() (OutputSnapshot, error) {
				acceptingOutput.Store(false)
				output.Finish()
				throttle.clearTimer()
				emitOutputUpdate()
				snapshot := output.Snapshot(true)
				if err := output.CloseTempFile(); err != nil {
					return snapshot, err
				}
				return snapshot, nil
			}

			formatOutput := func(snapshot OutputSnapshot, emptyText string) (string, *BashToolDetails) {
				truncation := snapshot.Truncation
				text := snapshot.Content
				if text == "" {
					text = emptyText
				}
				var details *BashToolDetails
				if truncation.Truncated {
					details = &BashToolDetails{Truncation: &truncation, FullOutputPath: snapshot.FullOutputPath}
					startLine := truncation.TotalLines - truncation.OutputLines + 1
					endLine := truncation.TotalLines
					if truncation.LastLinePartial {
						lastLineSize := harnessutils.FormatSize(int64(output.GetLastLineBytes()))
						text += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
							harnessutils.FormatSize(int64(truncation.OutputBytes)), endLine, lastLineSize, snapshot.FullOutputPath)
					} else if truncation.TruncatedBy == harnessutils.TruncatedByLines {
						text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
							startLine, endLine, truncation.TotalLines, snapshot.FullOutputPath)
					} else {
						text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
							startLine, endLine, truncation.TotalLines, harnessutils.FormatSize(harnessutils.DefaultMaxBytes), snapshot.FullOutputPath)
					}
				}
				return text, details
			}

			appendStatus := func(text string, status string) string {
				if text != "" {
					return text + "\n\n" + status
				}
				return status
			}

			var exitCode *int
			result, err := func() (agent.AgentToolResult, error) {
				var execErr error
				execErr = func() error {
					var err error
					exitCode, err = ops.Exec(ctx, spawnContext.Command, spawnContext.Cwd, &BashExecOptions{
						OnData:  handleData,
						Timeout: input.Timeout,
						Env:     spawnContext.Env,
					})
					return err
				}()
				if execErr != nil {
					snapshot, finishErr := finishOutput()
					if finishErr != nil {
						return agent.AgentToolResult{}, finishErr
					}
					text, _ := formatOutput(snapshot, "")
					if execErr.Error() == "aborted" {
						return agent.AgentToolResult{}, fmt.Errorf("%s", appendStatus(text, "Command aborted"))
					}
					if strings.HasPrefix(execErr.Error(), "timeout:") {
						timeoutSecs := strings.SplitN(execErr.Error(), ":", 2)[1]
						return agent.AgentToolResult{}, fmt.Errorf("%s", appendStatus(text, fmt.Sprintf("Command timed out after %s seconds", timeoutSecs)))
					}
					return agent.AgentToolResult{}, execErr
				}

				snapshot, finishErr := finishOutput()
				if finishErr != nil {
					return agent.AgentToolResult{}, finishErr
				}
				outputText, details := formatOutput(snapshot, "(no output)")
				if exitCode != nil && *exitCode != 0 {
					return agent.AgentToolResult{}, fmt.Errorf("%s", appendStatus(outputText, fmt.Sprintf("Command exited with code %d", *exitCode)))
				}
				var detailsValue any
				if details != nil {
					detailsValue = details
				}
				return agent.AgentToolResult{
					Content: []types.Content{asTextContent(outputText)},
					Details: detailsValue,
				}, nil
			}()
			throttle.clearTimer()
			return result, err
		},
		NewRenderState: func() any { return &BashRenderState{} },
		RenderCall: func(args any, theme UITheme, context ToolRenderContext) tui.Component {
			state := context.State.(*BashRenderState)
			if context.ExecutionStarted && state.StartedAt == 0 {
				state.StartedAt = time.Now().UnixMilli()
				state.EndedAt = 0
			}
			text, ok := context.LastComponent.(*tui.Text)
			if !ok {
				text = tui.NewText("", 0, 0)
			}
			text.SetText(formatShellCall(theme, argsToMap(args), config.Prompt))
			return text
		},
		RenderResult: func(result ToolRenderResult, options ToolRenderResultOptions, theme UITheme, context ToolRenderContext) tui.Component {
			state := context.State.(*BashRenderState)
			if state.StartedAt != 0 && options.IsPartial && state.Interval == nil {
				state.Interval = startElapsedTicker(context.Invalidate)
			}
			if !options.IsPartial || context.IsError {
				if state.EndedAt == 0 {
					state.EndedAt = time.Now().UnixMilli()
				}
				if state.Interval != nil {
					state.Interval.Stop()
					state.Interval = nil
				}
			}
			var component *BashResultRenderComponent
			if c, ok := context.LastComponent.(*BashResultRenderComponent); ok {
				component = c
			} else {
				component = &BashResultRenderComponent{}
			}
			rebuildBashResultRenderComponent(theme, component, result, options, context.ShowImages, state.StartedAt, state.EndedAt)
			component.Invalidate()
			return component
		},
	}
}

// argsToMap normalizes raw call arguments to the map shape the renderers
// consume (pi: args is the raw streamed object; pig carries whatever the
// caller decoded — JSON objects arrive as map[string]any).
func argsToMap(args any) map[string]any {
	if m, ok := args.(map[string]any); ok {
		return m
	}
	return nil
}

// startElapsedTicker drives the 1s elapsed-time refresh while a bash result
// is partial (pi: setInterval(() => context.invalidate(), 1000)).
func startElapsedTicker(invalidate func()) *time.Ticker {
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			invalidate()
		}
	}()
	return ticker
}

// optionsShellPath extracts the shell path (pi: options?.shellPath).
func optionsShellPath(options *BashToolOptions) string {
	if options == nil {
		return ""
	}
	return options.ShellPath
}

// promptGuidelinesCopy copies the guidelines when exposing session env (pi:
// exposeSessionEnvironment && config.promptGuidelines ? [...] : undefined).
func promptGuidelinesCopy(expose bool, guidelines []string) []string {
	if !expose || guidelines == nil {
		return nil
	}
	return append([]string{}, guidelines...)
}

// bashToolConfig is the bash variant (pi: bashToolConfig).
var bashToolConfig = ShellToolConfig{
	Name:             "bash",
	Label:            "bash",
	ShellName:        "bash",
	Prompt:           "$",
	PromptSnippet:    BashToolSystemPromptContribution.Snippet,
	PromptGuidelines: BashToolSystemPromptContribution.Guidelines,
	TempFilePrefix:   "pi-bash",
}

// CreateBashToolDefinition builds the bash definition (pi:
// createBashToolDefinition).
func CreateBashToolDefinition(cwd string, options *BashToolOptions) *ToolDefinition {
	return CreateShellToolDefinition(cwd, bashToolConfig, options)
}

// CreateBashTool wraps the bash definition into an AgentTool (pi:
// createBashTool).
func CreateBashTool(cwd string, options *BashToolOptions) Tool {
	return WrapToolDefinition(CreateBashToolDefinition(cwd, options), nil)
}

// runtimeCwdExists checks the working directory is accessible (pi: fsAccess
// F_OK).
func runtimeCwdExists(cwd string) bool {
	_, err := os.Stat(cwd)
	return err == nil
}

// truncationOrNil erases the truncation payload when not truncated (pi:
// snapshot.truncation.truncated ? snapshot.truncation : undefined).
func truncationOrNil(snapshot OutputSnapshot) *harnessutils.TruncationResult {
	if snapshot.Truncation.Truncated {
		t := snapshot.Truncation
		return &t
	}
	return nil
}
