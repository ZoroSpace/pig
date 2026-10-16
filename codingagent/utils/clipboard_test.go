// clipboard_test.go — pi: test/clipboard.test.ts (rewritten in v0.86.0
// around runClipboardCommand). Pig adaptations declared at each site:
//   - pi's native clipboard (getNativeClipboard) has no Go equivalent
//     (pig's GetNativeClipboard() is nil, S6-2), so the native-read/native-
//     write cases collapse to their tool-chain outcomes: after every command
//     fails, readClipboardText resolves "" (pi null) and copyToClipboard
//     falls through to the error/OSC 52 paths.
//   - pi's process.stdout.write interception becomes the osc52Writer seam.
//   - pi's node:os platform() mock becomes the clipboardPlatformFn seam.
package utils

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

type clipboardOptionsCall struct {
	command string
	args    []string
	options *clipboardCommandOptions
}

// stubClipboardRunCommandOptions replaces the clipboardRunCommandOptionsFn
// seam for the duration of the test and records every call. responder
// receives the live options pointer.
func stubClipboardRunCommandOptions(t *testing.T, responder func(command string, args []string, options *clipboardCommandOptions) ([]byte, bool)) *[]clipboardOptionsCall {
	t.Helper()
	calls := []clipboardOptionsCall{}
	previous := clipboardRunCommandOptionsFn
	clipboardRunCommandOptionsFn = func(command string, args []string, options *clipboardCommandOptions) ([]byte, bool) {
		calls = append(calls, clipboardOptionsCall{command: command, args: append([]string(nil), args...), options: options})
		return responder(command, args, options)
	}
	t.Cleanup(func() { clipboardRunCommandOptionsFn = previous })
	return &calls
}

// stubOsc52 captures OSC 52 writes (pi: process.stdout.write interception).
func stubOsc52(t *testing.T) *[]string {
	t.Helper()
	writes := []string{}
	previous := osc52Writer
	osc52Writer = func(b []byte) (int, error) {
		writes = append(writes, string(b))
		return len(b), nil
	}
	t.Cleanup(func() { osc52Writer = previous })
	return &writes
}

// clipboardTextEnv neutralizes every env var the clipboard paths consult
// (pi: vi.stubEnv for SSH_CONNECTION/SSH_CLIENT/MOSH_CONNECTION/
// WAYLAND_DISPLAY/DISPLAY/TERMUX_VERSION to "").
func clipboardTextEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"SSH_CONNECTION", "SSH_CLIENT", "MOSH_CONNECTION", "PI_CLIPBOARD_OSC52", "WAYLAND_DISPLAY", "DISPLAY", "TERMUX_VERSION"} {
		t.Setenv(name, "")
	}
}

func stubTextPlatform(t *testing.T, platform string) {
	t.Helper()
	previous := clipboardPlatformFn
	clipboardPlatformFn = func() string { return platform }
	t.Cleanup(func() { clipboardPlatformFn = previous })
}

// TestReadClipboardTextEmptyEnvReturnsEmpty covers the no-backend case: on
// linux with no display env and no native clipboard (pig deviation), the
// read resolves "" (pi null).
func TestReadClipboardTextEmptyEnvReturnsEmpty(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "linux")
	callsPtr := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	if got := ReadClipboardText(); got != "" {
		t.Fatalf("ReadClipboardText = %q, want \"\"", got)
	}
	if calls := *callsPtr; len(calls) != 0 {
		t.Fatalf("expected no commands without display env, got %+v", calls)
	}
}

