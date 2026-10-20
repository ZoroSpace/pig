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
	"path"
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
// isLegacyWslBashPath). When SystemRoot is known, compare against the actual
// Windows installation directories; otherwise fall back to the conventional
// drive:\windows paths.
func isLegacyWslBashPath(path string) bool {
	normalized := normalizeWindowsPath(path)
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot != "" {
		if !windowsDriveRootRe.MatchString(systemRoot) {
			return false
		}
		return normalized == normalizeWindowsPath(joinWindowsPath(systemRoot, "System32", "bash.exe")) ||
			normalized == normalizeWindowsPath(joinWindowsPath(systemRoot, "Sysnative", "bash.exe"))
	}
	return legacyWslBashPathRe.MatchString(normalized)
}

// windowsDriveRootRe matches an absolute Windows drive path (pi:
// /^[a-z]:[\\/]/i in isLegacyWslBashPath).
var windowsDriveRootRe = regexp.MustCompile(`^[a-zA-Z]:[\\/]`)

// joinWindowsPath joins Windows path segments independent of the host OS (pi:
// win32.join; filepath.Join uses the host separator, and its Clean treats
// backslashes as ordinary characters on Unix).
func joinWindowsPath(parts ...string) string {
	converted := make([]string, len(parts))
	for i, part := range parts {
		converted[i] = strings.ReplaceAll(part, "\\", "/")
	}
	return strings.ReplaceAll(path.Join(converted...), "/", "\\")
}

// normalizeWindowsPath normalizes a Windows path for case-insensitive
// comparison (pi: win32.normalize(...).toLowerCase()).
func normalizeWindowsPath(value string) string {
	slashForm := strings.ReplaceAll(value, "\\", "/")
	cleaned := path.Clean(slashForm)
	return strings.ToLower(strings.ReplaceAll(cleaned, "/", "\\"))
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
			if firstMatch != "" && shellPathExists(firstMatch) {
				return firstMatch
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
		if shellPathExists(resolved) {
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
			if shellPathExists(path) {
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
	if shellPathExists("/bin/bash") {
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

// shellLookup is the platform/environment seam for GetPowerShellConfig (pi
// tests stub process.platform, env, fs.accessSync, and spawnSync).
type shellLookup struct {
	goos       string
	getenv     func(string) string
	pathExists func(string) bool
	findOnPath func(string) string
}

func defaultShellLookup() shellLookup {
	return shellLookup{
		goos:       runtime.GOOS,
		getenv:     os.Getenv,
		pathExists: shellPathExists,
		findOnPath: findExecutableOnPath,
	}
}

// GetPowerShellConfig resolves PowerShell on Windows, preferring PowerShell 7
// when available (pi: getPowerShellConfig).
func GetPowerShellConfig() (ShellConfig, error) {
	return getPowerShellConfig(defaultShellLookup())
}

func getPowerShellConfig(lookup shellLookup) (ShellConfig, error) {
	if lookup.goos != "windows" {
		return ShellConfig{}, fmt.Errorf("The powershell tool is only available on Windows.")
	}
	shell := lookup.findOnPath("pwsh.exe")
	// Installation directories may be on any drive and need not be on PATH.
	if shell == "" {
		if programFiles := lookup.getenv("ProgramFiles"); programFiles != "" {
			pwsh7 := joinWindowsPath(programFiles, "PowerShell", "7", "pwsh.exe")
			if lookup.pathExists(pwsh7) {
				shell = pwsh7
			}
		}
	}
	if shell == "" {
		shell = lookup.findOnPath("powershell.exe")
	}
	if shell == "" {
		if systemRoot := lookup.getenv("SystemRoot"); systemRoot != "" {
			windowsPowerShell := joinWindowsPath(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
			if lookup.pathExists(windowsPowerShell) {
				shell = windowsPowerShell
			}
		}
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
var sanitizeBinaryOutputPattern = regexp.MustCompile("[\x00-\x08\x0B\x0C\x0E-\x1F\uFFF9-\uFFFB]")

func SanitizeBinaryOutput(str string) string {
	// Single regex replace (pi v0.99.1): all removed characters are single
	// code points, so surrogate pairs are never split. pi's rationale: the
	// removed set shrank to the characters that crash string-width or break
	// display.
	return sanitizeBinaryOutputPattern.ReplaceAllString(str, "")
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
