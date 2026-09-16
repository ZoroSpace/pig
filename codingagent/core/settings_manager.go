// pi: src/core/settings-manager.ts — layered settings (global ~/.pi/agent/
// settings.json + project .pi/settings.json), modified-field tracking on
// save, migrations, and the typed accessor surface.
//
// Representation deviation (declared): pi's Settings is a TS interface whose
// runtime values are plain JSON objects; pi's own implementation manipulates
// them dynamically (field-name-keyed modified tracking, migrateSettings key
// deletes, deep merge over arbitrary JSON, unknown-field preservation on
// rewrite). pig therefore models Settings as a JSON object map and provides
// the typed surface exclusively through the SettingsManager accessors below,
// mirroring pi's class API 1:1. pi's Settings interface is documented field
// by field on Settings. Consequences:
//   - JSON null (a nil interface value) merges as an override; a missing key
//     does not. pi additionally skips undefined-valued keys, which Go map
//     literals cannot express (declared; only reachable from in-memory
//     overrides).
//   - Wrong-typed values (e.g. a string where a number is expected) fall
//     back to the default instead of propagating pi's runtime type lie.
//   - JSON.stringify preserves object insertion order; Go marshals maps with
//     sorted keys — settings.json key order differs from pi (content
//     equivalent).
//   - pi's writeQueue promise chain serializes async writes; pig writes
//     synchronously under the manager mutex (same visible ordering for
//     callers; Flush then only waits for in-flight access). Errors the
//     promise chain would swallow are recorded via recordError the same way.
//   - proper-lockfile → an O_EXCL lockfile at "<path>.lock" with the same
//     10×20ms retry loop (pi's lockSync ELOCKED retry); release = removal.
//     pi's `withLock(scope, fn): void` throws on failure; Go returns error.
//   - Setters that pi can throw from (the setProject* family and
//     SetHttpIdleTimeoutMs) return error; the rest keep void signatures.
//   - PackageSource's TS string|object union → PackageSource struct with an
//     ObjectForm marker; settings maps store the JSON-native form so deep
//     merge / clone / rewrite stay shape-faithful.
package core

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
	"github.com/samber/lo"
)

// Settings holds one settings object (pi: Settings interface; see header for
// the representation deviation). Field inventory follows pi's interface, in
// declaration order:
//
//	lastChangelogVersion?: string
//	defaultProvider?: string
//	defaultModel?: string
//	defaultThinkingLevel?: ThinkingLevel
//	modelThinkingLevels?: Record<string, ThinkingLevel>  — keyed "provider/modelId"
//	transport?: TransportSetting                         — default "auto"
//	steeringMode?: "all" | "one-at-a-time"
//	followUpMode?: "all" | "one-at-a-time"
//	theme?: string
//	compaction?: CompactionSettings
//	branchSummary?: BranchSummarySettings
//	retry?: RetrySettings
//	hideThinkingBlock?: boolean
//	showCacheMissNotices?: boolean                       — default false - show cache cost and provider recovery notices
//	externalEditor?: string                              — takes precedence over VISUAL/EDITOR
//	shellPath?: string                                   — supports leading ~ expansion
//	quietStartup?: boolean
//	defaultProjectTrust?: DefaultProjectTrust            — default "ask"; global only
//	shellCommandPrefix?: string
//	npmCommand?: string[]
//	collapseChangelog?: boolean
//	enableInstallTelemetry?: boolean                     — default true
//	enableAnalytics?: boolean                            — default false; opt-in
//	trackingId?: string                                  — generated on first opt-in
//	packages?: PackageSource[]
//	extensions?: string[]
//	skills?: string[]
//	prompts?: string[]
//	themes?: string[]
//	enableSkillCommands?: boolean                        — default true
//	terminal?: TerminalSettings
//	images?: ImageSettings
//	enabledModels?: string[]
//	defaultTools?: string[]
//	doubleEscapeAction?: "fork" | "tree" | "none"        — default "tree"
//	treeFilterMode?: "default" | "no-tools" | "user-only" | "labeled-only" | "all"
//	thinkingBudgets?: ThinkingBudgetsSettings
//	editorPaddingX?: number                              — default 0
//	outputPad?: 0 | 1                                    — default 1
//	autocompleteMaxVisible?: number                      — default 5
//	showHardwareCursor?: boolean
//	markdown?: MarkdownSettings
//	warnings?: WarningSettings
//	sessionDir?: string
//	httpProxy?: string
//	httpIdleTimeoutMs?: number
//	websocketConnectTimeoutMs?: number
//	tuiMode?: TuiMode                                    — default "regular"
//	fullscreenExitOutput?: FullscreenExitOutput          — default "transcript"
//	fullscreenScrollbar?: ScrollViewScrollbar            — default "auto"
//	fullscreenCopyOnSelect?: boolean                     — default true; no effect in regular TUI mode (v0.85.1)
//	cjkSegmenter?: "bigram" | "sst" | "gse"              — pig-only; default via PIG_CJK_SEGMENTER env, else "bigram"
//	showResourceStats?: boolean                          — pig-only; footer resource stats (MEM always, G/FD/CPU alerts on anomaly), default false
type Settings map[string]any

// SettingsScope selects which settings layer a storage operation targets.
type SettingsScope = string

const (
	SettingsScopeGlobal  SettingsScope = "global"
	SettingsScopeProject SettingsScope = "project"
)

// TransportSetting selects the provider transport (pi: TransportSetting =
// Transport from pi-ai).
type TransportSetting = ai.Transport

// TuiMode selects the interactive renderer mode (pi: TuiMode =
// RendererTuiMode from pi-tui; the canonical type lands with the tui batch).
type TuiMode = string

const (
	TuiModeRegular    TuiMode = "regular"
	TuiModeFullscreen TuiMode = "fullscreen"
)

// FullscreenExitOutput selects what the fullscreen renderer prints on exit.
type FullscreenExitOutput = string

const (
	FullscreenExitOutputTranscript FullscreenExitOutput = "transcript"
	FullscreenExitOutputResumeHint FullscreenExitOutput = "resume-hint"
)

// ScrollViewScrollbar selects fullscreen scroll-view scrollbar behavior (pi:
// ScrollViewScrollbar from pi-tui; canonical type lands with the tui batch).
type ScrollViewScrollbar = string

const (
	ScrollViewScrollbarAuto   ScrollViewScrollbar = "auto"
	ScrollViewScrollbarAlways ScrollViewScrollbar = "always"
	ScrollViewScrollbarHidden ScrollViewScrollbar = "hidden"
)

// DefaultProjectTrust selects the default project trust decision.
type DefaultProjectTrust = string

const (
	DefaultProjectTrustAsk    DefaultProjectTrust = "ask"
	DefaultProjectTrustAlways DefaultProjectTrust = "always"
	DefaultProjectTrustNever  DefaultProjectTrust = "never"
)

// MermaidRenderingMode selects when mermaid diagrams render.
type MermaidRenderingMode = string

// Mermaid rendering mode values (pi: "off" | "final" | "streaming").
const (
	MermaidRenderingOff       MermaidRenderingMode = "off"
	MermaidRenderingFinal     MermaidRenderingMode = "final"
	MermaidRenderingStreaming MermaidRenderingMode = "streaming"
)

