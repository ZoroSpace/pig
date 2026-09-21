// pi: src/core/bug-report.ts — /bug report collection, redaction, bundling
// and model-written summaries (pi v0.86.0). Deviations:
//   - collectEnvironment reports Go runtime facts (go/<version>, GOOS/GOARCH,
//     syscall.Uname) instead of bun/node; the PI_ env scan also collects
//     PIG_* names (pig's own prefix).
//   - SimpleStreamOptions carries ctx as the Go context argument (AbortSignal
//     → context rule); options.signal members are dropped.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/agent"
	harness "github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	utils "github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/samber/lo"
)

// BugReportCustomEntryType is the custom entry type recording bug report
// deliveries (pi: BUG_REPORT_CUSTOM_ENTRY_TYPE).
const BugReportCustomEntryType = "pi.bug-report"

// bugReportSchemaVersion is the report schema version (pi:
// BUG_REPORT_SCHEMA_VERSION).
const bugReportSchemaVersion = 1

// bugReportRedacted replaces redacted values (pi: REDACTED).
const bugReportRedacted = "<redacted>"

// bugReportSensitiveKey matches secret-looking key names (pi:
// SENSITIVE_KEY).
var bugReportSensitiveKey = regexp.MustCompile(`(?i)(?:^|[-_])(api[-_]?key|secret|token|password|passwd|credential|authorization|cookie)(?:$|[-_])`)

// bugReportCamelBoundary inserts _ between lower/digit and upper (pi:
// key.replace(/([a-z0-9])([A-Z])/g, "$1_$2")).
var bugReportCamelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func bugReportIsSensitiveKey(key string) bool {
	return bugReportSensitiveKey.MatchString(bugReportCamelBoundary.ReplaceAllString(key, "${1}_${2}"))
}

// RedactUrl strips credentials and secret-looking query parameters from a
// URL (pi: redactUrl).
func RedactUrl(value string) string {
	nested := regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*:)([a-z][a-z0-9+.-]*://.*)$`).FindStringSubmatch(value)
	if nested != nil {
		return nested[1] + RedactUrl(nested[2])
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	changed := false
	if parsed.User != nil {
		parsed.User = nil
		changed = true
	}
	// url.Values is a map; JS searchParams preserves insertion order, so the
	// raw query is rebuilt pair by pair.
	if parsed.RawQuery != "" {
		pairs := strings.Split(parsed.RawQuery, "&")
		out := make([]string, 0, len(pairs))
		for _, pair := range pairs {
			key, _, _ := strings.Cut(pair, "=")
			decodedKey, err := url.QueryUnescape(key)
			if err != nil {
				decodedKey = key
			}
			if bugReportIsSensitiveKey(decodedKey) {
				out = append(out, key+"="+url.QueryEscape(bugReportRedacted))
				changed = true
				continue
			}
			out = append(out, pair)
		}
		parsed.RawQuery = strings.Join(out, "&")
	}
	if !changed {
		return value
	}
	return parsed.String()
}

// RedactJsonValue copies a JSON value while removing values that may contain
// credentials (pi: redactJsonValue). Structs are converted through their
// JSON representation; the result is the generic decoded value.
func RedactJsonValue(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var generic any
	if json.Unmarshal(data, &generic) != nil {
		return nil
	}
	return redactJsonWalk(generic)
}

func redactJsonWalk(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if child != nil && bugReportIsSensitiveKey(key) {
				out[key] = bugReportRedacted
				continue
			}
			if text, ok := child.(string); ok {
				out[key] = RedactUrl(text)
				continue
			}
			out[key] = redactJsonWalk(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactJsonWalk(child)
		}
		return out
	default:
		return value
	}
}

// redactSettings removes the tracking id and redacts credential-shaped
// values (pi: redactSettings).
func redactSettings(settings Settings) any {
	redacted := RedactJsonValue(settings)
	if object, ok := redacted.(map[string]any); ok {
		delete(object, "trackingId")
	}
	return redacted
}

// bugReportEnvironment describes the runtime environment (pi:
// collectEnvironment).
type bugReportEnvironment struct {
	Version   string  `json:"version"`
	UserAgent string  `json:"userAgent"`
	Runtime   string  `json:"runtime"`
	Platform  string  `json:"platform"`
	Arch      string  `json:"arch"`
	OsRelease string  `json:"osRelease"`
	OsVersion string  `json:"osVersion"`
	Shell     *string `json:"shell"`
	Terminal  struct {
		Term           *string `json:"term"`
		Program        *string `json:"program"`
		ProgramVersion *string `json:"programVersion"`
		Colorterm      *string `json:"colorterm"`
		Tmux           bool    `json:"tmux"`
		Ssh            bool    `json:"ssh"`
		Ci             bool    `json:"ci"`
	} `json:"terminal"`
	PiEnvironmentVariables []string `json:"piEnvironmentVariables"`
}

