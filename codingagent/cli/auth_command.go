// pi: src/cli/auth-command.ts — auth subcommand parsing and validation.
package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent"
)

// AuthCommandKind is one auth subcommand (pi: AuthCommandKind).
type AuthCommandKind = string

const (
	AuthCommandCheck       AuthCommandKind = "check"
	AuthCommandAPIKey      AuthCommandKind = "api_key"
	AuthCommandBearerToken AuthCommandKind = "bearer_token"
)

// AuthCommand is a parsed auth subcommand (pi: AuthCommand).
type AuthCommand struct {
	Kind       AuthCommandKind
	Args       []string
	JSON       bool
	Credential bool
	NoRefresh  bool
	// MinExpiryMs is the --min-expiry duration; nil = not provided (pi:
	// minExpiryMs?: number).
	MinExpiryMs *int64
}

// AuthCommandError is an auth command parse/validation failure (pi:
// AuthCommandError).
type AuthCommandError struct {
	Message string
}

func (e *AuthCommandError) Error() string { return e.Message }

// authCommandUsage mirrors AUTH_COMMAND_USAGE (pi: record literal).
var authCommandUsage = map[AuthCommandKind]string{
	AuthCommandCheck:       fmt.Sprintf("%s auth check --provider <provider> [--json] [--credentials] [--no-refresh]", codingagent.AppName),
	AuthCommandAPIKey:      fmt.Sprintf("%s auth print-api-key --provider <provider> [--model <model>]", codingagent.AppName),
	AuthCommandBearerToken: fmt.Sprintf("%s auth print-bearer-token --provider <provider> [--model <model>] [--min-expiry <duration>]", codingagent.AppName),
}

// GetAuthCommandName returns the display name of an auth command (pi:
// getAuthCommandName).
func GetAuthCommandName(kind AuthCommandKind) string {
	switch kind {
	case AuthCommandCheck:
		return "auth check"
	case AuthCommandAPIKey:
		return "auth print-api-key"
	default:
		return "auth print-bearer-token"
	}
}

// GetAuthCommandUsage returns the usage line of an auth command (pi:
// getAuthCommandUsage).
func GetAuthCommandUsage(kind AuthCommandKind) string {
	return authCommandUsage[kind]
}

// IsAuthCommandHelp reports whether the args invoke auth help (pi:
// isAuthCommandHelp).
func IsAuthCommandHelp(args []string) bool {
	if len(args) == 0 || args[0] != "auth" {
		return false
	}
	if len(args) == 1 || args[1] == "help" {
		return true
	}
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

// PrintAuthCommandHelp prints auth command usage (pi: printAuthCommandHelp).
func PrintAuthCommandHelp() {
	fmt.Printf(`Usage:
  %s auth print-api-key [--provider <provider>] [--model <model>]
  %s auth print-bearer-token [--provider <provider>] [--model <model>] [--min-expiry <duration>]
  %s auth check [--provider <provider>] [--model <model>] [--json] [--credentials] [--no-refresh]

Auth commands require at least one of --provider or --model. Checks refresh expired OAuth credentials by default; --no-refresh prevents this. --credentials emits the credential, or includes it in JSON output.`,
		codingagent.AppName, codingagent.AppName, codingagent.AppName)
	fmt.Println()
}

// ParseAuthCommand parses an auth subcommand; ok=false when args are not an
// auth command at all (pi: parseAuthCommand returns undefined). Errors are
// reported as *AuthCommandError.
func ParseAuthCommand(args []string) (*AuthCommand, error) {
	if len(args) == 0 || args[0] != "auth" {
		return nil, nil
	}

	var kind AuthCommandKind
	switch {
	case len(args) > 1 && args[1] == "check":
		kind = AuthCommandCheck
	case len(args) > 1 && args[1] == "print-api-key":
		kind = AuthCommandAPIKey
	case len(args) > 1 && args[1] == "print-bearer-token":
		kind = AuthCommandBearerToken
	default:
		subcommand := ""
		if len(args) > 1 {
			subcommand = args[1]
		}
		return nil, &AuthCommandError{Message: fmt.Sprintf(
			"Unknown auth command %q. Use %q, %q, or %q.",
			subcommand,
			codingagent.AppName+" auth print-api-key",
			codingagent.AppName+" auth print-bearer-token",
			codingagent.AppName+" auth check",
		)}
	}

	var commandArgs []string
	json := false
	credentials := false
	noRefresh := false
	var minExpiryMs *int64
	for index := 2; index < len(args); index++ {
		arg := args[index]
		if arg == "--min-expiry" {
			if kind != AuthCommandBearerToken {
				return nil, &AuthCommandError{Message: "--min-expiry is only supported by print-bearer-token"}
			}
			index++
			value := ""
			if index < len(args) {
				value = args[index]
			}
			ms, ok := parseMinExpiry(value)
			if !ok {
				return nil, &AuthCommandError{Message: "--min-expiry must use a duration such as 30m or 1h"}
			}
			minExpiryMs = &ms
			continue
		}
		if arg == "--json" || arg == "--credentials" || arg == "--no-refresh" {
			if kind != AuthCommandCheck {
				return nil, &AuthCommandError{Message: fmt.Sprintf("%s is only supported by auth check", arg)}
			}
			switch arg {
			case "--json":
				json = true
			case "--credentials":
				credentials = true
			default:
				noRefresh = true
			}
			continue
		}
		commandArgs = append(commandArgs, arg)
	}

	return &AuthCommand{
		Kind:        kind,
		Args:        commandArgs,
		JSON:        json,
		Credential:  credentials,
		NoRefresh:   noRefresh,
		MinExpiryMs: minExpiryMs,
	}, nil
}

// minExpiryPattern matches durations like 30m or 1h (pi:
// /^(\d+)(ms|s|m|h)$/iu).
var minExpiryPattern = regexp.MustCompile(`^(\d+)(ms|s|m|h)$`)

// parseMinExpiry converts a duration string to milliseconds (pi's inline
// regex + unit multiplier chain).
func parseMinExpiry(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	match := minExpiryPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, false
	}
	amount, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, false
	}
	var unitMs int64
	switch match[2] {
	case "ms":
		unitMs = 1
	case "s":
		unitMs = 1_000
	case "m":
		unitMs = 60_000
	default: // "h"
		unitMs = 3_600_000
	}
	return amount * unitMs, true
}

