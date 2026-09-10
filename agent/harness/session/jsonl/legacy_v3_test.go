// pi: test/harness/jsonl-v3-migration.test.ts — legacy v3 migration.
// Ported subset covering the core migration scenarios (discovery, usage
// reporting, v4 upgrade, lane configuration reconstruction, session info /
// labels / custom messages / branch summaries / compactions / message
// chains); the remaining cases ride the same helpers and follow-ups land
// with the S2 batch.
package jsonl_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/env"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
	"github.com/ZoroSpace/pig/ai/types"
)

const migrationNow = int64(1_700_000_000_000)

type migrationEnv struct {
	fs    *env.NodeExecutionEnv
	root  string
	repo  *jsonl.JsonlSessionRepo
	t     *testing.T
	files map[string]string
}

func newMigrationEnv(t *testing.T) *migrationEnv {
	t.Helper()
	root := t.TempDir()
	fs := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	repo := jsonl.NewJsonlSessionRepo(jsonl.JsonlSessionRepoOptions{
		FileSystem: fs, SessionsRoot: "sessions", Now: func() int64 { return migrationNow },
	})
	t.Cleanup(func() { _ = repo.Close(context.Background()) })
	return &migrationEnv{fs: fs, root: root, repo: repo, t: t, files: map[string]string{}}
}

func (m *migrationEnv) ctx() context.Context { return context.Background() }