func envOrNull(name string) *string {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	return &value
}

func collectEnvironment() bugReportEnvironment {
	environment := bugReportEnvironment{
		Version:   codingagent.Version,
		UserAgent: utils.GetPiUserAgent(codingagent.Version),
		// pi: bun/x.y.z | node/vX — pig reports the Go runtime (declared).
		Runtime:  "go/" + strings.TrimPrefix(runtime.Version(), "go"),
		Platform: runtime.GOOS,
		Arch:     runtime.GOARCH,
		OsRelease: func() string {
			release, _ := osReleaseVersion()
			return release
		}(),
		OsVersion: func() string {
			_, version := osReleaseVersion()
			return version
		}(),
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		if index := strings.LastIndexAny(shell, `\/`); index >= 0 {
			shell = shell[index+1:]
		}
		environment.Shell = &shell
	}
	environment.Terminal.Term = envOrNull("TERM")
	environment.Terminal.Program = envOrNull("TERM_PROGRAM")
	environment.Terminal.ProgramVersion = envOrNull("TERM_PROGRAM_VERSION")
	environment.Terminal.Colorterm = envOrNull("COLORTERM")
	environment.Terminal.Tmux = os.Getenv("TMUX") != ""
	environment.Terminal.Ssh = os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != ""
	environment.Terminal.Ci = os.Getenv("CI") != ""
	// Names help diagnose configuration; values never leave the machine
	// (pi comment). pig scans PI_ and PIG_ (its own prefix — declared).
	for _, name := range os.Environ() {
		key, _, _ := strings.Cut(name, "=")
		if strings.HasPrefix(key, "PI_") || strings.HasPrefix(key, "PIG_") {
			environment.PiEnvironmentVariables = append(environment.PiEnvironmentVariables, key)
		}
	}
	sort.Strings(environment.PiEnvironmentVariables)
	return environment
}

// osReleaseVersion reads the kernel release/version (pi: os.release() /
// os.version()). Non-Linux platforms return empty strings.
func osReleaseVersion() (release string, version string) {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return "", ""
	}
	release = strings.TrimSpace(string(data))
	hostname, err := os.ReadFile("/proc/sys/kernel/hostname")
	if err == nil {
		version = strings.TrimSpace(string(hostname))
	}
	return release, version
}

// bugReportModel describes the session model (pi: describeModel).
type bugReportModel struct {
	Provider         string                  `json:"provider"`
	ID               string                  `json:"id"`
	Name             string                  `json:"name"`
	API              types.Api               `json:"api"`
	BaseURL          string                  `json:"baseUrl"`
	Reasoning        bool                    `json:"reasoning"`
	Input            []types.ModelInput      `json:"input"`
	ContextWindow    int64                   `json:"contextWindow"`
	MaxTokens        int64                   `json:"maxTokens"`
	SamplingParams   any                     `json:"samplingParams"`
	Compat           any                     `json:"compat"`
	ThinkingLevelMap *types.ThinkingLevelMap `json:"thinkingLevelMap"`
	HeaderNames      []string                `json:"headerNames"`
}

func describeModel(model *types.Model) bugReportModel {
	samplingParams := any(nil)
	if model.SamplingParams != nil {
		samplingParams = RedactJsonValue(model.SamplingParams)
	}
	compat := any(nil)
	if model.Compat != nil {
		compat = RedactJsonValue(model.Compat)
	}
	var input []types.ModelInput
	if model.Input != nil {
		input = model.Input
	} else {
		input = []types.ModelInput{}
	}
	headerNames := make([]string, 0, len(model.Headers))
	for name := range model.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	return bugReportModel{
		Provider:         string(model.Provider),
		ID:               model.ID,
		Name:             model.Name,
		API:              model.API,
		BaseURL:          RedactUrl(model.BaseURL),
		Reasoning:        model.Reasoning,
		Input:            input,
		ContextWindow:    model.ContextWindow,
		MaxTokens:        model.MaxTokens,
		SamplingParams:   samplingParams,
		Compat:           compat,
		ThinkingLevelMap: lo.ToPtr(model.ThinkingLevelMap),
		HeaderNames:      headerNames,
	}
}

