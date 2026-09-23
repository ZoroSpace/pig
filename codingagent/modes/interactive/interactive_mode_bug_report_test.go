// interactive_mode_bug_report_test.go — pi: test/bug-report.test.ts
// "bug report prompt" describe (v0.86.1): the /bug description prompt is the
// multiline ExtensionEditorComponent, so pasted multi-line text survives
// into the delivery summary. pig drives reportBug on a seam-constructed
// mode; the description/selector components receive raw key input exactly
// like pi's VirtualTerminal test.
package interactive

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// bugReportWait polls until cond passes (pi: vi.waitFor).
func bugReportWait(t *testing.T, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(time.Millisecond)
	}
}

// bugReportFocusTUI adds SetFocus tracking and RunExclusive serialization to
// the stub surface the bug-report overlay swap exercises. pi's single
// event loop serializes component access for free; pig guards the overlay
// mutations (mutateUI) against the test's reads with the same mutex.
type bugReportFocusTUI struct {
	stubRequestRenderTUI
	mu      sync.Mutex
	focused tui.Component
}

// SetFocus writes focused without taking mu: mutateUI calls it while
// RunExclusive already holds mu (sync.Mutex is not reentrant), and no
// reader observes the field.
func (b *bugReportFocusTUI) SetFocus(component tui.Component) {
	b.focused = component
}

func (b *bugReportFocusTUI) RunExclusive(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn()
}

func TestBugReportPreservesMultilineDescription(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	m := newSeamTestMode(session, nil)
	focusTUI := &bugReportFocusTUI{}
	m.ui = focusTUI
	m.editor = components.NewCustomEditor(&stubRequestRenderTUI{}, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)

	statuses := []string{}
	bc := bugReportContext{
		session:         session,
		showStatus:      func(message string) { statuses = append(statuses, message) },
		showError:       func(string) {},
		editorContainer: m.editorContainer,
		editor:          m.editor,
		keybindings:     core.CreateKeybindingsManager(""),
		ui:              focusTUI,
	}
	// All test-side reads/inputs take the same lock mutateUI holds, so the
	// component tree is never touched concurrently (pi: single thread).
	sync := func(fn func()) {
		focusTUI.mu.Lock()
		defer focusTUI.mu.Unlock()
		fn()
	}
	countChildren := func() int {
		n := 0
		sync(func() { n = len(m.editorContainer.Children) })
		return n
	}
	childAt := func(i int) tui.Component {
		var child tui.Component
		sync(func() {
			if len(m.editorContainer.Children) > i {
				child = m.editorContainer.Children[i]
			}
		})
		return child
	}
	isSelector := func() bool {
		_, ok := childAt(0).(*components.ExtensionSelectorComponent)
		return ok
	}
	handleInput := func(c tui.Component, key string) {
		handler, ok := c.(tui.InputHandler)
		if !ok {
			t.Fatalf("component %T cannot handle input", c)
		}
		sync(func() { handler.HandleInput(key) })
	}
	renderChild := func(c tui.Component) []string {
		var lines []string
		sync(func() { lines = c.Render(120) })
		return lines
	}

	done := make(chan struct{})
	go func() {
		m.reportBug(context.Background(), bc, "")
		close(done)
	}()

	// The description prompt is the multiline extension editor.
	bugReportWait(t, func() bool { return countChildren() > 0 }, "description editor never appeared")
	descriptionChild := childAt(0)
	descriptionEditor, ok := descriptionChild.(*components.ExtensionEditorComponent)
	if !ok {
		t.Fatalf("description prompt = %T, want ExtensionEditorComponent", descriptionChild)
	}
	handleInput(descriptionEditor, "\x1b[200~Request failed\r\n  ↳ pig exiting...\r\nstack trace\x1b[201~")
	handleInput(descriptionEditor, "\r")

	// Transcript selector: answer No so the summary prompt appears. (pi's
	// restore-then-resolve is synchronous; pig's restore goroutine races the
	// next overlay, so the observable assertion is the selector appearing.)
	bugReportWait(t, isSelector, "transcript selector never appeared")
	transcriptSelector := childAt(0).(*components.ExtensionSelectorComponent)
	rendered := strings.Join(renderChild(transcriptSelector), "\n")
	if !strings.Contains(utils.StripAnsi(rendered), "Include the session transcript?") {
		t.Fatalf("transcript prompt missing: %s", rendered)
	}
	handleInput(transcriptSelector, "j")
	handleInput(transcriptSelector, "\r")

	// Summary selector (transcript was declined). Wait for a NEW selector
	// instance: the previous component object lingers until the overlay
	// goroutine swaps it.
	bugReportWait(t, func() bool {
		return isSelector() && childAt(0) != tui.Component(transcriptSelector)
	}, "summary selector never appeared")
	summarySelector := childAt(0).(*components.ExtensionSelectorComponent)
	rendered = strings.Join(renderChild(summarySelector), "\n")
	if !strings.Contains(utils.StripAnsi(rendered), "Attach a summary written by") {
		t.Fatalf("summary prompt missing: %s", rendered)
	}
	handleInput(summarySelector, "j")
	handleInput(summarySelector, "\r")

	// Delivery selector shows the multiline description preserved.
	bugReportWait(t, func() bool {
		return isSelector() && childAt(0) != tui.Component(summarySelector)
	}, "delivery selector never appeared")
	deliverySelector := childAt(0).(*components.ExtensionSelectorComponent)
	deliveryLines := strings.Split(utils.StripAnsi(strings.Join(renderChild(deliverySelector), "\n")), "\n")
	has := func(substr string) bool {
		for _, line := range deliveryLines {
			if strings.Contains(line, substr) {
				return true
			}
		}
		return false
	}
	if !has("Description: Request failed") || !has("↳ pig exiting...") || !has("stack trace") {
		t.Fatalf("delivery summary lost the multiline description: %v", deliveryLines)
	}

	// Cancel the delivery: two j movements land on Cancel.
	handleInput(deliverySelector, "j")
	handleInput(deliverySelector, "j")
	handleInput(deliverySelector, "\r")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reportBug did not settle")
	}
	if len(statuses) == 0 || statuses[len(statuses)-1] != "Bug report cancelled" {
		t.Fatalf("statuses = %v, want the cancelled status", statuses)
	}
}