// TestReadClipboardTextCommandResultsStopFallback ports the env × tool
// matrix: a successful (or empty) read from the first available tool stops
// the fallback chain (regression #7248: empty Wayland content must not fall
// through to stale X11).
func TestReadClipboardTextCommandResultsStopFallback(t *testing.T) {
	cases := []struct {
		name     string
		wayland  bool
		x11      bool
		termux   bool
		command  string
		args     []string
		calls    []string
		lastArgs []string
	}{
		{name: "wl-paste", wayland: true, command: "wl-paste",
			args: []string{"--no-newline", "--type", "text"}, calls: []string{"wl-paste"},
			lastArgs: []string{"--no-newline", "--type", "text"}},
		{name: "xclip", x11: true, command: "xclip",
			args: []string{"-selection", "clipboard", "-out"}, calls: []string{"xclip"},
			lastArgs: []string{"-selection", "clipboard", "-out"}},
		{name: "xsel", x11: true, command: "xsel",
			args: []string{"--clipboard", "--output"}, calls: []string{"xclip", "xsel"},
			lastArgs: []string{"--clipboard", "--output"}},
		{name: "termux-clipboard-get", termux: true, command: "termux-clipboard-get",
			args: []string{}, calls: []string{"termux-clipboard-get"},
			lastArgs: []string{}},
	}
	for _, result := range []string{"clipboard text", ""} {
		for _, tc := range cases {
			t.Run(tc.name+"/"+result, func(t *testing.T) {
				clipboardTextEnv(t)
				stubTextPlatform(t, "linux")
				if tc.wayland {
					t.Setenv("WAYLAND_DISPLAY", "wayland-0")
				}
				if tc.x11 {
					t.Setenv("DISPLAY", ":0")
				}
				if tc.termux {
					t.Setenv("TERMUX_VERSION", "0.119")
				}
				callsPtr := stubClipboardRunCommandOptions(t, func(command string, args []string, options *clipboardCommandOptions) ([]byte, bool) {
					if command == tc.command {
						return []byte(result), true
					}
					return nil, false
				})

				got := ReadClipboardText()
				want := result
				if want == "" {
					want = "" // pi: bytes.toString("utf8") || null
				}
				if got != want {
					t.Fatalf("ReadClipboardText = %q, want %q", got, want)
				}
				calls := *callsPtr
				if len(calls) != len(tc.calls) {
					t.Fatalf("command calls = %+v, want %v", calls, tc.calls)
				}
				for i, want := range tc.calls {
					if calls[i].command != want {
						t.Fatalf("call %d = %s, want %s", i, calls[i].command, want)
					}
				}
				last := calls[len(calls)-1]
				if strings.Join(last.args, " ") != strings.Join(tc.lastArgs, " ") {
					t.Fatalf("last args = %v, want %v", last.args, tc.lastArgs)
				}
				if last.options == nil || last.options.TimeoutMs != 5000 {
					t.Fatalf("last timeout = %+v, want 5000", last.options)
				}
				if last.options.HasInput {
					t.Fatalf("read must not send stdin input")
				}
			})
		}
	}
}

// TestReadClipboardTextAllCommandsFail ports "uses native X11 after command
// failures": pig has no native clipboard (declared), so the failure outcome
// is "" (pi null) after the full tool chain.
func TestReadClipboardTextAllCommandsFail(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	callsPtr := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	if got := ReadClipboardText(); got != "" {
		t.Fatalf("ReadClipboardText = %q, want \"\"", got)
	}
	calls := *callsPtr
	var names []string
	for _, call := range calls {
		names = append(names, call.command)
	}
	if strings.Join(names, ",") != "wl-paste,xclip,xsel" {
		t.Fatalf("commands = %v, want [wl-paste xclip xsel]", names)
	}
}

// TestReadClipboardTextFallsBackToX11WhenWlPasteUnavailable ports "falls
// back to X11 tools when wl-paste is unavailable".
func TestReadClipboardTextFallsBackToX11WhenWlPasteUnavailable(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	callsPtr := stubClipboardRunCommandOptions(t, func(command string, args []string, options *clipboardCommandOptions) ([]byte, bool) {
		if command == "wl-paste" {
			return nil, false
		}
		return []byte("X11 text"), true
	})
	if got := ReadClipboardText(); got != "X11 text" {
		t.Fatalf("ReadClipboardText = %q, want %q", got, "X11 text")
	}
	if calls := *callsPtr; calls[0].command != "wl-paste" || calls[1].command != "xclip" {
		t.Fatalf("unexpected call order: %+v", calls)
	}
}

// TestCopyToClipboardLocalNativeSuccessSkipsOsc52 adapts "local native
// success skips OSC 52 and commands": pig's darwin write path is pbcopy
// (no native writer, declared); success must not emit OSC 52.
func TestCopyToClipboardLocalNativeSuccessSkipsOsc52(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "darwin")
	callsPtr := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, true
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard = %v", err)
	}
	if calls := *callsPtr; len(calls) != 1 || calls[0].command != "pbcopy" {
		t.Fatalf("calls = %+v, want [pbcopy]", calls)
	}
	if last := (*callsPtr)[0]; !last.options.HasInput || last.options.Input != "hello" || last.options.TimeoutMs != 5000 {
		t.Fatalf("pbcopy options = %+v, want input=hello timeout=5000", last.options)
	}
	if len(*osc52) != 0 {
		t.Fatalf("unexpected OSC 52 writes: %v", *osc52)
	}
}

