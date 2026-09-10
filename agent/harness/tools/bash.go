// pi: src/harness/tools/bash.ts — the bash tool.
//
// Segment deviations:
//   - The update throttle uses time.AfterFunc guarded by a mutex; TS relies
//     on the event loop with setTimeout. Emission ordering and coalescing
//     semantics match: an immediate emit clears any pending timer, and a
//     scheduled timer fires at most once until re-armed by dirtiness.
//   - prepare receives ctx (pi: signal third parameter).
//   - `Command timed out after ${timeout} seconds` formats the timeout via
//     the shortest round-trip float form (JS template literal).
//   - capture.cancelled/executionError handling uses the typed
//     ShellCaptureResult fields (pi: property access).
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
)

// bashUpdateThrottleMs is the minimum interval between streamed output
// updates (pi: BASH_UPDATE_THROTTLE_MS).
const bashUpdateThrottleMs = 100

// bashSchemaJSON is the bash tool's parameter schema (pi: bashSchema via
// TypeBox).
const bashSchemaJSON = `{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "Bash command to execute"
    },
    "timeout": {
      "type": "number",
      "description": "Timeout in seconds (optional, no default timeout)"
    }
  },
  "required": ["command"]
}`

// BashToolInput is the bash tool's parameter shape (pi: BashToolInput).
type BashToolInput struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitempty"`
}

// BashToolDetails carries the truncation state and the full-output spill
// path (pi: BashToolDetails).
type BashToolDetails struct {
	Truncation     *utils.TruncationResult `json:"truncation,omitempty"`
	FullOutputPath string                  `json:"fullOutputPath,omitempty"`
}

// BashExecution is the shell execution description handed to prepare (pi:
// BashExecution).
type BashExecution struct {
	Command    string
	Cwd        string
	Env        map[string]string
	InheritEnv bool
}

// BashPrepare mutates the execution before it runs (pi: BashPrepare).
type BashPrepare func(ctx context.Context, execution *BashExecution, context any)

// BashToolOptions parameterize the bash tool (pi: BashToolOptions).
type BashToolOptions struct {
	// CommandPrefix is prepended to every command, followed by a newline.
	CommandPrefix string
	// Prepare is called with the resolved execution and turn context.
	Prepare BashPrepare
}

// validateTimeout checks the optional timeout (pi: validateTimeout).
func validateTimeout(timeout *float64) error {
	if timeout == nil {
		return nil
	}
	t := *timeout
	if t != t || isInf(t) || t <= 0 {
		return errors.New("Invalid timeout: must be a finite number of seconds")
	}
	const maxTimeoutSeconds = 2147483647.0 / 1000
	if t > maxTimeoutSeconds {
		return fmt.Errorf("Invalid timeout: maximum is %v seconds", maxTimeoutSeconds)
	}
	return nil
}

func isInf(f float64) bool {
	return f > 1.7976931348623157e308 || f < -1.7976931348623157e308
}

// bashUpdateThrottle coalesces streamed updates (pi: the closure state:
// getLatestProgress/updateTimer/updateDirty/lastUpdateAt).
type bashUpdateThrottle struct {
	mu          sync.Mutex
	onUpdate    agent.AgentToolUpdateCallback
	latest      func() utils.ShellCaptureProgress
	dirty       bool
	lastUpdate  time.Time
	timer       *time.Timer
	timeCleared bool
}

// emitOutputUpdate sends the current progress snapshot if dirty (pi:
// emitOutputUpdate).
func (t *bashUpdateThrottle) emitOutputUpdate() {
	t.mu.Lock()
	if t.onUpdate == nil || !t.dirty || t.latest == nil {
		t.mu.Unlock()
		return
	}
	t.dirty = false
	t.lastUpdate = time.Now()
	progress := t.latest()
	onUpdate := t.onUpdate
	t.mu.Unlock()
	onUpdate(agent.AgentToolResult{
		Content: []types.Content{
			&types.TextContent{Kind: types.KindText, Text: progress.Output},
		},
		Details: &BashToolDetails{
			Truncation:     truncationOrNil(progress),
			FullOutputPath: progress.FullOutputPath,
		},
	})
}

func truncationOrNil(progress utils.ShellCaptureProgress) *utils.TruncationResult {
	if progress.Truncation.Truncated {
		t := progress.Truncation
		return &t
	}
	return nil
}

// clearUpdateTimer cancels a pending timer (pi: clearUpdateTimer).
func (t *bashUpdateThrottle) clearUpdateTimer() {
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()
}

// scheduleOutputUpdate marks dirty and emits immediately or schedules (pi:
// scheduleOutputUpdate).
func (t *bashUpdateThrottle) scheduleOutputUpdate() {
	t.mu.Lock()
	if t.onUpdate == nil {
		t.mu.Unlock()
		return
	}
	t.dirty = true
	delay := bashUpdateThrottleMs - time.Since(t.lastUpdate).Milliseconds()
	if delay <= 0 {
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		t.mu.Unlock()
		t.emitOutputUpdate()
		return
	}
	if t.timer == nil {
		t.timer = time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
			t.mu.Lock()
			t.timer = nil
			t.mu.Unlock()
			t.emitOutputUpdate()
		})
	}
	t.mu.Unlock()
}

