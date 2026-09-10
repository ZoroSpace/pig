// pi: src/core/resolve-config-value.ts — resolve configuration values that
// may be shell commands, environment variables, or literals. Used by
// auth-storage.ts and model-registry.ts.
//
// Segment deviations:
//   - pi's getShellConfig() reads the process cwd implicitly; pig's
//     utils.GetShellConfig takes an explicit cwd — "" is passed (no custom
//     shell path here, so cwd is never used).
//   - executeWithDefaultShell: node's execSync uses /bin/sh -c on POSIX and
//     cmd.exe on Windows; pig mirrors both branches.
//   - process.platform probes go through configValuePlatform (a var so tests
//     can pin the platform like pi's Object.defineProperty mock); the
//     getShellConfig call goes through getShellConfigForConfigValue for the
//     same reason (pi mocks shellModule.getShellConfig).
//   - The command result cache is guarded by a mutex (pi relies on the JS
//     event loop).
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

// commandResultCache caches shell command results for the process lifetime
// (pi: commandResultCache; nil value = cached undefined).
var commandResultCache = struct {
	sync.Mutex
	m map[string]*string
}{m: map[string]*string{}}

// configValuePlatform is the platform probe (pi: process.platform reads).
var configValuePlatform = runtime.GOOS

// getShellConfigForConfigValue resolves the shell for command config values
// (pi: getShellConfig() from utils/shell.ts; var so tests can stub).
var getShellConfigForConfigValue = func() (utils.ShellConfig, error) {
	return utils.GetShellConfig("", "")
}

var envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var envVarNamePrefixRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)

// templatePart is one template segment (pi: TemplatePart union — literal |
// env; pig keeps one struct with an env marker).
type templatePart struct {
	env  bool
	text string
}

// configValueReference is a parsed config value (pi: ConfigValueReference
// union — command | template).
type configValueReference struct {
	command bool
	config  string // command form: the raw "!..." config
	parts   []templatePart
}

func appendLiteral(parts []templatePart, value string) []templatePart {
	if value == "" {
		return parts
	}
	if len(parts) > 0 && !parts[len(parts)-1].env {
		parts[len(parts)-1].text += value
		return parts
	}
	return append(parts, templatePart{text: value})
}

func parseConfigValueTemplate(config string) []templatePart {
	var parts []templatePart
	index := 0

	for index < len(config) {
		dollarIndex := strings.Index(config[index:], "$")
		if dollarIndex < 0 {
			parts = appendLiteral(parts, config[index:])
			break
		}
		dollarIndex += index

		parts = appendLiteral(parts, config[index:dollarIndex])
		var nextChar byte
		if dollarIndex+1 < len(config) {
			nextChar = config[dollarIndex+1]
		}

		if nextChar == '$' || nextChar == '!' {
			parts = appendLiteral(parts, string(nextChar))
			index = dollarIndex + 2
			continue
		}

		if nextChar == '{' {
			endIndex := strings.Index(config[dollarIndex+2:], "}")
			if endIndex < 0 {
				parts = appendLiteral(parts, "$")
				index = dollarIndex + 1
				continue
			}
			endIndex += dollarIndex + 2

			name := config[dollarIndex+2 : endIndex]
			if envVarNameRe.MatchString(name) {
				parts = append(parts, templatePart{env: true, text: name})
			} else {
				parts = appendLiteral(parts, config[dollarIndex:endIndex+1])
			}
			index = endIndex + 1
			continue
		}

		if match := envVarNamePrefixRe.FindString(config[dollarIndex+1:]); match != "" {
			parts = append(parts, templatePart{env: true, text: match})
			index = dollarIndex + 1 + len(match)
			continue
		}

		parts = appendLiteral(parts, "$")
		index = dollarIndex + 1
	}

	return parts
}

func parseConfigValueReference(config string) configValueReference {
	if strings.HasPrefix(config, "!") {
		return configValueReference{command: true, config: config}
	}

	return configValueReference{parts: parseConfigValueTemplate(config)}
}

// resolveEnvConfigValue resolves one env var, credential-scoped env first
// (pi: env?.[name] || process.env[name] || undefined — empty values fall
// through in both).
func resolveEnvConfigValue(name string, env map[string]string) string {
	if env != nil {
		if value, ok := env[name]; ok && value != "" {
			return value
		}
	}
	return os.Getenv(name)
}