// TestCopyToClipboardLinuxSkipsNativeWriter ports "Linux skips the native
// writer": xclip receives the text as stdin.
func TestCopyToClipboardLinuxSkipsNativeWriter(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("DISPLAY", ":0")
	callsPtr := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, true
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard = %v", err)
	}
	calls := *callsPtr
	if len(calls) != 1 || calls[0].command != "xclip" {
		t.Fatalf("calls = %+v, want [xclip]", calls)
	}
	if strings.Join(calls[0].args, " ") != "-selection clipboard" {
		t.Fatalf("xclip args = %v", calls[0].args)
	}
	if !calls[0].options.HasInput || calls[0].options.Input != "hello" || calls[0].options.TimeoutMs != 5000 {
		t.Fatalf("xclip options = %+v", calls[0].options)
	}
	if len(*osc52) != 0 {
		t.Fatalf("unexpected OSC 52 writes: %v", *osc52)
	}
}

// TestCopyToClipboardTriesXclipAndXselAfterWlCopyFails ports "tries xclip
// and xsel after wl-copy fails".
func TestCopyToClipboardTriesXclipAndXselAfterWlCopyFails(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	callsPtr := stubClipboardRunCommandOptions(t, func(command string, args []string, options *clipboardCommandOptions) ([]byte, bool) {
		if command == "xsel" {
			return nil, true
		}
		return nil, false
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard = %v", err)
	}
	calls := *callsPtr
	var names []string
	for _, call := range calls {
		names = append(names, call.command)
	}
	if strings.Join(names, ",") != "wl-copy,xclip,xsel" {
		t.Fatalf("commands = %v, want [wl-copy xclip xsel]", names)
	}
	if len(*osc52) != 0 {
		t.Fatalf("unexpected OSC 52 writes: %v", *osc52)
	}
}

// TestCopyToClipboardLocalLinuxFailureDoesNotEmitOsc52 ports the #9618
// regression: a local Linux failure must not report an unverified OSC 52
// write as success.
func TestCopyToClipboardLocalLinuxFailureDoesNotEmitOsc52(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("DISPLAY", ":0")
	callsPtr := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	osc52 := stubOsc52(t)
	err := CopyToClipboard("hello")
	if err == nil || err.Error() != "Clipboard unavailable: install `xclip` or `xsel`, or check X11 access" {
		t.Fatalf("error = %v", err)
	}
	var names []string
	for _, call := range *callsPtr {
		names = append(names, call.command)
	}
	if strings.Join(names, ",") != "xclip,xsel" {
		t.Fatalf("commands = %v, want [xclip xsel]", names)
	}
	if len(*osc52) != 0 {
		t.Fatalf("unexpected OSC 52 writes: %v", *osc52)
	}
}

// TestCopyToClipboardReportsWaylandTool ports "reports the Wayland clipboard
// tool instead of the X11 fallback".
func TestCopyToClipboardReportsWaylandTool(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	callsPtr := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	err := CopyToClipboard("hello")
	if err == nil || err.Error() != "Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access" {
		t.Fatalf("error = %v", err)
	}
	var names []string
	for _, call := range *callsPtr {
		names = append(names, call.command)
	}
	if strings.Join(names, ",") != "wl-copy,xclip,xsel" {
		t.Fatalf("commands = %v, want [wl-copy xclip xsel]", names)
	}
}

// TestCopyToClipboardRemoteOsc52WhenCommandsFail ports "uses OSC 52 when
// native and command writes fail in a remote session" (pig: no native
// writer, so the darwin pbcopy failure is the last local attempt).
func TestCopyToClipboardRemoteOsc52WhenCommandsFail(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "darwin")
	t.Setenv("SSH_CONNECTION", "client server")
	callsPtr := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard = %v", err)
	}
	if calls := *callsPtr; len(calls) != 1 || calls[0].command != "pbcopy" {
		t.Fatalf("calls = %+v, want [pbcopy]", calls)
	}
	if len(*osc52) != 1 {
		t.Fatalf("expected exactly one OSC 52 write, got %v", *osc52)
	}
	if !strings.HasPrefix((*osc52)[0], "\x1b]52;c;") {
		t.Fatalf("OSC 52 payload = %q", (*osc52)[0])
	}
}