// bugReportProvider describes the model's provider (pi: describeProvider).
type bugReportProvider struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	BaseURL               *string    `json:"baseUrl"`
	HeaderNames           []string   `json:"headerNames"`
	AuthTypes             []string   `json:"authTypes"`
	AuthStatus            authStatus `json:"authStatus"`
	UsingOAuth            bool       `json:"usingOAuth"`
	RegisteredByExtension bool       `json:"registeredByExtension"`
}

// authStatus mirrors ModelRuntime.AuthStatus with pi's JSON shape.
type authStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	Label      string `json:"label,omitempty"`
}

func describeProvider(modelRuntime *ModelRuntime, provider ai.Provider) bugReportProvider {
	authTypes := []string{}
	if provider.Auth().APIKey != nil {
		authTypes = append(authTypes, "api_key")
	}
	if provider.Auth().OAuth != nil {
		authTypes = append(authTypes, "oauth")
	}
	var baseURL *string
	if provider.BaseURL() != "" {
		baseURL = lo.ToPtr(RedactUrl(provider.BaseURL()))
	}
	headerNames := make([]string, 0)
	for name := range provider.Headers() {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	registered := false
	for _, id := range modelRuntime.GetRegisteredProviderIds() {
		if id == provider.ID() {
			registered = true
			break
		}
	}
	status := modelRuntime.GetProviderAuthStatus(provider.ID())
	return bugReportProvider{
		ID:                    provider.ID(),
		Name:                  provider.Name(),
		BaseURL:               baseURL,
		HeaderNames:           headerNames,
		AuthTypes:             authTypes,
		AuthStatus:            authStatus{Configured: status.Configured, Source: status.Source, Label: status.Label},
		UsingOAuth:            modelRuntime.IsUsingOAuth(provider.ID()),
		RegisteredByExtension: registered,
	}
}

// bugReportExtension describes one loaded extension (pi: describeExtension).
type bugReportExtension struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Scope  string `json:"scope"`
	Origin string `json:"origin"`
	Hidden bool   `json:"hidden"`
}

func describeExtension(extension *Extension) bugReportExtension {
	return bugReportExtension{
		Path:   extension.Path,
		Source: RedactUrl(extension.SourceInfo.Source),
		Scope:  string(extension.SourceInfo.Scope),
		Origin: string(extension.SourceInfo.Origin),
		Hidden: extension.Hidden,
	}
}

// CollectBugReportMetadataOptions are the collectBugReportMetadata
// parameters (pi: CollectBugReportMetadataOptions).
type CollectBugReportMetadataOptions struct {
	ID              string // "" = a fresh uuidv7
	Hint            string // "" = null
	SessionID       string
	Cwd             string
	IncludeSession  bool
	IncludeSummary  bool
	MessageCount    int
	Model           *types.Model
	ModelRuntime    *ModelRuntime
	ThinkingLevel   agent.ThinkingLevel
	Extensions      []*Extension
	ExtensionErrors []LoadExtensionError
	GlobalSettings  Settings
	ProjectSettings Settings
}

// bugReportMetadata is the report.json metadata shape (pi: the
// collectBugReportMetadata return object).
type bugReportMetadata struct {
	SchemaVersion   int                  `json:"schemaVersion"`
	ID              string               `json:"id"`
	CreatedAt       string               `json:"createdAt"`
	Hint            *string              `json:"hint"`
	Environment     bugReportEnvironment `json:"environment"`
	Session         bugReportSession     `json:"session"`
	Model           *bugReportModel      `json:"model"`
	Provider        *bugReportProvider   `json:"provider"`
	ThinkingLevel   string               `json:"thinkingLevel"`
	Extensions      []bugReportExtension `json:"extensions"`
	ExtensionErrors []struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	} `json:"extensionErrors"`
	Settings struct {
		Global  any `json:"global"`
		Project any `json:"project"`
	} `json:"settings"`
}

type bugReportSession struct {
	ID              string  `json:"id"`
	Included        bool    `json:"included"`
	SummaryIncluded bool    `json:"summaryIncluded"`
	MessageCount    int     `json:"messageCount"`
	Cwd             *string `json:"cwd,omitempty"`
}

