// pi: harness/session/testing/conformance/storage.ts — runner-independent
// conformance cases for the durable Storage contract.
//
// Assertion mapping: node:assert deepStrictEqual → JSON-normalized
// reflect.DeepEqual (session values contain slices and are not comparable
// with ==); rejects → expecting a non-nil error.
package testing

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
)

// conformanceCtx is pi's BACKGROUND_CONTEXT.
var conformanceCtx = context.Background()

// StorageConformanceCases creates fresh, runner-independent cases for the
// durable Storage contract (pi: createStorageConformance).
func StorageConformanceCases(factory func(t *testing.T) StorageFixture) []ConformanceCase {
	newStorage := func(t *testing.T) session.Storage {
		t.Helper()
		fixture := factory(t)
		t.Cleanup(fixture.Close)
		return fixture.Storage
	}
	return []ConformanceCase{
		{
			Group: "transactions", Name: "commits mixed writes atomically in write order",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				result, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("entry", nil, "entry")),
					session.SetValue(testName, "session"),
					session.InsertUsage(session.UsageRow{ID: "usage", Usage: conformanceUsage(2, 3, nil), Adjustment: false, EntryID: strPtr("entry")}),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Seqs) != 3 {
					t.Fatalf("seqs = %v", result.Seqs)
				}
				if result.FirstSeq != result.Seqs[0] {
					t.Fatalf("firstSeq = %d", result.FirstSeq)
				}
				assertCommitStats(t, storage, result)
				assertStrictlyIncreasing(t, "seqs", result.Seqs)
				if result.Timestamp < 0 {
					t.Fatalf("timestamp = %d", result.Timestamp)
				}
				entries, err := storage.GetEntries([]string{"entry"}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				wantEntry := conformanceUserEntry("entry", nil, "entry")
				wantEntry.Base().Seq = result.Seqs[0]
				wantEntry.Base().Timestamp = result.Timestamp
				assertJSONEqual(t, "entries", entries, map[string]any{"entry": wantEntry})
				stored, err := storage.GetValue(testName, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				assertJSONEqual(t, "testName", stored, session.StoredValue{Address: testName, Value: "session", Seq: result.Seqs[1]})
				usage, err := storage.ScanUsage(session.UsageScan{Order: "asc"}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				assertJSONEqual(t, "usage", usage, []session.UsageRow{{ID: "usage", Seq: result.Seqs[2], Usage: conformanceUsage(2, 3, nil), Adjustment: false, EntryID: strPtr("entry")}})
			},
		},
		{
			Group: "transactions", Name: "rolls back every store when a mixed transaction fails",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				if _, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("root", nil, "root")),
					session.InsertUsage(session.UsageRow{ID: "taken", Usage: conformanceUsage(1, 1, nil), Adjustment: false}),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				entriesBefore, _ := storage.ScanEntries(session.EntryScan{Order: "asc"}, conformanceCtx)
				usageBefore, _ := storage.ScanUsage(session.UsageScan{Order: "asc"}, conformanceCtx)
				statsBefore, _ := storage.GetStats(conformanceCtx)

				err := func() error {
					_, err := storage.Commit([]session.Write{
						session.SetValue(testName, "transient"),
						session.InsertEntry(conformanceCustomEntry("transient-entry", strPtr("root"), "note", map[string]any{"id": "transient-entry"})),
						session.InsertUsage(session.UsageRow{ID: "transient-usage", Usage: conformanceUsage(5, 8, nil), Adjustment: true}),
						session.InsertEntry(conformanceCustomEntry("taken", nil, "note", map[string]any{"id": "taken"})),
					}, conformanceCtx)
					return err
				}()
				expectError(t, "mixed transaction", err)

				entriesAfter, _ := storage.ScanEntries(session.EntryScan{Order: "asc"}, conformanceCtx)
				usageAfter, _ := storage.ScanUsage(session.UsageScan{Order: "asc"}, conformanceCtx)
				statsAfter, _ := storage.GetStats(conformanceCtx)
				assertJSONEqual(t, "entries", entriesAfter, entriesBefore)
				assertJSONEqual(t, "usage", usageAfter, usageBefore)
				assertJSONEqual(t, "stats", statsAfter, statsBefore)
				stored, _ := storage.GetValue(testName, conformanceCtx)
				if stored != nil {
					t.Fatal("transient value must roll back")
				}
			},
		},
		{
			Group: "transactions", Name: "preserves overwritten and deleted values when a transaction fails",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				if _, err := storage.Commit([]session.Write{
					session.SetValue(conformanceTestValue("overwritten"), "original"),
					session.SetValue(conformanceTestValue("deleted"), map[string]any{"kept": true}),
					session.InsertEntry(conformanceUserEntry("taken", nil, "taken")),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				overwrittenBefore, _ := storage.GetValue(conformanceTestValue("overwritten"), conformanceCtx)
				deletedBefore, _ := storage.GetValue(conformanceTestValue("deleted"), conformanceCtx)

				err := func() error {
					_, err := storage.Commit([]session.Write{
						session.SetValue(conformanceTestValue("overwritten"), "transient"),
						session.DeleteValue(conformanceTestValue("deleted")),
						session.InsertEntry(conformanceCustomEntry("transient", strPtr("taken"), "note", map[string]any{"id": "transient"})),
						session.InsertEntry(conformanceCustomEntry("taken", nil, "note", map[string]any{"id": "taken"})),
					}, conformanceCtx)
					return err
				}()
				expectError(t, "value transaction", err)

				overwrittenAfter, _ := storage.GetValue(conformanceTestValue("overwritten"), conformanceCtx)
				deletedAfter, _ := storage.GetValue(conformanceTestValue("deleted"), conformanceCtx)
				assertJSONEqual(t, "overwritten", overwrittenAfter, overwrittenBefore)
				assertJSONEqual(t, "deleted", deletedAfter, deletedBefore)
				entries, _ := storage.GetEntries([]string{"transient"}, conformanceCtx)
				if len(entries) != 0 {
					t.Fatal("transient entry must roll back")
				}
			},
		},
		{
			Group: "transactions", Name: "enforces one shared entry and usage id namespace",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				if _, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("existing-entry", nil, "existing-entry")),
					session.InsertUsage(session.UsageRow{ID: "existing-usage", Usage: conformanceUsage(1, 1, nil), Adjustment: false}),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				_, err := storage.Commit([]session.Write{
					session.InsertUsage(session.UsageRow{ID: "existing-entry", Usage: conformanceUsage(2, 2, nil), Adjustment: false}),
				}, conformanceCtx)
				expectError(t, "usage id dup", err)
				_, err = storage.Commit([]session.Write{
					session.InsertEntry(conformanceCustomEntry("existing-usage", nil, "note", map[string]any{"id": "existing-usage"})),
				}, conformanceCtx)
				expectError(t, "entry id dup", err)

				for _, tc := range []struct {
					id     string
					writes []session.Write
				}{
					{"entry-then-usage", []session.Write{
						session.InsertEntry(conformanceCustomEntry("entry-then-usage", nil, "note", map[string]any{"id": "entry-then-usage"})),
						session.InsertUsage(session.UsageRow{ID: "entry-then-usage", Usage: conformanceUsage(3, 3, nil), Adjustment: false}),
					}},
					{"usage-then-entry", []session.Write{
						session.InsertUsage(session.UsageRow{ID: "usage-then-entry", Usage: conformanceUsage(4, 4, nil), Adjustment: false}),
						session.InsertEntry(conformanceCustomEntry("usage-then-entry", nil, "note", map[string]any{"id": "usage-then-entry"})),
					}},
				} {
					_, err := storage.Commit(tc.writes, conformanceCtx)
					expectError(t, "duplicate id "+tc.id, err)
				}

				entries, _ := storage.ScanEntries(session.EntryScan{Order: "asc"}, conformanceCtx)
				assertJSONEqual(t, "entries", conformanceIDs(t, entries), []string{"existing-entry"})
				usage, _ := storage.ScanUsage(session.UsageScan{Order: "asc"}, conformanceCtx)
				ids := []string{}
				for _, row := range usage {
					ids = append(ids, row.ID)
				}
				assertJSONEqual(t, "usage ids", ids, []string{"existing-usage"})
			},
		},
		{
			Group: "transactions", Name: "resolves parents only from prior entries and earlier writes",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				if _, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("root", nil, "root")),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				if _, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceCustomEntry("child", strPtr("root"), "note", map[string]any{"id": "child"})),
					session.InsertEntry(conformanceCustomEntry("grandchild", strPtr("child"), "note", map[string]any{"id": "grandchild"})),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				branch, _ := storage.ScanBranch(session.StorageBranchScan{Start: "grandchild", BranchScan: session.BranchScan{Order: "oldestFirst"}}, conformanceCtx)
				assertJSONEqual(t, "branch", conformanceIDs(t, branch), []string{"root", "child", "grandchild"})

				err := func() error {
					_, err := storage.Commit([]session.Write{
						session.InsertEntry(conformanceCustomEntry("before-parent", strPtr("later-parent"), "note", map[string]any{"id": "before-parent"})),
						session.InsertEntry(conformanceCustomEntry("later-parent", strPtr("root"), "note", map[string]any{"id": "later-parent"})),
						session.SetValue(session.EntryLabel("before-parent"), "transient"),
					}, conformanceCtx)
					return err
				}()
				expectError(t, "forward parent", err)
				err = func() error {
					_, err := storage.Commit([]session.Write{
						session.InsertEntry(conformanceCustomEntry("orphan", strPtr("missing"), "note", map[string]any{"id": "orphan"})),
					}, conformanceCtx)
					return err
				}()
				expectError(t, "missing parent", err)
				if _, err := storage.Commit([]session.Write{
					session.InsertUsage(session.UsageRow{ID: "usage-is-not-parent", Usage: conformanceUsage(1, 1, nil), Adjustment: false}),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				err = func() error {
					_, err := storage.Commit([]session.Write{
						session.InsertEntry(conformanceCustomEntry("usage-child", strPtr("usage-is-not-parent"), "note", map[string]any{"id": "usage-child"})),
					}, conformanceCtx)
					return err
				}()
				expectError(t, "usage as parent", err)

				entries, _ := storage.GetEntries([]string{"before-parent", "later-parent", "orphan", "usage-child"}, conformanceCtx)
				if len(entries) != 0 {
					t.Fatalf("unexpected entries: %v", entries)
				}
				stored, _ := storage.GetValue(session.EntryLabel("before-parent"), conformanceCtx)
				if stored != nil {
					t.Fatal("transient label must roll back")
				}
			},
		},
		{
			Group: "transactions", Name: "places pending content under its reserved entry id",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				entry := conformanceUserEntry("reserved", nil, "queued")
				if _, err := storage.Commit([]session.Write{
					session.SetValue(session.PendingEntryAddress(entry.ID), map[string]any{"type": "message", "payload": entry.Message}),
					session.SetValue(session.BranchTip("main"), nil),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				entries, _ := storage.GetEntries([]string{entry.ID}, conformanceCtx)
				if len(entries) != 0 {
					t.Fatal("pending entry must not be placed yet")
				}
				pending, _ := storage.GetValue(session.PendingEntryAddress(entry.ID), conformanceCtx)
				assertJSONEqual(t, "pending", pending.Value, map[string]any{"type": "message", "payload": entry.Message})
				tip, _ := storage.GetValue(session.BranchTip("main"), conformanceCtx)
				if tip.Value != nil {
					t.Fatalf("tip = %v", tip.Value)
				}

				placement, err := storage.Commit([]session.Write{
					session.InsertEntry(entry),
					session.DeleteValue(session.PendingEntryAddress(entry.ID)),
					session.SetValue(session.BranchTip("main"), entry.ID),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				placed, _ := storage.GetEntries([]string{entry.ID}, conformanceCtx)
				wantEntry := conformanceUserEntry("reserved", nil, "queued")
				wantEntry.Base().Seq = placement.Seqs[0]
				wantEntry.Base().Timestamp = placement.Timestamp
				assertJSONEqual(t, "placed", placed, map[string]any{entry.ID: wantEntry})
				pending, _ = storage.GetValue(session.PendingEntryAddress(entry.ID), conformanceCtx)
				if pending != nil {
					t.Fatal("pending value must be deleted")
				}
				tip, _ = storage.GetValue(session.BranchTip("main"), conformanceCtx)
				assertJSONEqual(t, "tip", tip, session.StoredValue{Address: session.BranchTip("main"), Value: entry.ID, Seq: placement.Seqs[2]})
			},
		},
		{
			Group: "values", Name: "sets, replaces, deletes, and recreates values without tombstones",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				first, err := storage.Commit([]session.Write{
					session.SetValue(conformanceTestValue("prefix/b"), 1),
					session.SetValue(conformanceTestValue("prefix/a"), 2),
					session.SetValue(conformanceTestValue("other"), 3),
					session.SetValue(conformanceTestValue("prefix/\ue000"), 4),
					session.SetValue(conformanceTestValue("prefix/\U00010000"), 5),
					session.SetValue(conformanceTestValue("prefix/a"), nil),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				stored, _ := storage.GetValue(conformanceTestValue("prefix/a"), conformanceCtx)
				assertJSONEqual(t, "null value", stored, session.StoredValue{Address: conformanceTestValue("prefix/a"), Value: nil, Seq: first.Seqs[5]})

				second, err := storage.Commit([]session.Write{
					session.DeleteValue(conformanceTestValue("prefix/a")),
					session.DeleteValue(conformanceTestValue("absent")),
					session.SetValue(conformanceTestValue("prefix/a"), "recreated"),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				scanned, _ := storage.ScanValues(conformanceTestValuePrefix("prefix/"), conformanceCtx)
				assertJSONEqual(t, "scan", scanned, []session.StoredValue{
					{Address: conformanceTestValue("prefix/a"), Value: "recreated", Seq: second.Seqs[2]},
					{Address: conformanceTestValue("prefix/b"), Value: 1, Seq: first.Seqs[0]},
					{Address: conformanceTestValue("prefix/\ue000"), Value: 4, Seq: first.Seqs[3]},
					{Address: conformanceTestValue("prefix/\U00010000"), Value: 5, Seq: first.Seqs[4]},
				})
				absent, _ := storage.GetValue(conformanceTestValue("absent"), conformanceCtx)
				if absent != nil {
					t.Fatal("absent value must be nil")
				}
			},
		},
		{
			Group: "values", Name: "applies same-transaction value and list operations in write order",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				keptValue := conformanceTestValue("write-order/kept")
				deletedValue := conformanceTestValue("write-order/deleted")
				keptList := conformanceTestList("write-order/kept")
				deletedList := conformanceTestList("write-order/deleted")
				result, err := storage.Commit([]session.Write{
					session.SetValue(deletedValue, "transient"),
					session.DeleteValue(deletedValue),
					session.SetValue(keptValue, "transient"),
					session.SetValue(keptValue, "kept"),
					session.AppendList(keptList, "transient"),
					session.DeleteList(keptList),
					session.AppendList(keptList, "kept"),
					session.AppendList(deletedList, "transient"),
					session.DeleteList(deletedList),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				deleted, _ := storage.GetValue(deletedValue, conformanceCtx)
				if deleted != nil {
					t.Fatal("deleted value must be gone")
				}
				kept, _ := storage.GetValue(keptValue, conformanceCtx)
				assertJSONEqual(t, "kept", kept, session.StoredValue{Address: keptValue, Value: "kept", Seq: result.Seqs[3]})
				list, _ := storage.ReadList(keptList, nil, conformanceCtx)
				assertJSONEqual(t, "kept list", list, []session.ListElement{{Seq: result.Seqs[6], Value: "kept"}})
				deletedListElements, _ := storage.ReadList(deletedList, nil, conformanceCtx)
				assertJSONEqual(t, "deleted list", deletedListElements, []session.ListElement{})
			},
		},
		{
			Group: "values", Name: "does not change historical stores during value-only commits",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				if _, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("root", nil, "root")),
					session.InsertUsage(session.UsageRow{ID: "historical-usage", Usage: conformanceUsage(2, 3, nil), Adjustment: false}),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				entriesBefore, _ := storage.ScanEntries(session.EntryScan{Order: "asc"}, conformanceCtx)
				usageBefore, _ := storage.ScanUsage(session.UsageScan{Order: "asc"}, conformanceCtx)
				statsBefore, _ := storage.GetStats(conformanceCtx)

				result, err := storage.Commit([]session.Write{
					session.SetValue(testName, "first"),
					session.SetValue(testName, "second"),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				entriesAfter, _ := storage.ScanEntries(session.EntryScan{Order: "asc"}, conformanceCtx)
				usageAfter, _ := storage.ScanUsage(session.UsageScan{Order: "asc"}, conformanceCtx)
				statsAfter, _ := storage.GetStats(conformanceCtx)
				assertJSONEqual(t, "entries", entriesAfter, entriesBefore)
				assertJSONEqual(t, "usage", usageAfter, usageBefore)
				assertJSONEqual(t, "stats", statsAfter, statsBefore)
				stored, _ := storage.GetValue(testName, conformanceCtx)
				assertJSONEqual(t, "value", stored, session.StoredValue{Address: testName, Value: "second", Seq: result.Seqs[1]})
			},
		},
		{
			Group: "lists", Name: "pages appends by global sequence and deletes whole lists",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				address := conformanceTestList("events")
				empty, _ := storage.ReadList(address, nil, conformanceCtx)
				if len(empty) != 0 {
					t.Fatal("expected an empty list")
				}
				result, err := storage.Commit([]session.Write{
					session.AppendList(address, "a"),
					session.SetValue(testName, "gap"),
					session.AppendList(address, "b"),
					session.AppendList(address, "c"),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				all, _ := storage.ReadList(address, nil, conformanceCtx)
				assertJSONEqual(t, "all", all, []session.ListElement{
					{Seq: result.Seqs[0], Value: "a"}, {Seq: result.Seqs[2], Value: "b"}, {Seq: result.Seqs[3], Value: "c"},
				})
				limit2, _ := storage.ReadList(address, &session.ListReadOptions{Limit: intPtr(2)}, conformanceCtx)
				assertJSONEqual(t, "limit 2", limit2, []session.ListElement{
					{Seq: result.Seqs[0], Value: "a"}, {Seq: result.Seqs[2], Value: "b"},
				})
				cursorAsc, _ := storage.ReadList(address, &session.ListReadOptions{Cursor: &session.ListCursor{Seq: result.Seqs[0]}, Limit: intPtr(2)}, conformanceCtx)
				assertJSONEqual(t, "cursor asc", cursorAsc, []session.ListElement{
					{Seq: result.Seqs[2], Value: "b"}, {Seq: result.Seqs[3], Value: "c"},
				})
				desc, _ := storage.ReadList(address, &session.ListReadOptions{Order: "desc", Limit: intPtr(2)}, conformanceCtx)
				assertJSONEqual(t, "desc", desc, []session.ListElement{
					{Seq: result.Seqs[3], Value: "c"}, {Seq: result.Seqs[2], Value: "b"},
				})
				descCursor, _ := storage.ReadList(address, &session.ListReadOptions{Order: "desc", Cursor: &session.ListCursor{Seq: result.Seqs[3]}, Limit: intPtr(2)}, conformanceCtx)
				assertJSONEqual(t, "desc cursor", descCursor, []session.ListElement{
					{Seq: result.Seqs[2], Value: "b"}, {Seq: result.Seqs[0], Value: "a"},
				})
				_, readErr := storage.ReadList(address, &session.ListReadOptions{Limit: intPtr(0)}, conformanceCtx)
				expectError(t, "limit 0", readErr)
				huge := int(^uint(0) >> 1)
				_, readErr = storage.ReadList(address, &session.ListReadOptions{Limit: &huge}, conformanceCtx)
				expectError(t, "limit max", readErr)

				if _, err := storage.Commit([]session.Write{
					session.DeleteList(address),
					session.DeleteList(conformanceTestList("absent")),
					session.AppendList(address, "new"),
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				after, _ := storage.ReadList(address, nil, conformanceCtx)
				values := []any{}
				for _, element := range after {
					values = append(values, element.Value)
				}
				assertJSONEqual(t, "after delete", values, []any{"new"})
			},
		},
		{
			Group: "lists", Name: "clamps one read page without limiting list growth",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				address := conformanceTestList("large")
				writes := make([]session.Write, 0, 10_001)
				for i := 0; i < 10_001; i++ {
					writes = append(writes, session.AppendList(address, i))
				}
				if _, err := storage.Commit(writes, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				firstPage, err := storage.ReadList(address, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if len(firstPage) != 1_000 {
					t.Fatalf("first page = %d", len(firstPage))
				}
				big, _ := storage.ReadList(address, &session.ListReadOptions{Limit: intPtr(20_000)}, conformanceCtx)
				if len(big) != 10_000 {
					t.Fatalf("big page = %d", len(big))
				}
				afterCursor, _ := storage.ReadList(address, &session.ListReadOptions{Cursor: &session.ListCursor{Seq: firstPage[len(firstPage)-1].Seq}}, conformanceCtx)
				if len(afterCursor) != 1_000 {
					t.Fatalf("after cursor = %d", len(afterCursor))
				}
			},
		},
		{
			Group: "lists", Name: "commits mixed list writes atomically and rolls them back with siblings",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				address := conformanceTestList("atomic")
				committed, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("mixed", nil, "mixed")),
					session.AppendList(address, "kept"),
					session.SetValue(testName, "kept"),
					session.InsertUsage(session.UsageRow{ID: "mixed-usage", Usage: conformanceUsage(1, 2, nil), Adjustment: false}),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				list, _ := storage.ReadList(address, nil, conformanceCtx)
				assertJSONEqual(t, "committed list", list, []session.ListElement{{Seq: committed.Seqs[1], Value: "kept"}})

				expectError(t, "mixed rollback", func() error {
					_, err := storage.Commit([]session.Write{
						session.AppendList(address, "transient"),
						session.DeleteValue(testName),
						session.InsertEntry(conformanceUserEntry("mixed", nil, "mixed")),
					}, conformanceCtx)
					return err
				})
				list, _ = storage.ReadList(address, nil, conformanceCtx)
				assertJSONEqual(t, "rolled-back list", list, []session.ListElement{{Seq: committed.Seqs[1], Value: "kept"}})
				stored, _ := storage.GetValue(testName, conformanceCtx)
				if stored == nil || stored.Value != "kept" {
					t.Fatal("kept value must survive")
				}
			},
		},
		{
			Group: "entry queries", Name: "stores custom entries with and without data",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				result, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceCustomEntry("without-data", nil, "marker", nil)),
					session.InsertEntry(conformanceCustomEntry("with-data", strPtr("without-data"), "note", map[string]any{"nested": []any{1, 2}})),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				entries, err := storage.GetEntries([]string{"without-data", "with-data"}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				withoutData := conformanceCustomEntry("without-data", nil, "marker", nil)
				withoutData.Base().Seq = result.Seqs[0]
				withoutData.Base().Timestamp = result.Timestamp
				withData := conformanceCustomEntry("with-data", strPtr("without-data"), "note", map[string]any{"nested": []any{1, 2}})
				withData.Base().Seq = result.Seqs[1]
				withData.Base().Timestamp = result.Timestamp
				assertJSONEqual(t, "entries", entries, map[string]any{
					"without-data": withoutData,
					"with-data":    withData,
				})
			},
		},
		{
			Group: "entry queries", Name: "scans global entries with explicit ranges, filters, orders, and limits",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				result, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("root", nil, "root")),
					session.InsertEntry(conformanceCustomEntry("note-1", strPtr("root"), "note", map[string]any{"id": "note-1"})),
					session.InsertEntry(conformanceCustomEntry("other", strPtr("note-1"), "other", map[string]any{"id": "other"})),
					session.InsertEntry(conformanceCustomEntry("note-2", strPtr("other"), "note", map[string]any{"id": "note-2"})),
					session.InsertEntry(conformanceUserEntry("tail", strPtr("note-2"), "tail")),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				customType := session.EntryType("custom")
				entries, _ := storage.ScanEntries(session.EntryScan{
					Type: &customType, CustomType: strPtr("note"),
					FromSeq: int64Ptr(result.Seqs[1]), ToSeq: int64Ptr(result.Seqs[3]), Order: "desc",
				}, conformanceCtx)
				assertJSONEqual(t, "filtered", conformanceIDs(t, entries), []string{"note-2", "note-1"})
				ascLimited, _ := storage.ScanEntries(session.EntryScan{Order: "asc", Limit: intPtr(2)}, conformanceCtx)
				assertJSONEqual(t, "asc limit", conformanceIDs(t, ascLimited), []string{"root", "note-1"})
				descLimited, _ := storage.ScanEntries(session.EntryScan{Order: "desc", Limit: intPtr(2)}, conformanceCtx)
				assertJSONEqual(t, "desc limit", conformanceIDs(t, descLimited), []string{"tail", "note-2"})
			},
		},
		{
			Group: "branch queries", Name: "applies stops before filters and cursors before limits",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				result, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("root", nil, "root")),
					session.InsertEntry(conformanceCustomEntry("marker", strPtr("root"), "marker", map[string]any{"id": "marker"})),
					session.InsertEntry(conformanceUserEntry("middle", strPtr("marker"), "middle")),
					session.InsertEntry(conformanceCompactionEntry("compact", strPtr("middle"))),
					session.InsertEntry(conformanceCustomEntry("note", strPtr("compact"), "note", map[string]any{"id": "note"})),
					session.InsertEntry(conformanceUserEntry("leaf", strPtr("note"), "leaf")),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				messageType := session.EntryTypeMessage
				compactionType := session.EntryTypeCompaction
				customType := session.EntryTypeCustom

				branch, _ := storage.ScanBranch(session.StorageBranchScan{Start: "leaf", BranchScan: session.BranchScan{StopAtType: &compactionType, Type: &messageType}}, conformanceCtx)
				assertJSONEqual(t, "stop+filter", conformanceIDs(t, branch), []string{"leaf"})
				branch, _ = storage.ScanBranch(session.StorageBranchScan{Start: "leaf", BranchScan: session.BranchScan{Order: "oldestFirst", StopAtID: strPtr("middle"), Type: &customType}}, conformanceCtx)
				assertJSONEqual(t, "oldestFirst stop+filter", conformanceIDs(t, branch), []string{"marker"})
				branch, _ = storage.ScanBranch(session.StorageBranchScan{Start: "leaf", BranchScan: session.BranchScan{Order: "newestFirst", Cursor: &session.EntryCursor{Seq: result.Seqs[4]}, Limit: intPtr(2)}}, conformanceCtx)
				assertJSONEqual(t, "newestFirst cursor", conformanceIDs(t, branch), []string{"compact", "middle"})
				branch, _ = storage.ScanBranch(session.StorageBranchScan{Start: "leaf", BranchScan: session.BranchScan{Order: "oldestFirst", Cursor: &session.EntryCursor{Seq: result.Seqs[1]}, Limit: intPtr(2)}}, conformanceCtx)
				assertJSONEqual(t, "oldestFirst cursor", conformanceIDs(t, branch), []string{"middle", "compact"})
				branch, _ = storage.ScanBranch(session.StorageBranchScan{Start: "leaf", BranchScan: session.BranchScan{StopAtID: strPtr("leaf"), Type: &customType}}, conformanceCtx)
				assertJSONEqual(t, "stop self", conformanceIDs(t, branch), []string{})
				branch, _ = storage.ScanBranch(session.StorageBranchScan{Start: "leaf", BranchScan: session.BranchScan{CustomType: strPtr("note")}}, conformanceCtx)
				assertJSONEqual(t, "customType filter", conformanceIDs(t, branch), []string{"note"})
				_, err = storage.ScanBranch(session.StorageBranchScan{Start: "missing"}, conformanceCtx)
				expectError(t, "unknown start", err)
			},
		},
		{
			Group: "branch queries", Name: "returns branch structure without payload fields",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				result, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("root", nil, "root")),
					session.InsertEntry(conformanceCustomEntry("child", strPtr("root"), "note", map[string]any{"id": "child"})),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				structure, err := storage.ScanBranchStructure(session.StorageBranchScan{Start: "child", BranchScan: session.BranchScan{Order: "oldestFirst"}}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				assertJSONEqual(t, "structure", structure, []session.EntryStructure{
					{ID: "root", Seq: result.Seqs[0], Timestamp: result.Timestamp, Type: session.EntryTypeMessage},
					{ID: "child", ParentID: strPtr("root"), Seq: result.Seqs[1], Timestamp: result.Timestamp, Type: session.EntryTypeCustom, CustomType: strPtr("note")},
				})
			},
		},
		{
			Group: "branch queries", Name: "applies branch query semantics to structure scans",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				result, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("root", nil, "root")),
					session.InsertEntry(conformanceCustomEntry("marker", strPtr("root"), "marker", map[string]any{"id": "marker"})),
					session.InsertEntry(conformanceUserEntry("middle", strPtr("marker"), "middle")),
					session.InsertEntry(conformanceCompactionEntry("compact", strPtr("middle"))),
					session.InsertEntry(conformanceCustomEntry("note", strPtr("compact"), "note", map[string]any{"id": "note"})),
					session.InsertEntry(conformanceUserEntry("leaf", strPtr("note"), "leaf")),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				messageType := session.EntryTypeMessage
				compactionType := session.EntryTypeCompaction
				structure, _ := storage.ScanBranchStructure(session.StorageBranchScan{Start: "leaf", BranchScan: session.BranchScan{StopAtType: &compactionType, Type: &messageType}}, conformanceCtx)
				assertJSONEqual(t, "stop+filter", conformanceIDs(t, structure), []string{"leaf"})
				structure, _ = storage.ScanBranchStructure(session.StorageBranchScan{Start: "leaf", BranchScan: session.BranchScan{Order: "oldestFirst", Cursor: &session.EntryCursor{Seq: result.Seqs[1]}, Limit: intPtr(2)}}, conformanceCtx)
				assertJSONEqual(t, "oldestFirst cursor", conformanceIDs(t, structure), []string{"middle", "compact"})
				_, err = storage.ScanBranchStructure(session.StorageBranchScan{Start: "missing"}, conformanceCtx)
				expectError(t, "unknown start", err)
			},
		},
		{
			Group: "usage and stats", Name: "scans the usage ledger with explicit ranges, orders, and limits",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				result, err := storage.Commit([]session.Write{
					session.InsertUsage(session.UsageRow{ID: "usage-1", Usage: conformanceUsage(1, 1, nil), Adjustment: false}),
					session.SetValue(testName, "sequence gap"),
					session.InsertUsage(session.UsageRow{ID: "usage-2", Usage: conformanceUsage(2, 2, nil), Adjustment: false}),
					session.InsertUsage(session.UsageRow{ID: "usage-3", Usage: conformanceUsage(3, 3, nil), Adjustment: true}),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				ranged, _ := storage.ScanUsage(session.UsageScan{FromSeq: int64Ptr(result.Seqs[1]), ToSeq: int64Ptr(result.Seqs[2]), Order: "asc"}, conformanceCtx)
				assertJSONEqual(t, "ranged", usageIDs(ranged), []string{"usage-2"})
				desc, _ := storage.ScanUsage(session.UsageScan{Order: "desc", Limit: intPtr(2)}, conformanceCtx)
				assertJSONEqual(t, "desc", usageIDs(desc), []string{"usage-3", "usage-2"})
				asc, _ := storage.ScanUsage(session.UsageScan{Order: "asc", Limit: intPtr(2)}, conformanceCtx)
				assertJSONEqual(t, "asc", usageIDs(asc), []string{"usage-1", "usage-2"})
			},
		},
		{
			Group: "usage and stats", Name: "keeps stats equal to message count and ledger totals",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				stats, _ := storage.GetStats(conformanceCtx)
				assertJSONEqual(t, "empty stats", stats, session.SessionStats{MessageCount: 0, Usage: conformanceZeroUsage()})

				firstUsage := conformanceUsage(2, 3, map[string]int64{"cacheWrite1h": 4, "reasoning": 1})
				first, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceUserEntry("message", nil, "message")),
					session.InsertUsage(session.UsageRow{ID: "usage-1", Usage: firstUsage, Adjustment: false}),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				assertCommitStats(t, storage, first)
				assertJSONEqual(t, "first stats", first.Stats, session.SessionStats{MessageCount: 1, Usage: firstUsage})

				secondUsage := conformanceUsage(5, 7, map[string]int64{"cacheWrite1h": 6, "reasoning": 2})
				second, err := storage.Commit([]session.Write{
					session.InsertEntry(conformanceCustomEntry("custom", strPtr("message"), "note", map[string]any{"id": "custom"})),
					session.InsertEntry(conformanceCompactionEntry("compaction", strPtr("custom"))),
					session.InsertUsage(session.UsageRow{ID: "usage-2", Usage: secondUsage, Adjustment: true}),
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				assertCommitStats(t, storage, second)
				totalUsage := conformanceAddUsage(firstUsage, secondUsage)
				assertJSONEqual(t, "second stats", second.Stats, session.SessionStats{MessageCount: 1, Usage: totalUsage})
			},
		},
		{
			Group: "serialization", Name: "serializes back-to-back commits in admission order",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				type commitOut struct {
					result session.CommitResult
					err    error
				}
				firstCh := make(chan commitOut, 1)
				secondCh := make(chan commitOut, 1)
				go func() {
					result, err := storage.Commit([]session.Write{session.InsertEntry(conformanceUserEntry("first", nil, "first"))}, conformanceCtx)
					firstCh <- commitOut{result, err}
				}()
				// pi enqueues the second commit after the first on the same
				// tick; Go needs a yield to preserve admission order.
				time.Sleep(20 * time.Millisecond)
				go func() {
					result, err := storage.Commit([]session.Write{session.InsertEntry(conformanceUserEntry("second", strPtr("first"), "second"))}, conformanceCtx)
					secondCh <- commitOut{result, err}
				}()
				first := <-firstCh
				second := <-secondCh
				if first.err != nil || second.err != nil {
					t.Fatalf("commit errors: %v, %v", first.err, second.err)
				}
				if first.result.Seqs[0] >= second.result.Seqs[0] {
					t.Fatalf("commit order violated: %d !< %d", first.result.Seqs[0], second.result.Seqs[0])
				}
				assertJSONEqual(t, "first stats", first.result.Stats, session.SessionStats{MessageCount: 1, Usage: conformanceZeroUsage()})
				assertJSONEqual(t, "second stats", second.result.Stats, session.SessionStats{MessageCount: 2, Usage: conformanceZeroUsage()})
				assertCommitStats(t, storage, second.result)
				entries, _ := storage.ScanEntries(session.EntryScan{Order: "asc"}, conformanceCtx)
				assertJSONEqual(t, "entries", conformanceIDs(t, entries), []string{"first", "second"})
			},
		},
		{
			Group: "lifecycle", Name: "seals admission, drains admitted commits, and closes idempotently",
			Run: func(t *testing.T) {
				storage := newStorage(t)
				type commitOut struct {
					result session.CommitResult
					err    error
				}
				admittedCh := make(chan commitOut, 1)
				go func() {
					result, err := storage.Commit([]session.Write{session.InsertEntry(conformanceUserEntry("admitted", nil, "admitted"))}, conformanceCtx)
					admittedCh <- commitOut{result, err}
				}()
				time.Sleep(20 * time.Millisecond)
				// pi: close() flips the state synchronously before waiting on
				// the commit queue; pig's Close drains the gate synchronously.
				closeDone := make(chan error, 2)
				go func() { closeDone <- storage.Close(conformanceCtx) }()
				time.Sleep(20 * time.Millisecond)

				_, err := storage.GetStats(conformanceCtx)
				expectError(t, "stats after close", err)
				_, err = storage.Commit(nil, conformanceCtx)
				expectError(t, "commit after close", err)
				admitted := <-admittedCh
				if admitted.err != nil {
					t.Fatal(admitted.err)
				}
				if len(admitted.result.Seqs) != 1 {
					t.Fatalf("admitted seqs = %v", admitted.result.Seqs)
				}
				if err := <-closeDone; err != nil {
					t.Fatal(err)
				}
				if err := storage.Close(conformanceCtx); err != nil {
					t.Fatal(err)
				}

				expectError(t, "getEntries", func() error { _, err := storage.GetEntries(nil, conformanceCtx); return err })
				expectError(t, "getValue", func() error { _, err := storage.GetValue(testName, conformanceCtx); return err })
				expectError(t, "scanValues", func() error { _, err := storage.ScanValues(testName, conformanceCtx); return err })
				expectError(t, "readList", func() error {
					_, err := storage.ReadList(conformanceTestList("events"), nil, conformanceCtx)
					return err
				})
				expectError(t, "scanBranch", func() error {
					_, err := storage.ScanBranch(session.StorageBranchScan{Start: "admitted"}, conformanceCtx)
					return err
				})
				expectError(t, "scanBranchStructure", func() error {
					_, err := storage.ScanBranchStructure(session.StorageBranchScan{Start: "admitted"}, conformanceCtx)
					return err
				})
				expectError(t, "scanEntries", func() error {
					_, err := storage.ScanEntries(session.EntryScan{Order: "asc"}, conformanceCtx)
					return err
				})
				expectError(t, "scanUsage", func() error { _, err := storage.ScanUsage(session.UsageScan{Order: "asc"}, conformanceCtx); return err })
				expectError(t, "getStats", func() error { _, err := storage.GetStats(conformanceCtx); return err })
			},
		},
	}
}