// TestCopyToClipboardForcedOsc52AfterSuccessfulCopy ports "PI_CLIPBOARD_OSC52=1
// emits OSC 52 after a successful copy" (regression #10826, #10835, #10114:
// remote clients without SSH markers).
func TestCopyToClipboardForcedOsc52AfterSuccessfulCopy(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "darwin")
	t.Setenv("PI_CLIPBOARD_OSC52", "1")
	callsPtr := stubClipboardRunCommandOptions(t, func(command string, args []string, options *clipboardCommandOptions) ([]byte, bool) {
		if command == "pbcopy" {
			return nil, true
		}
		return nil, false
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard = %v", err)
	}
	if calls := *callsPtr; len(calls) != 1 || calls[0].command != "pbcopy" {
		t.Fatalf("calls = %+v, want [pbcopy]", calls)
	}
	if len(*osc52) != 1 {
		t.Fatalf("expected exactly one OSC 52 write, got %v", *osc52)
	}
	if (*osc52)[0] != "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte("hello"))+"\x07" {
		t.Fatalf("OSC 52 payload = %q", (*osc52)[0])
	}
}

// TestCopyToClipboardDoesNotEmitOversizedOsc52 ports "does not emit
// oversized OSC 52 payloads" (80_000 chars → base64 > 100_000).
func TestCopyToClipboardDoesNotEmitOversizedOsc52(t *testing.T) {
	clipboardTextEnv(t)
	stubTextPlatform(t, "darwin")
	t.Setenv("SSH_CONNECTION", "client server")
	stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	osc52 := stubOsc52(t)
	err := CopyToClipboard(strings.Repeat("x", 80_000))
	if err == nil || err.Error() != "Clipboard unavailable: text exceeds the OSC 52 size limit" {
		t.Fatalf("error = %v", err)
	}
	if len(*osc52) != 0 {
		t.Fatalf("unexpected OSC 52 writes: %d", len(*osc52))
	}
}

// clipboardTextEnv v0.86.1 补充：三个新 env 也需清零（pi: beforeEach
// stubEnv WT_SESSION/WSL_DISTRO_NAME/WSLENV to ""）。
func clipboardWSLEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"WT_SESSION", "WSL_DISTRO_NAME", "WSLENV"} {
		t.Setenv(name, "")
	}
}

// TestCopyToClipboardDisplaylessLinuxFallsBackToOsc52 ports "display-less
// Linux falls back to OSC 52" (regression #9688: containers without X11/
// Wayland access).
func TestCopyToClipboardDisplaylessLinuxFallsBackToOsc52(t *testing.T) {
	clipboardTextEnv(t)
	clipboardWSLEnv(t)
	stubTextPlatform(t, "linux")
	calls := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard error: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no commands, got %+v", *calls)
	}
	if len(*osc52) != 1 {
		t.Fatalf("osc52 writes = %d, want 1", len(*osc52))
	}
}

// TestCopyToClipboardWSLPowerShell ports "WSL without a display writes the
// Windows clipboard through PowerShell" (#9688: WSL with WSLg disabled).
func TestCopyToClipboardWSLPowerShell(t *testing.T) {
	clipboardTextEnv(t)
	clipboardWSLEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	tmpWritten := ""
	calls := stubClipboardRunCommandOptions(t, func(command string, args []string, options *clipboardCommandOptions) ([]byte, bool) {
		if command != "wslpath" {
			return nil, true
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			t.Fatalf("wslpath target unreadable: %v", err)
		}
		tmpWritten = string(raw)
		return []byte("\\\\wsl.localhost\\Ubuntu\\tmp\\clip.txt\n"), true
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("héllo"); err != nil {
		t.Fatalf("CopyToClipboard error: %v", err)
	}
	commandNames := make([]string, 0, len(*calls))
	for _, call := range *calls {
		commandNames = append(commandNames, call.command)
	}
	if strings.Join(commandNames, ",") != "wslpath,powershell.exe" {
		t.Fatalf("commands = %v, want [wslpath powershell.exe]", commandNames)
	}
	if tmpWritten != "héllo" {
		t.Fatalf("temp file content = %q, want %q", tmpWritten, "héllo")
	}
	wslpathArgs := (*calls)[0].args
	if _, err := os.Stat(wslpathArgs[1]); !os.IsNotExist(err) {
		t.Fatalf("temp file should be removed after the copy, stat err = %v", err)
	}
	powershellScript := ""
	for _, call := range *calls {
		if call.command == "powershell.exe" {
			powershellScript = call.args[2]
		}
	}
	if !strings.Contains(powershellScript, "Set-Clipboard") {
		t.Fatalf("powershell script missing Set-Clipboard: %s", powershellScript)
	}
	if !strings.Contains(powershellScript, `'\\wsl.localhost\Ubuntu\tmp\clip.txt'`) {
		t.Fatalf("powershell script missing the quoted Windows path: %s", powershellScript)
	}
	if len(*osc52) != 0 {
		t.Fatalf("unexpected OSC 52 writes: %d", len(*osc52))
	}
}

// TestCopyToClipboardWSLFallsBackToOsc52 ports "WSL falls back to OSC 52
// when Windows interop is unavailable".
func TestCopyToClipboardWSLFallsBackToOsc52(t *testing.T) {
	clipboardTextEnv(t)
	clipboardWSLEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	calls := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard error: %v", err)
	}
	commandNames := []string{}
	for _, call := range *calls {
		commandNames = append(commandNames, call.command)
	}
	if strings.Join(commandNames, ",") != "wslpath" {
		t.Fatalf("commands = %v, want [wslpath]", commandNames)
	}
	if len(*osc52) != 1 {
		t.Fatalf("osc52 writes = %d, want 1", len(*osc52))
	}
}

// TestCopyToClipboardWSLWindowsTerminalPrefersOsc52 ports "WSL in Windows
// Terminal prefers OSC 52 over PowerShell".
func TestCopyToClipboardWSLWindowsTerminalPrefersOsc52(t *testing.T) {
	clipboardTextEnv(t)
	clipboardWSLEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("WT_SESSION", "session")
	calls := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard error: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no commands, got %+v", *calls)
	}
	if len(*osc52) != 1 {
		t.Fatalf("osc52 writes = %d, want 1", len(*osc52))
	}
}