// writeLegacyV3Fixture writes one legacy v3 session file (pi:
// writeLegacyV3Fixture).
func (m *migrationEnv) writeLegacyV3Fixture(records []map[string]any, parentSession string) (string, string) {
	t := m.t
	t.Helper()
	directory, err := m.fs.JoinPath(m.ctx(), []string{"sessions", "--workspace--"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.fs.CreateDir(m.ctx(), directory, nil); err != nil {
		t.Fatal(err)
	}
	relativePath, err := m.fs.JoinPath(m.ctx(), []string{directory, "legacy.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := m.fs.AbsolutePath(m.ctx(), relativePath)
	if err != nil {
		t.Fatal(err)
	}
	header := map[string]any{
		"type": "session", "version": 3, "id": "legacy",
		"timestamp": "2023-11-14T22:13:20.000Z", "cwd": "/workspace",
	}
	if parentSession != "" {
		header["parentSession"] = parentSession
	}
	lines := []string{marshalRecord(t, header)}
	for _, record := range records {
		lines = append(lines, marshalRecord(t, record))
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := m.fs.WriteFile(m.ctx(), path, []byte(content)); err != nil {
		t.Fatal(err)
	}
	m.files[path] = content
	return path, content
}

func marshalRecord(t *testing.T, record map[string]any) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func uuidTimestamp(id string) int64 {
	compact := strings.ReplaceAll(id, "-", "")
	value, err := strconv.ParseInt(compact[:12], 16, 64)
	if err != nil {
		return 0
	}
	return value
}

func (m *migrationEnv) mainBranch(sess session.Session) session.Branch {
	t := m.t
	t.Helper()
	branch, err := sess.Branch("main", m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if branch == nil {
		t.Fatal("Expected imported main Branch")
	}
	return branch
}

func (m *migrationEnv) mainTip(sess session.Session) *string {
	t := m.t
	t.Helper()
	tip, err := m.mainBranch(sess).GetTipID(m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	return tip
}

// listConformanceRepo lists and opens the legacy fixture through the repo.
func (m *migrationEnv) openLegacy() session.Session {
	t := m.t
	t.Helper()
	listed, err := m.repo.List(nil, m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %d sessions", len(listed))
	}
	adapter := &jsonlRepoAdapter{repo: m.repo, cwd: "/workspace"}
	sess, err := adapter.Open(listed[0], m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func userMsg(text string) map[string]any {
	return map[string]any{
		"role":      "user",
		"content":   []any{map[string]any{"type": "text", "text": text}},
		"timestamp": 1,
	}
}

func legacyEntry(id string, parent any, entryType string, extra map[string]any) map[string]any {
	record := map[string]any{
		"type": entryType, "id": id, "parentId": parent, "timestamp": "2023-11-14T22:13:21.000Z",
	}
	for key, value := range extra {
		record[key] = value
	}
	return record
}

func TestDiscoversLegacyV3SessionFilesWithoutRewritingThem(t *testing.T) {
	m := newMigrationEnv(t)
	path, content := m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
	}, "")
	listed, err := m.repo.List(nil, m.ctx())
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed = %v, %v", listed, err)
	}
	metadata := listed[0]
	if metadata.ID != "legacy" || metadata.StorageVersion != jsonl.JSONLStorageVersion {
		t.Fatalf("metadata = %+v", metadata)
	}
	if metadata.Cwd != "/workspace" {
		t.Fatalf("cwd = %v", metadata.Cwd)
	}
	if got, _ := m.fs.ReadTextFile(m.ctx(), path); got != content {
		t.Fatal("legacy file must not be rewritten on discovery")
	}
}

func TestPreservesAnInvalidParentPathAsLegacyMetadata(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
	}, "/nonexistent/parent.jsonl")
	listed, err := m.repo.List(nil, m.ctx())
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed = %v, %v", listed, err)
	}
	if listed[0].LegacyParentSessionPath == nil || *listed[0].LegacyParentSessionPath != "/nonexistent/parent.jsonl" {
		t.Fatalf("legacyParentSessionPath = %+v", listed[0])
	}
}

func TestOpensAnEmptyLegacySessionWithADataOnlyMainBranch(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture(nil, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()
	tip := m.mainTip(sess)
	if tip != nil {
		t.Fatalf("tip = %v", tip)
	}
	entries, err := sess.FindEntries(nil, m.ctx())
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries = %v, %v", entries, err)
	}
}

func TestReportsEmbeddedLegacyUsageWithoutCreatingUsageRowsOrRewritingTheSource(t *testing.T) {
	m := newMigrationEnv(t)
	assistantUsage := map[string]any{
		"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 3,
		"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
	}
	assistantMessage := map[string]any{
		"role": "assistant", "content": []any{}, "stopReason": "stop",
		"api": "anthropic-messages", "provider": "anthropic", "model": "claude-sonnet-4-5",
		"usage": assistantUsage, "timestamp": 1,
	}
	path, content := m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": assistantMessage}),
	}, "")
	sess := m.openLegacy()
	stats, err := sess.GetStats(m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Usage.Input != 1 || stats.Usage.Output != 2 || stats.Usage.TotalTokens != 3 {
		t.Fatalf("stats usage = %+v", stats.Usage)
	}
	_ = sess.Close(m.ctx())
	if got, _ := m.fs.ReadTextFile(m.ctx(), path); got != content {
		t.Fatal("legacy file must not be rewritten")
	}
}

func TestWritesOneZeroValuedUsageAdjustmentWhenConvertingWithoutImportedUsage(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
	}, "")
	sess := m.openLegacy()
	if err := sess.SetValue(session.SessionNameAddress, "trigger", m.ctx()); err != nil {
		t.Fatal(err)
	}
	// The commit that triggered the upgrade also persisted one zero-valued
	// usage adjustment; assert via a fresh v4 read.
	_ = sess.Close(m.ctx())
	listed, err := m.repo.List(nil, m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	adapter := &jsonlRepoAdapter{repo: m.repo, cwd: "/workspace"}
	reopened, err := adapter.Open(listed[0], m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	name, err := reopened.GetName(m.ctx())
	if err != nil || name == nil || *name != "trigger" {
		t.Fatalf("name = %v, %v", name, err)
	}
	_ = reopened.Close(m.ctx())
}

func TestConvertsToV4AndPreservesTheFirstCallerTransaction(t *testing.T) {
	m := newMigrationEnv(t)
	path, _ := m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
	}, "")
	sess := m.openLegacy()
	branch := m.mainBranch(sess)
	id, err := branch.AppendCustomEntry("note", map[string]any{"ok": true}, m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	tip := m.mainTip(sess)
	if tip == nil || *tip != id {
		t.Fatalf("tip = %v", tip)
	}
	entries, _ := sess.FindEntries(&session.EntryQuery{Order: "asc"}, m.ctx())
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	if _, ok := entries[1].(*session.CustomEntry); !ok {
		t.Fatalf("second entry = %T", entries[1])
	}
	// The v3 source is gone: the file has been rewritten in place as v4.
	firstLine, err := m.fs.ReadTextLines(m.ctx(), path, intPtr(1))
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(firstLine[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header["v"] != float64(jsonl.JSONLFormatVersion) || header["kind"] != "header" {
		t.Fatalf("upgraded header = %+v", header)
	}
	_ = sess.Close(m.ctx())
}

func TestReconstructsLegacyLaneConfigurationAndReparentsARetainedChild(t *testing.T) {
	m := newMigrationEnv(t)
	modelChange := legacyEntry("mc", nil, "model_change", map[string]any{
		"provider": "anthropic", "modelId": "claude-sonnet-4-5",
	})
	thinkingChange := legacyEntry("tc", "mc", "thinking_level_change", map[string]any{
		"thinkingLevel": "high",
	})
	message := legacyEntry("msg", "tc", "message", map[string]any{"message": userMsg("hello")})
	m.writeLegacyV3Fixture([]map[string]any{modelChange, thinkingChange, message}, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()

	config, err := sess.GetValue(session.LaneConfig("main"), m.ctx())
	if err != nil || config == nil {
		t.Fatalf("lane config = %v, %v", config, err)
	}
	var configuration session.LaneConfiguration
	switch typed := config.Value.(type) {
	case session.LaneConfiguration:
		configuration = typed
	case *session.LaneConfiguration:
		configuration = *typed
	default:
		t.Fatalf("configuration = %T", config.Value)
	}
	if configuration.Model.Provider != "anthropic" || configuration.Model.ModelID != "claude-sonnet-4-5" ||
		configuration.ThinkingLevel != "high" {
		t.Fatalf("configuration = %+v", configuration)
	}
	tip := m.mainTip(sess)
	if tip == nil {
		t.Fatal("main tip must resolve through trailing configuration changes")
	}
	entries, _ := sess.FindEntries(&session.EntryQuery{Order: "asc"}, m.ctx())
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].Base().ID == "msg" {
		t.Fatal("entry id must be reminted")
	}
}

func TestImportsSessionInfoAsTheCurrentNameWithoutRetainingATreeEntry(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
		legacyEntry("info", "a", "session_info", map[string]any{"name": "my session"}),
	}, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()
	name, err := sess.GetName(m.ctx())
	if err != nil || name == nil || *name != "my session" {
		t.Fatalf("name = %v, %v", name, err)
	}
	entries, _ := sess.FindEntries(nil, m.ctx())
	if len(entries) != 1 {
		t.Fatalf("session_info must not be retained: %d entries", len(entries))
	}
	tip := m.mainTip(sess)
	if tip == nil || *tip != entries[0].Base().ID {
		t.Fatalf("tip = %v", tip)
	}
}

func TestImportsALabelForItsRemappedEntry(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
		legacyEntry("label", "a", "label", map[string]any{"targetId": "a", "label": "pinned"}),
	}, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()
	entries, _ := sess.FindEntries(nil, m.ctx())
	if len(entries) != 1 {
		t.Fatalf("labels must not be retained: %d entries", len(entries))
	}
	reminted := entries[0].Base().ID
	label, err := sess.GetLabel(reminted, m.ctx())
	if err != nil || label == nil || *label != "pinned" {
		t.Fatalf("label = %v, %v", label, err)
	}
}

func TestSkipsALabelWhoseTargetHasNoRetainedAncestor(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("mc", nil, "model_change", map[string]any{"provider": "anthropic", "modelId": "m"}),
		legacyEntry("a", "mc", "message", map[string]any{"message": userMsg("a")}),
		legacyEntry("label", "a", "label", map[string]any{"targetId": "mc", "label": "gone"}),
	}, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()
	// The label target (discarded model_change "mc") has no retained
	// ancestor, so the label is skipped entirely.
	entries, _ := sess.FindEntries(nil, m.ctx())
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	reminted := entries[0].Base().ID
	label, err := sess.GetLabel(reminted, m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if label != nil {
		t.Fatalf("label = %v, want none", *label)
	}
}

func TestImportsACustomMessageAsACurrentMessageEntry(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("cm", nil, "custom_message", map[string]any{
			"customType": "notice", "content": "maintenance", "display": true,
		}),
	}, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()
	entries, _ := sess.FindEntries(&session.EntryQuery{Order: "asc"}, m.ctx())
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	messageEntry, ok := entries[0].(*session.MessageEntry)
	if !ok {
		t.Fatalf("entry = %T", entries[0])
	}
	message, ok := messageEntry.Message.(harnessCustomMessage)
	if !ok {
		t.Fatalf("message = %T", messageEntry.Message)
	}
	if message.CustomType != "notice" || message.Display != true {
		t.Fatalf("message = %+v", message)
	}
}

