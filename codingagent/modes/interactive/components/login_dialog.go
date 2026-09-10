// login_dialog.go (pi: components/login-dialog.ts) — the login dialog that
// replaces the editor during OAuth/API-key login flows: shows auth URLs
// (OSC 8 hyperlinks), device codes, prompts, progress, and collects manual
// input.
//
// pig concurrency notes (deviations declared here once):
//   - pi's AbortController → context.Context+cancel (repo rule); the
//     showPrompt/showManualInput Promises resolve over buffered channels,
//     single-send guarded by a mutex.
//   - pi mutates containers on the single event loop; pig's login flow runs
//     on a separate goroutine, so the wiring injects Mutator
//     (InteractiveMode.mutateUI) and every Show* mutation is funneled
//     through it. The blocking wait happens outside the mutation.
package components

import (
	"context"
	"runtime"
	"sync"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// loginAbortController is pi's AbortController (the abort race collapses
// into ctx cancellation).
type loginAbortController struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newLoginAbortController() *loginAbortController {
	ctx, cancel := context.WithCancel(context.Background())
	return &loginAbortController{ctx: ctx, cancel: cancel}
}

// loginInputResult carries one pending ShowPrompt/ShowManualInput resolution
// (pi: the showPrompt/showManualInput Promise).
type loginInputResult struct {
	value string
	err   error
}

// LoginDialogComponent is the login dialog — replaces the editor during
// login flows (pi: LoginDialogComponent extends Container implements
// Focusable).
type LoginDialogComponent struct {
	tui.Container

	contentContainer *tui.Container
	input            *tui.Input
	tui              tui.TUI
	abortController  *loginAbortController

	// Mutator serializes container mutations with the render pipeline (pig
	// addition — see file header). Nil = mutate inline.
	Mutator func(fn func())

	pendingMu    sync.Mutex
	pendingInput chan loginInputResult
	onComplete   func(success bool, message string)

	// Focusable implementation - propagate to input for IME cursor
	// positioning (pi: _focused/focused getter+setter).
	focused bool
}

// NewLoginDialogComponent builds the dialog (pi: constructor). The name and
// title overrides are pi's optional providerNameOverride/titleOverride — ""
// means unset.
func NewLoginDialogComponent(
	tuiInstance tui.TUI,
	providerID string,
	onComplete func(success bool, message string),
	providerNameOverride string,
	titleOverride string,
) *LoginDialogComponent {
	d := &LoginDialogComponent{
		tui:             tuiInstance,
		abortController: newLoginAbortController(),
		onComplete:      onComplete,
	}

	providerName := providerNameOverride
	if providerName == "" {
		providerName = providerID
	}
	title := titleOverride
	if title == "" {
		title = "Login to " + providerName
	}

	// Top border
	d.AddChild(NewDynamicBorder(nil))

	// Title
	currentTheme := theme.Current()
	d.AddChild(tui.NewText(currentTheme.Fg("accent", currentTheme.Bold(title)), 1, 0))

	// Dynamic content area
	d.contentContainer = &tui.Container{}
	d.AddChild(d.contentContainer)

	// Input (always present, used when needed)
	d.input = tui.NewInput()
	d.input.OnSubmit = func(string) {
		value := d.input.GetValue()
		d.resolvePending(loginInputResult{value: value}, true)
	}
	d.input.OnEscape = func() {
		d.cancel()
	}

	// Bottom border
	d.AddChild(NewDynamicBorder(nil))
	return d
}

// Context returns the dialog's abort context (pi: get signal — the
// AbortSignal handed to the login flow; pig hands the context).
func (d *LoginDialogComponent) Context() context.Context {
	return d.abortController.ctx
}

// GetFocused implements Focusable.
func (d *LoginDialogComponent) GetFocused() bool { return d.focused }

// SetFocused implements Focusable.
func (d *LoginDialogComponent) SetFocused(value bool) {
	d.focused = value
	d.input.SetFocused(value)
}

func (d *LoginDialogComponent) mutate(fn func()) {
	if d.Mutator != nil {
		d.Mutator(fn)
		return
	}
	fn()
}

func (d *LoginDialogComponent) replaceInputWithSubmittedText(value string) {
	for i, child := range d.contentContainer.Children {
		if child == d.input {
			d.contentContainer.Children[i] = tui.NewText("> "+value, 0, 0)
		}
	}
}

// resolvePending delivers result to the pending prompt waiter exactly once
// (pi: inputResolver(value) / inputRejecter(error)). replace rewrites the
// submitted input into the transcript first (pi: replaceInputWithSubmittedText
// on submit).
func (d *LoginDialogComponent) resolvePending(result loginInputResult, replace bool) {
	d.pendingMu.Lock()
	pending := d.pendingInput
	d.pendingInput = nil
	d.pendingMu.Unlock()
	if pending == nil {
		return
	}
	if replace {
		value := result.value
		d.mutate(func() { d.replaceInputWithSubmittedText(value) })
	}
	pending <- result
}

func (d *LoginDialogComponent) cancel() {
	d.abortController.cancel()
	d.resolvePending(loginInputResult{err: errorString("Login cancelled")}, false)
	if d.onComplete != nil {
		d.onComplete(false, "Login cancelled")
	}
}

// errorString is a minimal error wrapper for the fixed cancel message.
type errorString string

func (e errorString) Error() string { return string(e) }

// waitPending registers a pending input resolution channel and blocks until
// the user submits or cancels (pi: `return new Promise(...)`).
func (d *LoginDialogComponent) waitPending() (string, error) {
	ch := make(chan loginInputResult, 1)
	d.pendingMu.Lock()
	d.pendingInput = ch
	d.pendingMu.Unlock()
	result := <-ch
	return result.value, result.err
}

func (d *LoginDialogComponent) requestRender() {
	if d.tui != nil {
		d.tui.RequestRender(false)
	}
}

// ShowAuth shows the URL and optional instructions (pi: showAuth — called
// by the onAuth callback).
func (d *LoginDialogComponent) ShowAuth(url string, instructions string) {
	d.mutate(func() {
		currentTheme := theme.Current()
		d.contentContainer.Clear()
		d.contentContainer.AddChild(tui.NewSpacer(1))
		linkedURL := "\x1b]8;;" + url + "\x07" + url + "\x1b]8;;\x07"
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("accent", linkedURL), 1, 0))

		clickHint := clickToOpenHint()
		hyperlink := "\x1b]8;;" + url + "\x07" + clickHint + "\x1b]8;;\x07"
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("dim", hyperlink), 1, 0))

		if instructions != "" {
			d.contentContainer.AddChild(tui.NewSpacer(1))
			d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("warning", instructions), 1, 0))
		}
	})

	utils.OpenBrowser(url)
	d.requestRender()
}