func intPtr(v int) *int { return &v }

func int64Ptr(v int64) *int64 { return &v }

func strPtr(s string) *string { return &s }

func usageIDs(rows []session.UsageRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

const conformanceMessageTimestamp = int64(1_650_000_000_000)

var testName = session.MustValue("test.session.name", "")

func conformanceTestValue(key string) session.Value {
	return session.MustValue("test.value", key)
}

func conformanceTestValuePrefix(prefix string) session.Value {
	return session.MustValue("test.value", prefix)
}

func conformanceTestList(key string) session.ValueList {
	return session.MustList("test.list", key)
}

func conformanceUsage(input, output int64, options map[string]int64) types.Usage {
	u := types.Usage{
		Input:       input,
		Output:      output,
		CacheRead:   input + 1,
		CacheWrite:  output + 1,
		TotalTokens: input + output,
		Cost: types.Cost{
			Input:      float64(input) / 100,
			Output:     float64(output) / 100,
			CacheRead:  float64(input+1) / 100,
			CacheWrite: float64(output+1) / 100,
			Total:      float64(input+output+2) / 100,
		},
	}
	if v, ok := options["cacheWrite1h"]; ok {
		u.CacheWrite1h = &v
	}
	if v, ok := options["reasoning"]; ok {
		u.Reasoning = &v
	}
	return u
}

func conformanceZeroUsage() types.Usage { return types.Usage{} }

func conformanceUserEntry(id string, parentID *string, text string) *session.MessageEntry {
	entry := &session.MessageEntry{
		Message: types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
			Timestamp: conformanceMessageTimestamp,
		},
	}
	entry.ID = id
	entry.ParentID = parentID
	entry.Type = session.EntryTypeMessage
	return entry
}

