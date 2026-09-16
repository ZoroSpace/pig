// interactive_mode_submit_test.go — regression tests for submit routing of
// "/"-prefixed text and the clipboard image placeholder flow (pi:
// setupEditorSubmitHandler's onSubmit body + handleClipboardPaste).
//
// Two behaviors locked here:
//
//  1. pi's onSubmit has NO unknown-command branch: text starting with "/"
//     that matches no interactive command falls through to normal message
//     submission (extension commands / skills / templates are resolved
//     inside AgentSession.prompt). pig's deferred-command placeholder used
//     to intercept every "/"-prefixed input with a "not yet available"
//     warning, which broke the paste-image flow: handleClipboardPaste used
//     to insert the macOS temp path (/var/folders/.../pi-clipboard-*.png)
//     at the cursor, so the pasted message always started with "/" and was
//     swallowed. See docs/fix/2026-09-16-clipboard-path-eaten-by-slash-command.md.
//
//  2. pig-only (opencode-style, user request): pasting an image inserts an
//     "[Image N]" placeholder into the editor instead of the raw path; the
//     placeholder expands back to the real path at submit time (the model
//     and the JSONL session receive the path), and the transcript renders
//     the label. See docs/feat/CLIPBOARD-IMAGE-PLACEHOLDER.md.
package interactive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// newSubmitTestTheme builds a theme with the full color set (reuses
// probeThemeColors from the probe harness — ShowWarning needs "warning",
// user-message rendering needs the message colors).
func newSubmitTestTheme(t *testing.T) *theme.Theme {
	t.Helper()
	tl, err := theme.NewTheme(probeThemeColors(), probeThemeColors(), theme.ColorMode256, nil)
	if err != nil {
		t.Fatalf("NewTheme: %v", err)
	}
	return tl
}

// newSubmitTestMode builds a seam-test InteractiveMode with a real editor
// and in-memory session (same-package construction; see
// test_session_seam_test.go).
func newSubmitTestMode(t *testing.T) *InteractiveMode {
	t.Helper()

	// ShowWarning / user-message rendering style via the global theme
	// (theme.Current() panics when unset).
	theme.SetGlobalTheme(newSubmitTestTheme(t))

	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	mode.editor = components.NewCustomEditor(&stubRequestRenderTUI{}, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)
	return mode
}

// stubClipboardReaders swaps the clipboard seam functions for the duration
// of the test (pig: pi's tests mock the clipboard module functions).
func stubClipboardReaders(t *testing.T, image *utils.ClipboardImage, text string) {
	t.Helper()
	originalImage, originalText := clipboardImageReaderFn, clipboardTextReaderFn
	clipboardImageReaderFn = func(*utils.ReadClipboardImageOptions) *utils.ClipboardImage { return image }
	clipboardTextReaderFn = func() string { return text }
	t.Cleanup(func() {
		clipboardImageReaderFn = originalImage
		clipboardTextReaderFn = originalText
	})
}

// submittedInputGateText drains the input gate non-blockingly (bounded wait)
// and fails the test when nothing arrives.
func submittedInputGateText(t *testing.T, m *InteractiveMode) string {
	t.Helper()
	ch := make(chan string, 1)
	go func() { ch <- m.inputGate.wait() }()
	select {
	case got := <-ch:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("input gate never received the submission")
		return ""
	}
}

// inputGateEmpty reports whether nothing was submitted to the gate (the
// deferred-command branch must swallow input).
func inputGateEmpty(m *InteractiveMode) bool {
	m.inputGate.mu.Lock()
	defer m.inputGate.mu.Unlock()
	return len(m.inputGate.pending) == 0 && m.inputGate.waiter == nil
}

