// pi: src/cli/args.ts — CLI argument parsing and help display.
package cli

import (
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/constants"
)

// Mode selects the CLI output mode (pi: Mode = "text" | "json" | "rpc").
type Mode = string

const (
	ModeText Mode = "text"
	ModeJSON Mode = "json"
	ModeRPC  Mode = "rpc"
)

// ArgDiagnostic is one CLI parse diagnostic (pi: Args.diagnostics members,
// `{ type: "warning" | "error"; message: string }`).
type ArgDiagnostic struct {
	Type    string
	Message string
}

const (
	ArgDiagnosticWarning = "warning"
	ArgDiagnosticError   = "error"
)

// Args is the parsed CLI invocation (pi: Args). Optional fields stay
// pointers: distinguishing "flag absent" from the zero value is part of the
// CLI semantics.
type Args struct {
	Provider           *string
	Model              *string
	APIKey             *string
	SystemPrompt       *string
	AppendSystemPrompt []string
	Thinking           *agent.ThinkingLevel
	Continue           *bool
	Resume             *bool
	Help               *bool
	Version            *bool
	Mode               *Mode
	Name               *string
	NoSession          *bool
	Session            *string
	SessionID          *string
	Fork               *string
	SessionDir         *string
	Models             []string
	Tools              []string
	ExcludeTools       []string
	NoMcp              *bool
	NoTools            *bool
	NoBuiltinTools     *bool
	Extensions         []string
	NoExtensions       *bool
	Print              *bool
	Export             *string
	NoSkills           *bool
	Skills             []string
	PromptTemplates    []string
	NoPromptTemplates  *bool
	Themes             []string
	UseTheme           *string
	NoThemes           *bool
	NoContextFiles     *bool
	ListModels         any // pi: string | true — provider filter string, or true for all
	Offline            *bool
	// TuiMode selects the interactive renderer mode (pi: tuiMode?: TuiMode
	// from core/settings-manager). pi's import is type-only (erased at
	// runtime); Go has no type-only imports and core already imports cli for
	// the thinking-level vocabulary, so the field is a plain string —
	// core.TuiMode is a `= string` alias and values interoperate directly.
	TuiMode              *string
	Verbose              *bool
	ProjectTrustOverride *bool
	Messages             []string
	FileArgs             []string
	// UnknownFlags are unknown flags (potentially extension flags) - map of
	// flag name to value (pi: Map<string, boolean | string>). JS Maps keep
	// insertion order; the parallel UnknownFlagKeys slice carries it (pi's
	// validateAuthCommandArgs reports the first-inserted unknown flag).
	UnknownFlags    map[string]any
	UnknownFlagKeys []string
	Diagnostics     []ArgDiagnostic
}

// IsValidThinkingLevel reports whether level is one of the valid thinking
// levels (pi: isValidThinkingLevel). The vocabulary lives in the constants
// package (see constants/thinking.go for the relocation rationale); the CLI
// keeps the name consumers previously used.
func IsValidThinkingLevel(level string) bool {
	return constants.IsValidThinkingLevel(level)
}

// NormalizeSessionName trims a session name; ok is false when the trimmed
// value is empty (pi: normalizeSessionName returns string | undefined).
func NormalizeSessionName(value string) (string, bool) {
	name := strings.TrimSpace(value)
	return name, len(name) > 0
}