func conformanceCustomEntry(id string, parentID *string, customType string, data any) *session.CustomEntry {
	entry := &session.CustomEntry{Data: data}
	entry.ID = id
	entry.ParentID = parentID
	entry.Type = session.EntryTypeCustom
	entry.CustomType = &customType
	return entry
}

func conformanceCompactionEntry(id string, parentID *string) *session.CompactionEntry {
	summary := "summary:" + id
	entry := &session.CompactionEntry{
		Summary:      summary,
		RetainedTail: []agent.AgentMessage{},
		TokensBefore: 10,
	}
	entry.ID = id
	entry.ParentID = parentID
	entry.Type = session.EntryTypeCompaction
	return entry
}

func conformanceIDs(t *testing.T, entries any) []string {
	t.Helper()
	var out []string
	switch v := entries.(type) {
	case []session.Entry:
		for _, entry := range v {
			out = append(out, entry.Base().ID)
		}
	case []session.EntryStructure:
		for _, entry := range v {
			out = append(out, entry.ID)
		}
	default:
		t.Fatalf("unsupported ids input %T", entries)
	}
	return out
}

// assertJSONEqual compares two values via JSON normalization (pi:
// deepStrictEqual on JSON-shaped data).
func AssertJSONEqual(t *testing.T, name string, got, want any) {
	t.Helper()
	assertJSONEqual(t, name, got, want)
}