func getTemplateEnvVarNames(parts []templatePart) []string {
	var names []string
	for _, part := range parts {
		if !part.env {
			continue
		}
		known := false
		for _, name := range names {
			if name == part.text {
				known = true
				break
			}
		}
		if !known {
			names = append(names, part.text)
		}
	}
	return names
}

func resolveTemplate(parts []templatePart, env map[string]string) (string, bool) {
	var resolved strings.Builder
	for _, part := range parts {
		if !part.env {
			resolved.WriteString(part.text)
			continue
		}
		envValue := resolveEnvConfigValue(part.text, env)
		if envValue == "" {
			return "", false
		}
		resolved.WriteString(envValue)
	}
	return resolved.String(), true
}

// GetConfigValueEnvVarName returns the env var name when the config value is
// exactly one env reference (pi: getConfigValueEnvVarName).
func GetConfigValueEnvVarName(config string) (string, bool) {
	reference := parseConfigValueReference(config)
	if reference.command {
		return "", false
	}
	if len(reference.parts) == 1 && reference.parts[0].env {
		return reference.parts[0].text, true
	}
	return "", false
}

// GetConfigValueEnvVarNames returns all referenced env var names (pi:
// getConfigValueEnvVarNames).
func GetConfigValueEnvVarNames(config string) []string {
	reference := parseConfigValueReference(config)
	if reference.command {
		return []string{}
	}
	return getTemplateEnvVarNames(reference.parts)
}

