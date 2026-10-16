// footer_width_test.go (pi: test/footer-width.test.ts).
//
// pi stubs the session with a plain object; pig builds the
// footerSessionView stub directly (see footer.go header deviation).
package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
	"github.com/samber/lo"
)

type footerAssistantUsage struct {
	input      int64
	output     int64
	cacheRead  int64
	cacheWrite int64
	costTotal  float64
}

func footerUsageToTypes(u footerAssistantUsage) types.Usage {
	return types.Usage{
		Input:       u.input,
		Output:      u.output,
		CacheRead:   u.cacheRead,
		CacheWrite:  u.cacheWrite,
		TotalTokens: u.input + u.output + u.cacheRead + u.cacheWrite,
		Cost:        types.Cost{Total: u.costTotal},
	}
}

type footerSessionOptions struct {
	sessionName       string
	modelId           string
	provider          string
	reasoning         bool
	thinkingLevel     string
	usage             *footerAssistantUsage
	branchUsage       *footerAssistantUsage
	compactionUsage   *footerAssistantUsage
	toolUsage         *footerAssistantUsage
	usingSubscription bool
	contextWindow     int64
	contextPercent    float64
}

func createFooterSession(options footerSessionOptions) (footerSessionView, *core.FooterDataProvider) {
	if options.modelId == "" {
		options.modelId = "test-model"
	}
	if options.provider == "" {
		options.provider = "test"
	}
	if options.thinkingLevel == "" {
		options.thinkingLevel = "off"
	}
	if options.contextWindow == 0 {
		options.contextWindow = 200_000
	}
	options.contextPercent = 12.3

	var entries []core.SessionEntry
	if options.usage != nil {
		entries = append(entries, &core.SessionMessageEntry{
			EntryBase: core.EntryBase{Type: "message", ID: "m"},
			Message: &types.AssistantMessage{
				Role:  types.RoleAssistant,
				Usage: footerUsageToTypes(*options.usage),
			},
		})
	}
	if options.branchUsage != nil {
		entries = append(entries, &core.BranchSummaryEntry{
			EntryBase: core.EntryBase{Type: "branch_summary", ID: "b"},
			Usage:     lo.ToPtr(footerUsageToTypes(*options.branchUsage)),
		})
	}
	if options.compactionUsage != nil {
		entries = append(entries, &core.CompactionEntry{
			EntryBase: core.EntryBase{Type: "compaction", ID: "c"},
			Usage:     lo.ToPtr(footerUsageToTypes(*options.compactionUsage)),
		})
	}
	if options.toolUsage != nil {
		entries = append(entries, &core.SessionMessageEntry{
			EntryBase: core.EntryBase{Type: "message", ID: "t"},
			Message: &types.ToolResultMessage{
				Role:  types.RoleToolResult,
				Usage: lo.ToPtr(footerUsageToTypes(*options.toolUsage)),
			},
		})
	}

	state := &agent.AgentState{
		Model: &types.Model{
			ID:            options.modelId,
			Provider:      types.ProviderID(options.provider),
			ContextWindow: options.contextWindow,
			Reasoning:     options.reasoning,
		},
		ThinkingLevel: agent.ThinkingLevel(options.thinkingLevel),
	}
	sessionName := options.sessionName
	cwd := "/tmp/project"
	percent := options.contextPercent
	usingSubscription := options.usingSubscription

	view := footerSessionView{
		state:       func() *agent.AgentState { return state },
		entries:     func() []core.SessionEntry { return entries },
		sessionName: func() (string, bool) { return sessionName, sessionName != "" },
		cwd:         func() string { return cwd },
		contextUsage: func() *core.ContextUsage {
			return &core.ContextUsage{ContextWindow: options.contextWindow, Percent: &percent}
		},
		isUsingSubscription: func(provider string) bool { return usingSubscription },
	}
	provider := core.NewFooterDataProvider(cwd)
	return view, provider
}

