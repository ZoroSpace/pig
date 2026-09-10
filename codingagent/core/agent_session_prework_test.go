// pig-side coverage for the AgentSession prework modules (pi exercises them
// through agent-session tests that arrive with the AgentSession batch).
package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
)

func TestExportSessionToJsonlWritesHeaderAndParentChain(t *testing.T) {
	manager, err := InMemorySession(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(userMsg("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(types.AssistantMessage{
		Role: types.RoleAssistant, Provider: "p", Model: "m", Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(t.TempDir(), "out", "export.jsonl")
	path, err := ExportSessionToJsonl(manager, outputPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path != outputPath {
		t.Fatalf("path = %q", path)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if !strings.HasPrefix(lines[0], `{"type":"session"`) {
		t.Fatalf("header = %q", lines[0])
	}
	// The header carries no parentId; entries carry the previous entry's id.
	if strings.Contains(lines[0], "parentId") {
		t.Fatalf("header has parentId: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"parentId":null`) {
		t.Fatalf("first entry = %q, want explicit null parent", lines[1])
	}
	if !strings.Contains(lines[2], `"parentId":"`) {
		t.Fatalf("second entry = %q, want chained parent", lines[2])
	}
}

func TestGetUsageCostBreakdownGroupsByModelAndBucketsOtherUsage(t *testing.T) {
	entries := []SessionEntry{
		&SessionMessageEntry{
			EntryBase: EntryBase{Type: "message", ID: "1"},
			Message: &types.AssistantMessage{
				Role: types.RoleAssistant, Provider: "openrouter", Model: "m1", ResponseModel: "m1-resolved",
				Usage: types.Usage{Input: 10, Output: 5, Cost: types.Cost{Total: 1.5}},
			},
		},
		&SessionMessageEntry{
			EntryBase: EntryBase{Type: "message", ID: "2"},
			Message: &types.ToolResultMessage{
				Role: types.RoleToolResult, Usage: &types.Usage{Input: 2, Output: 1, Cost: types.Cost{Total: 0.25}},
			},
		},
		&CompactionEntry{
			EntryBase: EntryBase{Type: "compaction", ID: "3"},
			Usage:     &types.Usage{Input: 4, Output: 0, Cost: types.Cost{Total: 0.1}},
		},
		&SessionMessageEntry{
			EntryBase: EntryBase{Type: "message", ID: "4"},
			Message: &types.UserMessage{
				Role: types.RoleUser, Content: types.UserContent{String: "no usage"},
			},
		},
	}

	breakdown := GetUsageCostBreakdown(entries)
	if len(breakdown) != 2 {
		t.Fatalf("breakdown = %+v, want 2 buckets", breakdown)
	}
	// Sorted by cost descending.
	if breakdown[0].Key != "openrouter/m1-resolved" || breakdown[0].Cost != 1.5 || breakdown[0].Tokens != 15 {
		t.Fatalf("entry 0 = %+v", breakdown[0])
	}
	if breakdown[1].Key != "Tools/summaries" || breakdown[1].Cost != 0.35 || breakdown[1].Tokens != 7 {
		t.Fatalf("entry 1 = %+v", breakdown[1])
	}
}

func TestAuthGuidanceMessages(t *testing.T) {
	if !strings.Contains(FormatNoModelsAvailableMessage(), "No models available.") {
		t.Fatal("no-models message drifted")
	}
	if !strings.Contains(FormatNoModelSelectedMessage(), "Then use /model to select a model.") {
		t.Fatal("no-model-selected message drifted")
	}
	if !strings.Contains(FormatNoApiKeyFoundMessage("openrouter"), "No API key found for openrouter.") {
		t.Fatal("provider name missing")
	}
	if !strings.Contains(FormatNoApiKeyFoundMessage("unknown"), "No API key found for the selected model.") {
		t.Fatal("unknown provider phrasing drifted")
	}
}

func TestBuiltinSlashCommandsIncludeQuitWithAppName(t *testing.T) {
	found := false
	for _, command := range BuiltinSlashCommands {
		if command.Name == "quit" {
			found = true
			if command.Description != "Quit pi" {
				t.Fatalf("quit description = %q", command.Description)
			}
		}
	}
	if !found {
		t.Fatal("quit command missing")
	}
}

func TestExecuteBashWithOperationsStreamsAndSucceeds(t *testing.T) {
	var chunks []string
	operations := tools.BashOperations{
		Exec: func(ctx context.Context, command string, cwd string, options *tools.BashExecOptions) (*int, error) {
			if options != nil && options.OnData != nil {
				options.OnData([]byte("hel"))
				options.OnData([]byte("lo"))
			}
			code := 0
			return &code, nil
		},
	}
	result, err := ExecuteBashWithOperations(context.Background(), "echo hello", ".", operations,
		&BashExecutorOptions{OnChunk: func(chunk string) { chunks = append(chunks, chunk) }})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "hello" {
		t.Fatalf("output = %q", result.Output)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exitCode = %+v", result.ExitCode)
	}
	if result.Cancelled || result.Truncated || result.FullOutputPath != "" {
		t.Fatalf("result = %+v", result)
	}
	if strings.Join(chunks, "") != "hello" {
		t.Fatalf("chunks = %q", chunks)
	}
}

func TestExecuteBashWithOperationsReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := tools.BashOperations{
		Exec: func(ctx context.Context, command string, cwd string, options *tools.BashExecOptions) (*int, error) {
			return nil, ctx.Err()
		},
	}
	result, err := ExecuteBashWithOperations(ctx, "sleep 100", ".", operations, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cancelled || result.ExitCode != nil {
		t.Fatalf("result = %+v", result)
	}
}
