// keybindings.go (pi: src/core/keybindings.ts).
//
// coding-agent keybindings: the merged definition table (pi-tui defaults +
// coding-agent's tui.* overrides and app.* bindings), legacy-name migration,
// and the user keybindings.json loader. The manager itself extends the tui
// package's KeybindingsManager (pi: KeybindingsManager extends
// TuiKeybindingsManager) via embedding.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// AppKeybinding is the app-level keybinding id union (pi: AppKeybinding =
// keyof AppKeybindings — an open string union in pig, matching the repo's
// `X | string` open-union rule).
type AppKeybinding = string

// App keybinding ids (pi: AppKeybindings interface keys, declaration order
// preserved).
const (
	AppKeybindingInterrupt                = "app.interrupt"
	AppKeybindingClear                    = "app.clear"
	AppKeybindingExit                     = "app.exit"
	AppKeybindingSuspend                  = "app.suspend"
	AppKeybindingThinkingCycle            = "app.thinking.cycle"
	AppKeybindingThinkingSave             = "app.thinking.save"
	AppKeybindingModelCycleForward        = "app.model.cycleForward"
	AppKeybindingModelCycleBackward       = "app.model.cycleBackward"
	AppKeybindingModelSelect              = "app.model.select"
	AppKeybindingToolsExpand              = "app.tools.expand"
	AppKeybindingThinkingToggle           = "app.thinking.toggle"
	AppKeybindingSessionToggleNamedFilter = "app.session.toggleNamedFilter"
	AppKeybindingEditorExternal           = "app.editor.external"
	AppKeybindingMessageCopy              = "app.message.copy"
	AppKeybindingMessageFollowUp          = "app.message.followUp"
	AppKeybindingMessageDequeue           = "app.message.dequeue"
	AppKeybindingClipboardPasteImage      = "app.clipboard.pasteImage"
	AppKeybindingSessionNew               = "app.session.new"
	AppKeybindingSessionTree              = "app.session.tree"
	AppKeybindingSessionFork              = "app.session.fork"
	AppKeybindingSessionResume            = "app.session.resume"
	AppKeybindingTreeFoldOrUp             = "app.tree.foldOrUp"
	AppKeybindingTreeUnfoldOrDown         = "app.tree.unfoldOrDown"
	AppKeybindingTreeEditLabel            = "app.tree.editLabel"
	AppKeybindingTreeToggleLabelTimestamp = "app.tree.toggleLabelTimestamp"
	AppKeybindingSessionTogglePath        = "app.session.togglePath"
	AppKeybindingSessionToggleSort        = "app.session.toggleSort"
	AppKeybindingSessionRename            = "app.session.rename"
	AppKeybindingSessionDelete            = "app.session.delete"
	AppKeybindingSessionDeleteNoninvasive = "app.session.deleteNoninvasive"
	AppKeybindingModelsSave               = "app.models.save"
	AppKeybindingModelsEnableAll          = "app.models.enableAll"
	AppKeybindingModelsClearAll           = "app.models.clearAll"
	AppKeybindingModelsToggleProvider     = "app.models.toggleProvider"
	AppKeybindingModelsReorderUp          = "app.models.reorderUp"
	AppKeybindingModelsReorderDown        = "app.models.reorderDown"
	AppKeybindingTreeFilterDefault        = "app.tree.filter.default"
	AppKeybindingTreeFilterNoTools        = "app.tree.filter.noTools"
	AppKeybindingTreeFilterUserOnly       = "app.tree.filter.userOnly"
	AppKeybindingTreeFilterLabeledOnly    = "app.tree.filter.labeledOnly"
	AppKeybindingTreeFilterAll            = "app.tree.filter.all"
	AppKeybindingTreeFilterCycleForward   = "app.tree.filter.cycleForward"
	AppKeybindingTreeFilterCycleBackward  = "app.tree.filter.cycleBackward"

	// pig-only: /mcp panel action keys (pi-mcp-adapter panel-keys semantics).
	AppKeybindingMCPReconnect      = "app.mcp.reconnect"
	AppKeybindingMCPToggleDisabled = "app.mcp.toggleDisabled"
	AppKeybindingMCPCopyError      = "app.mcp.copyError"
)