// CollectBugReportMetadata assembles the redacted report metadata (pi:
// collectBugReportMetadata).
func CollectBugReportMetadata(options CollectBugReportMetadataOptions) bugReportMetadata {
	var hint *string
	if trimmed := strings.TrimSpace(options.Hint); trimmed != "" {
		hint = lo.ToPtr(trimmed)
	}
	id := options.ID
	if id == "" {
		id = ai.UUIDv7()
	}
	var model *bugReportModel
	if options.Model != nil {
		described := describeModel(options.Model)
		model = &described
	}
	var provider *bugReportProvider
	if options.Model != nil {
		if resolved := options.ModelRuntime.GetProvider(string(options.Model.Provider)); resolved != nil {
			described := describeProvider(options.ModelRuntime, resolved)
			provider = &described
		}
	}
	metadata := bugReportMetadata{
		SchemaVersion: bugReportSchemaVersion,
		ID:            id,
		CreatedAt:     timeNowUTC(),
		Hint:          hint,
		Environment:   collectEnvironment(),
		Session: bugReportSession{
			ID:              options.SessionID,
			Included:        options.IncludeSession,
			SummaryIncluded: options.IncludeSummary,
			MessageCount:    options.MessageCount,
		},
		Model:         model,
		Provider:      provider,
		ThinkingLevel: string(options.ThinkingLevel),
	}
	if options.IncludeSession {
		cwd := options.Cwd
		metadata.Session.Cwd = &cwd
	}
	metadata.Extensions = make([]bugReportExtension, 0, len(options.Extensions))
	for _, extension := range options.Extensions {
		metadata.Extensions = append(metadata.Extensions, describeExtension(extension))
	}
	if metadata.Extensions == nil {
		metadata.Extensions = []bugReportExtension{}
	}
	metadata.ExtensionErrors = make([]struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	}, 0, len(options.ExtensionErrors))
	for _, loadError := range options.ExtensionErrors {
		metadata.ExtensionErrors = append(metadata.ExtensionErrors, struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		}{Path: loadError.Path, Error: loadError.Error})
	}
	metadata.Settings.Global = redactSettings(options.GlobalSettings)
	metadata.Settings.Project = redactSettings(options.ProjectSettings)
	return metadata
}

// timeNowUTC is the report createdAt timestamp (pi: new Date().toISOString()).
func timeNowUTC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// bugReportAssistantDiagnostics is one failed assistant turn (pi: the
// collectBugReportDiagnostics assistant entries).
type bugReportAssistantDiagnostics struct {
	EntryID       string                             `json:"entryId"`
	Timestamp     string                             `json:"timestamp"`
	Provider      string                             `json:"provider"`
	Model         string                             `json:"model"`
	API           types.Api                          `json:"api"`
	StopReason    types.StopReason                   `json:"stopReason"`
	RawStopReason string                             `json:"rawStopReason,omitempty"`
	ErrorMessage  string                             `json:"errorMessage,omitempty"`
	Diagnostics   []types.AssistantMessageDiagnostic `json:"diagnostics"`
}

// bugReportDiagnostics is the diagnostics.json shape (pi:
// collectBugReportDiagnostics).
type bugReportDiagnostics struct {
	SchemaVersion         int                             `json:"schemaVersion"`
	SessionID             string                          `json:"sessionId"`
	EntryCount            int                             `json:"entryCount"`
	AssistantMessageCount int                             `json:"assistantMessageCount"`
	Assistant             []bugReportAssistantDiagnostics `json:"assistant"`
	Crashes               []CrashRecord                   `json:"crashes"`
}

// CollectBugReportDiagnostics collects failed assistant turns without
// collecting conversation content (pi: collectBugReportDiagnostics).
func CollectBugReportDiagnostics(sessionManager *SessionManager, crashes []CrashRecord) bugReportDiagnostics {
	entries := sessionManager.GetEntries()
	diagnostics := bugReportDiagnostics{
		SchemaVersion: bugReportSchemaVersion,
		SessionID:     sessionManager.GetSessionID(),
		EntryCount:    len(entries),
		Assistant:     []bugReportAssistantDiagnostics{},
		Crashes:       []CrashRecord{},
	}
	for _, entry := range entries {
		messageEntry, ok := entry.(*SessionMessageEntry)
		if !ok {
			continue
		}
		message := assistantMessageOf(messageEntry.Message)
		if message == nil {
			continue
		}
		diagnostics.AssistantMessageCount++
		if len(message.Diagnostics) == 0 &&
			message.StopReason != types.StopReasonError &&
			message.StopReason != types.StopReasonAborted &&
			message.ErrorMessage == "" {
			continue
		}
		diagnostics.Assistant = append(diagnostics.Assistant, bugReportAssistantDiagnostics{
			EntryID:       messageEntry.ID,
			Timestamp:     messageEntry.Timestamp,
			Provider:      string(message.Provider),
			Model:         message.Model,
			API:           message.API,
			StopReason:    message.StopReason,
			RawStopReason: message.RawStopReason,
			ErrorMessage:  message.ErrorMessage,
			Diagnostics:   message.Diagnostics,
		})
	}
	for _, crash := range crashes {
		// pi strips notified before attaching (record without the flag).
		crash.Notified = nil
		diagnostics.Crashes = append(diagnostics.Crashes, crash)
	}
	return diagnostics
}

