// pi: test/harness/jsonl-storage.test.ts — JsonlStorage snapshot creation,
// persistence, fork-source snapshots, and torn-tail repair.
package jsonl_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/env"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
	pigtesting "github.com/ZoroSpace/pig/agent/harness/session/testing"
	"github.com/ZoroSpace/pig/ai/types"
)

// harnessFS aliases harness.FileSystem for local brevity.
type harnessFS = harness.FileSystem

const harnessFileKindDirectory = harness.FileKindDirectory

const storageNow = int64(1_700_000_000_000)

func storageHeader(id string) jsonl.JsonlStorageHeader {
	return jsonl.JsonlStorageHeader{
		V: jsonl.JSONLFormatVersion, Kind: "header", ID: id,
		StorageVersion: jsonl.JSONLStorageVersion, CreatedAt: storageNow, Cwd: "/workspace",
	}
}

func storageOptions(t *testing.T, path string) (harnessFS, *jsonl.JsonlStorageOptions) {
	t.Helper()
	root := t.TempDir()
	fileSystem := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	return fileSystem, &jsonl.JsonlStorageOptions{FileSystem: fileSystem, Path: path, Now: func() int64 { return storageNow }}
}

func readTextFile(t *testing.T, fs harnessFS, path string) string {
	t.Helper()
	content, err := fs.ReadTextFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func fileExists(t *testing.T, fs harnessFS, path string) bool {
	t.Helper()
	exists, err := fs.Exists(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

func storedValue(address session.Value, value any, seq int64) session.StoredValue {
	return session.StoredValue{Address: address, Value: value, Seq: seq}
}

func preparedSnapshot() session.ForkDestinationSnapshot {
	userMessage := types.UserMessage{
		Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("hello")}}, Timestamp: 1,
	}
	root := &session.MessageEntry{Message: userMessage}
	root.ID = "root"
	root.Type = session.EntryTypeMessage
	root.Seq = 1
	root.Timestamp = storageNow
	return session.ForkDestinationSnapshot{
		Entries: map[string]session.Entry{
			"root": root,
		},
		ScalarValues: []session.StoredValue{
			storedValue(session.BranchTip("main"), "root", 2),
			storedValue(session.LaneStateAddress("main"), session.LaneState{Inbox: []session.InboxItem{}}, 3),
			storedValue(session.SessionNameAddress, "forked", 4),
		},
		NextSeq: 9,
	}
}

func storageUserEntry(id string, text string) *session.MessageEntry {
	entry := &session.MessageEntry{
		Message: types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent(text)}}, Timestamp: 1,
		},
	}
	entry.ID = id
	entry.Type = session.EntryTypeMessage
	return entry
}

func storageCustomEntry(id, customType string) *session.CustomEntry {
	entry := &session.CustomEntry{}
	entry.ID = id
	entry.Type = session.EntryTypeCustom
	entry.CustomType = &customType
	return entry
}

func storageUsageRow() session.UsageRow {
	return session.UsageRow{
		ID: "usage", EntryID: strPtr("root"), Adjustment: false,
		Usage: types.Usage{Input: 1, Output: 2, TotalTokens: 3, Cost: types.Cost{}},
	}
}

func strPtr(s string) *string { return &s }

