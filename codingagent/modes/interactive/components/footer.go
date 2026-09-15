// footer.go (pi: components/footer.ts) — footer component showing pwd,
// token stats, and context usage. Computes token/context stats from the
// session; git branch and extension statuses come from the provider. When
// the pig-only showResourceStats setting is on, the stats line also shows
// process resource stats (MEM, separated by "|").
//
// Declared deviation: pi reads session.sessionManager/session.state
// directly on the concrete AgentSession; pig routes the accesses through
// function fields (newFooterSessionView defaults them to the real
// session) so tests can stub the view (Go has no structural duck typing
// on a concrete struct).
package components

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// sanitizeStatusText sanitizes text for display in a single-line status:
// removes newlines, tabs, carriage returns, and other control characters.
func sanitizeStatusText(text string) string {
	replacer := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")
	return strings.TrimSpace(collapseSpaces(replacer.Replace(text)))
}

var footerMultiSpaceRegex = compileFooterRegex(" +")

func collapseSpaces(text string) string {
	return footerMultiSpaceRegex.ReplaceAllString(text, " ")
}

// FormatTokens formats token counts for compact footer display.
func FormatTokens(count int64) string {
	switch {
	case count < 1000:
		return fmt.Sprintf("%d", count)
	case count < 10000:
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	case count < 1000000:
		return fmt.Sprintf("%dk", int64(float64(count)/1000+0.5))
	case count < 10000000:
		return fmt.Sprintf("%.1fM", float64(count)/1000000)
	default:
		return fmt.Sprintf("%dM", int64(float64(count)/1000000+0.5))
	}
}

// FormatCwdForFooter replaces the home directory prefix with ~ (pi:
// formatCwdForFooter).
func FormatCwdForFooter(cwd string, home string) string {
	if home == "" {
		return cwd
	}

	resolvedCwd, err := filepath.Abs(cwd)
	if err != nil {
		resolvedCwd = cwd
	}
	resolvedHome, err := filepath.Abs(home)
	if err != nil {
		resolvedHome = home
	}
	relativeToHome, err := filepath.Rel(resolvedHome, resolvedCwd)
	if err != nil {
		return cwd
	}
	// pi: path.relative(same, same) === ""; Go's filepath.Rel returns ".".
	if relativeToHome == "." {
		relativeToHome = ""
	}
	isInsideHome := relativeToHome == "" ||
		(relativeToHome != ".." && !strings.HasPrefix(relativeToHome, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativeToHome))

	if !isInsideHome {
		return cwd
	}
	if relativeToHome == "" {
		return "~"
	}
	return "~" + string(filepath.Separator) + relativeToHome
}

// ReadonlyFooterDataProvider is the provider view the footer consumes (pi:
// ReadonlyFooterDataProvider pick type; *core.FooterDataProvider satisfies
// it).
type ReadonlyFooterDataProvider interface {
	GetGitBranch() *string
	GetExtensionStatuses() map[string]string
	GetAvailableProviderCount() int
}

// footerSessionView is the session surface the footer reads (pig seam; see
// header deviation).
type footerSessionView struct {
	state               func() *agent.AgentState
	entries             func() []core.SessionEntry
	sessionName         func() (string, bool)
	cwd                 func() string
	contextUsage        func() *core.ContextUsage
	isUsingSubscription func(provider string) bool
}

func newFooterSessionView(session *core.AgentSession) footerSessionView {
	return footerSessionView{
		state:               session.State,
		entries:             session.SessionManager.GetEntries,
		sessionName:         session.SessionManager.GetSessionName,
		cwd:                 session.SessionManager.GetCwd,
		contextUsage:        session.GetContextUsage,
		isUsingSubscription: session.GetModelRuntime().IsUsingSubscription,
	}
}

// FooterComponent is the footer line pair: pwd (branch, session name) and
// token/context stats with the right-aligned model name. When resource
// stats are enabled (pig-only), the stats line gains a "| MEM …" segment.
type FooterComponent struct {
	mu sync.Mutex

	autoCompactEnabled bool
	showResourceStats  bool // pig-only: gates the MEM segment (showResourceStats setting)
	session            *core.AgentSession
	view               footerSessionView
	footerData         ReadonlyFooterDataProvider

	// Resource sampling (pig-only): RSS is read off-frame on a slow ticker
	// and cached here; 0 = unsampled (segment hidden). The sampler runs
	// only while the segment is enabled — never to poll an invisible one.
	rssBytes      atomic.Int64
	stopRSSPoller func()
}