func TestImportsBranchSummariesWithRootSentinelAndFromHook(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
		legacyEntry("bs", "a", "branch_summary", map[string]any{
			"fromId": "root", "summary": "the branch", "fromHook": true,
		}),
	}, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()
	entries, _ := sess.FindEntries(&session.EntryQuery{Order: "asc"}, m.ctx())
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	summary, ok := entries[1].(*session.BranchSummaryEntry)
	if !ok {
		t.Fatalf("entry = %T", entries[1])
	}
	if summary.FromID != nil {
		t.Fatalf(`fromId "root" must normalize to null, got %v`, *summary.FromID)
	}
	if summary.Summary != "the branch" || !summary.FromHook {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestMaterializesTheRetainedTailAndPreservesTheCompactionPayload(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
		legacyEntry("b", "a", "message", map[string]any{"message": userMsg("b")}),
		legacyEntry("compact", "b", "compaction", map[string]any{
			"summary": "the summary", "firstKeptEntryId": "a", "tokensBefore": 42,
			"fromHook": true,
		}),
	}, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()
	entries, _ := sess.FindEntries(&session.EntryQuery{Order: "asc"}, m.ctx())
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	compaction, ok := entries[2].(*session.CompactionEntry)
	if !ok {
		t.Fatalf("entry = %T", entries[2])
	}
	if compaction.Summary != "the summary" || compaction.TokensBefore != 42 || !compaction.FromHook {
		t.Fatalf("compaction = %+v", compaction)
	}
	if len(compaction.RetainedTail) != 2 {
		t.Fatalf("retained tail = %d", len(compaction.RetainedTail))
	}
	if user, ok := compaction.RetainedTail[0].(types.UserMessage); !ok || !strings.Contains(fmt.Sprint(user.Content.Parts[0]), "a") {
		t.Fatalf("tail[0] = %+v", compaction.RetainedTail[0])
	}
}