// clickToOpenHint is pi's platform branch for the hyperlink hint.
func clickToOpenHint() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+click to open"
	}
	return "Ctrl+click to open"
}

// ShowDeviceCode shows the URL and user code (pi: showDeviceCode — called
// by the onDeviceCode callback).
func (d *LoginDialogComponent) ShowDeviceCode(info ai.OAuthDeviceCodeInfo) {
	d.mutate(func() {
		currentTheme := theme.Current()
		d.contentContainer.Clear()
		d.contentContainer.AddChild(tui.NewSpacer(1))
		linkedURL := "\x1b]8;;" + info.VerificationURI + "\x07" + info.VerificationURI + "\x1b]8;;\x07"
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("accent", linkedURL), 1, 0))

		hyperlink := "\x1b]8;;" + info.VerificationURI + "\x07" + clickToOpenHint() + "\x1b]8;;\x07"
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("dim", hyperlink), 1, 0))
		d.contentContainer.AddChild(tui.NewSpacer(1))
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("warning", "Enter code: "+info.UserCode), 1, 0))
	})

	d.requestRender()
}

// ShowManualInput collects a manual code/URL entry (pi: showManualInput —
// for callback server providers). Blocks until submit or cancel; the error
// is "Login cancelled" when aborted (pi: the rejected Promise).
func (d *LoginDialogComponent) ShowManualInput(prompt string) (string, error) {
	d.mutate(func() {
		currentTheme := theme.Current()
		d.input.SetValue("")
		d.contentContainer.AddChild(tui.NewSpacer(1))
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("dim", prompt), 1, 0))
		d.contentContainer.AddChild(d.input)
		d.contentContainer.AddChild(tui.NewText("("+coretools.KeyHint("tui.select.cancel", "to cancel")+")", 1, 0))
	})
	d.requestRender()

	return d.waitPending()
}