func TestSnapshotCreationAtomicallyPublishesAdvancesAndReopens(t *testing.T) {
	ctx := context.Background()
	fileSystem, options := storageOptions(t, "fork.jsonl")
	storage, err := jsonl.CreateJsonlStorageFromForkSnapshot(options, storageHeader("fork"), preparedSnapshot(), ctx)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(readTextFile(t, fileSystem, "fork.jsonl"), "\n"), "\n")
	for _, line := range lines[1:] {
		var parsed any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatal(err)
		}
		if _, isArray := parsed.([]any); isArray {
			t.Fatal("snapshot writes must be one line each")
		}
	}
	entry, err := storage.GetEntries([]string{"root"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry["root"]; e == nil || e.Base().Timestamp != storageNow {
		t.Fatalf("root entry = %v", e)
	}
	tip, _ := storage.GetValue(session.BranchTip("main"), ctx)
	if tip == nil || tip.Value != "root" {
		t.Fatalf("tip = %v", tip)
	}
	state, _ := storage.GetValue(session.LaneStateAddress("main"), ctx)
	pigtesting.AssertJSONEqual(t, "lane state", state.Value, session.LaneState{Inbox: []session.InboxItem{}})
	name, _ := storage.GetValue(session.SessionNameAddress, ctx)
	if name == nil || name.Value != "forked" {
		t.Fatalf("name = %v", name)
	}
	stats, _ := storage.GetStats(ctx)
	pigtesting.AssertJSONEqual(t, "stats", stats, session.SessionStats{MessageCount: 1, Usage: types.Usage{}})
	usage, _ := storage.ScanUsage(session.UsageScan{Order: "asc"}, ctx)
	if len(usage) != 0 {
		t.Fatalf("usage = %v", usage)
	}
	result, err := storage.Commit([]session.Write{session.SetValue(session.SessionNameAddress, "updated")}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.FirstSeq != 9 {
		t.Fatalf("firstSeq = %d", result.FirstSeq)
	}
	if fileExists(t, fileSystem, "fork.jsonl.tmp") {
		t.Fatal("temp file must be cleaned up")
	}
	_ = storage.Close(ctx)

	reopened, err := jsonl.OpenJsonlStorage(options, ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := reopened.GetEntries([]string{"root"}, ctx)
	if _, ok := entries["root"]; !ok {
		t.Fatal("root must survive reopen")
	}
	name, _ = reopened.GetValue(session.SessionNameAddress, ctx)
	if name == nil || name.Value != "updated" {
		t.Fatalf("reopened name = %v", name)
	}
	next, _ := reopened.Commit(nil, ctx)
	if next.FirstSeq != 10 {
		t.Fatalf("next firstSeq = %d", next.FirstSeq)
	}
	_ = reopened.Close(ctx)
}

func TestSnapshotCreationRemovesTheTemporaryFileWhenPublicationFails(t *testing.T) {
	ctx := context.Background()
	fileSystem, options := storageOptions(t, "blocked.jsonl")
	if err := fileSystem.CreateDir(ctx, "blocked.jsonl", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := jsonl.CreateJsonlStorageFromForkSnapshot(options, storageHeader("blocked"), preparedSnapshot(), ctx); err == nil {
		t.Fatal("publication onto a directory must fail")
	} else if !strings.Contains(err.Error(), "Failed to publish JSONL storage") {
		t.Fatalf("err = %v", err)
	}
	if fileExists(t, fileSystem, "blocked.jsonl.tmp") {
		t.Fatal("temp file must be removed on failure")
	}
	info, err := fileSystem.FileInfo(ctx, "blocked.jsonl")
	if err != nil || info.Kind != harnessFileKindDirectory {
		t.Fatalf("blocked.jsonl = %v, %v", info, err)
	}
}

func TestReplaysWholeListDeletionWithoutResurrectingEarlierAppends(t *testing.T) {
	ctx := context.Background()
	_, options := storageOptions(t, "list-delete.jsonl")
	events := session.MustList("test.events", "")
	storage, err := jsonl.CreateJsonlStorage(options, storageHeader("list-delete"), nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Commit([]session.Write{
		session.AppendList(events, "first"), session.AppendList(events, "second"),
	}, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Commit([]session.Write{session.DeleteList(events)}, ctx); err != nil {
		t.Fatal(err)
	}
	_ = storage.Close(ctx)

	reopened, err := jsonl.OpenJsonlStorage(options, ctx)
	if err != nil {
		t.Fatal(err)
	}
	elements, _ := reopened.ReadList(events, nil, ctx)
	if len(elements) != 0 {
		t.Fatalf("elements = %v", elements)
	}
	recreated, _ := reopened.Commit([]session.Write{session.AppendList(events, "after")}, ctx)
	if recreated.FirstSeq != 4 {
		t.Fatalf("firstSeq = %d", recreated.FirstSeq)
	}
	elements, _ = reopened.ReadList(events, nil, ctx)
	pigtesting.AssertJSONEqual(t, "recreated", elements, []session.ListElement{{Seq: 4, Value: "after"}})
	_ = reopened.Close(ctx)
}

func TestWritesOneLinePerTransactionAndReplaysStampedState(t *testing.T) {
	ctx := context.Background()
	fileSystem, options := storageOptions(t, "session.jsonl")
	storage, err := jsonl.CreateJsonlStorage(options, storageHeader("round-trip"), nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := storage.Commit([]session.Write{
		session.InsertEntry(storageUserEntry("root", "hello")),
		session.SetValue(session.BranchTip("main"), "root"),
		session.InsertUsage(storageUsageRow()),
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Commit([]session.Write{session.SetValue(session.SessionNameAddress, "name")}, ctx); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(readTextFile(t, fileSystem, "session.jsonl"), "\n"), "\n")
	var headerLine jsonl.JsonlStorageHeader
	if err := json.Unmarshal([]byte(lines[0]), &headerLine); err != nil {
		t.Fatal(err)
	}
	pigtesting.AssertJSONEqual(t, "header", headerLine, storageHeader("round-trip"))
	var firstTransaction []any
	if err := json.Unmarshal([]byte(lines[1]), &firstTransaction); err != nil {
		t.Fatal(err)
	}
	if len(firstTransaction) != 3 {
		t.Fatalf("first transaction length = %d", len(firstTransaction))
	}
	var secondTransaction any
	if err := json.Unmarshal([]byte(lines[2]), &secondTransaction); err != nil {
		t.Fatal(err)
	}
	if _, isArray := secondTransaction.([]any); isArray {
		t.Fatal("second transaction must be a single object")
	}
	_ = storage.Close(ctx)

	reopened, err := jsonl.OpenJsonlStorage(options, ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := reopened.GetEntries([]string{"root"}, ctx)
	want := storageUserEntry("root", "hello")
	want.Base().Seq = committed.Seqs[0]
	want.Base().Timestamp = committed.Timestamp
	pigtesting.AssertJSONEqual(t, "root", entries["root"], want)
	tip, _ := reopened.GetValue(session.BranchTip("main"), ctx)
	pigtesting.AssertJSONEqual(t, "tip", tip, session.StoredValue{Address: session.BranchTip("main"), Value: "root", Seq: committed.Seqs[1]})
	usage, _ := reopened.ScanUsage(session.UsageScan{Order: "asc"}, ctx)
	rows := []any{}
	for _, row := range usage {
		rows = append(rows, map[string]any{"id": row.ID, "seq": row.Seq})
	}
	pigtesting.AssertJSONEqual(t, "usage", rows, []any{map[string]any{"id": "usage", "seq": committed.Seqs[2]}})
	historicalStats := session.SessionStats{
		MessageCount: 1,
		Usage:        types.Usage{Input: 1, Output: 2, TotalTokens: 3, Cost: types.Cost{}},
	}
	stats, _ := reopened.GetStats(ctx)
	pigtesting.AssertJSONEqual(t, "stats", stats, historicalStats)
	next, _ := reopened.Commit(nil, ctx)
	if next.FirstSeq != 5 {
		t.Fatalf("firstSeq = %d", next.FirstSeq)
	}
	pigtesting.AssertJSONEqual(t, "next stats", next.Stats, historicalStats)
	_ = reopened.Close(ctx)
}

func TestCapturesOneSerializedBoundaryBetweenCommits(t *testing.T) {
	ctx := context.Background()
	_, options := storageOptions(t, "session.jsonl")
	storage, err := jsonl.CreateJsonlStorage(options, storageHeader("snapshot"), nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Commit([]session.Write{
		session.InsertEntry(storageCustomEntry("root", "root")),
		session.SetValue(session.BranchTip("main"), "root"),
	}, ctx); err != nil {
		t.Fatal(err)
	}
	captured, err := storage.CaptureForkSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Commit([]session.Write{
		session.InsertEntry(storageCustomEntry("child", "child")),
		session.SetValue(session.BranchTip("main"), "child"),
	}, ctx); err != nil {
		t.Fatal(err)
	}

	ids := []string{}
	for _, entry := range captured.Entries {
		ids = append(ids, entry.Base().ID)
	}
	pigtesting.AssertJSONEqual(t, "captured entries", ids, []string{"root"})
	var tipValue any
	for _, stored := range captured.ScalarValues {
		if stored.Address.Namespace == session.BranchTip("main").Namespace && stored.Address.Key == "main" {
			tipValue = stored.Value
		}
	}
	if tipValue != "root" {
		t.Fatalf("captured tip = %v", tipValue)
	}
	entries, _ := storage.GetEntries([]string{"child"}, ctx)
	if _, ok := entries["child"]; !ok {
		t.Fatal("child must be committed")
	}
	tip, _ := storage.GetValue(session.BranchTip("main"), ctx)
	if tip == nil || tip.Value != "child" {
		t.Fatalf("tip = %v", tip)
	}
	_ = storage.Close(ctx)
}

func seedTornFile(t *testing.T) (harnessFS, *jsonl.JsonlStorageOptions, string) {
	t.Helper()
	ctx := context.Background()
	fileSystem, options := storageOptions(t, "session.jsonl")
	storage, err := jsonl.CreateJsonlStorage(options, storageHeader("torn"), nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Commit([]session.Write{session.InsertEntry(storageUserEntry("kept", "kept"))}, ctx); err != nil {
		t.Fatal(err)
	}
	_ = storage.Close(ctx)
	prefix := readTextFile(t, fileSystem, "session.jsonl")
	return fileSystem, options, prefix
}

func tornEntryLine(t *testing.T, id string) string {
	t.Helper()
	entry := storageUserEntry(id, id)
	entry.Base().Seq = 2
	entry.Base().Timestamp = storageNow
	data, err := json.Marshal(session.CommittedEntryWrite{Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDiscardsAnUnterminatedFinalObjectLineAndTruncatesBeforeAdmittingWrites(t *testing.T) {
	ctx := context.Background()
	fileSystem, options, prefix := seedTornFile(t)
	if err := fileSystem.AppendFile(ctx, "session.jsonl", []byte(tornEntryLine(t, "torn"))); err != nil {
		t.Fatal(err)
	}

	reopened, err := jsonl.OpenJsonlStorage(options, ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := reopened.GetEntries([]string{"kept", "torn"}, ctx)
	if _, ok := entries["torn"]; ok {
		t.Fatal("torn entry must be discarded")
	}
	if _, ok := entries["kept"]; !ok {
		t.Fatal("kept entry must survive")
	}
	if got := readTextFile(t, fileSystem, "session.jsonl"); got != prefix {
		t.Fatal("file must be truncated back to the complete prefix")
	}
	if fileExists(t, fileSystem, "session.jsonl.tmp") {
		t.Fatal("temp file must be cleaned up")
	}

	next, err := reopened.Commit([]session.Write{session.InsertEntry(storageUserEntry("after", "after"))}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next.FirstSeq != 2 {
		t.Fatalf("firstSeq = %d", next.FirstSeq)
	}
	entries, _ = reopened.GetEntries([]string{"after"}, ctx)
	if entries["after"] == nil || entries["after"].Base().Seq != 2 {
		t.Fatalf("after = %v", entries["after"])
	}
	_ = reopened.Close(ctx)
}

func TestDiscardsATornArrayLineWhollyIncludingListElements(t *testing.T) {
	ctx := context.Background()
	fileSystem, options, prefix := seedTornFile(t)
	events := session.MustList("test.events", "")
	first := storageUserEntry("torn-a", "torn-a")
	first.Base().Seq = 2
	first.Base().Timestamp = storageNow
	firstWrite, err := json.Marshal(session.CommittedEntryWrite{Entry: first})
	if err != nil {
		t.Fatal(err)
	}
	valueWrite, err := json.Marshal(session.CommittedValueSetWrite{Seq: 3, Namespace: session.SessionNameAddress.Namespace, Key: session.SessionNameAddress.Key, Value: "lost"})
	if err != nil {
		t.Fatal(err)
	}
	listWrite, err := json.Marshal(session.CommittedListAppendWrite{Seq: 4, Namespace: events.Namespace, Key: events.Key, Value: "lost"})
	if err != nil {
		t.Fatal(err)
	}
	torn := "[" + string(firstWrite) + "," + string(valueWrite) + "," + string(listWrite) + "]"
	if err := fileSystem.AppendFile(ctx, "session.jsonl", []byte(torn)); err != nil {
		t.Fatal(err)
	}

	reopened, err := jsonl.OpenJsonlStorage(options, ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := reopened.GetEntries([]string{"torn-a"}, ctx)
	if _, ok := entries["torn-a"]; ok {
		t.Fatal("torn array line must be discarded wholly")
	}
	if stored, _ := reopened.GetValue(session.SessionNameAddress, ctx); stored != nil {
		t.Fatal("value from torn line must be discarded")
	}
	elements, _ := reopened.ReadList(events, nil, ctx)
	if len(elements) != 0 {
		t.Fatal("list elements from torn line must be discarded")
	}
	if got := readTextFile(t, fileSystem, "session.jsonl"); got != prefix {
		t.Fatal("file must be truncated back to the prefix")
	}
	_ = reopened.Close(ctx)
}

func requireOpenError(t *testing.T, options *jsonl.JsonlStorageOptions, match string, corrupted string, fileSystem harnessFS) {
	t.Helper()
	_, err := jsonl.OpenJsonlStorage(options, context.Background())
	if err == nil || !strings.Contains(err.Error(), match) {
		t.Fatalf("err = %v, want match %q", err, match)
	}
	if got := readTextFile(t, fileSystem, options.Path); got != corrupted {
		t.Fatal("corrupted file must not be rewritten")
	}
}

func TestRejectsAMalformedInteriorLineWithoutRewriting(t *testing.T) {
	fileSystem, options, prefix := seedTornFile(t)
	corrupted := prefix + "not-json\n" + marshalValueSet(t, "after") + "\n"
	if err := fileSystem.WriteFile(context.Background(), "session.jsonl", []byte(corrupted)); err != nil {
		t.Fatal(err)
	}
	requireOpenError(t, options, "line 3", corrupted, fileSystem)
}

func TestRejectsTheUnsupportedPreWP01ScalarRecordSpelling(t *testing.T) {
	fileSystem, options, prefix := seedTornFile(t)
	legacyKind := "reg" + "ister"
	corrupted := prefix + `{"kind":"` + legacyKind + `","op":"set","seq":2,"namespace":"legacy.value","key":"state","value":true}` + "\n"
	if err := fileSystem.WriteFile(context.Background(), "session.jsonl", []byte(corrupted)); err != nil {
		t.Fatal(err)
	}
	requireOpenError(t, options, "line 3", corrupted, fileSystem)
}

func TestRejectsACompleteMalformedFinalLineWithoutRewriting(t *testing.T) {
	fileSystem, options, prefix := seedTornFile(t)
	corrupted := prefix + "not-json\n"
	if err := fileSystem.WriteFile(context.Background(), "session.jsonl", []byte(corrupted)); err != nil {
		t.Fatal(err)
	}
	requireOpenError(t, options, "line 3", corrupted, fileSystem)
}

func TestRejectsACompleteFinalLineWithInvalidTransactionFraming(t *testing.T) {
	fileSystem, options, prefix := seedTornFile(t)
	corrupted := prefix + `{"kind":"nope","seq":2}` + "\n"
	if err := fileSystem.WriteFile(context.Background(), "session.jsonl", []byte(corrupted)); err != nil {
		t.Fatal(err)
	}
	requireOpenError(t, options, "line 3", corrupted, fileSystem)
}

func TestRejectsAnUnterminatedHeader(t *testing.T) {
	fileSystem, options := storageOptions(t, "session.jsonl")
	headerJSON, err := json.Marshal(storageHeader("torn"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fileSystem.WriteFile(context.Background(), "session.jsonl", headerJSON[:len(headerJSON)-4]); err != nil {
		t.Fatal(err)
	}
	_, err = jsonl.OpenJsonlStorage(options, context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing header") {
		t.Fatalf("err = %v, want missing header", err)
	}
}

func marshalValueSet(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(session.CommittedValueSetWrite{Seq: 3, Namespace: session.SessionNameAddress.Namespace, Key: session.SessionNameAddress.Key, Value: value})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