// The seam containers are accessed from the single test goroutine plus the
// mutateUI closures only, so plain polling is race-safe under -race.

// pi c7cdb460a + 1e0fe2049: only the upload delivery is rejected offline
// (#9841); zip export proceeds. pi ships no test for either branch; this
// locks the upload branch (the zip branch is the one-line delivery condition
// and stays covered by the zip flow).
func TestBugReportUploadRejectedOffline(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	m := newSeamTestMode(session, nil)
	focusTUI := &bugReportFocusTUI{}
	m.ui = focusTUI
	m.editor = components.NewCustomEditor(&stubRequestRenderTUI{}, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)

	var errMu sync.Mutex
	var errorMessages []string
	bc := bugReportContext{
		session:         session,
		showStatus:      func(string) {},
		showError:       func(message string) { errMu.Lock(); errorMessages = append(errorMessages, message); errMu.Unlock() },
		editorContainer: m.editorContainer,
		editor:          m.editor,
		keybindings:     core.CreateKeybindingsManager(""),
		ui:              focusTUI,
	}
	sync := func(fn func()) {
		focusTUI.mu.Lock()
		defer focusTUI.mu.Unlock()
		fn()
	}
	countChildren := func() int {
		n := 0
		sync(func() { n = len(m.editorContainer.Children) })
		return n
	}
	childAt := func(i int) tui.Component {
		var child tui.Component
		sync(func() {
			if len(m.editorContainer.Children) > i {
				child = m.editorContainer.Children[i]
			}
		})
		return child
	}
	isSelector := func() bool {
		_, ok := childAt(0).(*components.ExtensionSelectorComponent)
		return ok
	}
	handleInput := func(c tui.Component, key string) {
		handler, ok := c.(tui.InputHandler)
		if !ok {
			t.Fatalf("component %T cannot handle input", c)
		}
		sync(func() { handler.HandleInput(key) })
	}

	done := make(chan struct{})
	go func() {
		m.reportBug(context.Background(), bc, "")
		close(done)
	}()

	// Description prompt: submit a one-line description.
	bugReportWait(t, func() bool { return countChildren() > 0 }, "description editor never appeared")
	descriptionEditor, ok := childAt(0).(*components.ExtensionEditorComponent)
	if !ok {
		t.Fatalf("description prompt = %T, want ExtensionEditorComponent", childAt(0))
	}
	handleInput(descriptionEditor, "\x1b[200~it broke\x1b[201~")
	handleInput(descriptionEditor, "\r")

	// Transcript: No. Summary: No.
	bugReportWait(t, isSelector, "transcript selector never appeared")
	transcriptSelector := childAt(0).(*components.ExtensionSelectorComponent)
	handleInput(transcriptSelector, "j")
	handleInput(transcriptSelector, "\r")
	bugReportWait(t, func() bool {
		return isSelector() && childAt(0) != tui.Component(transcriptSelector)
	}, "summary selector never appeared")
	summarySelector := childAt(0).(*components.ExtensionSelectorComponent)
	handleInput(summarySelector, "j")
	handleInput(summarySelector, "\r")

	// Delivery: Upload Report (first item) — must be rejected offline.
	bugReportWait(t, func() bool {
		return isSelector() && childAt(0) != tui.Component(summarySelector)
	}, "delivery selector never appeared")
	handleInput(childAt(0), "\r")

	bugReportWait(t, func() bool {
		errMu.Lock()
		defer errMu.Unlock()
		return len(errorMessages) > 0
	}, "offline upload rejection never surfaced")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reportBug did not return after the offline rejection")
	}
	errMu.Lock()
	firstError := ""
	if len(errorMessages) > 0 {
		firstError = errorMessages[0]
	}
	errMu.Unlock()
	if firstError != "Uploading bug reports requires online mode. Use Export as Zip instead." {
		t.Fatalf("error = %q", firstError)
	}
}