// ShowPrompt shows a prompt and waits for input (pi: showPrompt — called by
// the onPrompt callback). Does NOT clear content, appends to existing
// (preserves the URL from ShowAuth). Blocks until submit or cancel.
func (d *LoginDialogComponent) ShowPrompt(message string, placeholder string) (string, error) {
	d.mutate(func() {
		currentTheme := theme.Current()
		d.contentContainer.AddChild(tui.NewSpacer(1))
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("text", message), 1, 0))
		if placeholder != "" {
			d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("dim", "e.g., "+placeholder), 1, 0))
		}
		d.contentContainer.AddChild(d.input)
		d.contentContainer.AddChild(tui.NewText(
			"("+coretools.KeyHint("tui.select.cancel", "to cancel,")+" "+coretools.KeyHint("tui.select.confirm", "to submit")+")",
			1, 0,
		))
		d.input.SetValue("")
	})
	d.requestRender()

	return d.waitPending()
}

// ShowDetails shows informational text before another login step (pi:
// showDetails).
func (d *LoginDialogComponent) ShowDetails(lines []string) {
	d.mutate(func() {
		d.contentContainer.Clear()
		d.contentContainer.AddChild(tui.NewSpacer(1))
		for _, line := range lines {
			d.contentContainer.AddChild(tui.NewText(line, 1, 0))
		}
	})
	d.requestRender()
}

// ShowInfo shows provider-owned information and links without starting an
// auth callback flow (pi: showInfo).
func (d *LoginDialogComponent) ShowInfo(message string, links []auth.AuthInfoLink, showCloseHint bool) {
	d.mutate(func() {
		currentTheme := theme.Current()
		d.contentContainer.AddChild(tui.NewSpacer(1))
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("text", message), 1, 0))
		for _, link := range links {
			text := link.URL
			if link.Label != "" {
				text = link.Label + ": " + link.URL
			}
			hyperlink := "\x1b]8;;" + link.URL + "\x07" + text + "\x1b]8;;\x07"
			d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("accent", hyperlink), 1, 0))
		}
		if showCloseHint {
			d.contentContainer.AddChild(tui.NewSpacer(1))
			d.contentContainer.AddChild(tui.NewText("("+coretools.KeyHint("tui.select.cancel", "to close")+")", 1, 0))
		}
	})
	d.requestRender()
}

// ShowWaiting shows a waiting message (pi: showWaiting — for polling flows
// like GitHub Copilot).
func (d *LoginDialogComponent) ShowWaiting(message string) {
	d.mutate(func() {
		currentTheme := theme.Current()
		d.contentContainer.AddChild(tui.NewSpacer(1))
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("dim", message), 1, 0))
		d.contentContainer.AddChild(tui.NewText("("+coretools.KeyHint("tui.select.cancel", "to cancel")+")", 1, 0))
	})
	d.requestRender()
}

// ShowProgress shows a progress message (pi: showProgress — called by the
// onProgress callback).
func (d *LoginDialogComponent) ShowProgress(message string) {
	d.mutate(func() {
		currentTheme := theme.Current()
		d.contentContainer.AddChild(tui.NewText(currentTheme.Fg("dim", message), 1, 0))
	})
	d.requestRender()
}

// HandleInput implements InputHandler (pi: handleInput).
func (d *LoginDialogComponent) HandleInput(data string) {
	kb := tui.GetKeybindings()

	if kb.Matches(data, "tui.select.cancel") {
		d.cancel()
		return
	}

	// Pass to input
	d.input.HandleInput(data)
}