// TestCopyToClipboardWSLWindowsTerminalRemoteEmitsOnce ports "WSL in Windows
// Terminal emits OSC 52 once in a remote session".
func TestCopyToClipboardWSLWindowsTerminalRemoteEmitsOnce(t *testing.T) {
	clipboardTextEnv(t)
	clipboardWSLEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("WT_SESSION", "session")
	t.Setenv("SSH_CONNECTION", "client server")
	calls := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, false
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard error: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no commands, got %+v", *calls)
	}
	if len(*osc52) != 1 {
		t.Fatalf("osc52 writes = %d, want 1", len(*osc52))
	}
}

// TestCopyToClipboardWSLWindowsTerminalOversizedUsesPowerShell ports "WSL in
// Windows Terminal uses PowerShell for oversized OSC 52 payloads".
func TestCopyToClipboardWSLWindowsTerminalOversizedUsesPowerShell(t *testing.T) {
	clipboardTextEnv(t)
	clipboardWSLEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("WT_SESSION", "session")
	calls := stubClipboardRunCommandOptions(t, func(command string, args []string, options *clipboardCommandOptions) ([]byte, bool) {
		if command == "wslpath" {
			return []byte("C:\\clip.txt"), true
		}
		return nil, true
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard(strings.Repeat("x", 80_000)); err != nil {
		t.Fatalf("CopyToClipboard error: %v", err)
	}
	commandNames := []string{}
	for _, call := range *calls {
		commandNames = append(commandNames, call.command)
	}
	if strings.Join(commandNames, ",") != "wslpath,powershell.exe" {
		t.Fatalf("commands = %v, want [wslpath powershell.exe]", commandNames)
	}
	if len(*osc52) != 0 {
		t.Fatalf("unexpected OSC 52 writes: %d", len(*osc52))
	}
}

// TestCopyToClipboardWSLWithDisplayPrefersLinuxTools ports "WSL with a
// display prefers the Linux clipboard tools".
func TestCopyToClipboardWSLWithDisplayPrefersLinuxTools(t *testing.T) {
	clipboardTextEnv(t)
	clipboardWSLEnv(t)
	stubTextPlatform(t, "linux")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	calls := stubClipboardRunCommandOptions(t, func(string, []string, *clipboardCommandOptions) ([]byte, bool) {
		return nil, true
	})
	osc52 := stubOsc52(t)
	if err := CopyToClipboard("hello"); err != nil {
		t.Fatalf("CopyToClipboard error: %v", err)
	}
	commandNames := []string{}
	for _, call := range *calls {
		commandNames = append(commandNames, call.command)
	}
	if strings.Join(commandNames, ",") != "wl-copy" {
		t.Fatalf("commands = %v, want [wl-copy]", commandNames)
	}
	if len(*osc52) != 0 {
		t.Fatalf("unexpected OSC 52 writes: %d", len(*osc52))
	}
}