// UseWindowsKeybindings reports whether Windows-flavoured defaults apply
// (pi: useWindowsKeybindings — native Windows or WSL).
func UseWindowsKeybindings(platform string, env func(key string) (string, bool)) bool {
	if env == nil {
		env = os.LookupEnv
	}
	if platform == "windows" {
		return true
	}
	if platform == "linux" {
		if v, ok := env("WSL_DISTRO_NAME"); ok && v != "" {
			return true
		}
		if v, ok := env("WSL_INTEROP"); ok && v != "" {
			return true
		}
	}
	return false
}

func useWindowsKeybindings() bool {
	return UseWindowsKeybindings(runtime.GOOS, nil)
}

func keys(values ...string) []tui.KeyId {
	return values
}

// Keybindings holds the merged default keybinding definitions (pi:
// KEYBINDINGS = { ...TUI_KEYBINDINGS, ...overrides, ...app } — declaration
// order follows pi: tui.* overrides first, then app.* in interface order).
var Keybindings = func() tui.KeybindingDefinitions {
	windows := useWindowsKeybindings()
	defs := tui.KeybindingDefinitions{}
	for id, def := range tui.TUIKeyBindings {
		defs[id] = def
	}
	// pi's tui.* overrides over the pi-tui defaults.
	defs["tui.editor.undo"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if runtime.GOOS == "windows" {
				return keys("ctrl+z")
			}
			if windows {
				return keys("alt+z")
			}
			return keys("ctrl+-")
		}(),
		Description: "Undo",
	}
	defs["tui.altScreen.previousPrompt"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if windows {
				return keys("ctrl+up")
			}
			return keys("ctrl+shift+up", "ctrl+up")
		}(),
		Description: "Jump to previous semantic prompt",
	}
	defs["tui.altScreen.nextPrompt"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if windows {
				return keys("ctrl+down")
			}
			return keys("ctrl+shift+down", "ctrl+down")
		}(),
		Description: "Jump to next semantic prompt",
	}
	defs["tui.altScreen.search"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if windows {
				return keys("ctrl+f")
			}
			return keys("ctrl+shift+f")
		}(),
		Description: "Search the primary scroll view",
	}

	// app.* bindings (pi: KEYBINDINGS app section, in order).
	defs["app.interrupt"] = tui.KeybindingDefinition{DefaultKeys: keys("escape"), Description: "Cancel or abort"}
	defs["app.clear"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+c"), Description: "Clear editor"}
	defs["app.exit"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+d"), Description: "Exit when editor is empty"}
	defs["app.suspend"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if runtime.GOOS == "windows" {
				return []tui.KeyId{}
			}
			return keys("ctrl+z")
		}(),
		Description: "Suspend to background",
	}
	defs["app.thinking.cycle"] = tui.KeybindingDefinition{DefaultKeys: keys("shift+tab"), Description: "Cycle thinking level"}
	defs["app.thinking.save"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+s"), Description: "Save thinking level"}
	defs["app.model.cycleForward"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+p"), Description: "Cycle to next model"}
	defs["app.model.cycleBackward"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if windows {
				return keys("alt+p")
			}
			return keys("shift+ctrl+p")
		}(),
		Description: "Cycle to previous model",
	}
	defs["app.model.select"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+l"), Description: "Open model selector"}
	defs["app.tools.expand"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+o"), Description: "Toggle tool output"}
	defs["app.thinking.toggle"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+t"), Description: "Toggle thinking blocks"}
	defs["app.session.toggleNamedFilter"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+n"), Description: "Toggle named session filter"}
	defs["app.editor.external"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+g"), Description: "Open external editor"}
	defs["app.message.copy"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+x"), Description: "Copy selection or last assistant message"}
	defs["app.message.followUp"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if windows {
				return keys("ctrl+q")
			}
			return keys("alt+enter")
		}(),
		Description: "Queue follow-up message",
	}
	defs["app.message.dequeue"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if windows {
				return keys("alt+q")
			}
			return keys("alt+up")
		}(),
		Description: "Restore queued messages",
	}
	defs["app.clipboard.pasteImage"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if windows {
				return keys("alt+v")
			}
			return keys("ctrl+v")
		}(),
		Description: "Paste image from clipboard (text fallback)",
	}
	defs["app.session.new"] = tui.KeybindingDefinition{DefaultKeys: []tui.KeyId{}, Description: "Start a new session"}
	defs["app.session.tree"] = tui.KeybindingDefinition{DefaultKeys: []tui.KeyId{}, Description: "Open session tree"}
	defs["app.session.fork"] = tui.KeybindingDefinition{DefaultKeys: []tui.KeyId{}, Description: "Fork current session"}
	defs["app.session.resume"] = tui.KeybindingDefinition{DefaultKeys: []tui.KeyId{}, Description: "Resume a session"}
	defs["app.tree.foldOrUp"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if runtime.GOOS == "darwin" {
				return keys("alt+left", "ctrl+left")
			}
			return keys("ctrl+left", "alt+left")
		}(),
		Description: "Fold tree branch or move up",
	}
	defs["app.tree.unfoldOrDown"] = tui.KeybindingDefinition{
		DefaultKeys: func() []tui.KeyId {
			if runtime.GOOS == "darwin" {
				return keys("alt+right", "ctrl+right")
			}
			return keys("ctrl+right", "alt+right")
		}(),
		Description: "Unfold tree branch or move down",
	}
	defs["app.tree.editLabel"] = tui.KeybindingDefinition{DefaultKeys: keys("shift+l"), Description: "Edit tree label"}
	defs["app.tree.toggleLabelTimestamp"] = tui.KeybindingDefinition{DefaultKeys: keys("shift+t"), Description: "Toggle tree label timestamps"}
	defs["app.session.togglePath"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+p"), Description: "Toggle session path display"}
	defs["app.session.toggleSort"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+s"), Description: "Toggle session sort mode"}
	defs["app.session.rename"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+r"), Description: "Rename session"}
	defs["app.session.delete"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+d"), Description: "Delete session"}
	defs["app.session.deleteNoninvasive"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+backspace"), Description: "Delete session when query is empty"}
	defs["app.models.save"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+s"), Description: "Save model selection"}
	defs["app.models.enableAll"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+a"), Description: "Enable all models"}
	defs["app.models.clearAll"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+x"), Description: "Clear all models"}
	defs["app.models.toggleProvider"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+p"), Description: "Toggle all models for provider"}
	defs["app.models.reorderUp"] = tui.KeybindingDefinition{DefaultKeys: keys("alt+up"), Description: "Move model up in order"}
	defs["app.models.reorderDown"] = tui.KeybindingDefinition{DefaultKeys: keys("alt+down"), Description: "Move model down in order"}
	defs["app.tree.filter.default"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+d"), Description: "Tree filter: default view"}
	defs["app.tree.filter.noTools"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+t"), Description: "Tree filter: hide tool results"}
	defs["app.tree.filter.userOnly"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+u"), Description: "Tree filter: user messages only"}
	defs["app.tree.filter.labeledOnly"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+l"), Description: "Tree filter: labeled entries only"}
	defs["app.tree.filter.all"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+a"), Description: "Tree filter: show all entries"}
	defs["app.tree.filter.cycleForward"] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+o"), Description: "Tree filter: cycle forward"}
	defs["app.tree.filter.cycleBackward"] = tui.KeybindingDefinition{DefaultKeys: keys("shift+ctrl+o"), Description: "Tree filter: cycle backward"}
	defs[AppKeybindingMCPReconnect] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+r"), Description: "MCP panel: reconnect server"}
	defs[AppKeybindingMCPToggleDisabled] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+d"), Description: "MCP panel: enable/disable server"}
	defs[AppKeybindingMCPCopyError] = tui.KeybindingDefinition{DefaultKeys: keys("ctrl+y"), Description: "MCP panel: copy server error"}
	return defs
}()

// keybindingNameMigrations maps legacy key names to namespaced ids (pi:
// KEYBINDING_NAME_MIGRATIONS; insertion order irrelevant — it's lookup-only).
var keybindingNameMigrations = map[string]string{
	"cursorUp":                 "tui.editor.cursorUp",
	"cursorDown":               "tui.editor.cursorDown",
	"cursorLeft":               "tui.editor.cursorLeft",
	"cursorRight":              "tui.editor.cursorRight",
	"cursorWordLeft":           "tui.editor.cursorWordLeft",
	"cursorWordRight":          "tui.editor.cursorWordRight",
	"cursorLineStart":          "tui.editor.cursorLineStart",
	"cursorLineEnd":            "tui.editor.cursorLineEnd",
	"jumpForward":              "tui.editor.jumpForward",
	"jumpBackward":             "tui.editor.jumpBackward",
	"pageUp":                   "tui.editor.pageUp",
	"pageDown":                 "tui.editor.pageDown",
	"deleteCharBackward":       "tui.editor.deleteCharBackward",
	"deleteCharForward":        "tui.editor.deleteCharForward",
	"deleteWordBackward":       "tui.editor.deleteWordBackward",
	"deleteWordForward":        "tui.editor.deleteWordForward",
	"deleteToLineStart":        "tui.editor.deleteToLineStart",
	"deleteToLineEnd":          "tui.editor.deleteToLineEnd",
	"yank":                     "tui.editor.yank",
	"yankPop":                  "tui.editor.yankPop",
	"undo":                     "tui.editor.undo",
	"newLine":                  "tui.input.newLine",
	"submit":                   "tui.input.submit",
	"tab":                      "tui.input.tab",
	"copy":                     "tui.input.copy",
	"selectUp":                 "tui.select.up",
	"selectDown":               "tui.select.down",
	"selectPageUp":             "tui.select.pageUp",
	"selectPageDown":           "tui.select.pageDown",
	"selectConfirm":            "tui.select.confirm",
	"selectCancel":             "tui.select.cancel",
	"interrupt":                "app.interrupt",
	"clear":                    "app.clear",
	"exit":                     "app.exit",
	"suspend":                  "app.suspend",
	"cycleThinkingLevel":       "app.thinking.cycle",
	"cycleModelForward":        "app.model.cycleForward",
	"cycleModelBackward":       "app.model.cycleBackward",
	"selectModel":              "app.model.select",
	"expandTools":              "app.tools.expand",
	"toggleThinking":           "app.thinking.toggle",
	"toggleSessionNamedFilter": "app.session.toggleNamedFilter",
	"externalEditor":           "app.editor.external",
	"followUp":                 "app.message.followUp",
	"dequeue":                  "app.message.dequeue",
	"pasteImage":               "app.clipboard.pasteImage",
	"newSession":               "app.session.new",
	"tree":                     "app.session.tree",
	"fork":                     "app.session.fork",
	"resume":                   "app.session.resume",
	"treeFoldOrUp":             "app.tree.foldOrUp",
	"treeUnfoldOrDown":         "app.tree.unfoldOrDown",
	"treeEditLabel":            "app.tree.editLabel",
	"treeToggleLabelTimestamp": "app.tree.toggleLabelTimestamp",
	"toggleSessionPath":        "app.session.togglePath",
	"toggleSessionSort":        "app.session.toggleSort",
	"renameSession":            "app.session.rename",
	"deleteSession":            "app.session.delete",
	"deleteSessionNoninvasive": "app.session.deleteNoninvasive",
}

// toKeybindingsConfig coerces a raw user config into typed bindings (pi:
// toKeybindingsConfig — string or string[] values only; others dropped).
func toKeybindingsConfig(value map[string]any) tui.KeybindingsConfig {
	config := tui.KeybindingsConfig{}
	for key, binding := range value {
		if s, ok := binding.(string); ok {
			config[key] = []tui.KeyId{s}
			continue
		}
		if arr, ok := binding.([]any); ok {
			allStrings := true
			ids := []tui.KeyId{}
			for _, entry := range arr {
				s, ok := entry.(string)
				if !ok {
					allStrings = false
					break
				}
				ids = append(ids, tui.KeyId(s))
			}
			if allStrings {
				config[key] = ids
			}
		}
	}
	return config
}

// MigrateKeybindingsConfig rewrites legacy key names to namespaced ids (pi:
// migrateKeybindingsConfig). The returned config preserves KEYBINDINGS
// declaration order first, then extras alphabetically
// (orderKeybindingsConfig).
func MigrateKeybindingsConfig(rawConfig map[string]any) (config map[string]any, migrated bool) {
	config = map[string]any{}
	for key, value := range rawConfig {
		nextKey := key
		if mapped, ok := keybindingNameMigrations[key]; ok {
			nextKey = mapped
		}
		if nextKey != key {
			migrated = true
		}
		if key != nextKey {
			if _, exists := rawConfig[nextKey]; exists {
				migrated = true
				continue
			}
		}
		config[nextKey] = value
	}
	return orderKeybindingsConfig(config), migrated
}

// orderKeybindingsConfig orders a config by KEYBINDINGS declaration order,
// then extras alphabetically (pi: orderKeybindingsConfig).
func orderKeybindingsConfig(config map[string]any) map[string]any {
	ordered := map[string]any{}
	keys := []string{}
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// KEYBINDINGS declaration order first. Go maps lose declaration order,
	// so pig walks a recorded id order list instead (pig deviation:
	// declaration order captured in keybindingsOrder at init).
	for _, keybinding := range keybindingsOrder {
		if _, ok := config[keybinding]; ok {
			ordered[keybinding] = config[keybinding]
		}
	}
	for _, key := range keys {
		if _, ok := ordered[key]; !ok {
			ordered[key] = config[key]
		}
	}
	return ordered
}

// keybindingsOrder records the declaration order of the Keybindings table
// for orderKeybindingsConfig (pig addition: Go maps are unordered, so the
// merged table's order — pi's TUI_KEYBINDINGS insertion order with app.*
// appended after the overrides — is recorded explicitly).
var keybindingsOrder = []string{
	// pi-tui TUI_KEYBINDINGS declaration order (packages/tui/src/keybindings.ts).
	"tui.editor.cursorUp", "tui.editor.cursorDown", "tui.editor.historyPrevious",
	"tui.editor.historyNext", "tui.editor.cursorLeft", "tui.editor.cursorRight",
	"tui.editor.cursorWordLeft", "tui.editor.cursorWordRight", "tui.editor.cursorLineStart",
	"tui.editor.cursorLineEnd", "tui.editor.jumpForward", "tui.editor.jumpBackward",
	"tui.editor.pageUp", "tui.editor.pageDown", "tui.editor.deleteCharBackward",
	"tui.editor.deleteCharForward", "tui.editor.deleteWordBackward", "tui.editor.deleteWordForward",
	"tui.editor.deleteToLineStart", "tui.editor.deleteToLineEnd", "tui.editor.yank",
	"tui.editor.yankPop", "tui.editor.undo", "tui.input.newLine", "tui.input.submit",
	"tui.input.tab", "tui.input.copy", "tui.select.up", "tui.select.down",
	"tui.select.pageUp", "tui.select.pageDown", "tui.select.confirm", "tui.select.cancel",
	"tui.altScreen.pageUp", "tui.altScreen.pageDown", "tui.altScreen.halfPageUp",
	"tui.altScreen.halfPageDown", "tui.altScreen.lineUp", "tui.altScreen.lineDown",
	"tui.altScreen.previousPrompt", "tui.altScreen.nextPrompt", "tui.altScreen.search",
	"tui.altScreen.searchNext", "tui.altScreen.searchPrevious", "tui.altScreen.searchClose",
	"tui.altScreen.top", "tui.altScreen.bottom",
	// app.* bindings in pi's app section order (the overrides above keep
	// the TUI keys' original positions under JS spread semantics).
	"app.interrupt", "app.clear", "app.exit", "app.suspend",
	"app.thinking.cycle", "app.model.cycleForward", "app.model.cycleBackward",
	"app.model.select", "app.tools.expand", "app.thinking.toggle",
	"app.session.toggleNamedFilter", "app.editor.external", "app.message.copy",
	"app.message.followUp", "app.message.dequeue", "app.clipboard.pasteImage",
	"app.session.new", "app.session.tree", "app.session.fork", "app.session.resume",
	"app.tree.foldOrUp", "app.tree.unfoldOrDown", "app.tree.editLabel",
	"app.tree.toggleLabelTimestamp", "app.session.togglePath", "app.session.toggleSort",
	"app.session.rename", "app.session.delete", "app.session.deleteNoninvasive",
	"app.models.save", "app.models.enableAll", "app.models.clearAll",
	"app.models.toggleProvider", "app.models.reorderUp", "app.models.reorderDown",
	"app.tree.filter.default", "app.tree.filter.noTools", "app.tree.filter.userOnly",
	"app.tree.filter.labeledOnly", "app.tree.filter.all",
	"app.tree.filter.cycleForward", "app.tree.filter.cycleBackward",
	// pig-only panel keys.
	"app.mcp.reconnect", "app.mcp.toggleDisabled", "app.mcp.copyError",
}

// loadRawConfig reads and parses a keybindings.json file (pi:
// loadRawConfig — missing/invalid → undefined).
func loadRawConfig(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(utils.StripBom(string(data)))
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return nil
	}
	return obj
}

// KeybindingsManager is the coding-agent keybindings manager (pi:
// KeybindingsManager extends TuiKeybindingsManager; pig embeds).
type KeybindingsManager struct {
	*tui.KeybindingsManager
	configPath string
}

// NewKeybindingsManager builds a manager over the coding-agent definitions
// (pi: constructor).
func NewKeybindingsManager(userBindings tui.KeybindingsConfig, configPath string) *KeybindingsManager {
	return &KeybindingsManager{
		KeybindingsManager: tui.NewKeybindingsManager(Keybindings, userBindings),
		configPath:         configPath,
	}
}

// CreateKeybindingsManager loads user bindings from the agent dir (pi:
// KeybindingsManager.create).
func CreateKeybindingsManager(agentDir string) *KeybindingsManager {
	if agentDir == "" {
		agentDir = codingagent.GetAgentDir()
	}
	configPath := filepath.Join(agentDir, "keybindings.json")
	userBindings := loadKeybindingsFromFile(configPath)
	return NewKeybindingsManager(userBindings, configPath)
}

// Reload re-reads the config file (pi: reload).
func (m *KeybindingsManager) Reload() {
	if m.configPath == "" {
		return
	}
	m.SetUserBindings(loadKeybindingsFromFile(m.configPath))
}

// GetEffectiveConfig returns the resolved bindings (pi:
// getEffectiveConfig).
func (m *KeybindingsManager) GetEffectiveConfig() tui.KeybindingsConfig {
	return m.GetResolvedBindings()
}

// loadKeybindingsFromFile loads and migrates a keybindings.json (pi:
// KeybindingsManager.loadFromFile).
func loadKeybindingsFromFile(path string) tui.KeybindingsConfig {
	rawConfig := loadRawConfig(path)
	if rawConfig == nil {
		return tui.KeybindingsConfig{}
	}
	config, _ := MigrateKeybindingsConfig(rawConfig)
	return toKeybindingsConfig(config)
}
