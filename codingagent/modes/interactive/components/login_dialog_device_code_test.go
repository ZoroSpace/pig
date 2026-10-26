// pi: test/login-dialog-device-code.test.ts — device-code convenience
// actions require explicit confirmation (#9282).
package components

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

var (
	deviceLoginTestMu      sync.Mutex
	deviceLoginTestOpened  []string
	deviceLoginTestCopied  []string
	deviceLoginTestCopyErr error
)

func stubDeviceCodeActions(t *testing.T) {
	t.Helper()
	deviceLoginTestMu.Lock()
	deviceLoginTestOpened = nil
	deviceLoginTestCopied = nil
	deviceLoginTestCopyErr = nil
	deviceLoginTestMu.Unlock()
	openBrowserFn = func(url string) {
		deviceLoginTestMu.Lock()
		deviceLoginTestOpened = append(deviceLoginTestOpened, url)
		deviceLoginTestMu.Unlock()
	}
	deviceCodeCopyFn = func(text string) error {
		deviceLoginTestMu.Lock()
		defer deviceLoginTestMu.Unlock()
		deviceLoginTestCopied = append(deviceLoginTestCopied, text)
		return deviceLoginTestCopyErr
	}
	t.Cleanup(func() {
		openBrowserFn = utils.OpenBrowser
		deviceCodeCopyFn = utils.CopyToClipboard
	})
}

func deviceCodeTestDevice() ai.OAuthDeviceCodeInfo {
	return ai.OAuthDeviceCodeInfo{UserCode: "ABCD-EFGH", VerificationURI: "https://example.com/device"}
}

func deviceActionSnapshot() (opened []string, copied []string) {
	deviceLoginTestMu.Lock()
	defer deviceLoginTestMu.Unlock()
	return append([]string(nil), deviceLoginTestOpened...), append([]string(nil), deviceLoginTestCopied...)
}