// AuthCommandTarget is validateAuthCommandArgs' resolved provider/model (pi:
// the inline `{ provider?: string; model?: string }`).
type AuthCommandTarget struct {
	Provider string
	Model    string
}

// ValidateAuthCommandArgs validates auth command arguments (pi:
// validateAuthCommandArgs). Errors are *AuthCommandError.
func ValidateAuthCommandArgs(args *Args, kind AuthCommandKind) (AuthCommandTarget, error) {
	provider := ""
	if args.Provider != nil {
		provider = trimOrDefault(*args.Provider)
	}
	model := ""
	if args.Model != nil {
		model = trimOrDefault(*args.Model)
	}
	if len(args.UnknownFlags) > 0 {
		option := args.UnknownFlagKeys[0]
		return AuthCommandTarget{}, &AuthCommandError{Message: fmt.Sprintf(
			"Unknown option --%s for %q.", option, GetAuthCommandName(kind))}
	}
	if args.APIKey != nil || len(args.Messages) > 0 || len(args.FileArgs) > 0 {
		return AuthCommandTarget{}, &AuthCommandError{Message: "Auth commands only accept --provider and --model"}
	}
	if kind == AuthCommandCheck {
		if provider == "" && model == "" {
			return AuthCommandTarget{}, &AuthCommandError{Message: "Auth checks require --provider <provider> or --model <model>"}
		}
		return AuthCommandTarget{Provider: provider, Model: model}, nil
	}
	if provider == "" && model == "" {
		return AuthCommandTarget{}, &AuthCommandError{Message: "Credential printing requires --provider <provider> or --model <model>"}
	}
	return AuthCommandTarget{Provider: provider, Model: model}, nil
}

// trimOrDefault trims a string; empty stays empty (pi's `x?.trim() ||
// undefined` — pig represents undefined as "").
func trimOrDefault(value string) string {
	return strings.TrimSpace(value)
}

// bearerPattern extracts the credential from an "Authorization: Bearer X"
// header value (pi: /^Bearer\s+(.+)$/iu).
var bearerPattern = regexp.MustCompile(`(?i)^Bearer\s+(.+)$`)

// GetAuthCredential extracts the printable credential from an auth result
// (pi: getAuthCredential). Header lookup uses a deterministic scan (pi's
// Object.entries order is insertion order; pig's header map is unordered,
// and at most one Authorization header exists in practice).
func GetAuthCredential(authResult *auth.AuthResult) string {
	if authResult == nil {
		return ""
	}
	if authResult.Auth.APIKey != "" {
		return authResult.Auth.APIKey
	}
	names := make([]string, 0, len(authResult.Auth.Headers))
	for name := range authResult.Auth.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.EqualFold(name, "authorization") {
			if header := authResult.Auth.Headers[name]; header != nil {
				if m := bearerPattern.FindStringSubmatch(*header); m != nil {
					return m[1]
				}
			}
		}
	}
	return ""
}
