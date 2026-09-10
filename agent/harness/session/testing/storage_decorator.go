// pi: harness/session/testing/storage-decorator.ts — test-only forwarding
// base for decorators that alter one part of Storage behavior.
package testing

import (
	"context"

	"github.com/ZoroSpace/pig/agent/harness/session"
)

// StorageDecorator forwards every Storage call to its delegate (pi:
// StorageDecorator).
type StorageDecorator struct {
	Delegate session.Storage
}

func (d *StorageDecorator) Commit(writes []session.Write, ctx context.Context) (session.CommitResult, error) {
	return d.Delegate.Commit(writes, ctx)
}

func (d *StorageDecorator) GetEntries(ids []string, ctx context.Context) (map[string]session.Entry, error) {
	return d.Delegate.GetEntries(ids, ctx)
}

func (d *StorageDecorator) GetValue(address session.Value, ctx context.Context) (*session.StoredValue, error) {
	return d.Delegate.GetValue(address, ctx)
}

func (d *StorageDecorator) ScanValues(prefix session.Value, ctx context.Context) ([]session.StoredValue, error) {
	return d.Delegate.ScanValues(prefix, ctx)
}

func (d *StorageDecorator) ReadList(address session.ValueList, options *session.ListReadOptions, ctx context.Context) ([]session.ListElement, error) {
	return d.Delegate.ReadList(address, options, ctx)
}

func (d *StorageDecorator) ScanBranch(query session.StorageBranchScan, ctx context.Context) ([]session.Entry, error) {
	return d.Delegate.ScanBranch(query, ctx)
}

func (d *StorageDecorator) ScanBranchStructure(query session.StorageBranchScan, ctx context.Context) ([]session.EntryStructure, error) {
	return d.Delegate.ScanBranchStructure(query, ctx)
}

func (d *StorageDecorator) ScanEntries(query session.EntryScan, ctx context.Context) ([]session.Entry, error) {
	return d.Delegate.ScanEntries(query, ctx)
}

func (d *StorageDecorator) ScanUsage(query session.UsageScan, ctx context.Context) ([]session.UsageRow, error) {
	return d.Delegate.ScanUsage(query, ctx)
}

func (d *StorageDecorator) GetStats(ctx context.Context) (session.SessionStats, error) {
	return d.Delegate.GetStats(ctx)
}

func (d *StorageDecorator) Close(ctx context.Context) error {
	return d.Delegate.Close(ctx)
}
