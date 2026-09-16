// pi: src/utils/shell.ts — shell resolution, environment assembly, and
// process-tree management for the coding-agent shell tools.
//
// This is pi's own duplicate of the agent package's env/shell helpers (the
// two TS copies drifted slightly); pig mirrors the duplication and ports
// this copy verbatim.
//
// Segment deviations:
//   - existsSync → os.Stat; spawnSync which/where → exec.Command with
//     timeout.
//   - getShellEnv returns the child environment as a []string slice
//     (NodeJS.ProcessEnv → string pairs; the PATH key lookup is
//     case-insensitive over os.Environ as in pi).
//   - sanitizeBinaryOutput iterates runes (pi iterates code points via
//     Array.from — equivalent for UTF-8 input).
package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/codingagent"
)

// legacyWslBashPathRe matches Windows' legacy WSL bash paths (pi:
// isLegacyWslBashPath's regex).
var legacyWslBashPathRe = regexp.MustCompile(`^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)

// ShellConfig selects the shell binary and argument transport (pi:
// ShellConfig).
type ShellConfig struct {
	Shell string
	Args  []string
	// CommandTransport is "" (argv) or "stdin" (pi: "argv" | "stdin" |
	// undefined; undefined collapses to "").
	CommandTransport string
}

// isLegacyWslBashPath reports whether the path is a legacy WSL bash (pi:
// isLegacyWslBashPath).
func isLegacyWslBashPath(path string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(path, "/", "\\"))
	return legacyWslBashPathRe.MatchString(normalized)
}

// getBashShellConfig picks the command transport for a bash path (pi:
// getBashShellConfig).
func getBashShellConfig(shell string) ShellConfig {
	if isLegacyWslBashPath(shell) {
		return ShellConfig{Shell: shell, Args: []string{"-s"}, CommandTransport: "stdin"}
	}
	return ShellConfig{Shell: shell, Args: []string{"-c"}}
}

// findExecutableOnPath locates an executable via which/where (pi:
// findExecutableOnPath); "" when not found.
func findExecutableOnPath(executable string) string {
	if runtime.GOOS == "windows" {
		// Windows: Use 'where' and verify file exists (where can return
		// non-existent paths).
		if stdout, err := runSyncCapture("where", []string{executable}, 5*time.Second); err == nil && stdout != "" {
			firstMatch := strings.TrimSpace(strings.SplitN(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n", 2)[0])
			if firstMatch != "" {
				if _, err := os.Stat(firstMatch); err == nil {
					return firstMatch
				}
			}
		}
		return ""
	}
	// Unix: Use 'which' and trust its output (handles Termux and special
	// filesystems).
	stdout, err := runSyncCapture("which", []string{executable}, 5*time.Second)
	if err != nil || stdout == "" {
		return ""
	}
	firstMatch := strings.TrimSpace(strings.SplitN(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n", 2)[0])
	return firstMatch
}

// runSyncCapture runs a command synchronously and captures stdout (pi:
// spawnSync with encoding utf-8 + timeout).
func runSyncCapture(command string, args []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	stdout, err := cmd.Output()
	return string(stdout), err
}

// GetShellConfig resolves the shell configuration based on platform and an
// optional explicit shell path (pi: getShellConfig; pi resolves relative
// custom paths against the process cwd, pig against the supplied cwd).
// Resolution errors are returned as plain errors (pi: throws).
func GetShellConfig(cwd string, customShellPath string) (ShellConfig, error) {
	// 1. Check user-specified shell path
	if customShellPath != "" {
		resolved := customShellPath
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(cwd, resolved)
		}
		if _, err := os.Stat(resolved); err == nil {
			// The transport decision is made on the raw path (pi:
			// getBashShellConfig(customShellPath)); pig spawns the resolved
			// path (os/exec resolves relative command paths against the
			// process cwd, not cmd.Dir).
			config := getBashShellConfig(customShellPath)
			config.Shell = resolved
			return config, nil
		}
		return ShellConfig{}, fmt.Errorf("Custom shell path not found: %s", customShellPath)
	}

	if runtime.GOOS == "windows" {
		// 2. Try Git Bash in known locations
		var paths []string
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			paths = append(paths, filepath.Join(programFiles, "Git", "bin", "bash.exe"))
		}
		if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
			paths = append(paths, filepath.Join(programFilesX86, "Git", "bin", "bash.exe"))
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return getBashShellConfig(path), nil
			}
		}

		// 3. Fallback: search bash.exe on PATH (Cygwin, MSYS2, WSL, etc.)
		if bashOnPath := findExecutableOnPath("bash.exe"); bashOnPath != "" {
			return getBashShellConfig(bashOnPath), nil
		}

		detail := ""
		for _, path := range paths {
			detail += fmt.Sprintf("  %s\n", path)
		}
		return ShellConfig{}, fmt.Errorf("No bash shell found. Options:\n"+
			"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
			"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n"+
			"  3. Set shellPath in settings.json\n\n"+
			"Searched Git Bash in:\n%s", detail)
	}

	// Unix: try /bin/bash, then bash on PATH, then fallback to sh
	if _, err := os.Stat("/bin/bash"); err == nil {
		return getBashShellConfig("/bin/bash"), nil
	}
	if bashOnPath := findExecutableOnPath("bash"); bashOnPath != "" {
		return getBashShellConfig(bashOnPath), nil
	}
	return ShellConfig{Shell: "sh", Args: []string{"-c"}}, nil
}

// PowerShellArgs are the fixed arguments for PowerShell invocations (pi:
// POWERSHELL_ARGS).
var PowerShellArgs = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}

// GetPowerShellConfig resolves PowerShell on Windows, preferring PowerShell 7
// when available (pi: getPowerShellConfig).
func GetPowerShellConfig() (ShellConfig, error) {
	if runtime.GOOS != "windows" {
		return ShellConfig{}, fmt.Errorf("The powershell tool is only available on Windows.")
	}
	shell := findExecutableOnPath("pwsh.exe")
	if shell == "" {
		shell = findExecutableOnPath("powershell.exe")
	}
	if shell == "" {
		return ShellConfig{}, fmt.Errorf("No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH.")
	}
	return ShellConfig{Shell: shell, Args: append([]string{}, PowerShellArgs...)}, nil
}

// GetShellEnv assembles the child environment with the managed bin dir
// prepended to PATH (pi: getShellEnv).
func GetShellEnv() []string {
	binDir := codingagent.GetBinDir()
	pathKey := "PATH"
	env := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[key] = value
			if strings.EqualFold(key, "PATH") {
				pathKey = key
			}
		}
	}
	currentPath := env[pathKey]
	var pathEntries []string
	for _, entry := range strings.Split(currentPath, string(os.PathListSeparator)) {
		if entry != "" {
			pathEntries = append(pathEntries, entry)
		}
	}
	hasBinDir := false
	for _, entry := range pathEntries {
		if entry == binDir {
			hasBinDir = true
			break
		}
	}
	if !hasBinDir {
		updated := []string{binDir}
		if currentPath != "" {
			updated = append(updated, currentPath)
		}
		env[pathKey] = strings.Join(updated, string(os.PathListSeparator))
	}
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

// SanitizeBinaryOutput strips characters that crash string-width or cause
// display issues (pi: sanitizeBinaryOutput): control characters except
// tab/newline/carriage-return, and the interlinear-annotation range.
func SanitizeBinaryOutput(str string) string {
	var b strings.Builder
	b.Grow(len(str))
	for _, char := range str {
		code := char
		if code == 0x09 || code == 0x0a || code == 0x0d {
			b.WriteRune(char)
			continue
		}
		if code <= 0x1f {
			continue
		}
		if code >= 0xfff9 && code <= 0xfffb {
			continue
		}
		b.WriteRune(char)
	}
	return b.String()
}

// trackedDetachedChildPids records detached child processes so they can be
// killed on parent shutdown signals (pi: trackedDetachedChildPids).
var trackedDetachedChildPids = struct {
	sync.Mutex
	pids map[int]struct{}
}{pids: map[int]struct{}{}}

// TrackDetachedChildPid records a live detached child (pi:
// trackDetachedChildPid).
func TrackDetachedChildPid(pid int) {
	trackedDetachedChildPids.Lock()
	trackedDetachedChildPids.pids[pid] = struct{}{}
	trackedDetachedChildPids.Unlock()
}

// UntrackDetachedChildPid drops a settled detached child (pi:
// untrackDetachedChildPid).
func UntrackDetachedChildPid(pid int) {
	trackedDetachedChildPids.Lock()
	delete(trackedDetachedChildPids.pids, pid)
	trackedDetachedChildPids.Unlock()
}

// KillTrackedDetachedChildren kills all tracked detached children (pi:
// killTrackedDetachedChildren).
func KillTrackedDetachedChildren() {
	trackedDetachedChildPids.Lock()
	pids := make([]int, 0, len(trackedDetachedChildPids.pids))
	for pid := range trackedDetachedChildPids.pids {
		pids = append(pids, pid)
	}
	trackedDetachedChildPids.pids = map[int]struct{}{}
	trackedDetachedChildPids.Unlock()
	for _, pid := range pids {
		KillProcessTree(pid)
	}
}

// KillProcessTree is per-platform (compile-time dispatch replaced the
// runtime GOOS branch in the Windows compile port, WINDOWS-SUPPORT):
// shell_kill_unix.go / shell_kill_windows.go.

// systemRoot was moved to shell_kill_windows.go (Windows-only).

// KillTrackedDetachedChildren kills all tracked detached children (pi:
// killTrackedDetachedChildren).