func TestClipboardPathSubmissionReachesModel(t *testing.T) {
	// Exact user repro: paste-image inserts the macOS temp path, the user
	// appends their question. (With the placeholder feature the editor now
	// holds "[Image 1]", but paths can still start a message — e.g. typed
	// by hand or pasted as text — and must reach the model verbatim.)
	const pasted = "/var/folders/dn/1y62hnsj18gf7f5hkj61jbsm0000gn/T/pi-clipboard-8cbd2707-7a57-4a8a-a344-2f9d34efe394.png 你好"

	tests := []struct {
		name string
		text string
	}{
		{"pasted clipboard image path plus question", pasted},
		{"bare pasted path", "/var/folders/dn/T/pi-clipboard-x.png"},
		{"other absolute path text", "/etc/hosts 里有什么内容"},
		{"unknown slash word", "/foo bar"},
		{"command-like prefix that is not a command", "/exports folder"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSubmitTestMode(t)
			m.processSubmitText(context.Background(), tt.text)

			if got := submittedInputGateText(t, m); got != tt.text {
				t.Fatalf("submitted %q, want %q", got, tt.text)
			}
			if warning := renderAllChildren(m.chatContainer, 80); strings.Contains(warning, "Warning:") {
				t.Fatalf("unexpected warning rendered for %q:\n%s", tt.text, normalizeRenderedOutput(warning))
			}
		})
	}
}

func TestDeferredInteractiveCommandsStillWarn(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string // warning headline shown to the user
	}{
		{"export bare", "/export", "/export"},
		{"export with path arg", "/export /tmp/session.md", "/export"},
		{"import with path arg", "/import /tmp/session.md", "/import"},
		{"share", "/share", "/share"},
		{"copy", "/copy", "/copy"},
		{"session", "/session", "/session"},
		{"changelog", "/changelog", "/changelog"},
		{"hotkeys", "/hotkeys", "/hotkeys"},
		{"clone", "/clone", "/clone"},
		{"debug", "/debug", "/debug"},
		{"easter egg arminsayshi", "/arminsayshi", "/arminsayshi"},
		{"easter egg dementedelves", "/dementedelves", "/dementedelves"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSubmitTestMode(t)
			m.processSubmitText(context.Background(), tt.text)

			rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 80))
			if !strings.Contains(rendered, "Warning: "+tt.want+" is not yet available") {
				t.Fatalf("missing deferred-command warning for %q, got:\n%s", tt.text, rendered)
			}
			// The deferred branch swallows the input: nothing reaches the model.
			if !inputGateEmpty(m) {
				t.Fatalf("deferred command %q leaked to submission", tt.text)
			}
		})
	}
}

func TestIsDeferredInteractiveCommandBoundaries(t *testing.T) {
	yes := []string{"/export", "/export x", "/import", "/import y", "/share", "/debug", "/dementedelves"}
	no := []string{
		"/exports x",                         // longer word, not the command
		"/exportx",                           // no space boundary
		"/var/folders/x/pi-clipboard.png 你好", // pasted path
		"/etc/hosts",                         // arbitrary path
		"/settings",                          // implemented commands are handled earlier, never deferred
		"/model",                             // implemented commands are handled earlier, never deferred
	}
	for _, text := range yes {
		if !isDeferredInteractiveCommand(text) {
			t.Errorf("isDeferredInteractiveCommand(%q) = false, want true", text)
		}
	}
	for _, text := range no {
		if isDeferredInteractiveCommand(text) {
			t.Errorf("isDeferredInteractiveCommand(%q) = true, want false", text)
		}
	}
}

func TestClipboardPasteInsertsImagePlaceholder(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	stubClipboardReaders(t, &utils.ClipboardImage{MimeType: "image/png", Bytes: []byte{0x89, 'P', 'N', 'G'}}, "")

	m := newSubmitTestMode(t)
	m.handleClipboardPaste()

	got := m.editor.GetText()
	if got != "[Image 1]" {
		t.Fatalf("editor text after paste = %q, want %q", got, "[Image 1]")
	}

	// The temp file must exist on disk — that is what the model reads.
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "pi-clipboard-*.png"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one pi-clipboard-*.png temp file, got %v (err=%v)", matches, err)
	}

	// Second paste in the same message counts up.
	m.handleClipboardPaste()
	if got := m.editor.GetText(); got != "[Image 1][Image 2]" {
		t.Fatalf("editor text after second paste = %q, want %q", got, "[Image 1][Image 2]")
	}
}