// ParseArgs parses CLI arguments (pi: parseArgs).
func ParseArgs(args []string) Args {
	result := Args{
		UnknownFlags: map[string]any{},
	}

	pushDiagnostic := func(typ, message string) {
		result.Diagnostics = append(result.Diagnostics, ArgDiagnostic{Type: typ, Message: message})
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			for _, positionalArg := range args[i+1:] {
				if strings.HasPrefix(positionalArg, "@") {
					result.FileArgs = append(result.FileArgs, strings.TrimPrefix(positionalArg, "@"))
				} else {
					result.Messages = append(result.Messages, positionalArg)
				}
			}
			return result
		case arg == "--help" || arg == "-h":
			result.Help = boolPtr(true)
		case arg == "--version" || arg == "-v":
			result.Version = boolPtr(true)
		case arg == "--mode":
			// pi e40126f57 (#9045): reject invalid --mode values instead of
			// silently ignoring them. A missing or flag-like value reports the
			// reminder diagnostic without consuming the next argument; an
			// invalid value is consumed and reported.
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				pushDiagnostic(ArgDiagnosticError, "--mode requires text, json, or rpc")
				break
			}
			i++
			mode := args[i]
			if mode != "text" && mode != "json" && mode != "rpc" {
				pushDiagnostic(ArgDiagnosticError, fmt.Sprintf("Invalid mode \"%s\". Valid values: text, json, rpc", mode))
				break
			}
			m := Mode(mode)
			result.Mode = &m
		case arg == "--continue" || arg == "-c":
			result.Continue = boolPtr(true)
		case arg == "--resume" || arg == "-r":
			result.Resume = boolPtr(true)
		case arg == "--provider" && i+1 < len(args):
			i++
			result.Provider = &args[i]
		case arg == "--model" && i+1 < len(args):
			i++
			result.Model = &args[i]
		case arg == "--api-key" && i+1 < len(args):
			i++
			result.APIKey = &args[i]
		case arg == "--system-prompt" && i+1 < len(args):
			i++
			result.SystemPrompt = &args[i]
		case arg == "--append-system-prompt" && i+1 < len(args):
			i++
			result.AppendSystemPrompt = append(result.AppendSystemPrompt, args[i])
		case arg == "--name" || arg == "-n":
			if i+1 < len(args) {
				i++
				result.Name = &args[i]
			} else {
				pushDiagnostic(ArgDiagnosticError, "--name requires a value")
			}
		case arg == "--no-session":
			result.NoSession = boolPtr(true)
		case arg == "--session" && i+1 < len(args):
			i++
			result.Session = &args[i]
		case arg == "--session-id" && i+1 < len(args):
			i++
			result.SessionID = &args[i]
		case arg == "--fork" && i+1 < len(args):
			i++
			result.Fork = &args[i]
		case arg == "--session-dir" && i+1 < len(args):
			i++
			result.SessionDir = &args[i]
		case arg == "--models" && i+1 < len(args):
			i++
			result.Models = splitTrimFilter(args[i], ",")
		case arg == "--no-tools" || arg == "-nt":
			result.NoTools = boolPtr(true)
		case arg == "--no-builtin-tools" || arg == "-nbt":
			result.NoBuiltinTools = boolPtr(true)
		case (arg == "--tools" || arg == "-t") && i+1 < len(args):
			i++
			toolsList := splitTrimFilter(args[i], ",")
			// A list of only +name/-name entries modifies the default
			// selection instead of replacing it (pi: #10549).
			if problem := core.GetToolListError(toolsList); problem != "" {
				pushDiagnostic(ArgDiagnosticError, fmt.Sprintf("%s: %s", arg, problem))
			} else {
				result.Tools = toolsList
			}
		case (arg == "--exclude-tools" || arg == "-xt") && i+1 < len(args):
			i++
			result.ExcludeTools = splitTrimFilter(args[i], ",")
		case arg == "--no-mcp":
			result.NoMcp = boolPtr(true)
		case arg == "--thinking" && i+1 < len(args):
			i++
			level := args[i]
			if constants.IsValidThinkingLevel(level) {
				t := agent.ThinkingLevel(level)
				result.Thinking = &t
			} else {
				pushDiagnostic(ArgDiagnosticWarning,
					fmt.Sprintf("Invalid thinking level %q. Valid values: %s", level, strings.Join(constants.ValidThinkingLevels, ", ")))
			}
		case arg == "--print" || arg == "-p":
			result.Print = boolPtr(true)
			if i+1 < len(args) {
				next := args[i+1]
				if !strings.HasPrefix(next, "@") && (!strings.HasPrefix(next, "-") || strings.HasPrefix(next, "---")) {
					result.Messages = append(result.Messages, next)
					i++
				}
			}
		case arg == "--export" && i+1 < len(args):
			i++
			result.Export = &args[i]
		case (arg == "--extension" || arg == "-e") && i+1 < len(args):
			i++
			result.Extensions = append(result.Extensions, args[i])
		case arg == "--no-extensions" || arg == "-ne":
			result.NoExtensions = boolPtr(true)
		case arg == "--skill" && i+1 < len(args):
			i++
			result.Skills = append(result.Skills, args[i])
		case arg == "--prompt-template" && i+1 < len(args):
			i++
			result.PromptTemplates = append(result.PromptTemplates, args[i])
		case arg == "--theme" && i+1 < len(args):
			i++
			result.Themes = append(result.Themes, args[i])
		case arg == "--use-theme":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				result.UseTheme = &args[i]
			} else {
				pushDiagnostic(ArgDiagnosticError, "--use-theme requires a theme name")
			}
		case arg == "--no-skills" || arg == "-ns":
			result.NoSkills = boolPtr(true)
		case arg == "--no-prompt-templates" || arg == "-np":
			result.NoPromptTemplates = boolPtr(true)
		case arg == "--no-themes":
			result.NoThemes = boolPtr(true)
		case arg == "--no-context-files" || arg == "-nc":
			result.NoContextFiles = boolPtr(true)
		case arg == "--list-models":
			// Check if next arg is a search pattern (not a flag or file arg)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.HasPrefix(args[i+1], "@") {
				i++
				result.ListModels = args[i]
			} else {
				result.ListModels = true
			}
		case arg == "--tui-mode":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				pushDiagnostic(ArgDiagnosticError, "--tui-mode requires regular or fullscreen")
				if i+1 < len(args) {
					i++
				}
			} else {
				i++
				mode := args[i]
				if mode == "regular" || mode == "fullscreen" {
					result.TuiMode = &mode
				} else {
					pushDiagnostic(ArgDiagnosticError, fmt.Sprintf("Invalid TUI mode %q. Valid values: regular, fullscreen", mode))
				}
			}
		case arg == "--verbose":
			result.Verbose = boolPtr(true)
		case arg == "--approve" || arg == "-a":
			result.ProjectTrustOverride = boolPtr(true)
		case arg == "--no-approve" || arg == "-na":
			result.ProjectTrustOverride = boolPtr(false)
		case arg == "--offline":
			result.Offline = boolPtr(true)
		case strings.HasPrefix(arg, "@"):
			result.FileArgs = append(result.FileArgs, strings.TrimPrefix(arg, "@"))
		case strings.HasPrefix(arg, "--"):
			eqIndex := strings.Index(arg, "=")
			if eqIndex != -1 {
				flagName := arg[2:eqIndex]
				if _, seen := result.UnknownFlags[flagName]; !seen {
					result.UnknownFlagKeys = append(result.UnknownFlagKeys, flagName)
				}
				result.UnknownFlags[flagName] = arg[eqIndex+1:]
			} else {
				flagName := arg[2:]
				next := ""
				hasNext := i+1 < len(args)
				if hasNext {
					next = args[i+1]
				}
				if hasNext && !strings.HasPrefix(next, "-") && !strings.HasPrefix(next, "@") {
					if _, seen := result.UnknownFlags[flagName]; !seen {
						result.UnknownFlagKeys = append(result.UnknownFlagKeys, flagName)
					}
					result.UnknownFlags[flagName] = next
					i++
				} else {
					if _, seen := result.UnknownFlags[flagName]; !seen {
						result.UnknownFlagKeys = append(result.UnknownFlagKeys, flagName)
					}
					result.UnknownFlags[flagName] = true
				}
			}
		case strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--"):
			pushDiagnostic(ArgDiagnosticError, fmt.Sprintf("Unknown option: %s", arg))
		default:
			result.Messages = append(result.Messages, arg)
		}
	}

	return result
}