// assistantMessageOf unwraps the value/pointer dual forms (pig dual-form
// convention).
func assistantMessageOf(message agent.AgentMessage) *types.AssistantMessage {
	switch typed := message.(type) {
	case types.AssistantMessage:
		return &typed
	case *types.AssistantMessage:
		return typed
	default:
		return nil
	}
}

// BugReportBundle is everything a delivery needs (pi: BugReportBundle).
type BugReportBundle struct {
	Metadata     bugReportMetadata
	Diagnostics  bugReportDiagnostics
	SessionJsonl string // "" = undefined
	Summary      string // "" = undefined
}

// BugReportSessionEntryData is the custom entry recording a delivery (pi:
// BugReportSessionEntryData).
type BugReportSessionEntryData struct {
	ID              string  `json:"id"`
	CreatedAt       string  `json:"createdAt"`
	Hint            *string `json:"hint"`
	SessionIncluded bool    `json:"sessionIncluded"`
	SummaryIncluded bool    `json:"summaryIncluded"`
	Delivery        string  `json:"delivery"` // "zip" | "upload"
	Path            string  `json:"path,omitempty"`
}

// bugReportFile is one archived file (pi: BugReportFile).
type bugReportFile struct {
	Name        string
	ContentType string
	Data        string
}

// bugReportFiles lists the files shared by upload and zip export (pi:
// bugReportFiles).
func bugReportFiles(bundle *BugReportBundle) []bugReportFile {
	files := []bugReportFile{
		{Name: "report.json", ContentType: "application/json", Data: bugReportJSON(bundle.Metadata)},
		{Name: "diagnostics.json", ContentType: "application/json", Data: bugReportJSON(bundle.Diagnostics)},
	}
	if bundle.SessionJsonl != "" {
		files = append(files, bugReportFile{Name: "session.jsonl", ContentType: "application/x-ndjson", Data: bundle.SessionJsonl})
	}
	if bundle.Summary != "" {
		summary := bundle.Summary
		if !strings.HasSuffix(summary, "\n") {
			summary += "\n"
		}
		files = append(files, bugReportFile{Name: "summary.md", ContentType: "text/markdown", Data: summary})
	}
	return files
}

// bugReportJSON serializes with 2-space indent + trailing newline (pi:
// JSON.stringify(value, null, 2) + "\n").
func bugReportJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(data) + "\n"
}

// WriteBugReportArchive writes the zip archive (pi: writeBugReportArchive).
func WriteBugReportArchive(bundle *BugReportBundle, filePath string) error {
	files := bugReportFiles(bundle)
	entries := make([]utils.ZipEntry, 0, len(files))
	for _, file := range files {
		entries = append(entries, utils.ZipEntry{Name: file.Name, Data: file.Data})
	}
	return utils.WriteZipArchive(filePath, entries)
}

// BugReportArchiveFileName is the export file name (pi:
// bugReportArchiveFileName).
func BugReportArchiveFileName(id string) string {
	return "pi-bug-report-" + id + ".zip"
}

// bugReportSummarySystemPrompt is the summary system prompt (pi:
// BUG_SUMMARY_SYSTEM_PROMPT).
const bugReportSummarySystemPrompt = `You are helping a user file a bug report about pi, the coding agent they are talking to. You will be shown the conversation transcript. Write a report for the pi developers describing what the user was doing and what went wrong.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the report.`

