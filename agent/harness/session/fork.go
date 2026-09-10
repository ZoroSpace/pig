// pi: harness/session/fork.ts — fork snapshot selection and reconstruction.
package session

import (
	"fmt"
	"sort"
)

// ForkSourceSnapshot is the state captured from a source session (pi:
// ForkSourceSnapshot).
type ForkSourceSnapshot struct {
	Entries      []Entry
	ScalarValues []StoredValue
	// EntriesComplete is false when a backend supplied only the requested
	// branch rather than the full tree. Pi's optional-true semantics: nil
	// means complete.
	EntriesComplete *bool
}

// ForkDestinationSnapshot is the complete logical state for a forked
// destination session (pi: ForkDestinationSnapshot).
type ForkDestinationSnapshot struct {
	Entries      map[string]Entry
	ScalarValues []StoredValue
	NextSeq      int64
}

func storedValuesInNamespace(values []StoredValue, address Value) []StoredValue {
	var out []StoredValue
	for _, stored := range values {
		if stored.Address.Namespace == address.Namespace {
			out = append(out, stored)
		}
	}
	return out
}

func findStoredValue(values []StoredValue, address Value) *StoredValue {
	for i := range values {
		stored := &values[i]
		if stored.Address.Namespace == address.Namespace && stored.Address.Key == address.Key {
			return stored
		}
	}
	return nil
}

// CreateForkSnapshot builds the complete logical state for a forked
// destination session (pi: createForkSnapshot).
func CreateForkSnapshot(source ForkSourceSnapshot, options ForkOptions) (ForkDestinationSnapshot, error) {
	sourceEntries := map[string]Entry{}
	for _, entry := range source.Entries {
		sourceEntries[entry.Base().ID] = entry
	}
	sourceTips := storedValuesInNamespace(source.ScalarValues, BranchTip(""))
	if err := validateForkSourceSnapshot(source, sourceEntries, sourceTips, options); err != nil {
		return ForkDestinationSnapshot{}, err
	}

	entryIDs, destinationTips, err := selectForkContents(sourceEntries, sourceTips, options)
	if err != nil {
		return ForkDestinationSnapshot{}, err
	}

	snapshot := ForkDestinationSnapshot{Entries: map[string]Entry{}, ScalarValues: []StoredValue{}}
	for _, id := range orderedKeys(sourceEntries) {
		if _, selected := entryIDs[id]; selected {
			snapshot.Entries[id] = sourceEntries[id]
		}
	}
	var nextSeq int64
	for _, entry := range snapshot.Entries {
		if entry.Base().Seq > nextSeq {
			nextSeq = entry.Base().Seq
		}
	}
	nextSeq++
	store := func(address Value, storedValue any) {
		snapshot.ScalarValues = append(snapshot.ScalarValues, StoredValue{
			Address: Value{Namespace: address.Namespace, Key: address.Key, kind: ValueKindValue},
			Value:   storedValue,
			Seq:     nextSeq,
		})
		nextSeq++
	}
	// Reconstruct tips deterministically: TS iterates destinationTips in
	// insertion order (branch scope: the single selected branch; tree scope:
	// sourceTips order). sort by key for determinism across map iteration.
	tipBranches := make([]string, 0, len(destinationTips))
	for branch := range destinationTips {
		tipBranches = append(tipBranches, branch)
	}
	sort.Strings(tipBranches)
	for _, branch := range tipBranches {
		tipID := destinationTips[branch]
		configuration := findStoredValue(source.ScalarValues, LaneConfig(branch))
		store(BranchTip(branch), derefOrNil(tipID))
		if configuration != nil {
			store(LaneConfig(branch), configuration.Value)
			store(LaneStateAddress(branch), LaneState{
				Inbox: []InboxItem{},
			})
		}
	}
	for _, stored := range source.ScalarValues {
		disposition, err := ClassifyForkAddress(stored.Address, options.Scope, func(entryID string) bool {
			_, ok := entryIDs[entryID]
			return ok
		})
		if err != nil {
			return ForkDestinationSnapshot{}, err
		}
		switch disposition {
		case ForkDispositionCopy:
			store(stored.Address, stored.Value)
		case ForkDispositionExclude, ForkDispositionReconstruct:
		}
	}
	snapshot.NextSeq = nextSeq
	return snapshot, nil
}