// boolPtr is a local shorthand used only inside ParseArgs (samber/lo is not
// imported here to keep the leaf import set minimal).
func boolPtr(v bool) *bool { return &v }

// splitAndTrim splits on sep and trims each member (pi's
// `.split(",").map(s => s.trim())` — members may be empty).
func splitAndTrim(value, sep string) []string {
	parts := strings.Split(value, sep)
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// splitTrimFilter splits on sep, trims each member, and drops empty members
// (pi's `.split(",").map(s => s.trim()).filter(name => name.length > 0)`).
func splitTrimFilter(value, sep string) []string {
	var out []string
	for _, p := range splitAndTrim(value, sep) {
		if len(p) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// PrintHelp prints the CLI help text (pi: printHelp). pi colors the headings
// with chalk; pig drops the coloring (non-interactive text, same words).
func PrintHelp(extensionFlags []core.ExtensionFlag) {
	extensionFlagsText := ""
	if len(extensionFlags) > 0 {
		var lines []string
		for _, flag := range extensionFlags {
			value := ""
			if flag.Type == "string" {
				value = " <value>"
			}
			description := flag.Description
			if description == "" {
				description = fmt.Sprintf("Registered by %s", flag.ExtensionPath)
			}
			lines = append(lines, fmt.Sprintf("%-30s%s", fmt.Sprintf("  --%s%s", flag.Name, value), description))
		}
		extensionFlagsText = fmt.Sprintf("\nExtension CLI Flags:\n%s\n", strings.Join(lines, "\n"))
	}

	fmt.Printf(`%s - AI coding assistant with read, bash, edit, write tools

Usage:
  %s [options] [--] [@files...] [messages...]

Commands:
  %s install <source> [-l]     Install extension source and add to settings
  %s remove <source> [-l]      Remove extension source from settings
  %s uninstall <source> [-l]   Alias for remove
  %s update [source|self|pi]   Update pig, extensions, or model catalogs // pig: was "Update pi"
  %s list                      List installed extensions from settings
  %s config [-l]               Open TUI to enable/disable package resources (Tab switches scope)
  %s auth <command>            Print credentials or check provider readiness
  %s <command> --help          Show help for install/remove/uninstall/update/list/config/auth

Options:
  --provider <name>              Provider to search for --model (requires --model)
  --model <pattern>              Model pattern or ID (supports "provider/id" and optional ":<thinking>")
  --api-key <key>                API key (defaults to env vars)
  --system-prompt <text>         System prompt (default: coding assistant prompt)
  --append-system-prompt <text>  Append text or file contents to the system prompt (can be used multiple times)
  --mode <mode>                  Output mode: text (default), json, or rpc
  --print, -p                    Non-interactive mode: process prompt and exit
  --continue, -c                 Continue previous session
  --resume, -r                   Select a session to resume
  --session <path|id>            Use specific session file or partial UUID
  --session-id <id>              Use exact project session ID, creating it if missing
  --fork <path|id>               Fork specific session file or partial UUID into a new session
  --session-dir <dir>            Directory for session storage and lookup
  --no-session                   Don't save session (ephemeral)
  --name, -n <name>              Set session display name
  --models <patterns>            Comma-separated model patterns for Ctrl+P cycling
                                 Supports globs (anthropic/*, *sonnet*) and fuzzy matching
  --no-tools, -nt                Disable all tools by default (built-in and extension)
  --no-builtin-tools, -nbt       Disable built-in tools by default but keep extension/custom tools enabled
  --tools, -t <tools>            Comma-separated allowlist of tool names or patterns (*) to enable
                                 Applies to built-in, extension, custom, and MCP tools
                                 Only +name/-name entries add to or remove from the defaults
  --exclude-tools, -xt <tools>   Comma-separated denylist of tool names or patterns (*) to disable
                                 Applies to all tools, MCP tools included
  --no-mcp                       Disable built-in MCP support: no servers connect and no MCP tools
  --thinking <level>             Set thinking level: off, minimal, low, medium, high, xhigh, max
  --extension, -e <path>         Load an extension file (can be used multiple times)
  --no-extensions, -ne           Disable extension discovery (explicit -e paths still work)
  --skill <path>                 Load a skill file or directory (can be used multiple times)
  --no-skills, -ns               Disable skills discovery and loading
  --prompt-template <path>       Load a prompt template file or directory (can be used multiple times)
  --no-prompt-templates, -np     Disable prompt template discovery and loading
  --theme <path>                 Load a theme file or directory (can be used multiple times)
  --use-theme <name[/name]>      Set the initial interactive theme for this run
  --no-themes                    Disable theme discovery and loading
  --no-context-files, -nc        Disable AGENTS.md and CLAUDE.md discovery and loading
  --export <file>                Export session file to HTML and exit
  --list-models [search]         List available models (with optional fuzzy search)
  --verbose                      Force verbose startup (overrides quietStartup setting)
  --tui-mode <mode>              TUI mode: fullscreen (default) or regular
  --approve, -a                  Trust project-local files for this run
  --no-approve, -na              Ignore project-local files for this run
  --offline                      Disable startup network operations (same as PI_OFFLINE=1)
  --                             End option parsing; treat remaining arguments as messages/files
  --help, -h                     Show this help
  --version, -v                  Show version number

Extensions can register additional flags (e.g., --plan from plan-mode extension).%s

Examples:
  # Print a provider API key for an external client
  %s auth print-api-key --provider openai

  # Print an OAuth bearer token for an external client (refreshes if expired)
  %s auth print-bearer-token --provider openai-codex

  # Interactive mode
  %s

  # Interactive mode with initial prompt
  %s "List all .ts files in src/"

  # Include files in initial message
  %s @prompt.md @image.png "What color is the sky?"

  # Non-interactive mode (process and exit)
  %s -p "List all .ts files in src/"

  # Prompt beginning with a dash
  %s -p -- "- Summarize these points"

  # Multiple messages (interactive)
  %s "Read package.json" "What dependencies do we have?"

  # Continue previous session
  %s --continue "What did we discuss?"

  # Start a named session
  %s --name "Refactor auth module"

  # Use different model
  %s --provider openai --model gpt-4o-mini "Help me refactor this code"

  # Use model with provider prefix (no --provider needed)
  %s --model openai/gpt-4o "Help me refactor this code"

  # Use model with thinking level shorthand
  %s --model sonnet:high "Solve this complex problem"

  # Limit model cycling to specific models
  %s --models claude-sonnet,claude-haiku,gpt-4o

  # Limit to a specific provider with glob pattern
  %s --models "github-copilot/*"

  # Cycle models with fixed thinking levels
  %s --models sonnet:high,haiku:low

  # Start with a specific thinking level
  %s --thinking high "Solve this complex problem"

  # Read-only mode (no file modifications possible)
  %s --tools read,grep,find,ls -p "Review the code in src/"

  # Add codemode to the default tools
  %s --tools +codemode

  # Pattern allowlist (only the mcp proxy tool plus read/bash)
  %s --tools 'read,bash,mcp*'

  # Disable one tool while keeping the rest available
  %s --exclude-tools ask_question

  # Export a session file to HTML
  %s --export ~/%s/agent/sessions/--path--/session.jsonl
  %s --export session.jsonl output.html

Environment Variables:
  ANTHROPIC_AUTH_TOKEN             - Anthropic bearer auth token
  ANTHROPIC_API_KEY                - Anthropic Claude API key
  ANTHROPIC_OAUTH_TOKEN            - Anthropic OAuth token (alternative to API key)
  ANT_LING_API_KEY                 - Ant Ling API key
  OPENAI_API_KEY                   - OpenAI GPT API key
  AZURE_OPENAI_API_KEY             - Azure OpenAI/Cognitive Services API key
  AZURE_OPENAI_BASE_URL            - Azure OpenAI/Cognitive Services base URL (e.g. https://{resource}.openai.azure.com)
  AZURE_OPENAI_RESOURCE_NAME       - Azure OpenAI resource name (alternative to base URL)
  AZURE_OPENAI_API_VERSION         - Azure OpenAI API version (default: v1)
  AZURE_OPENAI_DEPLOYMENT_NAME_MAP - Azure OpenAI model=deployment map (comma-separated)
  DEEPSEEK_API_KEY                 - DeepSeek API key
  NVIDIA_API_KEY                   - NVIDIA NIM API key
  GEMINI_API_KEY                   - Google Gemini API key
  GROQ_API_KEY                     - Groq API key
  CEREBRAS_API_KEY                 - Cerebras API key
  XAI_API_KEY                      - xAI Grok API key
  FIREWORKS_API_KEY                - Fireworks API key
  TOGETHER_API_KEY                 - Together AI API key
  BASETEN_API_KEY                  - Baseten API key
  OPENROUTER_API_KEY               - OpenRouter API key
  AI_GATEWAY_API_KEY               - Vercel AI Gateway API key
  ZAI_API_KEY                      - ZAI Coding Plan API key (Global)
  ZAI_CODING_CN_API_KEY            - ZAI Coding Plan API key (China)
  MISTRAL_API_KEY                  - Mistral API key
  MINIMAX_API_KEY                  - MiniMax API key
  MOONSHOT_API_KEY                 - Moonshot AI API key
  OPENCODE_API_KEY                 - OpenCode Zen/OpenCode Go API key
  KIMI_API_KEY                     - Kimi For Coding API key
  META_API_KEY                     - Meta Model API key
  CLOUDFLARE_API_KEY               - Cloudflare API token (Workers AI and AI Gateway)
  CLOUDFLARE_ACCOUNT_ID            - Cloudflare account id (required for both)
  CLOUDFLARE_GATEWAY_ID            - Cloudflare AI Gateway slug (required for AI Gateway)
  QWEN_TOKEN_PLAN_API_KEY          - Qwen Token Plan API key (international region)
  QWEN_TOKEN_PLAN_CN_API_KEY       - Qwen Token Plan API key (China region)
  XIAOMI_API_KEY                   - Xiaomi MiMo API key (api.xiaomimimo.com billing)
  XIAOMI_TOKEN_PLAN_CN_API_KEY     - Xiaomi MiMo Token Plan API key (China region)
  XIAOMI_TOKEN_PLAN_AMS_API_KEY    - Xiaomi MiMo Token Plan API key (Amsterdam region)
  XIAOMI_TOKEN_PLAN_SGP_API_KEY    - Xiaomi MiMo Token Plan API key (Singapore region)
  AWS_PROFILE                      - AWS profile for Amazon Bedrock
  AWS_ACCESS_KEY_ID                - AWS access key for Amazon Bedrock
  AWS_SECRET_ACCESS_KEY            - AWS secret key for Amazon Bedrock
  AWS_BEARER_TOKEN_BEDROCK         - Bedrock API key (bearer token)
  AWS_REGION                       - AWS region for Amazon Bedrock (e.g., us-east-1)
  %-32s - Config directory (default: ~/%s/agent)
  %-32s - Session storage directory (overridden by --session-dir)
  PI_PACKAGE_DIR                   - Override package directory (for Nix/Guix store paths)
  PI_OFFLINE                       - Disable startup network operations when set to 1/true/yes
  PI_TELEMETRY                     - Override install telemetry when set to 1/true/yes or 0/false/no
  PI_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pi.dev/session/)

Built-in Tool Names:
  read       - Read file contents
  bash       - Execute bash commands
  powershell - Execute PowerShell commands on Windows
  edit       - Edit files with find/replace
  write      - Write files (creates/overwrites)
  grep       - Search file contents (read-only, off by default)
  find       - Find files by glob pattern (read-only, off by default)
  ls         - List directory contents (read-only, off by default)
`,
		codingagent.AppName,
		codingagent.AppName,
		codingagent.AppName, codingagent.AppName, codingagent.AppName, codingagent.AppName, codingagent.AppName,
		codingagent.AppName, codingagent.AppName, codingagent.AppName,
		extensionFlagsText,
		codingagent.AppName, codingagent.AppName, codingagent.AppName, codingagent.AppName, codingagent.AppName,
		codingagent.AppName, codingagent.AppName, codingagent.AppName,
		codingagent.AppName,
		codingagent.AppName, codingagent.AppName,
		codingagent.AppName, codingagent.AppName, codingagent.AppName, codingagent.AppName, codingagent.AppName,
		codingagent.AppName, codingagent.AppName, codingagent.AppName, codingagent.AppName, codingagent.AppName,
		codingagent.AppName,
		codingagent.ConfigDirName, codingagent.AppName,
		codingagent.EnvAgentDir, codingagent.ConfigDirName,
		codingagent.EnvSessionDir,
	)
}