func TestClipboardPlaceholderExpandsToPathOnSubmit(t *testing.T) {
	m := newSubmitTestMode(t)

	if label := m.recordClipboardImagePath("/tmp/first.png"); label != "[Image 1]" {
		t.Fatalf("first label = %q, want [Image 1]", label)
	}
	if label := m.recordClipboardImagePath("/tmp/second.png"); label != "[Image 2]" {
		t.Fatalf("second label = %q, want [Image 2]", label)
	}

	m.processSubmitText(context.Background(), "[Image 2] 对比一下 [Image 1]")
	if got := submittedInputGateText(t, m); got != "/tmp/second.png 对比一下 /tmp/first.png" {
		t.Fatalf("submitted %q, want the expanded paths", got)
	}

	// Editor history recalls the compact placeholder, not the raw path.
	// (History is populated from the pre-expansion text; verified via the
	// transcript label map below and the editor history indirectly.)

	// Numbering restarts per message after a submission.
	if label := m.recordClipboardImagePath("/tmp/third.png"); label != "[Image 1]" {
		t.Fatalf("label after resubmit = %q, want [Image 1] (per-message numbering)", label)
	}

	// Unknown placeholder numbers are left as-is (user-typed literal).
	m2 := newSubmitTestMode(t)
	m2.processSubmitText(context.Background(), "[Image 9] nothing pasted")
	if got := submittedInputGateText(t, m2); got != "[Image 9] nothing pasted" {
		t.Fatalf("unknown placeholder mangled: %q", got)
	}
}

func TestTranscriptRendersImagePlaceholderLabel(t *testing.T) {
	m := newSubmitTestMode(t)
	m.recordClipboardImagePath("/tmp/pasted-shot.png")

	msg := types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: "/tmp/pasted-shot.png 这张图里是什么"},
		Timestamp: 1,
	}
	m.addMessageToChat(msg, nil)

	rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 120))
	if !strings.Contains(rendered, "[Image 1] 这张图里是什么") {
		t.Fatalf("transcript should render the placeholder label, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "/tmp/pasted-shot.png") {
		t.Fatalf("transcript must not show the raw pasted path, got:\n%s", rendered)
	}
}

// stubEditorTerminal provides the terminal surface Editor.Render needs
// (Rows only — the render path is pure string assembly).
type stubEditorTerminal struct {
	tui.Terminal
}

func (s *stubEditorTerminal) Rows() int    { return 24 }
func (s *stubEditorTerminal) Columns() int { return 80 }

// stubEditorTUI routes Terminal() to the stub so Editor.Render can run.
type stubEditorTUI struct {
	stubRequestRenderTUI
}

func (s *stubEditorTUI) Terminal() tui.Terminal { return &stubEditorTerminal{} }

func TestImagePlaceholdersAreColorized(t *testing.T) {
	theme.SetGlobalTheme(newSubmitTestTheme(t))

	// Editor decorator: plain line gets the imagePlaceholder SGR around the
	// label, visible text unchanged.
	line := colorizeImagePlaceholders("hello [Image 1] world")
	if !strings.Contains(line, theme.Fg("imagePlaceholder", "[Image 1]")) {
		t.Fatalf("expected imagePlaceholder-colored placeholder, got %q", line)
	}
	if got := ansiRe.ReplaceAllString(line, ""); got != "hello [Image 1] world" {
		t.Fatalf("visible text changed: %q", got)
	}

	// A placeholder split by the embedded fake-cursor span stays uncolored.
	split := "hello [Im\x1b[7ma\x1b[0mge 1] world"
	if got := colorizeImagePlaceholders(split); got != split {
		t.Fatalf("split placeholder should stay untouched, got %q", got)
	}

	// Integration: CustomEditor.Render applies the decorator; the editor
	// buffer itself stays plain text.
	editorTUI := &stubEditorTUI{}
	editor := components.NewCustomEditor(editorTUI, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)
	editor.TextDecorator = colorizeImagePlaceholders
	editor.SetText("[Image 1] hi")
	rendered := strings.Join(editor.Render(60), "\n")
	if !strings.Contains(rendered, theme.Fg("imagePlaceholder", "[Image 1]")) {
		t.Fatalf("rendered editor should colorize the placeholder:\n%q", rendered)
	}
	if got := editor.GetText(); got != "[Image 1] hi" {
		t.Fatalf("editor buffer must stay plain, got %q", got)
	}
}