// PackageSource is one npm/git package source (pi: PackageSource union).
// The string form loads all resources from the package; the object form
// filters which resources to load; Autoload=false starts empty and only
// applies explicit resource patterns.
type PackageSource struct {
	// Source is the package spec; in string form it holds the whole value.
	Source string
	// ObjectForm records that the settings.json value was the object form
	// (string form round-trips as a bare string). Not serialized.
	ObjectForm bool
	Autoload   *bool    `json:"autoload,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Prompts    []string `json:"prompts,omitempty"`
	Themes     []string `json:"themes,omitempty"`
}

// toNative converts a PackageSource to its JSON-native settings value
// (string form stays a string; object form becomes a map).
func (p PackageSource) toNative() any {
	if !p.ObjectForm {
		return p.Source
	}
	native := map[string]any{"source": p.Source}
	if p.Autoload != nil {
		native["autoload"] = *p.Autoload
	}
	if p.Extensions != nil {
		native["extensions"] = stringsToNative(p.Extensions)
	}
	if p.Skills != nil {
		native["skills"] = stringsToNative(p.Skills)
	}
	if p.Prompts != nil {
		native["prompts"] = stringsToNative(p.Prompts)
	}
	if p.Themes != nil {
		native["themes"] = stringsToNative(p.Themes)
	}
	return native
}

// packageSourceFromNative converts a decoded settings value to a
// PackageSource. Non-string/non-object members fall back to their zero form.
func packageSourceFromNative(value any) PackageSource {
	switch v := value.(type) {
	case string:
		return PackageSource{Source: v}
	case map[string]any:
		source := PackageSource{ObjectForm: true}
		if s, ok := v["source"].(string); ok {
			source.Source = s
		}
		if b, ok := v["autoload"].(bool); ok {
			source.Autoload = lo.ToPtr(b)
		}
		if items, ok := v["extensions"].([]any); ok {
			source.Extensions = nativeToStrings(items)
		}
		if items, ok := v["skills"].([]any); ok {
			source.Skills = nativeToStrings(items)
		}
		if items, ok := v["prompts"].([]any); ok {
			source.Prompts = nativeToStrings(items)
		}
		if items, ok := v["themes"].([]any); ok {
			source.Themes = nativeToStrings(items)
		}
		return source
	default:
		return PackageSource{}
	}
}

// CompactionSettings holds the user-facing compaction settings shape (pi:
// CompactionSettings in settings-manager.ts — the optional shape; distinct
// from the resolved shape of the same name in harness).
type CompactionSettings struct {
	Enabled          *bool  // default: true
	ReserveTokens    *int64 // default: 16384
	KeepRecentTokens *int64 // default: 20000
}

// BranchSummarySettings holds the branch summary settings shape (pi:
// BranchSummarySettings).
type BranchSummarySettings struct {
	ReserveTokens *int64 // default: 16384 (tokens reserved for prompt + LLM response)
	SkipPrompt    *bool  // default: false - when true, skips "Summarize branch?" prompt and defaults to no summary
}

// ProviderRetrySettings holds the SDK/provider retry settings shape (pi:
// ProviderRetrySettings).
type ProviderRetrySettings struct {
	TimeoutMs       *int64 // SDK/provider request timeout in milliseconds
	MaxRetries      *int64 // SDK/provider retry attempts
	MaxRetryDelayMs *int64 // default: 60000 (max server-requested delay before failing)
}

// RetrySettings holds the agent retry settings shape (pi: RetrySettings).
type RetrySettings struct {
	Enabled     *bool                  // default: true
	MaxRetries  *int64                 // default: 3
	BaseDelayMs *int64                 // default: 2000 (exponential backoff: 2s, 4s, 8s)
	Provider    *ProviderRetrySettings // pi: provider?
}

// TerminalSettings holds the terminal rendering settings shape (pi:
// TerminalSettings).
type TerminalSettings struct {
	ShowImages           *bool  // default: true (only relevant if terminal supports images)
	ImageWidthCells      *int64 // default: 60 (preferred inline image width in terminal cells)
	ClearOnShrink        *bool  // default: false (clear empty rows when content shrinks)
	ShowTerminalProgress *bool  // default: false (OSC 9;4 terminal progress indicators)
	// Hyperlinks / Images / TrueColor mirror pi's boolean | "auto" unions
	// (Images additionally "kitty" | "iterm2"; false forces off). pig keeps
	// the raw JSON values in the settings map; v0.85.1 additions.
	Hyperlinks any
	Images     any
	TrueColor  any
}

// TerminalCapabilityOverrides is the user-forced subset of terminal
// capabilities (pi: Partial<TerminalCapabilities> from
// getTerminalCapabilityOverrides; the canonical type lives in pi-tui, so
// pig aliases tui.TerminalCapabilityOverrides; pi's images:null force-off
// maps to Images=false here).
type TerminalCapabilityOverrides = tui.TerminalCapabilityOverrides

// ImageSettings holds the image handling settings shape (pi: ImageSettings).
type ImageSettings struct {
	AutoResize  *bool // default: true (resize images to 2000x2000 max for better model compatibility)
	BlockImages *bool // default: false - when true, prevents all images from being sent to LLM providers
}

// ThinkingBudgetsSettings holds custom token budgets for thinking levels
// (pi: ThinkingBudgetsSettings). pi's numbers are TS numbers; pig keeps
// float64 for JSON fidelity.
type ThinkingBudgetsSettings struct {
	Minimal *float64
	Low     *float64
	Medium  *float64
	High    *float64
}

// MarkdownSettings holds the markdown rendering settings shape (pi:
// MarkdownSettings).
type MarkdownSettings struct {
	CodeBlockIndent *string               // default: "  "
	Mermaid         *MermaidRenderingMode // default: "streaming"
}

// WarningSettings holds the warning toggle settings shape (pi:
// WarningSettings).
type WarningSettings struct {
	AnthropicExtraUsage *bool // default: true
}

// settingsString reads a string-typed member (pi: typeof check).
func settingsString(s Settings, key string) (string, bool) {
	if v, ok := s[key]; ok {
		if str, ok := v.(string); ok {
			return str, true
		}
	}
	return "", false
}

// settingsBool reads a bool-typed member.
func settingsBool(s Settings, key string) (bool, bool) {
	if v, ok := s[key]; ok {
		if b, ok := v.(bool); ok {
			return b, true
		}
	}
	return false, false
}

// settingsNumber reads a number-typed member (JSON numbers decode as
// float64).
func settingsNumber(s Settings, key string) (float64, bool) {
	if v, ok := s[key]; ok {
		if n, ok := v.(float64); ok {
			return n, true
		}
	}
	return 0, false
}

// settingsObject reads an object-typed member. Both map forms are accepted:
// JSON round-trips produce plain map[string]any, while setters create nested
// Settings maps in place (named types fail a direct type assertion).
func settingsObject(s Settings, key string) (Settings, bool) {
	if v, ok := s[key]; ok {
		if m, ok := v.(map[string]any); ok {
			return m, true
		}
		if m, ok := v.(Settings); ok {
			return m, true
		}
	}
	return nil, false
}

// settingsStringSlice reads a string[]-typed member, copying only string
// elements (pi's spread copies arbitrary members; pig's []string return
// cannot hold non-strings — declared).
func settingsStringSlice(s Settings, key string) ([]string, bool) {
	if v, ok := s[key]; ok {
		if items, ok := v.([]any); ok {
			result := make([]string, 0, len(items))
			for _, item := range items {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result, true
		}
	}
	return nil, false
}

// stringsToNative converts a []string to the JSON-native []any form.
func stringsToNative(items []string) []any {
	result := make([]any, len(items))
	for i, item := range items {
		result[i] = item
	}
	return result
}

// nativeToStrings converts a decoded []any to []string, skipping
// non-string members.
func nativeToStrings(items []any) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if str, ok := item.(string); ok {
			result = append(result, str)
		}
	}
	return result
}

// cloneSettings deep-copies a settings object via JSON round-trip (pi:
// structuredClone over JSON objects).
func cloneSettings(s Settings) Settings {
	if s == nil {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return Settings{}
	}
	clone := Settings{}
	if err := json.Unmarshal(data, &clone); err != nil {
		return Settings{}
	}
	return clone
}

func isMergeableObject(value any) (map[string]any, bool) {
	if m, ok := value.(map[string]any); ok {
		return m, true
	}
	// Setters create nested Settings maps in place (named type); see
	// settingsObject.
	m, ok := value.(Settings)
	return m, ok
}

func deepMergeObjects(base map[string]any, overrides map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(overrides))
	for key, value := range base {
		result[key] = value
	}

	for key, overrideValue := range overrides {
		// pi: skip undefined-valued overrides; Go map keys are always
		// "present", and a nil interface is JSON null — null overwrites in pi
		// too, so every present key merges (header declaration).
		if baseValue, mergeable := isMergeableObject(base[key]); mergeable {
			if overrideMap, mergeable := isMergeableObject(overrideValue); mergeable {
				result[key] = deepMergeObjects(baseValue, overrideMap)
				continue
			}
		}
		result[key] = overrideValue
	}

	return result
}

// deepMergeSettings deep merges settings: project/overrides take precedence,
// nested objects merge recursively (pi: deepMergeSettings).
func deepMergeSettings(base Settings, overrides Settings) Settings {
	return deepMergeObjects(base, overrides)
}

// parseTimeoutSetting parses an optional timeout setting, erroring on
// present-but-invalid values (pi: parseTimeoutSetting; throw → error).
func parseTimeoutSetting(value any, settingName string) (*int64, error) {
	if timeoutMs, ok := ParseHTTPIdleTimeoutMs(value); ok {
		return lo.ToPtr(timeoutMs), nil
	}
	if value != nil {
		return nil, fmt.Errorf("Invalid %s setting: %s", settingName, formatAnyValue(value))
	}
	return nil, nil
}

// formatAnyValue renders a value for error messages (pi: String(value)).
func formatAnyValue(value any) string {
	if str, ok := value.(string); ok {
		return str
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

// SettingsManagerCreateOptions carries creation options (pi:
// SettingsManagerCreateOptions).
type SettingsManagerCreateOptions struct {
	ProjectTrusted *bool
}

// SettingsStorage abstracts settings persistence (pi: SettingsStorage).
// pi's withLock throws; pig returns error (header declaration).
type SettingsStorage interface {
	WithLock(scope SettingsScope, fn func(current *string) *string) error
}

// SettingsError records one settings load/persist failure (pi:
// SettingsError; path?: string → empty string = absent).
type SettingsError struct {
	Scope SettingsScope
	Path  string
	Error error
}

// settingsPaths is the Partial<Record<SettingsScope, string>> path memo.
type settingsPaths map[SettingsScope]string

func toSettingsError(scope SettingsScope, err error, path string) SettingsError {
	return SettingsError{Scope: scope, Path: path, Error: err}
}

// FileSettingsStorage persists settings as JSON files under lock (pi:
// FileSettingsStorage).
type FileSettingsStorage struct {
	globalSettingsPath  string
	projectSettingsPath string
}

// NewFileSettingsStorage builds the file-backed storage (pi: constructor).
func NewFileSettingsStorage(cwd string, agentDir string) *FileSettingsStorage {
	resolvedCwd := resolveAgainstProcessCwd(cwd)
	resolvedAgentDir := resolveAgainstProcessCwd(agentDir)
	return &FileSettingsStorage{
		globalSettingsPath:  filepath.Join(resolvedAgentDir, "settings.json"),
		projectSettingsPath: filepath.Join(resolvedCwd, codingagent.ConfigDirName, "settings.json"),
	}
}

// acquireLockSyncWithRetry locks the settings file with retry (pi:
// acquireLockSyncWithRetry — proper-lockfile lockSync, ELOCKED retried 10×
// every 20ms). pig uses an O_EXCL "<path>.lock" lockfile (header
// declaration). Returns the release func.
func (s *FileSettingsStorage) acquireLockSyncWithRetry(path string) (func(), error) {
	const maxAttempts = 10
	const delayMs = 20
	lockPath := path + ".lock"

	var lastError error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			// pi: only ELOCKED is retried; other errors throw immediately.
			return nil, err
		}
		lastError = err
		if attempt == maxAttempts {
			break
		}
		time.Sleep(delayMs * time.Millisecond)
	}

	return nil, lastError
}

// WithLock runs fn under the settings file lock (pi: withLock). The file is
// read (when present) and, when fn returns new content, the directory is
// created on demand and the file written — all under lock.
func (s *FileSettingsStorage) WithLock(scope SettingsScope, fn func(current *string) *string) error {
	path := s.globalSettingsPath
	if scope == SettingsScopeProject {
		path = s.projectSettingsPath
	}
	dir := filepath.Dir(path)

	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()

	// Only create directory and lock if file exists or we need to write
	fileExists := false
	if _, err := os.Stat(path); err == nil {
		fileExists = true
	}
	if fileExists {
		acquired, err := s.acquireLockSyncWithRetry(path)
		if err != nil {
			return err
		}
		release = acquired
	}

	var current *string
	if fileExists {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contentString := string(content)
		current = &contentString
	}
	next := fn(current)
	if next != nil {
		// Only create directory when we actually need to write
		if _, err := os.Stat(dir); err != nil {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		if release == nil {
			acquired, err := s.acquireLockSyncWithRetry(path)
			if err != nil {
				return err
			}
			release = acquired
		}
		if err := os.WriteFile(path, []byte(*next), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// InMemorySettingsStorage keeps both scopes in memory (pi:
// InMemorySettingsStorage).
type InMemorySettingsStorage struct {
	mutex   sync.Mutex
	global  *string
	project *string
}

// WithLock runs fn against the in-memory content (pi: withLock).
func (s *InMemorySettingsStorage) WithLock(scope SettingsScope, fn func(current *string) *string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	var current *string
	if scope == SettingsScopeGlobal {
		current = s.global
	} else {
		current = s.project
	}
	next := fn(current)
	if next != nil {
		if scope == SettingsScopeGlobal {
			s.global = next
		} else {
			s.project = next
		}
	}
	return nil
}

// orderedStringSet preserves insertion order (pi: Set iteration order for
// modifiedFields / nested keys).
type orderedStringSet struct {
	items   []string
	present map[string]struct{}
}

func newOrderedStringSet() *orderedStringSet {
	return &orderedStringSet{present: map[string]struct{}{}}
}

func (s *orderedStringSet) add(value string) {
	if _, ok := s.present[value]; ok {
		return
	}
	s.present[value] = struct{}{}
	s.items = append(s.items, value)
}

func (s *orderedStringSet) clear() {
	s.items = nil
	s.present = map[string]struct{}{}
}

func (s *orderedStringSet) clone() []string {
	return append([]string(nil), s.items...)
}

func (s *orderedStringSet) len() int {
	return len(s.items)
}

// SettingsManager is the layered settings manager (pi: SettingsManager
// class). All state is guarded by one mutex (pi relies on the JS event
// loop).
type SettingsManager struct {
	mutex                       sync.Mutex
	storage                     SettingsStorage
	globalSettings              Settings
	projectSettings             Settings
	settings                    Settings
	projectTrusted              bool
	modifiedFields              *orderedStringSet            // Track global fields modified during session
	modifiedNestedFields        map[string]*orderedStringSet // Track global nested field modifications
	modifiedProjectFields       *orderedStringSet            // Track project fields modified during session
	modifiedProjectNestedFields map[string]*orderedStringSet // Track project nested field modifications
	globalSettingsLoadError     error                        // Track if global settings file had parse errors
	projectSettingsLoadError    error                        // Track if project settings file had parse errors
	errors                      []SettingsError
	pathsMemo                   settingsPaths
}

// newSettingsManager is the private constructor (pi: private constructor).
func newSettingsManager(
	storage SettingsStorage,
	initialGlobal Settings,
	initialProject Settings,
	globalLoadError error,
	projectLoadError error,
	initialErrors []SettingsError,
	projectTrusted bool,
	pathsMemo settingsPaths,
) *SettingsManager {
	manager := &SettingsManager{
		storage:                     storage,
		globalSettings:              initialGlobal,
		projectSettings:             initialProject,
		projectTrusted:              projectTrusted,
		globalSettingsLoadError:     globalLoadError,
		projectSettingsLoadError:    projectLoadError,
		errors:                      append([]SettingsError(nil), initialErrors...),
		pathsMemo:                   pathsMemo,
		modifiedFields:              newOrderedStringSet(),
		modifiedNestedFields:        map[string]*orderedStringSet{},
		modifiedProjectFields:       newOrderedStringSet(),
		modifiedProjectNestedFields: map[string]*orderedStringSet{},
	}
	manager.settings = deepMergeSettings(manager.globalSettings, manager.projectSettings)
	return manager
}

// resolveAgainstProcessCwd resolves a path the way pi's resolvePath does —
// relative inputs resolve against the process cwd (pi: resolvePath(cwd)).
func resolveAgainstProcessCwd(input string) string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return utils.ResolvePath(input, cwd, nil)
}

// CreateSettingsManager creates a SettingsManager that loads from files (pi:
// SettingsManager.create).
func CreateSettingsManager(cwd string, agentDir string, options *SettingsManagerCreateOptions) *SettingsManager {
	resolvedCwd := resolveAgainstProcessCwd(cwd)
	resolvedAgentDir := resolveAgainstProcessCwd(agentDir)
	storage := NewFileSettingsStorage(resolvedCwd, resolvedAgentDir)
	return settingsManagerFromStorageWithPaths(storage, options, settingsPaths{
		SettingsScopeGlobal:  filepath.Join(resolvedAgentDir, "settings.json"),
		SettingsScopeProject: filepath.Join(resolvedCwd, codingagent.ConfigDirName, "settings.json"),
	})
}

// SettingsManagerFromStorage creates a SettingsManager from an arbitrary
// storage backend (pi: SettingsManager.fromStorage).
func SettingsManagerFromStorage(storage SettingsStorage, options *SettingsManagerCreateOptions) *SettingsManager {
	return settingsManagerFromStorageWithPaths(storage, options, settingsPaths{})
}

// settingsManagerFromStorageWithPaths creates a manager while retaining
// optional file paths for reported storage errors (pi:
// fromStorageWithPaths).
func settingsManagerFromStorageWithPaths(
	storage SettingsStorage,
	options *SettingsManagerCreateOptions,
	pathsMemo settingsPaths,
) *SettingsManager {
	projectTrusted := true
	if options != nil && options.ProjectTrusted != nil {
		projectTrusted = *options.ProjectTrusted
	}
	globalLoad := tryLoadFromStorage(storage, SettingsScopeGlobal, true)
	projectLoad := tryLoadFromStorage(storage, SettingsScopeProject, projectTrusted)
	var initialErrors []SettingsError
	if globalLoad.err != nil {
		initialErrors = append(initialErrors, toSettingsError(SettingsScopeGlobal, globalLoad.err, pathsMemo[SettingsScopeGlobal]))
	}
	if projectLoad.err != nil {
		initialErrors = append(initialErrors, toSettingsError(SettingsScopeProject, projectLoad.err, pathsMemo[SettingsScopeProject]))
	}

	return newSettingsManager(
		storage,
		globalLoad.settings,
		projectLoad.settings,
		globalLoad.err,
		projectLoad.err,
		initialErrors,
		projectTrusted,
		pathsMemo,
	)
}

// InMemorySettingsManager creates an in-memory SettingsManager (pi:
// SettingsManager.inMemory).
func InMemorySettingsManager(settings Settings, options *SettingsManagerCreateOptions) *SettingsManager {
	if settings == nil {
		settings = Settings{}
	}
	storage := &InMemorySettingsStorage{}
	initialSettings := migrateSettings(cloneSettings(settings))
	native, err := json.MarshalIndent(initialSettings, "", "  ")
	if err != nil {
		native = []byte("{}")
	}
	nativeString := string(native)
	_ = storage.WithLock(SettingsScopeGlobal, func(current *string) *string {
		return &nativeString
	})
	return SettingsManagerFromStorage(storage, options)
}

// settingsLoad is the tryLoadFromStorage result.
type settingsLoad struct {
	settings Settings
	err      error
}

// loadFromStorage loads and migrates one scope (pi: loadFromStorage).
func loadFromStorage(storage SettingsStorage, scope SettingsScope, projectTrusted bool) (Settings, error) {
	if scope == SettingsScopeProject && !projectTrusted {
		return Settings{}, nil
	}

	var content *string
	if err := storage.WithLock(scope, func(current *string) *string {
		content = current
		return nil
	}); err != nil {
		return nil, err
	}

	if content == nil || *content == "" {
		return Settings{}, nil
	}
	parsed, err := parseSettingsObject(*content)
	if err != nil {
		return nil, err
	}
	return migrateSettings(parsed), nil
}

// tryLoadFromStorage loads one scope, converting failure into an error
// result (pi: tryLoadFromStorage).
func tryLoadFromStorage(storage SettingsStorage, scope SettingsScope, projectTrusted bool) settingsLoad {
	settings, err := loadFromStorage(storage, scope, projectTrusted)
	if err != nil {
		return settingsLoad{settings: Settings{}, err: err}
	}
	return settingsLoad{settings: settings}
}

// parseSettingsObject parses a settings file body (pi: JSON.parse +
// stripBom — arbitrary shape never fails at parse in TS unless the JSON is
// malformed). pi's migrateSettings throws on non-object top levels (e.g.
// "null"); pig rejects all non-object top levels here (declared edge
// divergence — pi would let a top-level array flow through untyped).
func parseSettingsObject(content string) (Settings, error) {
	var parsed any
	if err := json.Unmarshal([]byte(utils.StripBom(content)), &parsed); err != nil {
		return nil, err
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, errors.New("settings content is not a JSON object")
	}
	return object, nil
}

// migrateSettings migrates old settings formats to new formats (pi:
// migrateSettings; mutates and returns the input like pi).
func migrateSettings(settings Settings) Settings {
	// Migrate queueMode -> steeringMode
	if _, hasQueueMode := settings["queueMode"]; hasQueueMode {
		if _, hasSteeringMode := settings["steeringMode"]; !hasSteeringMode {
			settings["steeringMode"] = settings["queueMode"]
		}
		delete(settings, "queueMode")
	}

	// Migrate legacy websockets boolean -> transport enum
	if _, hasTransport := settings["transport"]; !hasTransport {
		if websockets, ok := settings["websockets"].(bool); ok {
			if websockets {
				settings["transport"] = "websocket"
			} else {
				settings["transport"] = "sse"
			}
			delete(settings, "websockets")
		}
	}

	// Migrate old skills object format to new array format
	if skillsValue, ok := settings["skills"]; ok {
		if skillsSettings, ok := skillsValue.(map[string]any); ok {
			if enableSkillCommands, ok := skillsSettings["enableSkillCommands"]; ok {
				if _, hasTopLevel := settings["enableSkillCommands"]; !hasTopLevel {
					settings["enableSkillCommands"] = enableSkillCommands
				}
			}
			if customDirectories, ok := skillsSettings["customDirectories"].([]any); ok && len(customDirectories) > 0 {
				settings["skills"] = customDirectories
			} else {
				delete(settings, "skills")
			}
		}
	}

	// Migrate retry.maxDelayMs -> retry.provider.maxRetryDelayMs
	if retryValue, ok := settings["retry"].(map[string]any); ok {
		providerSettings, hasProvider := retryValue["provider"].(map[string]any)
		if maxDelayMs, ok := retryValue["maxDelayMs"].(float64); ok {
			providerMaxRetryDelayMissing := !hasProvider ||
				retryValue["provider"] == nil ||
				providerSettings["maxRetryDelayMs"] == nil
			if providerMaxRetryDelayMissing {
				merged := map[string]any{}
				if hasProvider {
					for key, value := range providerSettings {
						merged[key] = value
					}
				}
				merged["maxRetryDelayMs"] = maxDelayMs
				retryValue["provider"] = merged
			}
		}
		delete(retryValue, "maxDelayMs")
	}

	return settings
}

// GetGlobalSettings returns a deep copy of the global settings (pi:
// getGlobalSettings).
func (m *SettingsManager) GetGlobalSettings() Settings {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return cloneSettings(m.globalSettings)
}

// GetProjectSettings returns a deep copy of the project settings (pi:
// getProjectSettings).
func (m *SettingsManager) GetProjectSettings() Settings {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return cloneSettings(m.projectSettings)
}

// IsProjectTrusted reports whether the project is trusted (pi:
// isProjectTrusted).
func (m *SettingsManager) IsProjectTrusted() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.projectTrusted
}

// SetProjectTrusted updates project trust and reloads project settings
// accordingly (pi: setProjectTrusted).
func (m *SettingsManager) SetProjectTrusted(trusted bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.projectTrusted == trusted {
		return
	}

	m.projectTrusted = trusted
	m.modifiedProjectFields.clear()
	m.modifiedProjectNestedFields = map[string]*orderedStringSet{}

	if !trusted {
		m.projectSettings = Settings{}
		m.projectSettingsLoadError = nil
		m.settings = deepMergeSettings(m.globalSettings, m.projectSettings)
		return
	}

	projectLoad := tryLoadFromStorage(m.storage, SettingsScopeProject, trusted)
	m.projectSettings = projectLoad.settings
	m.projectSettingsLoadError = projectLoad.err
	if projectLoad.err != nil {
		m.recordError(SettingsScopeProject, projectLoad.err)
	}
	m.settings = deepMergeSettings(m.globalSettings, m.projectSettings)
}

// Reload reloads both scopes from storage (pi: reload). pi awaits the write
// queue first; pig writes synchronously so there is nothing to wait for.
func (m *SettingsManager) Reload() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	globalLoad := tryLoadFromStorage(m.storage, SettingsScopeGlobal, true)
	if globalLoad.err == nil {
		m.globalSettings = globalLoad.settings
		m.globalSettingsLoadError = nil
	} else {
		m.globalSettingsLoadError = globalLoad.err
		m.recordError(SettingsScopeGlobal, globalLoad.err)
	}

	m.modifiedFields.clear()
	m.modifiedNestedFields = map[string]*orderedStringSet{}
	m.modifiedProjectFields.clear()
	m.modifiedProjectNestedFields = map[string]*orderedStringSet{}

	projectLoad := tryLoadFromStorage(m.storage, SettingsScopeProject, m.projectTrusted)
	if projectLoad.err == nil {
		m.projectSettings = projectLoad.settings
		m.projectSettingsLoadError = nil
	} else {
		m.projectSettingsLoadError = projectLoad.err
		m.recordError(SettingsScopeProject, projectLoad.err)
	}

	m.settings = deepMergeSettings(m.globalSettings, m.projectSettings)
}

// ApplyOverrides applies additional overrides on top of current settings
// (pi: applyOverrides).
func (m *SettingsManager) ApplyOverrides(overrides Settings) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.settings = deepMergeSettings(m.settings, overrides)
}

// markModified marks a global field as modified during this session (pi:
// markModified).
func (m *SettingsManager) markModified(field string, nestedKey string) {
	m.modifiedFields.add(field)
	if nestedKey != "" {
		set, ok := m.modifiedNestedFields[field]
		if !ok {
			set = newOrderedStringSet()
			m.modifiedNestedFields[field] = set
		}
		set.add(nestedKey)
	}
}

// markProjectModified marks a project field as modified during this session
// (pi: markProjectModified).
func (m *SettingsManager) markProjectModified(field string, nestedKey string) {
	m.modifiedProjectFields.add(field)
	if nestedKey != "" {
		set, ok := m.modifiedProjectNestedFields[field]
		if !ok {
			set = newOrderedStringSet()
			m.modifiedProjectNestedFields[field] = set
		}
		set.add(nestedKey)
	}
}

// assertProjectTrustedForWrite errors when project settings cannot be
// written (pi: assertProjectTrustedForWrite; throw → error).
func (m *SettingsManager) assertProjectTrustedForWrite() error {
	if !m.projectTrusted {
		return errors.New("Project is not trusted; refusing to write project settings")
	}
	return nil
}

// recordError appends a settings error (pi: recordError).
func (m *SettingsManager) recordError(scope SettingsScope, err error) {
	m.errors = append(m.errors, toSettingsError(scope, err, m.pathsMemo[scope]))
}

// clearModifiedScope resets modified tracking for one scope (pi:
// clearModifiedScope).
func (m *SettingsManager) clearModifiedScope(scope SettingsScope) {
	if scope == SettingsScopeGlobal {
		m.modifiedFields.clear()
		m.modifiedNestedFields = map[string]*orderedStringSet{}
		return
	}

	m.modifiedProjectFields.clear()
	m.modifiedProjectNestedFields = map[string]*orderedStringSet{}
}

// cloneModifiedNestedFields snapshots the nested modified tracking (pi:
// cloneModifiedNestedFields).
func cloneModifiedNestedFields(source map[string]*orderedStringSet) map[string][]string {
	snapshot := make(map[string][]string, len(source))
	for key, value := range source {
		snapshot[key] = value.clone()
	}
	return snapshot
}

// persistScopedSettings merges modified fields into the on-disk settings and
// returns the new file body (pi: persistScopedSettings). Runs inside
// WithLock; a persist error is captured for the caller.
func (m *SettingsManager) persistScopedSettings(
	scope SettingsScope,
	snapshotSettings Settings,
	modifiedFields []string,
	modifiedNestedFields map[string][]string,
) error {
	var persistErr error
	lockErr := m.storage.WithLock(scope, func(current *string) *string {
		currentFileSettings := Settings{}
		if current != nil {
			parsed, parseErr := parseSettingsObject(*current)
			if parseErr != nil {
				persistErr = parseErr
				return nil
			}
			currentFileSettings = migrateSettings(parsed)
		}
		mergedSettings := cloneSettings(currentFileSettings)
		for _, field := range modifiedFields {
			value, present := snapshotSettings[field]
			nestedModified, isNestedModified := modifiedNestedFields[field]
			valueObject, valueIsObject := isMergeableObject(value)
			if isNestedModified && valueIsObject {
				baseNested, _ := currentFileSettings[field].(map[string]any)
				if baseNested == nil {
					baseNested = map[string]any{}
				}
				mergedNested := make(map[string]any, len(baseNested)+len(nestedModified))
				for key, nestedValue := range baseNested {
					mergedNested[key] = nestedValue
				}
				for _, nestedKey := range nestedModified {
					// pi: mergedNested[nestedKey] = inMemoryNested[nestedKey] —
					// an absent key assigns undefined, which JSON.stringify
					// omits; pig deletes the key instead.
					if nestedValue, ok := valueObject[nestedKey]; ok {
						mergedNested[nestedKey] = nestedValue
					} else {
						delete(mergedNested, nestedKey)
					}
				}
				mergedSettings[field] = mergedNested
			} else if present {
				mergedSettings[field] = value
			} else {
				// pi: snapshotSettings[field] === undefined → the assignment
				// stores undefined and JSON.stringify drops the key.
				delete(mergedSettings, field)
			}
		}

		native, marshalErr := marshalSettingsJSON(mergedSettings)
		if marshalErr != nil {
			persistErr = marshalErr
			return nil
		}
		return lo.ToPtr(native)
	})
	if lockErr != nil {
		return lockErr
	}
	return persistErr
}

// marshalSettingsJSON serializes a settings body the way pi writes it —
// JSON.stringify(x, null, 2), no HTML escaping (Go's default encoder escapes
// <, >, &; pi does not). Key order is sorted (header declaration).
func marshalSettingsJSON(settings Settings) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(settings); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// save recomputes the merged view and schedules the global write (pi:
// save). The write runs synchronously (header declaration).
func (m *SettingsManager) save() {
	m.settings = deepMergeSettings(m.globalSettings, m.projectSettings)

	if m.globalSettingsLoadError != nil {
		return
	}

	snapshotGlobalSettings := cloneSettings(m.globalSettings)
	modifiedFields := m.modifiedFields.clone()
	modifiedNestedFields := cloneModifiedNestedFields(m.modifiedNestedFields)

	// pi: enqueueWrite chains the task and records failures; pig runs it
	// inline under the mutex. A failed persist does not clear the modified
	// tracking (pi's catch skips clearModifiedScope).
	if err := m.persistScopedSettings(SettingsScopeGlobal, snapshotGlobalSettings, modifiedFields, modifiedNestedFields); err != nil {
		m.recordError(SettingsScopeGlobal, err)
	} else {
		m.clearModifiedScope(SettingsScopeGlobal)
	}
}

// saveProjectSettings stores and schedules the project write (pi:
// saveProjectSettings).
func (m *SettingsManager) saveProjectSettings(settings Settings) error {
	if err := m.assertProjectTrustedForWrite(); err != nil {
		return err
	}
	m.projectSettings = cloneSettings(settings)
	m.settings = deepMergeSettings(m.globalSettings, m.projectSettings)

	if m.projectSettingsLoadError != nil {
		return nil
	}

	snapshotProjectSettings := cloneSettings(m.projectSettings)
	modifiedFields := m.modifiedProjectFields.clone()
	modifiedNestedFields := cloneModifiedNestedFields(m.modifiedProjectNestedFields)
	if err := m.persistScopedSettings(SettingsScopeProject, snapshotProjectSettings, modifiedFields, modifiedNestedFields); err != nil {
		m.recordError(SettingsScopeProject, err)
	} else {
		m.clearModifiedScope(SettingsScopeProject)
	}
	return nil
}

// updateProjectSettings applies a mutation to the project settings (pi:
// updateProjectSettings).
func (m *SettingsManager) updateProjectSettings(field string, update func(Settings)) error {
	if err := m.assertProjectTrustedForWrite(); err != nil {
		return err
	}
	projectSettings := cloneSettings(m.projectSettings)
	update(projectSettings)
	m.markProjectModified(field, "")
	if err := m.saveProjectSettings(projectSettings); err != nil {
		return err
	}
	return nil
}

// Flush waits for pending writes (pi: flush). pig writes synchronously, so
// this only waits for in-flight access.
func (m *SettingsManager) Flush() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
}

// DrainErrors returns and clears collected settings errors (pi:
// drainErrors).
func (m *SettingsManager) DrainErrors() []SettingsError {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	drained := append([]SettingsError(nil), m.errors...)
	m.errors = nil
	return drained
}

// GetLastChangelogVersion returns the last shown changelog version (pi:
// getLastChangelogVersion).
func (m *SettingsManager) GetLastChangelogVersion() (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return settingsString(m.settings, "lastChangelogVersion")
}

// SetLastChangelogVersion records the last shown changelog version (pi:
// setLastChangelogVersion).
func (m *SettingsManager) SetLastChangelogVersion(version string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["lastChangelogVersion"] = version
	m.markModified("lastChangelogVersion", "")
	m.save()
}

// GetSessionDir returns the custom session storage directory with ~
// expanded (pi: getSessionDir).
func (m *SettingsManager) GetSessionDir() (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	sessionDir, ok := settingsString(m.settings, "sessionDir")
	if !ok || sessionDir == "" {
		return sessionDir, ok
	}
	return utils.NormalizePath(sessionDir, nil), true
}

// GetDefaultProvider returns the default provider (pi: getDefaultProvider).
func (m *SettingsManager) GetDefaultProvider() (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return settingsString(m.settings, "defaultProvider")
}

// GetDefaultModel returns the default model (pi: getDefaultModel).
func (m *SettingsManager) GetDefaultModel() (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return settingsString(m.settings, "defaultModel")
}

// SetDefaultProvider records the default provider (pi: setDefaultProvider).
func (m *SettingsManager) SetDefaultProvider(provider string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["defaultProvider"] = provider
	m.markModified("defaultProvider", "")
	m.save()
}

// SetDefaultModel records the default model (pi: setDefaultModel).
func (m *SettingsManager) SetDefaultModel(modelID string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["defaultModel"] = modelID
	m.markModified("defaultModel", "")
	m.save()
}

// SetDefaultModelAndProvider records both defaults (pi:
// setDefaultModelAndProvider).
func (m *SettingsManager) SetDefaultModelAndProvider(provider string, modelID string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["defaultProvider"] = provider
	m.globalSettings["defaultModel"] = modelID
	m.markModified("defaultProvider", "")
	m.markModified("defaultModel", "")
	m.save()
}

// GetSteeringMode returns the steering mode (pi: getSteeringMode — falsy →
// "one-at-a-time").
func (m *SettingsManager) GetSteeringMode() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	mode, ok := settingsString(m.settings, "steeringMode")
	if !ok || mode == "" {
		return "one-at-a-time"
	}
	return mode
}

// SetSteeringMode records the steering mode (pi: setSteeringMode).
func (m *SettingsManager) SetSteeringMode(mode string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["steeringMode"] = mode
	m.markModified("steeringMode", "")
	m.save()
}

// GetFollowUpMode returns the follow-up mode (pi: getFollowUpMode — falsy →
// "one-at-a-time").
func (m *SettingsManager) GetFollowUpMode() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	mode, ok := settingsString(m.settings, "followUpMode")
	if !ok || mode == "" {
		return "one-at-a-time"
	}
	return mode
}

// SetFollowUpMode records the follow-up mode (pi: setFollowUpMode).
func (m *SettingsManager) SetFollowUpMode(mode string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["followUpMode"] = mode
	m.markModified("followUpMode", "")
	m.save()
}

// GetThemeSetting returns the raw theme setting, including slash-separated
// automatic themes (pi: getThemeSetting).
func (m *SettingsManager) GetThemeSetting() (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return settingsString(m.settings, "theme")
}

// GetTheme returns the fixed theme name; slash-separated automatic theme
// settings return undefined (pi: getTheme).
func (m *SettingsManager) GetTheme() (string, bool) {
	theme, ok := m.GetThemeSetting()
	if ok && strings.Contains(theme, "/") {
		return "", false
	}
	return theme, ok
}

// SetTheme records the theme (pi: setTheme).
func (m *SettingsManager) SetTheme(theme string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["theme"] = theme
	m.markModified("theme", "")
	m.save()
}

// GetDefaultThinkingLevel returns the default thinking level (pi:
// getDefaultThinkingLevel).
func (m *SettingsManager) GetDefaultThinkingLevel() (agent.ThinkingLevel, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	level, ok := settingsString(m.settings, "defaultThinkingLevel")
	if !ok {
		return "", false
	}
	return agent.ThinkingLevel(level), true
}

// SetDefaultThinkingLevel records the default thinking level (pi:
// setDefaultThinkingLevel).
func (m *SettingsManager) SetDefaultThinkingLevel(level agent.ThinkingLevel) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["defaultThinkingLevel"] = string(level)
	m.markModified("defaultThinkingLevel", "")
	m.save()
}

// GetModelThinkingLevel returns the per-model thinking level override (pi:
// getModelThinkingLevel).
func (m *SettingsManager) GetModelThinkingLevel(provider string, modelID string) (agent.ThinkingLevel, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	levels, ok := settingsObject(m.settings, "modelThinkingLevels")
	if !ok {
		return "", false
	}
	level, ok := settingsString(levels, provider+"/"+modelID)
	if !ok {
		return "", false
	}
	return agent.ThinkingLevel(level), true
}

// GetAllModelThinkingLevels returns a copy of all per-model thinking level
// overrides (pi: getAllModelThinkingLevels; non-string members are skipped —
// pi would propagate the type lie, header declaration).
func (m *SettingsManager) GetAllModelThinkingLevels() map[string]agent.ThinkingLevel {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	result := map[string]agent.ThinkingLevel{}
	levels, ok := settingsObject(m.settings, "modelThinkingLevels")
	if !ok {
		return result
	}
	for key, value := range levels {
		if level, ok := value.(string); ok {
			result[key] = agent.ThinkingLevel(level)
		}
	}
	return result
}

// SetModelThinkingLevel records a per-model thinking level override (pi:
// setModelThinkingLevel).
func (m *SettingsManager) SetModelThinkingLevel(provider string, modelID string, level agent.ThinkingLevel) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	levels, ok := settingsObject(m.globalSettings, "modelThinkingLevels")
	if !ok {
		levels = Settings{}
		m.globalSettings["modelThinkingLevels"] = levels
	}
	levels[provider+"/"+modelID] = string(level)
	m.markModified("modelThinkingLevels", "")
	m.save()
}

// RemoveModelThinkingLevel drops a per-model thinking level override (pi:
// removeModelThinkingLevel).
func (m *SettingsManager) RemoveModelThinkingLevel(provider string, modelID string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	levels, ok := settingsObject(m.globalSettings, "modelThinkingLevels")
	if !ok {
		return
	}
	delete(levels, provider+"/"+modelID)
	if len(levels) == 0 {
		delete(m.globalSettings, "modelThinkingLevels")
	}
	m.markModified("modelThinkingLevels", "")
	m.save()
}

// GetTransport returns the transport setting (pi: getTransport).
func (m *SettingsManager) GetTransport() TransportSetting {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	transport, ok := settingsString(m.settings, "transport")
	if !ok {
		return ai.TransportAuto
	}
	return TransportSetting(transport)
}

// SetTransport records the transport setting (pi: setTransport).
func (m *SettingsManager) SetTransport(transport TransportSetting) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["transport"] = string(transport)
	m.markModified("transport", "")
	m.save()
}

// GetCompactionEnabled returns whether compaction is enabled (pi:
// getCompactionEnabled).
func (m *SettingsManager) GetCompactionEnabled() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	compaction, ok := settingsObject(m.settings, "compaction")
	if !ok {
		return true
	}
	enabled, ok := settingsBool(compaction, "enabled")
	if !ok {
		return true
	}
	return enabled
}

// SetCompactionEnabled records the compaction toggle (pi:
// setCompactionEnabled).
func (m *SettingsManager) SetCompactionEnabled(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	compaction, ok := settingsObject(m.globalSettings, "compaction")
	if !ok {
		compaction = Settings{}
		m.globalSettings["compaction"] = compaction
	}
	compaction["enabled"] = enabled
	m.markModified("compaction", "enabled")
	m.save()
}

// GetCompactionReserveTokens returns the compaction reserve (pi:
// getCompactionReserveTokens).
func (m *SettingsManager) GetCompactionReserveTokens() int64 {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	compaction, ok := settingsObject(m.settings, "compaction")
	if !ok {
		return 16384
	}
	reserve, ok := settingsNumber(compaction, "reserveTokens")
	if !ok {
		return 16384
	}
	return int64(reserve)
}

// GetCompactionKeepRecentTokens returns the compaction retention (pi:
// getCompactionKeepRecentTokens).
func (m *SettingsManager) GetCompactionKeepRecentTokens() int64 {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	compaction, ok := settingsObject(m.settings, "compaction")
	if !ok {
		return 20000
	}
	keepRecent, ok := settingsNumber(compaction, "keepRecentTokens")
	if !ok {
		return 20000
	}
	return int64(keepRecent)
}

// CompactionSettingsValues is the resolved compaction settings shape (pi:
// getCompactionSettings's inline object literal).
type CompactionSettingsValues struct {
	Enabled          bool
	ReserveTokens    int64
	KeepRecentTokens int64
}

// GetCompactionSettings returns the resolved compaction settings (pi:
// getCompactionSettings).
func (m *SettingsManager) GetCompactionSettings() CompactionSettingsValues {
	return CompactionSettingsValues{
		Enabled:          m.GetCompactionEnabled(),
		ReserveTokens:    m.GetCompactionReserveTokens(),
		KeepRecentTokens: m.GetCompactionKeepRecentTokens(),
	}
}

// BranchSummarySettingsValues is the resolved branch summary shape (pi:
// getBranchSummarySettings's inline object literal).
type BranchSummarySettingsValues struct {
	ReserveTokens int64
	SkipPrompt    bool
}

// GetBranchSummarySettings returns the resolved branch summary settings (pi:
// getBranchSummarySettings).
func (m *SettingsManager) GetBranchSummarySettings() BranchSummarySettingsValues {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	branchSummary, ok := settingsObject(m.settings, "branchSummary")
	reserveTokens := 16384.0
	skipPrompt := false
	if ok {
		if value, present := settingsNumber(branchSummary, "reserveTokens"); present {
			reserveTokens = value
		}
		if value, present := settingsBool(branchSummary, "skipPrompt"); present {
			skipPrompt = value
		}
	}
	return BranchSummarySettingsValues{
		ReserveTokens: int64(reserveTokens),
		SkipPrompt:    skipPrompt,
	}
}

// GetBranchSummarySkipPrompt returns the branch summary prompt toggle (pi:
// getBranchSummarySkipPrompt).
func (m *SettingsManager) GetBranchSummarySkipPrompt() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	branchSummary, ok := settingsObject(m.settings, "branchSummary")
	if !ok {
		return false
	}
	skipPrompt, ok := settingsBool(branchSummary, "skipPrompt")
	if !ok {
		return false
	}
	return skipPrompt
}

// GetRetryEnabled returns whether retries are enabled (pi: getRetryEnabled).
func (m *SettingsManager) GetRetryEnabled() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	retry, ok := settingsObject(m.settings, "retry")
	if !ok {
		return true
	}
	enabled, ok := settingsBool(retry, "enabled")
	if !ok {
		return true
	}
	return enabled
}

// SetRetryEnabled records the retry toggle (pi: setRetryEnabled).
func (m *SettingsManager) SetRetryEnabled(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	retry, ok := settingsObject(m.globalSettings, "retry")
	if !ok {
		retry = Settings{}
		m.globalSettings["retry"] = retry
	}
	retry["enabled"] = enabled
	m.markModified("retry", "enabled")
	m.save()
}

// RetrySettingsValues is the resolved retry shape (pi: getRetrySettings's
// inline object literal).
type RetrySettingsValues struct {
	Enabled     bool
	MaxRetries  int64
	BaseDelayMs int64
}

// GetRetrySettings returns the resolved retry settings (pi:
// getRetrySettings).
func (m *SettingsManager) GetRetrySettings() RetrySettingsValues {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	enabled := true
	maxRetries := 3.0
	baseDelayMs := 2000.0
	if retry, ok := settingsObject(m.settings, "retry"); ok {
		if value, present := settingsBool(retry, "enabled"); present {
			enabled = value
		}
		if value, present := settingsNumber(retry, "maxRetries"); present {
			maxRetries = value
		}
		if value, present := settingsNumber(retry, "baseDelayMs"); present {
			baseDelayMs = value
		}
	}
	return RetrySettingsValues{
		Enabled:     enabled,
		MaxRetries:  int64(maxRetries),
		BaseDelayMs: int64(baseDelayMs),
	}
}

// GetHTTPIdleTimeoutMs returns the HTTP idle timeout (pi:
// getHttpIdleTimeoutMs; pi throws on invalid values — pig returns error).
func (m *SettingsManager) GetHTTPIdleTimeoutMs() (int64, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	timeoutMs, err := parseTimeoutSetting(m.settings["httpIdleTimeoutMs"], "httpIdleTimeoutMs")
	if err != nil {
		return 0, err
	}
	if timeoutMs == nil {
		return DefaultHTTPIdleTimeoutMs, nil
	}
	return *timeoutMs, nil
}

// SetHTTPIdleTimeoutMs records the HTTP idle timeout (pi:
// setHttpIdleTimeoutMs; pi throws on invalid values — pig returns error).
func (m *SettingsManager) SetHTTPIdleTimeoutMs(timeoutMs float64) error {
	if timeoutMs != timeoutMs || timeoutMs < 0 { // NaN or negative
		return fmt.Errorf("Invalid httpIdleTimeoutMs setting: %s", formatAnyValue(timeoutMs))
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["httpIdleTimeoutMs"] = float64(int64(timeoutMs)) // Math.floor
	m.markModified("httpIdleTimeoutMs", "")
	m.save()
	return nil
}

// ProviderRetrySettingsValues is the resolved provider retry shape (pi:
// getProviderRetrySettings's inline object literal).
type ProviderRetrySettingsValues struct {
	TimeoutMs       *int64
	MaxRetries      *int64
	MaxRetryDelayMs int64
}

// GetProviderRetrySettings returns the resolved provider retry settings (pi:
// getProviderRetrySettings).
func (m *SettingsManager) GetProviderRetrySettings() ProviderRetrySettingsValues {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	result := ProviderRetrySettingsValues{MaxRetryDelayMs: 60000}
	retry, ok := settingsObject(m.settings, "retry")
	if !ok {
		return result
	}
	provider, hasProvider := settingsObject(retry, "provider")
	if !hasProvider {
		return result
	}
	if value, present := settingsNumber(provider, "timeoutMs"); present {
		result.TimeoutMs = lo.ToPtr(int64(value))
	}
	if value, present := settingsNumber(provider, "maxRetries"); present {
		result.MaxRetries = lo.ToPtr(int64(value))
	}
	if value, present := settingsNumber(provider, "maxRetryDelayMs"); present {
		result.MaxRetryDelayMs = int64(value)
	}
	return result
}

// GetWebSocketConnectTimeoutMs returns the WebSocket connect timeout (pi:
// getWebSocketConnectTimeoutMs; nil = unset).
func (m *SettingsManager) GetWebSocketConnectTimeoutMs() (*int64, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return parseTimeoutSetting(m.settings["websocketConnectTimeoutMs"], "websocketConnectTimeoutMs")
}

// GetHideThinkingBlock returns whether thinking blocks are hidden (pi:
// getHideThinkingBlock).
func (m *SettingsManager) GetHideThinkingBlock() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	hide, ok := settingsBool(m.settings, "hideThinkingBlock")
	if !ok {
		return false
	}
	return hide
}

// GetShowCacheMissNotices returns whether cache-miss notices show (pi:
// getShowCacheMissNotices).
func (m *SettingsManager) GetShowCacheMissNotices() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	show, ok := settingsBool(m.settings, "showCacheMissNotices")
	if !ok {
		return false
	}
	return show
}

// externalEditorDefaultPlatform is the platform probe for
// GetExternalEditorCommand (pi reads process.platform; a var so tests can
// pin the platform like pi's Object.defineProperty mock).
var externalEditorDefaultPlatform = runtime.GOOS

// GetExternalEditorCommand resolves the external editor command by
// precedence (pi: getExternalEditorCommand).
func (m *SettingsManager) GetExternalEditorCommand() string {
	m.mutex.Lock()
	configuredEditor, _ := settingsString(m.settings, "externalEditor")
	m.mutex.Unlock()
	if strings.TrimSpace(configuredEditor) != "" {
		return configuredEditor
	}
	environmentEditor := os.Getenv("VISUAL")
	if environmentEditor == "" {
		environmentEditor = os.Getenv("EDITOR")
	}
	if environmentEditor != "" {
		return environmentEditor
	}
	if externalEditorDefaultPlatform == "windows" {
		return "notepad"
	}
	return "nano"
}

// SetHideThinkingBlock records the thinking-block toggle (pi:
// setHideThinkingBlock).
func (m *SettingsManager) SetHideThinkingBlock(hide bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["hideThinkingBlock"] = hide
	m.markModified("hideThinkingBlock", "")
	m.save()
}

// SetShowCacheMissNotices records the cache-miss notice toggle (pi:
// setShowCacheMissNotices).
func (m *SettingsManager) SetShowCacheMissNotices(show bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["showCacheMissNotices"] = show
	m.markModified("showCacheMissNotices", "")
	m.save()
}

// GetShellPath returns the custom shell path with ~ expanded (pi:
// getShellPath).
func (m *SettingsManager) GetShellPath() (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	shellPath, ok := settingsString(m.settings, "shellPath")
	if !ok || shellPath == "" {
		return shellPath, ok
	}
	return utils.NormalizePath(shellPath, nil), true
}

// SetShellPath records the custom shell path (pi: setShellPath).
func (m *SettingsManager) SetShellPath(path string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if path == "" {
		delete(m.globalSettings, "shellPath")
	} else {
		m.globalSettings["shellPath"] = path
	}
	m.markModified("shellPath", "")
	m.save()
}

// GetQuietStartup returns the quiet startup toggle (pi: getQuietStartup).
func (m *SettingsManager) GetQuietStartup() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	quiet, ok := settingsBool(m.settings, "quietStartup")
	if !ok {
		return false
	}
	return quiet
}

// SetQuietStartup records the quiet startup toggle (pi: setQuietStartup).
func (m *SettingsManager) SetQuietStartup(quiet bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["quietStartup"] = quiet
	m.markModified("quietStartup", "")
	m.save()
}

// GetShowResourceStats returns whether the footer shows process resource
// stats (pig-only; no pi counterpart — pi's footer shows token stats only).
// Default false: the segment stays hidden until explicitly enabled.
func (m *SettingsManager) GetShowResourceStats() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	show, ok := settingsBool(m.settings, "showResourceStats")
	if !ok {
		return false
	}
	return show
}

// SetShowResourceStats records the footer resource-stats toggle (pig-only).
func (m *SettingsManager) SetShowResourceStats(show bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["showResourceStats"] = show
	m.markModified("showResourceStats", "")
	m.save()
}

// GetDefaultProjectTrust returns the default project trust (pi:
// getDefaultProjectTrust; reads global settings only, invalid → "ask").
func (m *SettingsManager) GetDefaultProjectTrust() DefaultProjectTrust {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	value, _ := settingsString(m.globalSettings, "defaultProjectTrust")
	if value == DefaultProjectTrustAlways || value == DefaultProjectTrustNever {
		return value
	}
	return DefaultProjectTrustAsk
}

// SetDefaultProjectTrust records the default project trust (pi:
// setDefaultProjectTrust).
func (m *SettingsManager) SetDefaultProjectTrust(defaultProjectTrust DefaultProjectTrust) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["defaultProjectTrust"] = string(defaultProjectTrust)
	m.markModified("defaultProjectTrust", "")
	m.save()
}

// GetShellCommandPrefix returns the shell command prefix (pi:
// getShellCommandPrefix).
func (m *SettingsManager) GetShellCommandPrefix() (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return settingsString(m.settings, "shellCommandPrefix")
}

// SetShellCommandPrefix records the shell command prefix (pi:
// setShellCommandPrefix; undefined → delete).
func (m *SettingsManager) SetShellCommandPrefix(prefix string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if prefix == "" {
		delete(m.globalSettings, "shellCommandPrefix")
	} else {
		m.globalSettings["shellCommandPrefix"] = prefix
	}
	m.markModified("shellCommandPrefix", "")
	m.save()
}

// GetNpmCommand returns a copy of the npm command argv (pi: getNpmCommand).
func (m *SettingsManager) GetNpmCommand() ([]string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	command, ok := settingsStringSlice(m.settings, "npmCommand")
	if !ok {
		return nil, false
	}
	return append([]string(nil), command...), true
}

// SetNpmCommand records the npm command argv (pi: setNpmCommand; undefined →
// delete).
func (m *SettingsManager) SetNpmCommand(command []string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if command == nil {
		delete(m.globalSettings, "npmCommand")
	} else {
		m.globalSettings["npmCommand"] = stringsToNative(append([]string(nil), command...))
	}
	m.markModified("npmCommand", "")
	m.save()
}

// GetCollapseChangelog returns the changelog collapse toggle (pi:
// getCollapseChangelog).
func (m *SettingsManager) GetCollapseChangelog() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	collapse, ok := settingsBool(m.settings, "collapseChangelog")
	if !ok {
		return false
	}
	return collapse
}

// SetCollapseChangelog records the changelog collapse toggle (pi:
// setCollapseChangelog).
func (m *SettingsManager) SetCollapseChangelog(collapse bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["collapseChangelog"] = collapse
	m.markModified("collapseChangelog", "")
	m.save()
}

// GetEnableInstallTelemetry returns the install telemetry toggle (pi:
// getEnableInstallTelemetry).
func (m *SettingsManager) GetEnableInstallTelemetry() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	enabled, ok := settingsBool(m.settings, "enableInstallTelemetry")
	if !ok {
		return true
	}
	return enabled
}

// SetEnableInstallTelemetry records the install telemetry toggle (pi:
// setEnableInstallTelemetry).
func (m *SettingsManager) SetEnableInstallTelemetry(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["enableInstallTelemetry"] = enabled
	m.markModified("enableInstallTelemetry", "")
	m.save()
}

// GetEnableAnalytics returns the analytics opt-in toggle (pi:
// getEnableAnalytics).
func (m *SettingsManager) GetEnableAnalytics() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	enabled, ok := settingsBool(m.settings, "enableAnalytics")
	if !ok {
		return false
	}
	return enabled
}

// GetTrackingId returns the analytics tracking identifier (pi:
// getTrackingId).
func (m *SettingsManager) GetTrackingId() (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return settingsString(m.settings, "trackingId")
}

// SetEnableAnalytics records the analytics opt-in; generates a tracking
// identifier on first opt-in (pi: setEnableAnalytics).
func (m *SettingsManager) SetEnableAnalytics(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["enableAnalytics"] = enabled
	m.markModified("enableAnalytics", "")
	if enabled {
		if _, hasTrackingID := settingsString(m.globalSettings, "trackingId"); !hasTrackingID {
			m.globalSettings["trackingId"] = randomUUID()
			m.markModified("trackingId", "")
		}
	}
	m.save()
}

// randomUUID generates a random UUIDv4 (pi: crypto.randomUUID from node
// crypto; pig has no shared v4 helper yet — generated here).
func randomUUID() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return ""
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40 // version 4
	buffer[8] = (buffer[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

// GetPackages returns a copy of the package sources (pi: getPackages).
func (m *SettingsManager) GetPackages() []PackageSource {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	items, ok := m.settings["packages"].([]any)
	if !ok {
		return []PackageSource{}
	}
	result := make([]PackageSource, 0, len(items))
	for _, item := range items {
		result = append(result, packageSourceFromNative(item))
	}
	return result
}

// SetPackages records the package sources (pi: setPackages).
func (m *SettingsManager) SetPackages(packages []PackageSource) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["packages"] = packageSourcesToNative(packages)
	m.markModified("packages", "")
	m.save()
}

// SetProjectPackages records the project package sources (pi:
// setProjectPackages).
func (m *SettingsManager) SetProjectPackages(packages []PackageSource) error {
	return m.updateProjectSettings("packages", func(settings Settings) {
		settings["packages"] = packageSourcesToNative(packages)
	})
}

// packageSourcesToNative converts package sources to their settings value.
func packageSourcesToNative(packages []PackageSource) []any {
	result := make([]any, len(packages))
	for i, pkg := range packages {
		result[i] = pkg.toNative()
	}
	return result
}

// GetExtensionPaths returns a copy of the extension paths (pi:
// getExtensionPaths).
func (m *SettingsManager) GetExtensionPaths() []string {
	return m.getStringListSetting("extensions")
}

// SetExtensionPaths records the extension paths (pi: setExtensionPaths).
func (m *SettingsManager) SetExtensionPaths(paths []string) {
	m.setStringListSetting("extensions", paths)
}

// SetProjectExtensionPaths records the project extension paths (pi:
// setProjectExtensionPaths).
func (m *SettingsManager) SetProjectExtensionPaths(paths []string) error {
	return m.updateProjectStringList("extensions", paths)
}

// GetSkillPaths returns a copy of the skill paths (pi: getSkillPaths).
func (m *SettingsManager) GetSkillPaths() []string {
	return m.getStringListSetting("skills")
}

// SetSkillPaths records the skill paths (pi: setSkillPaths).
func (m *SettingsManager) SetSkillPaths(paths []string) {
	m.setStringListSetting("skills", paths)
}

// SetProjectSkillPaths records the project skill paths (pi:
// setProjectSkillPaths).
func (m *SettingsManager) SetProjectSkillPaths(paths []string) error {
	return m.updateProjectStringList("skills", paths)
}

// GetPromptTemplatePaths returns a copy of the prompt template paths (pi:
// getPromptTemplatePaths).
func (m *SettingsManager) GetPromptTemplatePaths() []string {
	return m.getStringListSetting("prompts")
}

// SetPromptTemplatePaths records the prompt template paths (pi:
// setPromptTemplatePaths).
func (m *SettingsManager) SetPromptTemplatePaths(paths []string) {
	m.setStringListSetting("prompts", paths)
}

// SetProjectPromptTemplatePaths records the project prompt template paths
// (pi: setProjectPromptTemplatePaths).
func (m *SettingsManager) SetProjectPromptTemplatePaths(paths []string) error {
	return m.updateProjectStringList("prompts", paths)
}

// GetThemePaths returns a copy of the theme paths (pi: getThemePaths).
func (m *SettingsManager) GetThemePaths() []string {
	return m.getStringListSetting("themes")
}

// SetThemePaths records the theme paths (pi: setThemePaths).
func (m *SettingsManager) SetThemePaths(paths []string) {
	m.setStringListSetting("themes", paths)
}

// SetProjectThemePaths records the project theme paths (pi:
// setProjectThemePaths).
func (m *SettingsManager) SetProjectThemePaths(paths []string) error {
	return m.updateProjectStringList("themes", paths)
}

// getStringListSetting reads one string-list setting (shared helper for the
// path-list getters; pi spreads each list inline).
func (m *SettingsManager) getStringListSetting(key string) []string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	items, ok := settingsStringSlice(m.settings, key)
	if !ok {
		return []string{}
	}
	return append([]string(nil), items...)
}

// setStringListSetting writes one string-list setting into the global
// settings (shared helper; undefined → delete like pi's optional writes).
func (m *SettingsManager) setStringListSetting(key string, paths []string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if paths == nil {
		delete(m.globalSettings, key)
	} else {
		m.globalSettings[key] = stringsToNative(append([]string(nil), paths...))
	}
	m.markModified(key, "")
	m.save()
}

// updateProjectStringList writes one project string-list setting (shared
// helper for the setProject*Path family).
func (m *SettingsManager) updateProjectStringList(key string, paths []string) error {
	return m.updateProjectSettings(key, func(settings Settings) {
		settings[key] = stringsToNative(append([]string(nil), paths...))
	})
}

// GetEnableSkillCommands returns whether skills register as commands (pi:
// getEnableSkillCommands).
func (m *SettingsManager) GetEnableSkillCommands() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	enabled, ok := settingsBool(m.settings, "enableSkillCommands")
	if !ok {
		return true
	}
	return enabled
}

// SetEnableSkillCommands records the skill-commands toggle (pi:
// setEnableSkillCommands).
func (m *SettingsManager) SetEnableSkillCommands(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["enableSkillCommands"] = enabled
	m.markModified("enableSkillCommands", "")
	m.save()
}

// GetThinkingBudgets returns the thinking budget overrides (pi:
// getThinkingBudgets; pi returns the live nested object, pig a fresh struct
// — declared).
func (m *SettingsManager) GetThinkingBudgets() (*ThinkingBudgetsSettings, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	budgets, ok := settingsObject(m.settings, "thinkingBudgets")
	if !ok {
		return nil, false
	}
	result := &ThinkingBudgetsSettings{}
	if value, present := settingsNumber(budgets, "minimal"); present {
		result.Minimal = lo.ToPtr(value)
	}
	if value, present := settingsNumber(budgets, "low"); present {
		result.Low = lo.ToPtr(value)
	}
	if value, present := settingsNumber(budgets, "medium"); present {
		result.Medium = lo.ToPtr(value)
	}
	if value, present := settingsNumber(budgets, "high"); present {
		result.High = lo.ToPtr(value)
	}
	return result, true
}

// GetTerminalCapabilityOverrides returns user-forced terminal capability
// overrides (pi: getTerminalCapabilityOverrides, v0.85.1).
func (m *SettingsManager) GetTerminalCapabilityOverrides() TerminalCapabilityOverrides {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	var overrides TerminalCapabilityOverrides
	terminal, ok := settingsObject(m.settings, "terminal")
	if !ok {
		return overrides
	}
	if images, present := terminal["images"]; present {
		switch v := images.(type) {
		case bool:
			if v {
				overrides.Images = true
			} else {
				// pi: images === false → images: null (force off).
				overrides.Images = false
			}
		case string:
			if v == "kitty" || v == "iterm2" {
				overrides.Images = v
			}
		}
	}
	if value, present := settingsBool(terminal, "trueColor"); present {
		overrides.TrueColor = &value
	}
	if value, present := settingsBool(terminal, "hyperlinks"); present {
		overrides.Hyperlinks = &value
	}
	return overrides
}

// GetShowImages returns whether inline images show (pi: getShowImages).
func (m *SettingsManager) GetShowImages() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	terminal, ok := settingsObject(m.settings, "terminal")
	if !ok {
		return true
	}
	show, ok := settingsBool(terminal, "showImages")
	if !ok {
		return true
	}
	return show
}

// SetShowImages records the inline-image toggle (pi: setShowImages).
func (m *SettingsManager) SetShowImages(show bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	terminal, ok := settingsObject(m.globalSettings, "terminal")
	if !ok {
		terminal = Settings{}
		m.globalSettings["terminal"] = terminal
	}
	terminal["showImages"] = show
	m.markModified("terminal", "showImages")
	m.save()
}

// GetImageWidthCells returns the inline image width (pi: getImageWidthCells;
// non-finite → 60, floored and clamped to ≥ 1).
func (m *SettingsManager) GetImageWidthCells() int64 {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	terminal, ok := settingsObject(m.settings, "terminal")
	if !ok {
		return 60
	}
	width, ok := settingsNumber(terminal, "imageWidthCells")
	if !ok {
		return 60
	}
	return max64(1, int64(width))
}

// SetImageWidthCells records the inline image width (pi: setImageWidthCells).
func (m *SettingsManager) SetImageWidthCells(width int64) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	terminal, ok := settingsObject(m.globalSettings, "terminal")
	if !ok {
		terminal = Settings{}
		m.globalSettings["terminal"] = terminal
	}
	terminal["imageWidthCells"] = float64(max64(1, width))
	m.markModified("terminal", "imageWidthCells")
	m.save()
}

// GetClearOnShrink returns the clear-on-shrink toggle (pi:
// getClearOnShrink — settings, then PI_CLEAR_ON_SHRINK=1, then false).
func (m *SettingsManager) GetClearOnShrink() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if terminal, ok := settingsObject(m.settings, "terminal"); ok {
		if value, present := settingsBool(terminal, "clearOnShrink"); present {
			return value
		}
	}
	return os.Getenv("PI_CLEAR_ON_SHRINK") == "1"
}

// SetClearOnShrink records the clear-on-shrink toggle (pi:
// setClearOnShrink).
func (m *SettingsManager) SetClearOnShrink(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	terminal, ok := settingsObject(m.globalSettings, "terminal")
	if !ok {
		terminal = Settings{}
		m.globalSettings["terminal"] = terminal
	}
	terminal["clearOnShrink"] = enabled
	m.markModified("terminal", "clearOnShrink")
	m.save()
}

// GetShowTerminalProgress returns the terminal progress toggle (pi:
// getShowTerminalProgress).
func (m *SettingsManager) GetShowTerminalProgress() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	terminal, ok := settingsObject(m.settings, "terminal")
	if !ok {
		return false
	}
	show, ok := settingsBool(terminal, "showTerminalProgress")
	if !ok {
		return false
	}
	return show
}

// SetShowTerminalProgress records the terminal progress toggle (pi:
// setShowTerminalProgress).
func (m *SettingsManager) SetShowTerminalProgress(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	terminal, ok := settingsObject(m.globalSettings, "terminal")
	if !ok {
		terminal = Settings{}
		m.globalSettings["terminal"] = terminal
	}
	terminal["showTerminalProgress"] = enabled
	m.markModified("terminal", "showTerminalProgress")
	m.save()
}

// GetTuiMode returns the renderer mode (pi: getTuiMode — non-"fullscreen" →
// "regular").
func (m *SettingsManager) GetTuiMode() TuiMode {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	mode, _ := settingsString(m.settings, "tuiMode")
	if mode == TuiModeFullscreen {
		return TuiModeFullscreen
	}
	return TuiModeRegular
}

// SetTuiMode records the renderer mode (pi: setTuiMode).
func (m *SettingsManager) SetTuiMode(mode TuiMode) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["tuiMode"] = string(mode)
	m.markModified("tuiMode", "")
	m.save()
}

// GetFullscreenExitOutput returns the fullscreen exit output (pi:
// getFullscreenExitOutput — non-"resume-hint" → "transcript").
func (m *SettingsManager) GetFullscreenExitOutput() FullscreenExitOutput {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	output, _ := settingsString(m.settings, "fullscreenExitOutput")
	if output == FullscreenExitOutputResumeHint {
		return FullscreenExitOutputResumeHint
	}
	return FullscreenExitOutputTranscript
}

// SetFullscreenExitOutput records the fullscreen exit output (pi:
// setFullscreenExitOutput).
func (m *SettingsManager) SetFullscreenExitOutput(output FullscreenExitOutput) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["fullscreenExitOutput"] = string(output)
	m.markModified("fullscreenExitOutput", "")
	m.save()
}

// GetFullscreenScrollbar returns the fullscreen scrollbar mode (pi:
// getFullscreenScrollbar — only "always"/"hidden" pass through).
func (m *SettingsManager) GetFullscreenScrollbar() ScrollViewScrollbar {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	mode, _ := settingsString(m.settings, "fullscreenScrollbar")
	if mode == ScrollViewScrollbarAlways || mode == ScrollViewScrollbarHidden {
		return mode
	}
	return ScrollViewScrollbarAuto
}

// SetFullscreenScrollbar records the fullscreen scrollbar mode (pi:
// setFullscreenScrollbar).
func (m *SettingsManager) SetFullscreenScrollbar(mode ScrollViewScrollbar) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["fullscreenScrollbar"] = string(mode)
	m.markModified("fullscreenScrollbar", "")
	m.save()
}

// GetFullscreenCopyOnSelect returns whether selecting text in the fullscreen
// transcript copies to the clipboard (pi: getFullscreenCopyOnSelect —
// v0.85.1; default true; no effect in regular TUI mode).
func (m *SettingsManager) GetFullscreenCopyOnSelect() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	enabled, ok := settingsBool(m.settings, "fullscreenCopyOnSelect")
	if !ok {
		return true
	}
	return enabled
}

// SetFullscreenCopyOnSelect records the fullscreen copy-on-select toggle (pi:
// setFullscreenCopyOnSelect — v0.85.1).
func (m *SettingsManager) SetFullscreenCopyOnSelect(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["fullscreenCopyOnSelect"] = enabled
	m.markModified("fullscreenCopyOnSelect", "")
	m.save()
}

// GetImageAutoResize returns the image auto-resize toggle (pi:
// getImageAutoResize).
func (m *SettingsManager) GetImageAutoResize() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	images, ok := settingsObject(m.settings, "images")
	if !ok {
		return true
	}
	enabled, ok := settingsBool(images, "autoResize")
	if !ok {
		return true
	}
	return enabled
}

// SetImageAutoResize records the image auto-resize toggle (pi:
// setImageAutoResize).
func (m *SettingsManager) SetImageAutoResize(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	images, ok := settingsObject(m.globalSettings, "images")
	if !ok {
		images = Settings{}
		m.globalSettings["images"] = images
	}
	images["autoResize"] = enabled
	m.markModified("images", "autoResize")
	m.save()
}

// GetBlockImages returns the image block toggle (pi: getBlockImages).
func (m *SettingsManager) GetBlockImages() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	images, ok := settingsObject(m.settings, "images")
	if !ok {
		return false
	}
	blocked, ok := settingsBool(images, "blockImages")
	if !ok {
		return false
	}
	return blocked
}

// SetBlockImages records the image block toggle (pi: setBlockImages).
func (m *SettingsManager) SetBlockImages(blocked bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	images, ok := settingsObject(m.globalSettings, "images")
	if !ok {
		images = Settings{}
		m.globalSettings["images"] = images
	}
	images["blockImages"] = blocked
	m.markModified("images", "blockImages")
	m.save()
}

// GetEnabledModels returns the model cycling patterns (pi:
// getEnabledModels).
func (m *SettingsManager) GetEnabledModels() ([]string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return settingsStringSlice(m.settings, "enabledModels")
}

// GetDefaultTools returns the initial built-in tool selection (pi:
// getDefaultTools — an empty list is preserved).
func (m *SettingsManager) GetDefaultTools() ([]string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	tools, ok := settingsStringSlice(m.settings, "defaultTools")
	if !ok {
		return nil, false
	}
	return append([]string(nil), tools...), true
}

// SetEnabledModels records the model cycling patterns (pi:
// setEnabledModels; undefined → delete).
func (m *SettingsManager) SetEnabledModels(patterns []string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if patterns == nil {
		delete(m.globalSettings, "enabledModels")
	} else {
		m.globalSettings["enabledModels"] = stringsToNative(append([]string(nil), patterns...))
	}
	m.markModified("enabledModels", "")
	m.save()
}

// GetDoubleEscapeAction returns the double-escape action (pi:
// getDoubleEscapeAction).
func (m *SettingsManager) GetDoubleEscapeAction() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	action, ok := settingsString(m.settings, "doubleEscapeAction")
	if !ok {
		return "tree"
	}
	return action
}

// SetDoubleEscapeAction records the double-escape action (pi:
// setDoubleEscapeAction).
func (m *SettingsManager) SetDoubleEscapeAction(action string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["doubleEscapeAction"] = action
	m.markModified("doubleEscapeAction", "")
	m.save()
}

// treeFilterModes lists the valid tree filter modes (pi: getTreeFilterMode's
// valid list).
var treeFilterModes = []string{"default", "no-tools", "user-only", "labeled-only", "all"}

// GetTreeFilterMode returns the /tree filter default (pi: getTreeFilterMode
// — invalid values → "default").
func (m *SettingsManager) GetTreeFilterMode() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	mode, ok := settingsString(m.settings, "treeFilterMode")
	if !ok {
		return "default"
	}
	for _, valid := range treeFilterModes {
		if mode == valid {
			return mode
		}
	}
	return "default"
}

// SetTreeFilterMode records the /tree filter default (pi: setTreeFilterMode).
func (m *SettingsManager) SetTreeFilterMode(mode string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["treeFilterMode"] = mode
	m.markModified("treeFilterMode", "")
	m.save()
}

// cjkSegmenterModes lists the valid CJK word-segmentation backends
// (pig-only setting; no pi counterpart — Intl.Segmenter ships inside the
// JS runtime; see tui/cjk_segmenter.go for the backend semantics).
var cjkSegmenterModes = []string{"bigram", "sst", "gse"}

// GetCJKSegmenter returns the CJK word-segmentation backend (pig-only;
// no pi counterpart — Intl.Segmenter ships inside the JS runtime). An
// invalid stored value resolves to "bigram", matching the tui resolver's
// never-error contract (configuration can only degrade word navigation,
// never break it).
func (m *SettingsManager) GetCJKSegmenter() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if mode, ok := settingsString(m.settings, "cjkSegmenter"); ok {
		for _, valid := range cjkSegmenterModes {
			if mode == valid {
				return mode
			}
		}
	}
	return "bigram"
}

// SetCJKSegmenter records the CJK word-segmentation backend (pig-only).
// Invalid values are ignored, mirroring the tui resolver's never-error
// contract (configuration can only degrade word navigation, never break
// it). Mid-session changes persist here; the in-process backend applies
// them from the next start (or immediately when it has not resolved yet).
func (m *SettingsManager) SetCJKSegmenter(mode string) {
	valid := false
	for _, candidate := range cjkSegmenterModes {
		if mode == candidate {
			valid = true
			break
		}
	}
	if !valid {
		return
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["cjkSegmenter"] = mode
	m.markModified("cjkSegmenter", "")
	m.save()
}

// GetShowHardwareCursor returns the hardware cursor toggle (pi:
// getShowHardwareCursor — settings, then PI_HARDWARE_CURSOR=1).
func (m *SettingsManager) GetShowHardwareCursor() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	show, ok := settingsBool(m.settings, "showHardwareCursor")
	if ok {
		return show
	}
	return os.Getenv("PI_HARDWARE_CURSOR") == "1"
}

// SetShowHardwareCursor records the hardware cursor toggle (pi:
// setShowHardwareCursor).
func (m *SettingsManager) SetShowHardwareCursor(enabled bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["showHardwareCursor"] = enabled
	m.markModified("showHardwareCursor", "")
	m.save()
}

// GetEditorPaddingX returns the editor horizontal padding (pi:
// getEditorPaddingX).
func (m *SettingsManager) GetEditorPaddingX() int64 {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	padding, ok := settingsNumber(m.settings, "editorPaddingX")
	if !ok {
		return 0
	}
	return int64(padding)
}

// SetEditorPaddingX records the editor horizontal padding, clamped to 0..3
// (pi: setEditorPaddingX).
func (m *SettingsManager) SetEditorPaddingX(padding int64) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["editorPaddingX"] = float64(min64(max64(0, padding), 3))
	m.markModified("editorPaddingX", "")
	m.save()
}

// GetOutputPad returns the chat output padding (pi: getOutputPad — only an
// exact 0 yields 0).
func (m *SettingsManager) GetOutputPad() int {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	padding, ok := settingsNumber(m.settings, "outputPad")
	if ok && padding == 0 {
		return 0
	}
	return 1
}

// SetOutputPad records the chat output padding (pi: setOutputPad).
func (m *SettingsManager) SetOutputPad(padding int) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["outputPad"] = float64(padding)
	m.markModified("outputPad", "")
	m.save()
}

// GetAutocompleteMaxVisible returns the autocomplete dropdown size (pi:
// getAutocompleteMaxVisible).
func (m *SettingsManager) GetAutocompleteMaxVisible() int64 {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	maxVisible, ok := settingsNumber(m.settings, "autocompleteMaxVisible")
	if !ok {
		return 5
	}
	return int64(maxVisible)
}

// SetAutocompleteMaxVisible records the autocomplete dropdown size, clamped
// to 3..20 (pi: setAutocompleteMaxVisible).
func (m *SettingsManager) SetAutocompleteMaxVisible(maxVisible int64) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.globalSettings["autocompleteMaxVisible"] = float64(min64(max64(3, maxVisible), 20))
	m.markModified("autocompleteMaxVisible", "")
	m.save()
}

// GetCodeBlockIndent returns the markdown code block indent (pi:
// getCodeBlockIndent).
func (m *SettingsManager) GetCodeBlockIndent() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	markdown, ok := settingsObject(m.settings, "markdown")
	if !ok {
		return "  "
	}
	indent, ok := settingsString(markdown, "codeBlockIndent")
	if !ok {
		return "  "
	}
	return indent
}

// GetMermaidRenderingMode returns the mermaid rendering mode (pi:
// getMermaidRenderingMode — only "off"/"final" pass through).
func (m *SettingsManager) GetMermaidRenderingMode() MermaidRenderingMode {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	markdown, ok := settingsObject(m.settings, "markdown")
	if !ok {
		return MermaidRenderingStreaming
	}
	mode, ok := settingsString(markdown, "mermaid")
	if !ok {
		return MermaidRenderingStreaming
	}
	if mode == MermaidRenderingOff || mode == MermaidRenderingFinal {
		return mode
	}
	return MermaidRenderingStreaming
}

// SetMermaidRenderingMode records the mermaid rendering mode (pi:
// setMermaidRenderingMode).
func (m *SettingsManager) SetMermaidRenderingMode(mode MermaidRenderingMode) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	markdown, ok := settingsObject(m.globalSettings, "markdown")
	if !ok {
		markdown = Settings{}
		m.globalSettings["markdown"] = markdown
	}
	markdown["mermaid"] = string(mode)
	m.markModified("markdown", "mermaid")
	m.save()
}

// GetWarnings returns a copy of the warning toggles (pi: getWarnings).
func (m *SettingsManager) GetWarnings() WarningSettings {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	warnings, ok := settingsObject(m.settings, "warnings")
	if !ok {
		return WarningSettings{}
	}
	result := WarningSettings{}
	if value, present := settingsBool(warnings, "anthropicExtraUsage"); present {
		result.AnthropicExtraUsage = lo.ToPtr(value)
	}
	return result
}

// SetWarnings records the warning toggles (pi: setWarnings).
func (m *SettingsManager) SetWarnings(warnings WarningSettings) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	native := Settings{}
	if warnings.AnthropicExtraUsage != nil {
		native["anthropicExtraUsage"] = *warnings.AnthropicExtraUsage
	}
	m.globalSettings["warnings"] = native
	m.markModified("warnings", "")
	m.save()
}

// max64 returns the larger of two int64 values (pi: Math.max on numbers).
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// min64 returns the smaller of two int64 values (pi: Math.min on numbers).
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