// causedError pairs a message with a cause (pi: new Error(msg, { cause })).
type causedError struct {
	msg   string
	cause error
}

func (e *causedError) Error() string { return e.msg }
func (e *causedError) Unwrap() error { return e.cause }

// CreateBashTool builds the bash tool (pi: createBashTool).
func CreateBashTool(options *BashToolOptions) *harness.AgentHarnessTool {
	return &harness.AgentHarnessTool{
		Name:  "bash",
		Label: "bash",
		Description: fmt.Sprintf("Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.",
			utils.DefaultMaxLines, utils.DefaultMaxBytes/1024),
		Parameters: json.RawMessage(bashSchemaJSON),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, context any) (agent.AgentToolResult, error) {
			input, err := decodeParams[BashToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			executionContext, err := executionContextOf(context)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if err := validateTimeout(input.Timeout); err != nil {
				return agent.AgentToolResult{}, err
			}
			env := executionContext.Env

			command := input.Command
			if options != nil && options.CommandPrefix != "" {
				command = options.CommandPrefix + "\n" + command
			}
			execution := BashExecution{
				Command:    command,
				Cwd:        env.Cwd(),
				Env:        map[string]string{},
				InheritEnv: true,
			}
			if options != nil && options.Prepare != nil {
				options.Prepare(ctx, &execution, context)
			}

			throttle := &bashUpdateThrottle{onUpdate: onUpdate}
			if onUpdate != nil {
				// pi: onUpdate?.({ content: [], details: undefined })
				onUpdate(agent.AgentToolResult{Content: []types.Content{}})
			}
			defer throttle.clearUpdateTimer()

			capture, err := utils.ExecuteShellWithCapture(ctx, env, execution.Command, &utils.ShellCaptureOptions{
				Cwd:                   execution.Cwd,
				Env:                   execution.Env,
				InheritEnv:            &execution.InheritEnv,
				Timeout:               input.Timeout,
				ReturnExecutionErrors: true,
				OnChunk: func(chunk string, getProgress func() utils.ShellCaptureProgress) {
					throttle.mu.Lock()
					throttle.latest = getProgress
					throttle.mu.Unlock()
					throttle.scheduleOutputUpdate()
				},
			})
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			throttle.clearUpdateTimer()
			throttle.mu.Lock()
			throttle.latest = func() utils.ShellCaptureProgress { return capture.ShellCaptureProgress }
			throttle.dirty = true
			throttle.mu.Unlock()
			throttle.emitOutputUpdate()

			outputText := capture.Output
			var details *BashToolDetails
			if capture.Truncation.Truncated {
				details = &BashToolDetails{Truncation: &capture.Truncation, FullOutputPath: capture.FullOutputPath}
				startLine := capture.Truncation.TotalLines - capture.Truncation.OutputLines + 1
				endLine := capture.Truncation.TotalLines
				if capture.Truncation.LastLinePartial {
					lastLineSize := utils.FormatSize(int64(capture.LastLineBytes))
					outputText += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
						utils.FormatSize(int64(capture.Truncation.OutputBytes)), endLine, lastLineSize, capture.FullOutputPath)
				} else if capture.Truncation.TruncatedBy == utils.TruncatedByLines {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
						startLine, endLine, capture.Truncation.TotalLines, capture.FullOutputPath)
				} else {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
						startLine, endLine, capture.Truncation.TotalLines, utils.FormatSize(utils.DefaultMaxBytes), capture.FullOutputPath)
				}
			}

			appendStatus := func(status string) string {
				if outputText != "" {
					return outputText + "\n\n" + status
				}
				return status
			}
			if capture.Cancelled {
				return agent.AgentToolResult{}, errors.New(appendStatus("Command aborted"))
			}
			if capture.ExecutionError != nil && capture.ExecutionError.Code == harness.ExecCodeTimeout {
				timeoutText := "undefined"
				if input.Timeout != nil {
					timeoutText = fmt.Sprintf("%v", *input.Timeout)
				}
				// pi: new Error(appendStatus(...), { cause: capture.executionError }).
				return agent.AgentToolResult{}, &causedError{
					msg:   appendStatus(fmt.Sprintf("Command timed out after %s seconds", timeoutText)),
					cause: capture.ExecutionError,
				}
			}
			if capture.ExecutionError != nil {
				return agent.AgentToolResult{}, capture.ExecutionError
			}
			if capture.ExitCode != nil && *capture.ExitCode != 0 {
				return agent.AgentToolResult{}, errors.New(appendStatus(fmt.Sprintf("Command exited with code %d", *capture.ExitCode)))
			}

			text := outputText
			if text == "" {
				text = "(no output)"
			}
			var detailsValue any
			if details != nil {
				detailsValue = details
			}
			return agent.AgentToolResult{
				Content: []types.Content{
					&types.TextContent{Kind: types.KindText, Text: text},
				},
				Details: detailsValue,
			}, nil
		},
	}
}