func createFooterData(providerCount int) ReadonlyFooterDataProvider {
	return &stubFooterData{providerCount: providerCount, branch: "main"}
}

type stubFooterData struct {
	providerCount int
	branch        string
}

func (s *stubFooterData) GetGitBranch() *string                   { return &s.branch }
func (s *stubFooterData) GetExtensionStatuses() map[string]string { return map[string]string{} }
func (s *stubFooterData) GetAvailableProviderCount() int          { return s.providerCount }

func TestFormatCwdForFooter(t *testing.T) {
	initTestTheme(t)
	t.Run("does not abbreviate sibling paths that share the home prefix", func(t *testing.T) {
		if got := FormatCwdForFooter("/home/user2", "/home/user"); got != "/home/user2" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("abbreviates the home directory and descendants", func(t *testing.T) {
		if got := FormatCwdForFooter("/home/user", "/home/user"); got != "~" {
			t.Fatalf("got %q", got)
		}
		if got := FormatCwdForFooter("/home/user/project", "/home/user"); got != "~/project" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestFooterComponentWidthHandling(t *testing.T) {
	t.Run("keeps all lines within width for wide session names", func(t *testing.T) {
		width := 93
		view, _ := createFooterSession(footerSessionOptions{sessionName: strings.Repeat("한글", 30)})
		footer := NewFooterComponentWithView(nil, view, createFooterData(1))

		for _, line := range footer.Render(width) {
			if got := tui.VisibleWidth(line); got > width {
				t.Fatalf("line width %d exceeds %d: %q", got, width, line)
			}
		}
	})

	t.Run("keeps stats line within width for wide model and provider names", func(t *testing.T) {
		width := 60
		view, _ := createFooterSession(footerSessionOptions{
			sessionName:   "",
			modelId:       strings.Repeat("模", 30),
			provider:      "공급자",
			reasoning:     true,
			thinkingLevel: "high",
			usage: &footerAssistantUsage{
				input:     12_345,
				output:    6_789,
				costTotal: 1.234,
			},
		})
		footer := NewFooterComponentWithView(nil, view, createFooterData(2))

		for _, line := range footer.Render(width) {
			if got := tui.VisibleWidth(line); got > width {
				t.Fatalf("line width %d exceeds %d: %q", got, width, line)
			}
		}
	})

	t.Run("includes summary and tool result usage in the total cost", func(t *testing.T) {
		view, _ := createFooterSession(footerSessionOptions{
			sessionName: "",
			usage:       &footerAssistantUsage{input: 100, output: 10, costTotal: 0.5},
			branchUsage: &footerAssistantUsage{input: 20, output: 5, costTotal: 0.25},
			compactionUsage: &footerAssistantUsage{
				input: 5, output: 2, costTotal: 0.125,
			},
			toolUsage: &footerAssistantUsage{
				input: 15, output: 3, costTotal: 0.375,
			},
		})
		footer := NewFooterComponentWithView(nil, view, createFooterData(1))

		statsLine := utils.StripAnsi(footer.Render(120)[1])
		if !strings.Contains(statsLine, "$1.250") {
			t.Fatalf("stats line %q missing $1.250", statsLine)
		}
	})

	t.Run("shows the latest cache hit rate when cache usage is present", func(t *testing.T) {
		view, _ := createFooterSession(footerSessionOptions{
			sessionName: "",
			usage:       &footerAssistantUsage{input: 100, output: 10, cacheRead: 50, cacheWrite: 50, costTotal: 0.001},
		})
		footer := NewFooterComponentWithView(nil, view, createFooterData(1))

		statsLine := utils.StripAnsi(footer.Render(120)[1])
		if !strings.Contains(statsLine, "CH25.0%") {
			t.Fatalf("stats line %q missing CH25.0%%", statsLine)
		}
	})

	t.Run("marks Kimi Coding costs as subscription estimates", func(t *testing.T) {
		view, _ := createFooterSession(footerSessionOptions{
			sessionName: "",
			provider:    "kimi-coding",
			usage:       &footerAssistantUsage{input: 100, output: 10, costTotal: 1.234},
		})
		footer := NewFooterComponentWithView(nil, view, createFooterData(1))

		if got := utils.StripAnsi(footer.Render(120)[1]); !strings.Contains(got, "$1.234 (sub)") {
			t.Fatalf("stats line %q missing $1.234 (sub)", got)
		}
	})

	t.Run("marks explicitly identified subscription auth", func(t *testing.T) {
		view, _ := createFooterSession(footerSessionOptions{
			sessionName:       "",
			provider:          "anthropic",
			usingSubscription: true,
		})
		footer := NewFooterComponentWithView(nil, view, createFooterData(1))

		if got := utils.StripAnsi(footer.Render(120)[1]); !strings.Contains(got, "$0.000 (sub)") {
			t.Fatalf("stats line %q missing $0.000 (sub)", got)
		}
	})

	t.Run("does not mark generic OAuth sign-in as a subscription", func(t *testing.T) {
		view, _ := createFooterSession(footerSessionOptions{
			sessionName: "",
			provider:    "openrouter",
			usage:       &footerAssistantUsage{input: 100, output: 10, costTotal: 1.234},
		})
		footer := NewFooterComponentWithView(nil, view, createFooterData(1))
		stats := utils.StripAnsi(footer.Render(120)[1])

		if !strings.Contains(stats, "$1.234") {
			t.Fatalf("stats line %q missing $1.234", stats)
		}
		if strings.Contains(stats, "(sub)") {
			t.Fatalf("stats line %q unexpectedly marked (sub)", stats)
		}
	})
}

// TestFooterProviderPrefixAlwaysShown is THE regression for the pig-only
// deviation in docs/feat/FOOTER-PROVIDER-PREFIX.md: pi prepends the provider
// only when 2+ providers are available; pig shows it even with exactly one
// (user request, 2026-10-16). The width fallback (drop the prefix, keep the
// model) must survive.
func TestFooterProviderPrefixAlwaysShown(t *testing.T) {
	t.Run("shows the provider prefix with a single available provider", func(t *testing.T) {
		view, _ := createFooterSession(footerSessionOptions{
			modelId:  "deepseek-v4-flash",
			provider: "volcengine-plan",
		})
		footer := NewFooterComponentWithView(nil, view, createFooterData(1))

		statsLine := utils.StripAnsi(footer.Render(120)[1])
		if !strings.HasSuffix(statsLine, "(volcengine-plan) deepseek-v4-flash") {
			t.Fatalf("stats line %q should end with the right-aligned provider+model", statsLine)
		}
	})

	t.Run("shows the provider prefix with several available providers too", func(t *testing.T) {
		view, _ := createFooterSession(footerSessionOptions{provider: "volcengine-plan"})
		footer := NewFooterComponentWithView(nil, view, createFooterData(2))

		statsLine := utils.StripAnsi(footer.Render(120)[1])
		if !strings.Contains(statsLine, "(volcengine-plan) test-model") {
			t.Fatalf("stats line %q missing provider prefix", statsLine)
		}
	})

	t.Run("drops the provider prefix when it does not fit the width", func(t *testing.T) {
		view, _ := createFooterSession(footerSessionOptions{
			modelId:  strings.Repeat("m", 40),
			provider: "very-long-provider-name",
		})
		footer := NewFooterComponentWithView(nil, view, createFooterData(1))

		statsLine := utils.StripAnsi(footer.Render(60)[1])
		if strings.Contains(statsLine, "very-long-provider-name") {
			t.Fatalf("stats line %q should fall back to model-only when too wide", statsLine)
		}
		if !strings.HasSuffix(statsLine, strings.Repeat("m", 40)) {
			t.Fatalf("stats line %q should keep the model name", statsLine)
		}
	})
}