// bugReportSummaryInstructions shape the model-written report (pi:
// BUG_SUMMARY_INSTRUCTIONS).
const bugReportSummaryInstructions = `Write the bug report in Markdown with these sections:

## What the user was doing
One short paragraph.

## What went wrong
Concrete description of the failure: wrong output, errors, hangs, tool failures, unexpected behavior. Quote error messages and tool output verbatim where they exist.

## Steps to reproduce
Numbered list, as specific as the transcript allows.

## Relevant details
Tool calls involved, files touched, model behavior, anything else that helps a developer reproduce or locate the problem.

Do not include file contents, secrets, or credentials from the transcript; refer to files by path only. Keep the report factual and concise.`

// bugReportSelectMessages picks the newest messages within a token budget
// (pi: selectMessages).
func bugReportSelectMessages(messages []agent.AgentMessage, tokenBudget int64) []agent.AgentMessage {
	var selected []agent.AgentMessage
	var tokens int64
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		next := EstimateTokens(message)
		if len(selected) > 0 && tokens+next > tokenBudget {
			break
		}
		selected = append(selected, message)
		tokens += next
	}
	// Reverse back to chronological order.
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected
}

// GenerateBugReportSummaryOptions are the generateBugReportSummary
// parameters (pi: GenerateBugReportSummaryOptions). pi's options.signal is
// dropped — cancellation travels as the Go ctx (AbortSignal → context rule).
type GenerateBugReportSummaryOptions struct {
	Messages      []agent.AgentMessage
	Hint          string
	Model         *types.Model
	APIKey        string
	Headers       map[string]string
	Env           types.ProviderEnv
	ThinkingLevel agent.ThinkingLevel
	StreamFn      agent.StreamFn
	Retry         *ai.RetryPolicy
	SessionID     string
}

// GenerateBugReportSummary asks the session model for a report when the user
// does not share the transcript (pi: generateBugReportSummary).
func GenerateBugReportSummary(ctx context.Context, options GenerateBugReportSummaryOptions) (string, error) {
	model := options.Model
	contextWindow := model.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128_000
	}
	messages := bugReportSelectMessages(options.Messages, int64(math.Floor(float64(contextWindow)*0.6)))
	hint := strings.TrimSpace(options.Hint)
	promptParts := []string{}
	if len(messages) < len(options.Messages) {
		promptParts = append(promptParts, fmt.Sprintf("Note: only the last %d of %d messages are shown.", len(messages), len(options.Messages)))
	}
	promptParts = append(promptParts, "<conversation>\n"+SerializeConversation(harness.ConvertToLlm(messages))+"\n</conversation>")
	if hint != "" {
		promptParts = append(promptParts, "<user-report>\n"+hint+"\n</user-report>")
	}
	promptParts = append(promptParts, bugReportSummaryInstructions)
	prompt := strings.Join(promptParts, "\n\n")

	maxTokens := int64(4096)
	if model.MaxTokens > 0 && model.MaxTokens < 4096 {
		maxTokens = model.MaxTokens
	}
	requestOptions := &ai.SimpleStreamOptions{}
	requestOptions.MaxTokens = lo.ToPtr(maxTokens)
	requestOptions.SessionID = options.SessionID
	requestOptions.APIKey = options.APIKey
	requestOptions.Env = options.Env
	if options.Headers != nil {
		headers := types.ProviderHeaders{}
		for name, value := range options.Headers {
			headers[name] = lo.ToPtr(value)
		}
		requestOptions.Headers = headers
	}
	if model.Reasoning && options.ThinkingLevel != "" && options.ThinkingLevel != types.ModelThinkingOff {
		requestOptions.Reasoning = types.ThinkingLevel(options.ThinkingLevel)
	}
	llmContext := &ai.Context{
		SystemPrompt: bugReportSummarySystemPrompt,
		Messages: []types.Message{
			types.UserMessage{
				Role:      types.RoleUser,
				Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(prompt)}},
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}
	response, err := CompleteSummarization(ctx, model, llmContext, requestOptions, options.StreamFn, options.Retry, nil)
	if err != nil {
		return "", err
	}
	if response.StopReason == types.StopReasonAborted {
		return "", fmt.Errorf("Bug report summary was cancelled")
	}
	if failure := GetSummarizationFailure(response, "Bug report summary"); failure != "" {
		return "", fmt.Errorf("%s", failure)
	}
	for _, block := range response.Content {
		if block.ContentKind() == types.KindToolCall {
			return "", fmt.Errorf("Bug report summary attempted to call a tool")
		}
	}
	text := strings.TrimSpace(ai.ContentText(response.Content, "\n\n"))
	if text == "" {
		return "", fmt.Errorf("Bug report summary was empty")
	}
	return text, nil
}
