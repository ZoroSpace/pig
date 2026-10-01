// pi: test/clipboard-paste-file-paths.test.ts (#10136/#9999) — Finder file
// copies paste their original paths ahead of the icon image; control
// characters are rejected; native errors surface instead of falling through
// to the image; paths are space-padded away from surrounding text; bash mode
// shell-quotes them into a command line.
package interactive

import (
	"errors"
	"strings"
	"testing"

	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// stubClipboardFilePathsReader swaps the file-path seam (pig: the pi mock of
// readClipboardFilePaths; nil paths, nil error = nothing on the clipboard).
func stubClipboardFilePathsReader(t *testing.T, paths []string, err error) {
	t.Helper()
	original := clipboardFilePathsReadFn
	clipboardFilePathsReadFn = func() ([]string, error) { return paths, err }
	t.Cleanup(func() { clipboardFilePathsReadFn = original })
}

func TestClipboardPasteFinderFilePathsPrecedeImage(t *testing.T) {
	mode := newSubmitTestMode(t)
	filePaths := []string{"/tmp/screenshot.png", "/tmp/My Photos/photo.png"}
	stubClipboardFilePathsReader(t, filePaths, nil)
	called := false
	originalImage := clipboardImageReaderFn
	clipboardImageReaderFn = func(*codingagentutils.ReadClipboardImageOptions) *codingagentutils.ClipboardImage {
		called = true
		return nil
	}
	t.Cleanup(func() { clipboardImageReaderFn = originalImage })

	mode.handleClipboardPaste()

	// Paths paste newline-separated outside bash mode; the file paths must
	// arrive as-is, and image reading never runs.
	if got := mode.editor.GetText(); got != strings.Join(filePaths, "\n") {
		t.Fatalf("editor text = %q, want %q", got, strings.Join(filePaths, "\n"))
	}
	if called {
		t.Fatal("image reader must not run when file paths are available")
	}
}

func TestClipboardPasteRejectsControlCharactersInFilePaths(t *testing.T) {
	mode := newSubmitTestMode(t)
	stubClipboardFilePathsReader(t, []string{"/tmp/photo\x1b]0;unsafe\x07.png"}, nil)
	called := false
	originalImage := clipboardImageReaderFn
	clipboardImageReaderFn = func(*codingagentutils.ReadClipboardImageOptions) *codingagentutils.ClipboardImage {
		called = true
		return nil
	}
	t.Cleanup(func() { clipboardImageReaderFn = originalImage })

	mode.handleClipboardPaste()

	if mode.editor.GetText() != "" {
		t.Fatalf("editor text = %q, want empty", mode.editor.GetText())
	}
	if called {
		t.Fatal("image reader must not run after a control-character rejection")
	}
	rendered := renderAllChildren(mode.chatContainer, 120)
	if !strings.Contains(rendered, "Failed to paste from clipboard: Clipboard file path contains control characters") {
		t.Fatalf("error not shown, rendered: %q", rendered)
	}
}

func TestClipboardPasteShowsNativeFilePathErrorsWithoutFallingThrough(t *testing.T) {
	mode := newSubmitTestMode(t)
	stubClipboardFilePathsReader(t, nil, errors.New("Native clipboard file read failed"))
	called := false
	originalImage := clipboardImageReaderFn
	clipboardImageReaderFn = func(*codingagentutils.ReadClipboardImageOptions) *codingagentutils.ClipboardImage {
		called = true
		return nil
	}
	t.Cleanup(func() { clipboardImageReaderFn = originalImage })

	mode.handleClipboardPaste()

	if mode.editor.GetText() != "" {
		t.Fatalf("editor text = %q, want empty", mode.editor.GetText())
	}
	if called {
		t.Fatal("image reader must not run after a native file-path error")
	}
	rendered := renderAllChildren(mode.chatContainer, 120)
	if !strings.Contains(rendered, "Failed to paste from clipboard: Native clipboard file read failed") {
		t.Fatalf("error not shown, rendered: %q", rendered)
	}
}

func TestClipboardPasteFilePathsSeparatedFromPrecedingText(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		cursorCol int
		want      string
	}{
		{"punctuation", "Review:", 7, " /tmp/photo.png"},
		{"unicode text", "確認", 2, " /tmp/photo.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode := newSubmitTestMode(t)
			stubClipboardFilePathsReader(t, []string{"/tmp/photo.png"}, nil)
			mode.editor.SetText(tc.text)
			// The seeded text puts the cursor at its end (the seam under
			// test reads the live editor cursor).
			if _, ok := mode.editor.(editorComponentCursor); !ok {
				t.Fatal("editor does not implement the cursor capability")
			}

			mode.handleClipboardPaste()

			if got := mode.editor.GetText(); got != tc.text+tc.want {
				t.Fatalf("editor text = %q, want %q", got, tc.text+tc.want)
			}
		})
	}
}

func TestClipboardPasteFilePathsBashModeShellQuotes(t *testing.T) {
	mode := newSubmitTestMode(t)
	stubClipboardFilePathsReader(t, []string{
		"/tmp/My Photos/photo.png",
		"/tmp/$(touch hacked).png",
		"/tmp/plain.png",
	}, nil)
	mode.editor.SetText("cat")
	mode.isBashMode = true

	mode.handleClipboardPaste()

	// The cursor sits after "cat", so the first path gains a leading space;
	// the text ends there, so no trailing space is added.
	want := "cat '/tmp/My Photos/photo.png' '/tmp/$(touch hacked).png' /tmp/plain.png"
	if got := mode.editor.GetText(); got != want {
		t.Fatalf("editor text = %q, want %q", got, want)
	}
}
