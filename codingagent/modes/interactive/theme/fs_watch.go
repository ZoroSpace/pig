// fs_watch.go (pi: src/modes/interactive/theme — theme file watching).
//
// pig keeps the poll-watcher implementation in codingagent/utils (pi keeps
// it in src/utils/fs-watch.ts, shared with the footer git watcher); these
// aliases preserve the theme package's internal call sites.
package theme

import "github.com/ZoroSpace/pig/codingagent/utils"

// FSWatchRetryDelayMs is pi's FS_WATCH_RETRY_DELAY_MS.
const FSWatchRetryDelayMs = utils.FSWatchRetryDelayMs

// PollWatcher is pi's FSWatcher (pig: mtime/size polling watcher).
type PollWatcher = utils.PollWatcher

// CloseWatcher stops a watcher, ignoring nil and close errors (pi:
// closeWatcher).
var CloseWatcher = utils.CloseWatcher

// WatchWithErrorHandler polls path for mtime/size changes and invokes
// onChange; poll-setup errors invoke onError and return nil.
var WatchWithErrorHandler = utils.WatchWithErrorHandler