// NewFooterComponent builds the footer (pi: constructor).
func NewFooterComponent(session *core.AgentSession, footerData *core.FooterDataProvider) *FooterComponent {
	return NewFooterComponentWithView(session, newFooterSessionView(session), footerData)
}

// NewFooterComponentWithView builds the footer with an explicit session
// view (pig test seam).
func NewFooterComponentWithView(session *core.AgentSession, view footerSessionView, footerData ReadonlyFooterDataProvider) *FooterComponent {
	return &FooterComponent{
		autoCompactEnabled: true,
		session:            session,
		view:               view,
		footerData:         footerData,
	}
}

// SetSession rebinds the footer to a session (pi: setSession).
func (f *FooterComponent) SetSession(session *core.AgentSession) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.session = session
}

// SetAutoCompactEnabled toggles the "(auto)" context indicator (pi:
// setAutoCompactEnabled).
func (f *FooterComponent) SetAutoCompactEnabled(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoCompactEnabled = enabled
}

// SetShowResourceStats toggles the footer resource segment (pig-only; pi
// has no counterpart — the showResourceStats setting). The RSS sampler
// starts lazily on first enable and stops while hidden: sampling exists
// only to feed a visible segment.
func (f *FooterComponent) SetShowResourceStats(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.showResourceStats = enabled
	if enabled {
		if f.stopRSSPoller == nil {
			// Sample once up front so the segment appears on the next
			// render instead of one interval later.
			if b, ok := readRSSBytes(); ok {
				f.rssBytes.Store(b)
			}
			f.stopRSSPoller = startRSSPoller(footerRSSInterval, &f.rssBytes)
		}
		return
	}
	if f.stopRSSPoller != nil {
		f.stopRSSPoller()
		f.stopRSSPoller = nil
	}
	f.rssBytes.Store(0)
}

// Invalidate is a no-op: git branch caching is handled by the provider
// (pi: invalidate — kept for call-site compatibility).
func (f *FooterComponent) Invalidate() {}

// Dispose stops the resource sampler if it is running; git watcher cleanup
// is handled by the provider (pi: dispose).
func (f *FooterComponent) Dispose() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopRSSPoller != nil {
		f.stopRSSPoller()
		f.stopRSSPoller = nil
	}
}