func waitForDeviceActions(t *testing.T, wantOpened int, wantCopied int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		opened, copied := deviceActionSnapshot()
		if len(opened) >= wantOpened && len(copied) >= wantCopied {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForDialogOutput(t *testing.T, h *authURLTestHarness, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output := h.output()
		if strings.Contains(output, want) {
			return output
		}
		time.Sleep(5 * time.Millisecond)
	}
	return h.output()
}

func TestLoginDialogDeviceCodeConfirmsBrowserAndCopyOnce(t *testing.T) {
	h := newAuthURLTestDialog(t)
	stubDeviceCodeActions(t)
	h.dialog.ShowDeviceCode(deviceCodeTestDevice())
	h.dialog.ShowWaiting("Waiting for authentication...")

	if output := h.output(); !strings.Contains(output, "to open browser and copy code") {
		t.Fatalf("confirmation hint missing: %q", output)
	}
	deviceLoginTestMu.Lock()
	if len(deviceLoginTestOpened) != 0 || len(deviceLoginTestCopied) != 0 {
		deviceLoginTestMu.Unlock()
		t.Fatal("browser/clipboard must wait for confirmation")
	}
	deviceLoginTestMu.Unlock()

	h.handleInput("\r")
	output := waitForDialogOutput(t, h, "Code copied to clipboard")
	waitForDeviceActions(t, 1, 1)
	h.handleInput("\r")
	time.Sleep(20 * time.Millisecond)

	opened, copied := deviceActionSnapshot()
	if len(opened) != 1 || opened[0] != deviceCodeTestDevice().VerificationURI {
		t.Fatalf("opened = %v", opened)
	}
	if len(copied) != 1 || copied[0] != deviceCodeTestDevice().UserCode {
		t.Fatalf("copied = %v", copied)
	}
	if !strings.Contains(output, "Waiting for authentication...") ||
		!strings.Contains(output, "Enter code: "+deviceCodeTestDevice().UserCode) ||
		strings.Contains(output, "to open browser and copy code") {
		t.Fatalf("output after confirmation = %q", output)
	}
}

func TestLoginDialogDeviceCodeHonorsProviderOverrides(t *testing.T) {
	cases := []struct {
		openBrowser bool
		copyCode    bool
	}{
		{false, false},
		{true, false},
		{false, true},
	}
	for _, tc := range cases {
		name := "browser=" + boolLabel(tc.openBrowser) + "/clipboard=" + boolLabel(tc.copyCode)
		t.Run(name, func(t *testing.T) {
			h := newAuthURLTestDialog(t)
			stubDeviceCodeActions(t)
			info := deviceCodeTestDevice()
			info.OpenBrowserOnConfirm = &tc.openBrowser
			info.CopyCodeOnConfirm = &tc.copyCode
			h.dialog.ShowDeviceCode(info)
			h.handleInput("\r")
			waitForDeviceActions(t, boolInt(tc.openBrowser), boolInt(tc.copyCode))
			time.Sleep(20 * time.Millisecond)

			opened, copied := deviceActionSnapshot()
			if len(opened) != boolInt(tc.openBrowser) {
				t.Fatalf("opened = %v, want browser=%v", opened, tc.openBrowser)
			}
			if len(copied) != boolInt(tc.copyCode) {
				t.Fatalf("copied = %v, want copy=%v", copied, tc.copyCode)
			}
			if h.dialog.Context().Err() != nil {
				t.Fatal("dialog was aborted")
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestLoginDialogDeviceCodeActionClearedByOtherScreens(t *testing.T) {
	h := newAuthURLTestDialog(t)
	stubDeviceCodeActions(t)

	h.dialog.ShowDeviceCode(deviceCodeTestDevice())
	h.dialog.ShowAuth("https://example.com/authorize", "")
	deviceLoginTestMu.Lock()
	deviceLoginTestOpened = nil
	deviceLoginTestMu.Unlock()
	h.handleInput("\r")
	time.Sleep(20 * time.Millisecond)
	opened, copied := deviceActionSnapshot()
	if len(opened) != 0 || len(copied) != 0 {
		t.Fatalf("cleared device action still fired: opened=%v copied=%v", opened, copied)
	}

	h.dialog.ShowDeviceCode(deviceCodeTestDevice())
	h.dialog.ShowDetails([]string{"Choose a login method"})
	h.handleInput("\r")
	time.Sleep(20 * time.Millisecond)
	opened, copied = deviceActionSnapshot()
	if len(opened) != 0 || len(copied) != 0 {
		t.Fatalf("cleared device action still fired: opened=%v copied=%v", opened, copied)
	}
}

func TestLoginDialogDeviceCodeCancelDoesNotAct(t *testing.T) {
	h := newAuthURLTestDialog(t)
	stubDeviceCodeActions(t)
	h.dialog.ShowDeviceCode(deviceCodeTestDevice())
	h.handleInput("\x1b")
	h.handleInput("\r")
	time.Sleep(20 * time.Millisecond)

	opened, copied := deviceActionSnapshot()
	if len(opened) != 0 || len(copied) != 0 {
		t.Fatalf("cancel fired actions: opened=%v copied=%v", opened, copied)
	}
}

func TestLoginDialogDeviceCodeKeepsManualLoginWhenActionsFail(t *testing.T) {
	h := newAuthURLTestDialog(t)
	stubDeviceCodeActions(t)
	deviceLoginTestMu.Lock()
	deviceLoginTestCopyErr = errors.New("Clipboard unavailable")
	deviceLoginTestMu.Unlock()
	invalid := deviceCodeTestDevice()
	invalid.VerificationURI = "file:///tmp/secret"

	h.dialog.ShowDeviceCode(invalid)
	h.dialog.ShowWaiting("Waiting for authentication...")
	h.handleInput("\r")
	output := waitForDialogOutput(t, h, "Copy code manually")
	waitForDeviceActions(t, 0, 1)

	opened, copied := deviceActionSnapshot()
	if len(opened) != 0 {
		t.Fatalf("non-web URL must not be opened: %v", opened)
	}
	if len(copied) != 1 {
		t.Fatalf("copied = %v", copied)
	}
	if !strings.Contains(output, invalid.VerificationURI) ||
		!strings.Contains(output, "Enter code: "+invalid.UserCode) ||
		!strings.Contains(output, "Waiting for authentication...") {
		t.Fatalf("manual login content lost: %q", output)
	}
	if h.dialog.Context().Err() != nil {
		t.Fatal("dialog was aborted")
	}
}