func TestRemapsALegacyMessageChainAndExposesItThroughCurrentAPIs(t *testing.T) {
	m := newMigrationEnv(t)
	m.writeLegacyV3Fixture([]map[string]any{
		legacyEntry("a", nil, "message", map[string]any{"message": userMsg("a")}),
		legacyEntry("b", "a", "message", map[string]any{"message": userMsg("b")}),
	}, "")
	sess := m.openLegacy()
	defer func() { _ = sess.Close(m.ctx()) }()
	entries, err := sess.FindEntries(&session.EntryQuery{Order: "asc"}, m.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	for i, entry := range entries {
		reminted := entry.Base().ID
		if uuidTimestamp(reminted) == 0 {
			t.Fatalf("entry %d id %q must be a UUIDv7 with the legacy timestamp", i, reminted)
		}
	}
	if tip := m.mainTip(sess); tip == nil || *tip != entries[1].Base().ID {
		t.Fatalf("tip = %v", tip)
	}
	if parent := entries[1].Base().ParentID; parent == nil || *parent != entries[0].Base().ID {
		t.Fatalf("parent chain = %v", parent)
	}
}

// harnessCustomMessage aliases pig's custom message type (pi:
// CustomMessage from harness/messages.ts).
type harnessCustomMessage = harness.CustomMessage