func assertJSONEqual(t *testing.T, name string, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal got: %v", name, err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("%s: marshal want: %v", name, err)
	}
	var gotAny, wantAny any
	if err := json.Unmarshal(gotJSON, &gotAny); err != nil {
		t.Fatalf("%s: unmarshal got: %v", name, err)
	}
	if err := json.Unmarshal(wantJSON, &wantAny); err != nil {
		t.Fatalf("%s: unmarshal want: %v", name, err)
	}
	// A nil Go slice marshals to null; treat null as [] when the other side
	// is an empty array.
	if gotAny == nil {
		if arr, ok := wantAny.([]any); ok && len(arr) == 0 {
			gotAny = []any{}
		}
	}
	if wantAny == nil {
		if arr, ok := gotAny.([]any); ok && len(arr) == 0 {
			wantAny = []any{}
		}
	}
	if !reflect.DeepEqual(gotAny, wantAny) {
		t.Fatalf("%s:\n  got  %s\n  want %s", name, gotJSON, wantJSON)
	}
}

func assertStrictlyIncreasing(t *testing.T, name string, values []int64) {
	t.Helper()
	for i := 1; i < len(values); i++ {
		if values[i-1] >= values[i] {
			t.Fatalf("%s not strictly increasing: %v", name, values)
		}
	}
}

func assertCommitStats(t *testing.T, storage session.Storage, result session.CommitResult) {
	t.Helper()
	stats, err := storage.GetStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, "commit stats", result.Stats, stats)
}

func expectError(t *testing.T, name string, errOrFn any) {
	t.Helper()
	switch v := errOrFn.(type) {
	case error:
		if v == nil {
			t.Fatalf("%s: expected an error, got nil", name)
		}
	case func() error:
		if err := v(); err == nil {
			t.Fatalf("%s: expected an error, got nil", name)
		}
	default:
		t.Fatalf("%s: unsupported expectError input %T", name, errOrFn)
	}
}

// conformanceAddUsage re-exports the harness usage aggregation for tests.
func conformanceAddUsage(left, right types.Usage) types.Usage { return utils.AddUsage(left, right) }
