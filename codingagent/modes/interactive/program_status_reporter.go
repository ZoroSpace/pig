// program-status-reporter (pi:
// modes/interactive/program-status-reporter.ts, S17 503c60552) — reports
// interactive-mode state to the terminal (OSC 7501): `working` during agent
// runs and compaction, `blocked` while a dialog waits for the user, then
// `done`, `error`, or `idle` once the run settles. Messages are limited to
// the session name, dialog titles, and the first line of errors; prompts and
// assistant output are never reported.
package interactive

import (
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

// blockedStatus is one blocked dialog report (pi: BlockedStatus).
type blockedStatus struct {
	kind    tui.ProgramStatusBlockedKind
	message string
}

// firstLineOf returns the first line of an error message (pi: firstLine);
// an empty message reports "Error".
func firstLineOf(text string) string {
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "Error"
	}
	return text
}

// ProgramStatusReporter tracks interactive-mode state and reports it to the
// terminal (pi: ProgramStatusReporter).
type ProgramStatusReporter struct {
	getTerminal    func() tui.Terminal
	getSessionName func() string

	runActive  bool
	compacting bool
	// runResult is the outcome of the current run, reported once it settles.
	runResult tui.ProgramStatus
	// restingStatus is the status while no run is active.
	restingStatus tui.ProgramStatus
	// blocked holds open dialogs by source; lastAdded preserves the Map's
	// insertion order so the most recently opened one is reported (pi: Map
	// iteration order).
	blocked    map[string]blockedStatus
	lastAdded  []string
	lastReport string
}

// NewProgramStatusReporter builds the reporter (pi: constructor).
func NewProgramStatusReporter(getTerminal func() tui.Terminal, getSessionName func() string) *ProgramStatusReporter {
	return &ProgramStatusReporter{
		getTerminal:    getTerminal,
		getSessionName: getSessionName,
		runResult:      tui.ProgramStatus{State: tui.ProgramStatusDone},
		restingStatus:  tui.ProgramStatus{State: tui.ProgramStatusIdle},
		blocked:        map[string]blockedStatus{},
	}
}

// HandleEvent updates the state from a session event and reports (pi:
// handleEvent).
func (r *ProgramStatusReporter) HandleEvent(event *core.AgentSessionEvent) {
	switch event.Type {
	case agent.AgentEventAgentStart:
		r.runActive = true
		r.runResult = tui.ProgramStatus{State: tui.ProgramStatusDone}
	case agent.AgentEventMessageEnd:
		// The latest response decides the outcome, so a retried error is
		// replaced by its successful retry. pi filters on role ===
		// "assistant"; non-assistant messages normalize to nil here.
		assistant := asAssistantMessage(event.Message)
		if assistant == nil {
			return
		}
		if assistant.StopReason == types.StopReasonError {
			r.runResult = tui.ProgramStatus{State: tui.ProgramStatusError, Message: firstLineOf(assistant.ErrorMessage)}
		} else {
			r.runResult = tui.ProgramStatus{State: tui.ProgramStatusDone}
		}
	case core.AgentSessionEventCompactionStart:
		r.compacting = true
	case core.AgentSessionEventCompactionEnd:
		r.compacting = false
		if r.runActive {
			// A failed recovery compaction ends the run unless a later
			// response succeeds.
			if event.Aborted {
				r.runResult = tui.ProgramStatus{State: tui.ProgramStatusIdle}
			} else if event.ErrorMessage != "" {
				r.runResult = tui.ProgramStatus{State: tui.ProgramStatusError, Message: firstLineOf(event.ErrorMessage)}
			}
		} else if event.Aborted {
			r.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusIdle}
		} else if event.Reason == "manual" {
			if event.ErrorMessage != "" {
				r.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusError, Message: firstLineOf(event.ErrorMessage)}
			} else {
				r.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusDone}
			}
		}
	case core.AgentSessionEventAgentSettled:
		r.runActive = false
		if event.Aborted {
			r.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusIdle}
		} else {
			r.restingStatus = r.runResult
		}
	case core.AgentSessionEventSessionInfoChanged:
		// The session name is part of working and done reports.
	default:
		return
	}
	r.Report()
}

// SetBlocked reports `blocked` for a dialog until it is cleared with nil.
// Reopening a source replaces it (pi: setBlocked).
func (r *ProgramStatusReporter) SetBlocked(source string, status *blockedStatus) {
	delete(r.blocked, source)
	kept := make([]string, 0, len(r.lastAdded))
	for _, name := range r.lastAdded {
		if name != source {
			kept = append(kept, name)
		}
	}
	r.lastAdded = kept
	if status != nil {
		r.blocked[source] = *status
		r.lastAdded = append(r.lastAdded, source)
	}
	r.Report()
}

// Reset forgets the previous session's run, for example after switching
// sessions (pi: reset).
func (r *ProgramStatusReporter) Reset() {
	r.runActive = false
	r.compacting = false
	r.runResult = tui.ProgramStatus{State: tui.ProgramStatusDone}
	r.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusIdle}
	r.Report()
}

// Report sends the current status when it changed (pi: report).
func (r *ProgramStatusReporter) Report() {
	status := r.currentStatus()
	status.App = codingagent.AppName
	key := reportKey(status)
	if key == r.lastReport {
		return
	}
	r.lastReport = key
	r.getTerminal().SetProgramStatus(status)
}

// currentStatus resolves the status to report (pi: currentStatus).
func (r *ProgramStatusReporter) currentStatus() tui.ProgramStatus {
	// The most recently opened dialog wins (pi:
	// [...this.blocked.values()].at(-1)).
	if len(r.lastAdded) > 0 {
		if blocked, ok := r.blocked[r.lastAdded[len(r.lastAdded)-1]]; ok {
			return tui.ProgramStatus{State: tui.ProgramStatusBlocked, Kind: blocked.kind, Message: blocked.message}
		}
	}
	if r.compacting {
		return tui.ProgramStatus{State: tui.ProgramStatusWorking, Message: "Compacting context"}
	}
	status := r.restingStatus
	if r.runActive {
		status = tui.ProgramStatus{State: tui.ProgramStatusWorking}
	}
	if status.State == tui.ProgramStatusWorking || status.State == tui.ProgramStatusDone {
		status.Message = r.getSessionName()
	}
	return status
}

// reportKey is the change-detection key (pi: JSON.stringify(status)).
func reportKey(status tui.ProgramStatus) string {
	return string(status.State) + "\x00" + status.App + "\x00" + string(status.Kind) + "\x00" + status.Message
}