// GetMissingConfigValueEnvVarNames returns referenced env var names that are
// not set (pi: getMissingConfigValueEnvVarNames).
func GetMissingConfigValueEnvVarNames(config string, env map[string]string) []string {
	names := GetConfigValueEnvVarNames(config)
	missing := make([]string, 0, len(names))
	for _, name := range names {
		if resolveEnvConfigValue(name, env) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

// IsCommandConfigValue reports whether the value is a shell command (pi:
// isCommandConfigValue).
func IsCommandConfigValue(config string) bool {
	return parseConfigValueReference(config).command
}

// IsConfigValueConfigured reports whether all referenced env vars are set
// (pi: isConfigValueConfigured).
func IsConfigValueConfigured(config string, env map[string]string) bool {
	return len(GetMissingConfigValueEnvVarNames(config, env)) == 0
}

// ResolveConfigValue resolves a config value (API key, header value, etc.)
// to an actual value (pi: resolveConfigValue):
//   - "!..." executes the rest as a shell command and uses stdout (cached)
//   - "$ENV_VAR" / "${ENV_VAR}" interpolates the named environment variable
//   - in non-command values, "$$" escapes a literal "$" and "$!" escapes a
//     literal "!"
//   - otherwise the value is a literal
func ResolveConfigValue(config string, env map[string]string) (string, bool) {
	reference := parseConfigValueReference(config)
	if reference.command {
		return executeCommand(reference.config)
	}
	return resolveTemplate(reference.parts, env)
}

// executeWithConfiguredShell runs a command through the configured shell
// (pi: executeWithConfiguredShell).
func executeWithConfiguredShell(command string) (executed bool, value string) {
	shellConfig, err := getShellConfigForConfigValue()
	if err != nil {
		return false, ""
	}
	commandFromStdin := shellConfig.CommandTransport == "stdin"

	args := shellConfig.Args
	if !commandFromStdin {
		args = append(append([]string(nil), args...), command)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shellConfig.Shell, args...)
	if commandFromStdin {
		cmd.Stdin = strings.NewReader(command)
	} else {
		cmd.Stdin = nil // pi: stdio ["ignore", ...]
	}
	cmd.Stderr = nil
	stdout, err := cmd.Output()
	if err != nil {
		// pi: result.error with code ENOENT → not executed; any other error
		// (non-zero status, timeout) → executed, undefined.
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return false, ""
		}
		return true, ""
	}
	value = strings.TrimSpace(string(stdout))
	if value == "" {
		return true, ""
	}
	return true, value
}

// executeWithDefaultShell runs a command through the default shell (pi:
// executeWithDefaultShell — node execSync: /bin/sh -c on POSIX, cmd.exe on
// Windows).
func executeWithDefaultShell(command string) (string, bool) {
	shell := []string{"/bin/sh", "-c"}
	if configValuePlatform == "windows" {
		shell = []string{"cmd", "/c"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell[0], append(shell[1:], command)...)
	cmd.Stdin = nil
	cmd.Stderr = nil
	output, err := cmd.Output()
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", false
	}
	return value, true
}

// executeCommandUncached resolves a command config value without the cache
// (pi: executeCommandUncached).
func executeCommandUncached(commandConfig string) (string, bool) {
	command := commandConfig[1:]
	if configValuePlatform == "windows" {
		executed, value := executeWithConfiguredShell(command)
		if executed {
			return value, value != ""
		}
		return executeWithDefaultShell(command)
	}
	return executeWithDefaultShell(command)
}

// executeCommand resolves a command config value through the process-lifetime
// cache (pi: executeCommand).
func executeCommand(commandConfig string) (string, bool) {
	commandResultCache.Lock()
	cached, ok := commandResultCache.m[commandConfig]
	commandResultCache.Unlock()
	if ok {
		if cached == nil {
			return "", false
		}
		return *cached, true
	}

	value, found := executeCommandUncached(commandConfig)
	var stored *string
	if found {
		valueCopy := value
		stored = &valueCopy
	}
	commandResultCache.Lock()
	commandResultCache.m[commandConfig] = stored
	commandResultCache.Unlock()
	return value, found
}

// ResolveConfigValueUncached resolves without consulting or filling the
// command cache (pi: resolveConfigValueUncached — used for header values).
func ResolveConfigValueUncached(config string, env map[string]string) (string, bool) {
	reference := parseConfigValueReference(config)
	if reference.command {
		return executeCommandUncached(reference.config)
	}
	return resolveTemplate(reference.parts, env)
}

// ResolveConfigValueOrThrow resolves or errors with pi's exact messages (pi:
// resolveConfigValueOrThrow).
func ResolveConfigValueOrThrow(config string, description string, env map[string]string) (string, error) {
	resolvedValue, found := ResolveConfigValueUncached(config, env)
	if found {
		return resolvedValue, nil
	}

	reference := parseConfigValueReference(config)
	if reference.command {
		return "", fmt.Errorf("Failed to resolve %s from shell command: %s", description, reference.config[1:])
	}

	missingEnvVars := GetMissingConfigValueEnvVarNames(config, env)
	if len(missingEnvVars) == 1 {
		return "", fmt.Errorf("Failed to resolve %s from environment variable: %s", description, missingEnvVars[0])
	}
	if len(missingEnvVars) > 1 {
		return "", fmt.Errorf("Failed to resolve %s from environment variables: %s", description, strings.Join(missingEnvVars, ", "))
	}

	return "", fmt.Errorf("Failed to resolve %s", description)
}

// ResolveHeaders resolves all header values using the same resolution logic
// as API keys (pi: resolveHeaders).
func ResolveHeaders(headers map[string]string, env map[string]string) (map[string]string, bool) {
	if headers == nil {
		return nil, false
	}
	resolved := map[string]string{}
	for key, value := range headers {
		resolvedValue, found := ResolveConfigValue(value, env)
		if found && resolvedValue != "" {
			resolved[key] = resolvedValue
		}
	}
	if len(resolved) == 0 {
		return nil, false
	}
	return resolved, true
}

// ResolveHeadersOrThrow resolves all header values, erroring on failure (pi:
// resolveHeadersOrThrow).
func ResolveHeadersOrThrow(headers map[string]string, description string, env map[string]string) (map[string]string, error) {
	if headers == nil {
		return nil, nil
	}
	resolved := map[string]string{}
	for key, value := range headers {
		resolvedValue, err := ResolveConfigValueOrThrow(value, fmt.Sprintf("%s header %q", description, key), env)
		if err != nil {
			return nil, err
		}
		resolved[key] = resolvedValue
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	return resolved, nil
}

// ClearConfigValueCache clears the command cache (pi:
// clearConfigValueCache — exported for testing).
func ClearConfigValueCache() {
	commandResultCache.Lock()
	commandResultCache.m = map[string]*string{}
	commandResultCache.Unlock()
}
