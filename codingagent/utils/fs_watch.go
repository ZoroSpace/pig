// fs_watch.go (pi: src/utils/fs-watch.ts).
//
// Declared deviation: pi wraps node:fs.watch; Go's stdlib has no file
// watcher. pig provides a mtime/size polling watcher with the same
// close-on-error contract. The 5s retry delay constant is kept for the
// footer git watcher (pi: FS_WATCH_RETRY_DELAY_MS).
package utils

import (
	"os"
	"time"
)

// FSWatchRetryDelayMs is pi's FS_WATCH_RETRY_DELAY_MS.
const FSWatchRetryDelayMs = 5000

// PollWatcher is the pig replacement for node's FSWatcher: it polls a
// path's mtime/size and fires onChange on detected changes.
type PollWatcher struct {
	stopCh chan struct{}
	done   chan struct{}
}

// CloseWatcher stops a watcher, ignoring nil and close errors (pi:
// closeWatcher).
func CloseWatcher(watcher *PollWatcher) {
	if watcher == nil {
		return
	}
	watcher.Close()
}

// Close stops the polling loop (pi: watcher.close()).
func (w *PollWatcher) Close() {
	if w == nil {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	close(w.stopCh)
	<-w.done
}

// WatchWithErrorHandler polls path for mtime/size changes and invokes
// onChange; poll-setup errors invoke onError and return nil, matching pi's
// watchWithErrorHandler error contract (pi's per-event errors surface via
// the watcher's 'error' listener → onError; pig has no async poll errors).
// The default poll interval is 500ms (pi: fs.watch coalescing heuristic);
// use WatchWithErrorHandlerWithInterval to override it.
func WatchWithErrorHandler(path string, onChange func(), onError func()) *PollWatcher {
	return WatchWithErrorHandlerWithInterval(path, 500*time.Millisecond, onChange, onError)
}

// WatchWithErrorHandlerWithInterval is WatchWithErrorHandler with an
// explicit poll interval (pig addition; the WSL git-HEAD poller and the
// reftable tables.list poller in pi watch files at fixed intervals).
func WatchWithErrorHandlerWithInterval(path string, interval time.Duration, onChange func(), onError func()) *PollWatcher {
	if _, err := os.Stat(path); err != nil {
		onError()
		return nil
	}
	w := &PollWatcher{stopCh: make(chan struct{}), done: make(chan struct{})}
	var lastModTime time.Time
	var lastSize int64
	if stat, err := os.Stat(path); err == nil {
		lastModTime = stat.ModTime()
		lastSize = stat.Size()
	}
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				stat, err := os.Stat(path)
				if err != nil {
					continue
				}
				if !stat.ModTime().Equal(lastModTime) || stat.Size() != lastSize {
					lastModTime = stat.ModTime()
					lastSize = stat.Size()
					onChange()
				}
			}
		}
	}()
	return w
}
