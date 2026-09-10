// pi: src/modes/interactive/model-catalog-refresh.ts — share concurrent
// interactive all-catalog refreshes while keeping each caller's cancellation
// independent.
//
// Segment deviations (declared): pi keys the active refresh in a WeakMap and
// races every waiter against its own signal; pig keys on the runtime (a
// comparable interface value here — the registry facade pointer) and uses a
// per-refresh context. A cancelled waiter returns immediately; the shared
// operation keeps running until the last waiter leaves.
package interactive

import (
	"context"
	"sync"

	"github.com/ZoroSpace/pig/ai"
)

// ModelCatalogRuntime is the refresh surface of the runtime (pi:
// ModelCatalogRuntime = Pick<ModelRuntime, "refresh">).
type ModelCatalogRuntime interface {
	Refresh(ctx context.Context, options *ai.ModelsRefreshOptions) ai.ModelsRefreshResult
}

// activeModelCatalogRefresh is one shared in-flight refresh (pi:
// ActiveModelCatalogRefresh). The result is stored before done is closed, so
// every waiter observes it (pi: a promise resolved once fans out to all).
type activeModelCatalogRefresh struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	result  ai.ModelsRefreshResult
	waiters int
}

// modelCatalogRefreshCoordinator shares refreshes per runtime (pi:
// ModelCatalogRefreshCoordinator). pi uses a WeakMap; pig relies on explicit
// lifetime via the waiter count.
type catalogRefreshCoordinator struct {
	mu     sync.Mutex
	active map[ModelCatalogRuntime]*activeModelCatalogRefresh
}

func newCatalogRefreshCoordinator() *catalogRefreshCoordinator {
	return &catalogRefreshCoordinator{active: map[ModelCatalogRuntime]*activeModelCatalogRefresh{}}
}

func (c *catalogRefreshCoordinator) refresh(ctx context.Context, modelRuntime ModelCatalogRuntime) (ai.ModelsRefreshResult, error) {
	if err := ctx.Err(); err != nil {
		return ai.ModelsRefreshResult{}, err
	}
	c.mu.Lock()
	active := c.active[modelRuntime]
	if active == nil {
		refreshCtx, cancel := context.WithCancel(context.Background())
		active = &activeModelCatalogRefresh{ctx: refreshCtx, cancel: cancel, done: make(chan struct{})}
		c.active[modelRuntime] = active
		go func() {
			result := modelRuntime.Refresh(refreshCtx, nil)
			active.result = result
			close(active.done)
		}()
	}
	active.waiters++
	c.mu.Unlock()

	select {
	case <-active.done:
		c.mu.Lock()
		result := active.result
		active.waiters--
		if active.waiters == 0 && c.active[modelRuntime] == active {
			delete(c.active, modelRuntime)
		}
		c.mu.Unlock()
		return result, nil
	case <-ctx.Done():
		// pi: raceWithAbortSignal — the waiter rejects while the shared
		// operation keeps running for the remaining waiters.
		c.mu.Lock()
		active.waiters--
		if active.waiters == 0 && c.active[modelRuntime] == active {
			delete(c.active, modelRuntime)
			active.cancel()
		}
		c.mu.Unlock()
		return ai.ModelsRefreshResult{}, ctx.Err()
	}
}

var modelCatalogRefreshCoordinator = newCatalogRefreshCoordinator()

// RefreshModelCatalogs shares concurrent interactive all-catalog refreshes
// while keeping each caller's cancellation independent (pi:
// refreshModelCatalogs).
func RefreshModelCatalogs(ctx context.Context, modelRuntime ModelCatalogRuntime) (ai.ModelsRefreshResult, error) {
	return modelCatalogRefreshCoordinator.refresh(ctx, modelRuntime)
}