func orderedKeys(m map[string]Entry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ForkSnapshotWrites converts a destination snapshot into committed writes
// ordered by seq (pi: forkSnapshotWrites).
func ForkSnapshotWrites(snapshot ForkDestinationSnapshot) []CommittedWrite {
	writes := []CommittedWrite{}
	for _, id := range orderedKeys(snapshot.Entries) {
		writes = append(writes, CommittedEntryWrite{Entry: snapshot.Entries[id]})
	}
	for _, stored := range snapshot.ScalarValues {
		writes = append(writes, CommittedValueSetWrite{
			Kind:      "value",
			Op:        "set",
			Seq:       stored.Seq,
			Namespace: stored.Address.Namespace,
			Key:       stored.Address.Key,
			Value:     stored.Value,
		})
	}
	sort.SliceStable(writes, func(i, j int) bool {
		return writes[i].CommittedSeq() < writes[j].CommittedSeq()
	})
	return writes
}

func selectForkContents(
	sourceEntries map[string]Entry,
	sourceTips []StoredValue,
	options ForkOptions,
) (map[string]struct{}, map[string]*string, error) {
	entryIDs := map[string]struct{}{}
	destinationTips := map[string]*string{}
	if options.Scope == ForkScopeTree {
		for id := range sourceEntries {
			entryIDs[id] = struct{}{}
		}
		for _, stored := range sourceTips {
			tip := stored.Value
			var tipPtr *string
			if s, ok := tip.(string); ok {
				tipPtr = &s
			}
			destinationTips[stored.Address.Key] = tipPtr
		}
		return entryIDs, destinationTips, nil
	}

	var sourceTip *StoredValue
	for i := range sourceTips {
		if sourceTips[i].Address.Key == options.Branch {
			sourceTip = &sourceTips[i]
			break
		}
	}
	if sourceTip == nil {
		return nil, nil, fmt.Errorf("Unknown source branch: %s", options.Branch)
	}

	sourceTipID, _ := sourceTip.Value.(string)
	var requested *string
	if options.EntryID != nil {
		requested = options.EntryID
	} else if sourceTipID != "" {
		requested = &sourceTipID
	}
	// found tracks whether the requested entry (or a null request) has been
	// reached on the walk to the root.
	found := requested == nil
	var tipID *string
	entryID := sourceTipID
	for entryID != "" {
		entry, ok := sourceEntries[entryID]
		if !ok {
			return nil, nil, fmt.Errorf("Corrupt source branch: missing parent %s", entryID)
		}
		base := entry.Base()
		if requested != nil && base.ID == *requested {
			found = true
			if options.Position == ForkPositionBefore {
				tipID = base.ParentID
			} else {
				tipID = &base.ID
			}
			if options.Position != ForkPositionBefore {
				entryIDs[base.ID] = struct{}{}
			}
		} else if found {
			entryIDs[base.ID] = struct{}{}
		}
		if base.ParentID == nil {
			break
		}
		entryID = *base.ParentID
	}
	if !found {
		return nil, nil, fmt.Errorf("Fork entry %v is not on source branch %q", requested, options.Branch)
	}
	destinationTips[options.Branch] = tipID
	return entryIDs, destinationTips, nil
}

func validateForkSourceSnapshot(
	source ForkSourceSnapshot,
	sourceEntries map[string]Entry,
	sourceTips []StoredValue,
	options ForkOptions,
) error {
	sourceTipKeys := map[string]struct{}{}
	for _, stored := range sourceTips {
		sourceTipKeys[stored.Address.Key] = struct{}{}
	}

	for _, stored := range source.ScalarValues {
		if stored.Address.Namespace == LaneConfig("").Namespace ||
			stored.Address.Namespace == LaneStateAddress("").Namespace {
			if _, ok := sourceTipKeys[stored.Address.Key]; !ok {
				return fmt.Errorf("Source session branch %q is missing branch.tip", stored.Address.Key)
			}
		}
	}
	for _, tip := range sourceTips {
		configuration := findStoredValue(source.ScalarValues, LaneConfig(tip.Address.Key))
		state := findStoredValue(source.ScalarValues, LaneStateAddress(tip.Address.Key))
		if (configuration == nil) != (state == nil) {
			return fmt.Errorf("Source session branch %q has incomplete lane state", tip.Address.Key)
		}
		if options.Scope == ForkScopeBranch && tip.Address.Key == options.Branch && configuration == nil {
			return fmt.Errorf("Source branch %q is not a configured AgentLane", options.Branch)
		}
		entriesComplete := source.EntriesComplete == nil || *source.EntriesComplete
		if (entriesComplete || options.Scope == ForkScopeTree) && tip.Value != nil {
			tipID, _ := tip.Value.(string)
			if tipID != "" {
				if _, ok := sourceEntries[tipID]; !ok {
					return fmt.Errorf("Source session branch %q has an unknown tip", tip.Address.Key)
				}
			}
		}
	}
	return nil
}

// derefOrNil converts an optional string pointer into the stored value shape
// (string | nil, pi's `string | null`).
func derefOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
