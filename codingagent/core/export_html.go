// export_html.go (pi: core/export-html/index.ts — session-facing half,
// v0.86.0)
//
// Go's import-cycle rules forced pi's export-html/index.ts to be split in
// two: the template half (ExportOptions consumers, ToolHtmlRenderer
// interface, SessionData, generateHtml) lives in
// codingagent/core/exporthtml, and this file carries the session-facing
// half — exportSessionToHtml, exportFromFile and preRenderCustomTools —
// which needs codingagent/core's SessionManager/SessionEntry types.
package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core/exporthtml"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// ExportSessionToHtml exports a session to HTML using SessionManager and
// AgentState (pi: exportSessionToHtml). Used by TUI's /export command.
func ExportSessionToHtml(sm *SessionManager, state *agent.AgentState, options exporthtml.ExportOptions) (string, error) {
	sessionFile := sm.GetSessionFile()
	if sessionFile == "" {
		return "", errors.New("Cannot export in-memory session to HTML")
	}
	if _, err := os.Stat(sessionFile); err != nil {
		return "", errors.New("Nothing to export yet - start a conversation first")
	}

	entries := sm.GetEntries()

	// Pre-render custom tools if a tool renderer is provided
	var renderedTools map[string]exporthtml.RenderedToolHtml
	if options.ToolRenderer != nil {
		renderedTools = preRenderCustomTools(entries, options.ToolRenderer)
		// Only include if we actually rendered something
		if len(renderedTools) == 0 {
			renderedTools = nil
		}
	}

	sessionData := exporthtml.SessionData{
		Header:        sm.GetHeader(),
		Entries:       entries,
		LeafID:        sm.GetLeafID(),
		SystemPrompt:  exportSystemPromptOf(state),
		Tools:         exportToolsOf(state),
		RenderedTools: renderedTools,
	}

	html, err := exporthtml.GenerateHtml(sessionData, options.ThemeName)
	if err != nil {
		return "", err
	}

	outputPath := options.OutputPath
	if outputPath == "" {
		sessionBasename := strings.TrimSuffix(filepath.Base(sessionFile), ".jsonl")
		outputPath = codingagent.AppName + "-session-" + sessionBasename + ".html"
	} else {
		outputPath = utils.NormalizePath(outputPath, nil)
	}

	if err := os.WriteFile(outputPath, []byte(html), 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}

// ExportFromFile exports a session file to HTML (standalone, without
// AgentState) (pi: exportFromFile). Used by CLI for exporting arbitrary
// session files.
func ExportFromFile(inputPath string, options exporthtml.ExportOptions) (string, error) {
	resolvedInputPath := utils.ResolvePath(inputPath, "", nil)

	if _, err := os.Stat(resolvedInputPath); err != nil {
		return "", fmt.Errorf("File not found: %s", resolvedInputPath)
	}

	sm, err := OpenSession(resolvedInputPath, "", nil)
	if err != nil {
		return "", err
	}

	sessionData := exporthtml.SessionData{
		Header:  sm.GetHeader(),
		Entries: sm.GetEntries(),
		LeafID:  sm.GetLeafID(),
	}

	html, err := exporthtml.GenerateHtml(sessionData, options.ThemeName)
	if err != nil {
		return "", err
	}

	outputPath := options.OutputPath
	if outputPath == "" {
		inputBasename := strings.TrimSuffix(filepath.Base(resolvedInputPath), ".jsonl")
		outputPath = codingagent.AppName + "-session-" + inputBasename + ".html"
	} else {
		outputPath = utils.NormalizePath(outputPath, nil)
	}

	if err := os.WriteFile(outputPath, []byte(html), 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}

// exportSystemPromptOf reads the agent state's system prompt (pi:
// state?.systemPrompt).
func exportSystemPromptOf(state *agent.AgentState) string {
	if state == nil {
		return ""
	}
	return state.SystemPrompt
}

// exportToolsOf maps the agent state's tools to the export payload shape
// (pi: state?.tools?.map((t) => ({ name, description, parameters }))).
// A non-nil state always produces a non-nil pointer so `"tools": []`
// serializes for an empty tool set like JS's map over an empty array; nil
// state yields nil (key omitted, pi: state undefined).
func exportToolsOf(state *agent.AgentState) *[]exporthtml.ToolSummary {
	if state == nil {
		return nil
	}
	tools := make([]exporthtml.ToolSummary, 0, len(state.Tools))
	for _, t := range state.Tools {
		tools = append(tools, exporthtml.ToolSummary{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return &tools
}

// preRenderCustomTools pre-renders custom tools to HTML using their TUI
// renderers (pi: preRenderCustomTools).
func preRenderCustomTools(entries []SessionEntry, toolRenderer exporthtml.ToolHtmlRenderer) map[string]exporthtml.RenderedToolHtml {
	renderedTools := map[string]exporthtml.RenderedToolHtml{}

	for _, entry := range entries {
		msgEntry, ok := entry.(*SessionMessageEntry)
		if !ok {
			continue
		}

		// Find tool calls in assistant messages
		switch msg := msgEntry.Message.(type) {
		case types.AssistantMessage:
			preRenderToolCalls(msg.Content, renderedTools, toolRenderer)
		case *types.AssistantMessage:
			if msg != nil {
				preRenderToolCalls(msg.Content, renderedTools, toolRenderer)
			}
		}

		// Find tool results
		var toolResult *types.ToolResultMessage
		switch msg := msgEntry.Message.(type) {
		case types.ToolResultMessage:
			toolResult = &msg
		case *types.ToolResultMessage:
			toolResult = msg
		}
		if toolResult != nil && toolResult.ToolCallID != "" {
			toolName := toolResult.ToolName
			// Only render if we have a pre-rendered call OR it's not
			// template-rendered
			existing, hasCall := renderedTools[toolResult.ToolCallID]
			if hasCall || !exporthtml.IsTemplateRenderedTool(toolName) {
				collapsed, expanded, ok := toolRenderer.RenderResult(
					toolResult.ToolCallID,
					toolName,
					toolResult.Content,
					toolResult.Details,
					toolResult.IsError,
				)
				if ok {
					existing.ResultHTMLCollapsed = collapsed
					existing.ResultHTMLExpanded = expanded
					renderedTools[toolResult.ToolCallID] = existing
				}
			}
		}
	}

	return renderedTools
}

// preRenderToolCalls renders toolCall blocks that the HTML template does
// not render itself (pi: the assistant-content branch of
// preRenderCustomTools).
func preRenderToolCalls(blocks []types.Content, renderedTools map[string]exporthtml.RenderedToolHtml, toolRenderer exporthtml.ToolHtmlRenderer) {
	for _, block := range blocks {
		var toolCall *types.ToolCall
		switch b := block.(type) {
		case types.ToolCall:
			toolCall = &b
		case *types.ToolCall:
			toolCall = b
		}
		if toolCall == nil || exporthtml.IsTemplateRenderedTool(toolCall.Name) {
			continue
		}
		callHTML := toolRenderer.RenderCall(toolCall.ID, toolCall.Name, toolCall.Arguments)
		if callHTML != "" {
			renderedTools[toolCall.ID] = exporthtml.RenderedToolHtml{CallHTML: callHTML}
		}
	}
}
