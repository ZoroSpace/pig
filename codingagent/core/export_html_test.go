// export_html_test.go — session-facing HTML export coverage (pi's
// export-html has no dedicated test file; P8-EH1 builds the coverage
// pig-side, mirroring pi's error-message and payload contracts).
package core

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core/exporthtml"
)

// newFileBackedSessionManager returns a session manager whose session file
// is flushed to disk (the first assistant message materializes it).
func newFileBackedSessionManager(t *testing.T) (*SessionManager, string) {
	t.Helper()
	sessionDir := t.TempDir()
	sm, err := CreateSessionManager(t.TempDir(), sessionDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.TextContent{Kind: types.KindText, Text: "hello"}},
		StopReason: types.StopReasonStop,
	}); err != nil {
		t.Fatal(err)
	}
	return sm, sessionDir
}

func TestExportSessionToHtmlPayload(t *testing.T) {
	sm, _ := newFileBackedSessionManager(t)
	outPath := filepath.Join(t.TempDir(), "out.html")

	got, err := ExportSessionToHtml(sm, &agent.AgentState{SystemPrompt: "sys"}, exporthtml.ExportOptions{OutputPath: outPath})
	if err != nil {
		t.Fatal(err)
	}
	if got != outPath {
		t.Fatalf("returned path = %q, want %q", got, outPath)
	}

	html, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "<!DOCTYPE html>") {
		t.Fatal("output is not HTML")
	}
	payload := decodeSessionDataPayload(t, string(html))

	var parsed struct {
		Header struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Cwd       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
		} `json:"header"`
		Entries []struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"entries"`
		LeafID       string `json:"leafId"`
		SystemPrompt string `json:"systemPrompt"`
		Tools        *[]any `json:"tools"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("payload JSON: %v (%.120s)", err, payload)
	}
	if parsed.Header.Type != "session" {
		t.Fatalf("header.type = %q", parsed.Header.Type)
	}
	if len(parsed.Entries) != 1 || parsed.Entries[0].Message.Role != "assistant" || parsed.Entries[0].Message.Content[0].Text != "hello" {
		t.Fatalf("entries mismatch: %.200s", payload)
	}
	if parsed.LeafID == "" {
		t.Fatal("leafId missing")
	}
	if parsed.SystemPrompt != "sys" {
		t.Fatalf("systemPrompt = %q", parsed.SystemPrompt)
	}
	// A non-nil AgentState yields "tools": [] (JS map over an empty array).
	if parsed.Tools == nil || len(*parsed.Tools) != 0 {
		t.Fatalf("tools should serialize as [], got %.200s", payload)
	}
}

func TestExportSessionToHtmlDefaultFilename(t *testing.T) {
	sm, _ := newFileBackedSessionManager(t)
	tmp := t.TempDir()
	t.Chdir(tmp)

	got, err := ExportSessionToHtml(sm, nil, exporthtml.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := codingagent.AppName + "-session-" + strings.TrimSuffix(filepath.Base(sm.GetSessionFile()), ".jsonl") + ".html"
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("default path = %q, want suffix %q", got, wantSuffix)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("exported file missing: %v", err)
	}
}