// Render renders the footer lines (pi: render).
func (f *FooterComponent) Render(width int) []string {
	f.mu.Lock()
	autoCompact := f.autoCompactEnabled
	showResourceStats := f.showResourceStats
	view := f.view
	f.mu.Unlock()

	state := view.state()

	// Calculate cumulative usage from ALL session entries (not just
	// post-compaction messages)
	usageTotals := core.CreateUsageTotals()
	var latestCacheHitRate *float64

	for _, entry := range view.entries() {
		if messageEntry, ok := entry.(*core.SessionMessageEntry); ok {
			switch messageEntry.Message.MessageRole() {
			case types.RoleAssistant:
				// pig's transcript holds both value messages (runtime appends
				// via AppendMessage) and pointer messages (jsonl decode);
				// accept both shapes like GetUsageCostBreakdown does. A
				// pointer-only assertion here dropped ALL usage stats (↑ ↓ R W
				// CH $) from the footer — see
				// docs/fix/2026-09-11-footer-usage-stats-missing.md.
				assistant, isAssistant := messageEntry.Message.(*types.AssistantMessage)
				if !isAssistant {
					value, isValue := messageEntry.Message.(types.AssistantMessage)
					if isValue {
						assistant = &value
					}
				}
				if assistant != nil {
					core.AddUsageToTotals(&usageTotals, assistant.Usage)

					latestPromptTokens := assistant.Usage.Input + assistant.Usage.CacheRead + assistant.Usage.CacheWrite
					// pi: latestCacheHitRate = latestPromptTokens > 0 ? ... : undefined
					// (re-assigned on every assistant message, cleared at 0).
					if latestPromptTokens > 0 {
						rate := float64(assistant.Usage.CacheRead) / float64(latestPromptTokens) * 100
						latestCacheHitRate = &rate
					} else {
						latestCacheHitRate = nil
					}
				}
			case types.RoleToolResult:
				// Both shapes, as above.
				toolResult, isToolResult := messageEntry.Message.(*types.ToolResultMessage)
				if !isToolResult {
					value, isValue := messageEntry.Message.(types.ToolResultMessage)
					if isValue {
						toolResult = &value
					}
				}
				if toolResult != nil && toolResult.Usage != nil {
					core.AddUsageToTotals(&usageTotals, *toolResult.Usage)
				}
			}
		} else if compactionEntry, ok := entry.(*core.CompactionEntry); ok && compactionEntry.Usage != nil {
			core.AddUsageToTotals(&usageTotals, *compactionEntry.Usage)
		} else if branchEntry, ok := entry.(*core.BranchSummaryEntry); ok && branchEntry.Usage != nil {
			core.AddUsageToTotals(&usageTotals, *branchEntry.Usage)
		}
	}

	// Calculate context usage from session (handles compaction correctly).
	// After compaction, tokens are unknown until the next LLM response.
	contextUsage := view.contextUsage()
	contextWindow := int64(0)
	if contextUsage != nil {
		contextWindow = contextUsage.ContextWindow
	} else if state.Model != nil {
		contextWindow = state.Model.ContextWindow
	}
	contextPercentValue := 0.0
	if contextUsage != nil && contextUsage.Percent != nil {
		contextPercentValue = *contextUsage.Percent
	}
	contextPercent := "?"
	if contextUsage != nil && contextUsage.Percent != nil {
		contextPercent = fmt.Sprintf("%.1f", contextPercentValue)
	}

	// Replace home directory with ~
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	pwd := FormatCwdForFooter(view.cwd(), home)

	// Add git branch if available
	branch := f.footerData.GetGitBranch()
	if branch != nil {
		pwd = fmt.Sprintf("%s (%s)", pwd, *branch)
	}

	// Add session name if set
	if sessionName, ok := view.sessionName(); ok {
		pwd = fmt.Sprintf("%s • %s", pwd, sessionName)
	}

	// Build stats line
	statsParts := []string{}
	if usageTotals.Input > 0 {
		statsParts = append(statsParts, "↑"+FormatTokens(usageTotals.Input))
	}
	if usageTotals.Output > 0 {
		statsParts = append(statsParts, "↓"+FormatTokens(usageTotals.Output))
	}
	if usageTotals.CacheRead > 0 {
		statsParts = append(statsParts, "R"+FormatTokens(usageTotals.CacheRead))
	}
	if usageTotals.CacheWrite > 0 {
		statsParts = append(statsParts, "W"+FormatTokens(usageTotals.CacheWrite))
	}
	if (usageTotals.CacheRead > 0 || usageTotals.CacheWrite > 0) && latestCacheHitRate != nil {
		statsParts = append(statsParts, fmt.Sprintf("CH%.1f%%", *latestCacheHitRate))
	}

	// Kimi Coding is subscription-backed despite using API-key
	// authentication.
	usingSubscription := false
	if state.Model != nil {
		usingSubscription = state.Model.Provider == "kimi-coding" ||
			view.isUsingSubscription(string(state.Model.Provider))
	}
	if usageTotals.Cost > 0 || usingSubscription {
		costStr := fmt.Sprintf("$%.3f", usageTotals.Cost)
		if usingSubscription {
			costStr += " (sub)"
		}
		statsParts = append(statsParts, costStr)
	}

	currentTheme := theme.Current()

	// Colorize context percentage based on usage
	var contextPercentStr string
	autoIndicator := ""
	if autoCompact {
		autoIndicator = " (auto)"
	}
	var contextPercentDisplay string
	if contextPercent == "?" {
		contextPercentDisplay = fmt.Sprintf("?/%s%s", FormatTokens(contextWindow), autoIndicator)
	} else {
		contextPercentDisplay = fmt.Sprintf("%s%%/%s%s", contextPercent, FormatTokens(contextWindow), autoIndicator)
	}
	if contextPercentValue > 90 {
		contextPercentStr = currentTheme.Fg("error", contextPercentDisplay)
	} else if contextPercentValue > 70 {
		contextPercentStr = currentTheme.Fg("warning", contextPercentDisplay)
	} else {
		contextPercentStr = contextPercentDisplay
	}
	statsParts = append(statsParts, contextPercentStr)
	if core.AreExperimentalFeaturesEnabled() {
		statsParts = append(statsParts, currentTheme.Fg("dim", "•")+" "+currentTheme.Bold(currentTheme.Fg("warning", "xp")))
	}

	// Resource segment (pig-only; pi has no counterpart): process stats
	// separated from the usage stats by "|". Hidden until the sampler
	// produced a value (0 = not sampled / no procfs platform).
	if showResourceStats {
		if rssBytes := f.rssBytes.Load(); rssBytes > 0 {
			statsParts = append(statsParts, "|", "MEM "+FormatMemBytes(rssBytes))
		}
	}

	statsLeft := strings.Join(statsParts, " ")

	// Add model name on the right side, plus thinking level if model
	// supports it
	modelName := "no-model"
	if state.Model != nil {
		modelName = state.Model.ID
	}

	statsLeftWidth := tui.VisibleWidth(statsLeft)

	// If statsLeft is too wide, truncate it
	if statsLeftWidth > width {
		statsLeft = tui.TruncateToWidth(statsLeft, width, "...", false)
		statsLeftWidth = tui.VisibleWidth(statsLeft)
	}

	// Calculate available space for padding (minimum 2 spaces between stats
	// and model)
	minPadding := 2

	// Add thinking level indicator if model supports reasoning
	rightSideWithoutProvider := modelName
	if state.Model != nil && state.Model.Reasoning {
		thinkingLevel := string(state.ThinkingLevel)
		if thinkingLevel == "" {
			thinkingLevel = "off"
		}
		if thinkingLevel == "off" {
			rightSideWithoutProvider = modelName + " • thinking off"
		} else {
			rightSideWithoutProvider = modelName + " • " + string(thinkingLevel)
		}
	}

	// Prepend the provider in parentheses if there are multiple providers
	// and there's enough room
	rightSide := rightSideWithoutProvider
	if f.footerData.GetAvailableProviderCount() > 1 && state.Model != nil {
		rightSide = fmt.Sprintf("(%s) %s", state.Model.Provider, rightSideWithoutProvider)
		if statsLeftWidth+minPadding+tui.VisibleWidth(rightSide) > width {
			// Too wide, fall back
			rightSide = rightSideWithoutProvider
		}
	}

	rightSideWidth := tui.VisibleWidth(rightSide)
	totalNeeded := statsLeftWidth + minPadding + rightSideWidth

	var statsLine string
	if totalNeeded <= width {
		// Both fit - add padding to right-align model
		padding := strings.Repeat(" ", width-statsLeftWidth-rightSideWidth)
		statsLine = statsLeft + padding + rightSide
	} else {
		// Need to truncate right side
		availableForRight := width - statsLeftWidth - minPadding
		if availableForRight > 0 {
			truncatedRight := tui.TruncateToWidth(rightSide, availableForRight, "", false)
			truncatedRightWidth := tui.VisibleWidth(truncatedRight)
			padding := strings.Repeat(" ", max(0, width-statsLeftWidth-truncatedRightWidth))
			statsLine = statsLeft + padding + truncatedRight
		} else {
			// Not enough space for right side at all
			statsLine = statsLeft
		}
	}

	// Apply dim to each part separately. statsLeft may contain color codes
	// (for context %) that end with a reset, which would clear an outer dim
	// wrapper. So we dim the parts before and after the colored section
	// independently.
	dimStatsLeft := currentTheme.Fg("dim", statsLeft)
	remainder := ""
	if len(statsLine) >= len(statsLeft) {
		remainder = statsLine[len(statsLeft):] // padding + rightSide
	}
	dimRemainder := currentTheme.Fg("dim", remainder)

	pwdLine := tui.TruncateToWidth(currentTheme.Fg("dim", pwd), width, currentTheme.Fg("dim", "..."), false)
	lines := []string{pwdLine, dimStatsLeft + dimRemainder}

	// Add extension statuses on a single line, sorted by key alphabetically
	extensionStatuses := f.footerData.GetExtensionStatuses()
	if len(extensionStatuses) > 0 {
		keys := make([]string, 0, len(extensionStatuses))
		for key := range extensionStatuses {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		statusTexts := make([]string, 0, len(keys))
		for _, key := range keys {
			statusTexts = append(statusTexts, sanitizeStatusText(extensionStatuses[key]))
		}
		statusLine := strings.Join(statusTexts, " ")
		// Truncate to terminal width with dim ellipsis for consistency with
		// the footer style
		lines = append(lines, tui.TruncateToWidth(statusLine, width, currentTheme.Fg("dim", "..."), false))
	}

	return lines
}

// compileFooterRegex compiles a pattern once at init.
func compileFooterRegex(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}