func TestExportSessionToHtmlErrors(t *testing.T) {
	t.Run("in-memory session", func(t *testing.T) {
		sm, err := InMemorySession(t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ExportSessionToHtml(sm, nil, exporthtml.ExportOptions{})
		if err == nil || err.Error() != "Cannot export in-memory session to HTML" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("nothing flushed yet", func(t *testing.T) {
		sm, err := CreateSessionManager(t.TempDir(), t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ExportSessionToHtml(sm, nil, exporthtml.ExportOptions{})
		if err == nil || err.Error() != "Nothing to export yet - start a conversation first" {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExportFromFile(t *testing.T) {
	sm, _ := newFileBackedSessionManager(t)
	inputPath := sm.GetSessionFile()

	outPath := filepath.Join(t.TempDir(), "exported.html")
	got, err := ExportFromFile(inputPath, exporthtml.ExportOptions{OutputPath: outPath})
	if err != nil {
		t.Fatal(err)
	}
	if got != outPath {
		t.Fatalf("returned path = %q", got)
	}
	html, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSessionDataPayload(t, string(html))
	var parsed struct {
		Header struct {
			Type string `json:"type"`
		} `json:"header"`
		Entries []any `json:"entries"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Type != "session" || len(parsed.Entries) != 1 {
		t.Fatalf("file export payload mismatch: %.200s", payload)
	}

	// No explicit theme and no state: systemPrompt/tools stay omitted.
	if strings.Contains(string(payload), `"systemPrompt"`) || strings.Contains(string(payload), `"tools"`) {
		t.Fatalf("file export should omit agent fields: %.200s", payload)
	}
}

func TestExportFromFileErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	_, err := ExportFromFile(missing, exporthtml.ExportOptions{})
	if err == nil || err.Error() != "File not found: "+missing {
		t.Fatalf("err = %v", err)
	}
}

// recordingToolRenderer records the custom-tool render calls.
type recordingToolRenderer struct {
	calls    []string
	results  []string
	callHTML map[string]string
}

func (r *recordingToolRenderer) RenderCall(toolCallID, toolName string, args any) string {
	r.calls = append(r.calls, toolName)
	return r.callHTML[toolCallID]
}

func (r *recordingToolRenderer) RenderResult(toolCallID, toolName string, result []types.Content, details any, isError bool) (string, string, bool) {
	r.results = append(r.results, toolName)
	return "<div>collapsed</div>", "<div>expanded " + toolName + "</div>", true
}

func TestPreRenderCustomTools(t *testing.T) {
	sm, _ := newFileBackedSessionManager(t)

	// Custom tool call + result (rendered via the renderer).
	if _, err := sm.AppendMessage(types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.Content{types.ToolCall{
			Kind: types.KindToolCall, ID: "call-custom", Name: "mytool", Arguments: map[string]any{"q": "x"},
		}},
		StopReason: types.StopReasonToolUse,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(types.ToolResultMessage{
		Role: types.RoleToolResult, ToolCallID: "call-custom", ToolName: "mytool",
		Content: []types.Content{types.TextContent{Kind: types.KindText, Text: "result"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Template-rendered tool (bash): no call pre-render, no result render.
	if _, err := sm.AppendMessage(types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.Content{types.ToolCall{
			Kind: types.KindToolCall, ID: "call-bash", Name: "bash", Arguments: map[string]any{"command": "ls"},
		}},
		StopReason: types.StopReasonToolUse,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(types.ToolResultMessage{
		Role: types.RoleToolResult, ToolCallID: "call-bash", ToolName: "bash",
		Content: []types.Content{types.TextContent{Kind: types.KindText, Text: "out"}},
	}); err != nil {
		t.Fatal(err)
	}

	renderer := &recordingToolRenderer{callHTML: map[string]string{"call-custom": "<div>custom call</div>"}}
	rendered := preRenderCustomTools(sm.GetEntries(), renderer)

	if len(renderer.calls) != 1 || renderer.calls[0] != "mytool" {
		t.Fatalf("renderCall invocations = %v, want [mytool]", renderer.calls)
	}
	if len(renderer.results) != 1 || renderer.results[0] != "mytool" {
		t.Fatalf("renderResult invocations = %v, want [mytool]", renderer.results)
	}
	rt, ok := rendered["call-custom"]
	if !ok || rt.CallHTML != "<div>custom call</div>" || rt.ResultHTMLExpanded != "<div>expanded mytool</div>" {
		t.Fatalf("call-custom rendered = %#v", rt)
	}
	if rt.ResultHTMLCollapsed != "<div>collapsed</div>" {
		t.Fatalf("collapsed = %q", rt.ResultHTMLCollapsed)
	}
	if _, ok := rendered["call-bash"]; ok {
		t.Fatal("template-rendered tool must not be pre-rendered")
	}

	// bash result without a pre-rendered call: renderer must not fire.
	renderer2 := &recordingToolRenderer{callHTML: map[string]string{}}
	preRenderCustomTools(sm.GetEntries(), renderer2)
	if len(renderer2.results) != 1 {
		t.Fatalf("bash result should skip the renderer, got %v", renderer2.results)
	}
}

func TestAgentSessionExportToHtml(t *testing.T) {
	session, sm := newExportFixtureSession(t)
	outPath := filepath.Join(t.TempDir(), "session.html")
	got, err := session.ExportToHtml(outPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != outPath {
		t.Fatalf("returned path = %q", got)
	}
	html, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSessionDataPayload(t, string(html))
	if !strings.Contains(string(payload), `"entries"`) {
		t.Fatalf("payload missing entries: %.120s", payload)
	}
	if sm.GetSessionFile() == "" {
		t.Fatal("fixture session should be file-backed")
	}
}

// newExportFixtureSession builds an AgentSession over a file-backed,
// already-flushed session manager (ExportToHtml needs a real session file;
// createAgentTestSession uses InMemorySession).
func newExportFixtureSession(t *testing.T) (*AgentSession, *SessionManager) {
	t.Helper()
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{GetAPIKey: true})
	sm, err := CreateSessionManager(t.TempDir(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.TextContent{Kind: types.KindText, Text: "hello"}},
		StopReason: types.StopReasonStop,
	}); err != nil {
		t.Fatal(err)
	}
	session.SessionManager = sm
	return session, sm
}

func decodeSessionDataPayload(t *testing.T, html string) []byte {
	t.Helper()
	re := regexp.MustCompile(`<script id="session-data" type="application/json">([A-Za-z0-9+/=]+)</script>`)
	match := re.FindStringSubmatch(html)
	if match == nil {
		t.Fatal("session-data payload missing")
	}
	decoded, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatalf("payload base64: %v", err)
	}
	return decoded
}

func TestAgentStateToolsSerializationShape(t *testing.T) {
	state := &agent.AgentState{Tools: []agent.AgentTool{{
		Tool: types.Tool{Name: "bash", Description: "Run bash", Parameters: json.RawMessage(`{"type":"object"}`)},
	}}}
	tools := exportToolsOf(state)
	if tools == nil || len(*tools) != 1 || (*tools)[0].Name != "bash" || string((*tools)[0].Parameters) != `{"type":"object"}` {
		t.Fatalf("exportToolsOf = %#v", tools)
	}
	encoded, err := jsonEncodeForTest(tools)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `[{"name":"bash","description":"Run bash","parameters":{"type":"object"}}]` {
		t.Fatalf("tools JSON = %s", encoded)
	}
	var nilState *agent.AgentState
	if exportToolsOf(nilState) != nil {
		t.Fatal("nil state should give nil tools")
	}
	if exportSystemPromptOf(nilState) != "" {
		t.Fatal("nil state should give empty systemPrompt")
	}
}

func jsonEncodeForTest(v any) ([]byte, error) {
	return json.Marshal(v)
}
