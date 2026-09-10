// footer_data_provider.go (pi: src/core/footer-data-provider.ts).
//
// Provides git branch and extension statuses — data not otherwise
// accessible to extensions. Context usage on session.GetContextUsage(),
// token stats on sessionManager.GetEntries(), model info on session.Model().
//
// Declared deviations:
//   - pi watches directories via node's fs.watch; pig uses mtime/size
//     polling watchers (codingagent/utils PollWatcher). pi's filename
//     filter (only react to HEAD) has no polling equivalent — a directory
//     change schedules a debounced refresh instead, which re-resolves the
//     branch from HEAD's content.
//   - pi's WSL watchFile(HEAD, { interval: 1000 }) half is folded into the
//     same poll-watcher mechanism (a second 1s poller on HEAD, kept in the
//     wslHeadWatcher field).
//   - child_process spawnSync/execFile sit behind unexported function vars
//     (footerResolveBranchSync/footerResolveBranchAsync) so tests can stub
//     git (pi: vi.mock("child_process")).
//   - pi is single-threaded; pig guards the provider with a mutex and only
//     invokes branch-change callbacks with the lock released.
package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

// footerResolveBranchSync asks git for the current branch synchronously.
// Returns nil on detached HEAD or if git is unavailable. (Function var is a
// pig test seam for child_process.)
var footerResolveBranchSync = func(repoDir string) *string {
	cmd := exec.Command("git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return nil
	}
	return &branch
}

// footerResolveBranchAsync asks git for the current branch asynchronously.
// Returns nil on detached HEAD or if git is unavailable.
var footerResolveBranchAsync = func(repoDir string) *string {
	cmd := exec.Command("git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return nil
	}
	return &branch
}

func isWslEnvironment() bool {
	if v, ok := os.LookupEnv("WSL_DISTRO_NAME"); ok && v != "" {
		return true
	}
	if v, ok := os.LookupEnv("WSL_INTEROP"); ok && v != "" {
		return true
	}
	return false
}

var windowsMountedRepoPathRegex = regexp.MustCompile(`(?i)^/mnt/[a-z](?:/|$)`)

func isWindowsMountedRepoPath(repoDir string) bool {
	return windowsMountedRepoPathRegex.MatchString(repoDir)
}

func shouldPollGitHead(repoDir string) bool {
	return isWslEnvironment() && isWindowsMountedRepoPath(repoDir)
}

// footerWatchDebounceMs is pi's FooterDataProvider.WATCH_DEBOUNCE_MS.
const footerWatchDebounceMs = 500 * time.Millisecond

// FooterDataProvider provides git branch and extension statuses for the
// footer.
type FooterDataProvider struct {
	mu sync.Mutex

	cwd      string
	disposed bool

	extensionStatuses      map[string]string
	cachedBranch           *string
	cachedBranchSet        bool
	gitPaths               *GitPaths
	headWatcher            *utils.PollWatcher
	wslHeadWatcher         *utils.PollWatcher
	reftableWatcher        *utils.PollWatcher
	reftableTablesWatcher  *utils.PollWatcher
	branchChangeCallbacks  []*footerBranchChangeSubscription
	availableProviderCount int
	refreshTimer           *time.Timer
	gitWatcherRetryTimer   *time.Timer
	refreshInFlight        bool
	refreshPending         bool
}

// footerBranchChangeSubscription identifies one branch-change subscriber
// (pig: funcs are not comparable, so subscriptions are boxed).
type footerBranchChangeSubscription struct {
	fn func()
}

// NewFooterDataProvider creates the provider and sets up the git watcher.
func NewFooterDataProvider(cwd string) *FooterDataProvider {
	p := &FooterDataProvider{
		cwd:               cwd,
		extensionStatuses: map[string]string{},
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gitPaths = findGitPaths(cwd)
	p.setupGitWatcherLocked()
	return p
}

// GetGitBranch returns the current git branch; nil if not in repo,
// "detached" if detached HEAD.
func (p *FooterDataProvider) GetGitBranch() *string {
	p.mu.Lock()
	if !p.cachedBranchSet {
		gitPaths := p.gitPaths
		p.mu.Unlock()
		branch := resolveGitBranchFromHead(gitPaths, footerResolveBranchSync)
		p.mu.Lock()
		p.cachedBranch = branch
		p.cachedBranchSet = true
	}
	defer p.mu.Unlock()
	return p.cachedBranch
}

// GetExtensionStatuses returns the extension status texts set via
// ctx.ui.setStatus(). The returned map is a copy (pig: read-only view).
func (p *FooterDataProvider) GetExtensionStatuses() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]string, len(p.extensionStatuses))
	for k, v := range p.extensionStatuses {
		out[k] = v
	}
	return out
}

// OnBranchChange subscribes to git branch changes. Returns unsubscribe
// function.
func (p *FooterDataProvider) OnBranchChange(callback func()) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	sub := &footerBranchChangeSubscription{fn: callback}
	p.branchChangeCallbacks = append(p.branchChangeCallbacks, sub)
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		for i, cb := range p.branchChangeCallbacks {
			if cb == sub {
				p.branchChangeCallbacks = append(p.branchChangeCallbacks[:i], p.branchChangeCallbacks[i+1:]...)
				break
			}
		}
	}
}

// SetExtensionStatus sets an extension status (internal: pi ctx.ui.setStatus).
func (p *FooterDataProvider) SetExtensionStatus(key string, text *string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if text == nil {
		delete(p.extensionStatuses, key)
	} else {
		p.extensionStatuses[key] = *text
	}
}

// ClearExtensionStatuses clears extension statuses (internal).
func (p *FooterDataProvider) ClearExtensionStatuses() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.extensionStatuses = map[string]string{}
}

// GetAvailableProviderCount returns the number of unique providers with
// available models (for footer display).
func (p *FooterDataProvider) GetAvailableProviderCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.availableProviderCount
}

// SetAvailableProviderCount updates the available provider count (internal).
func (p *FooterDataProvider) SetAvailableProviderCount(count int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.availableProviderCount = count
}

// SetCwd re-targets the provider at a new cwd.
func (p *FooterDataProvider) SetCwd(cwd string) {
	p.mu.Lock()
	if p.cwd == cwd {
		p.mu.Unlock()
		return
	}
	p.cwd = cwd
	if p.refreshTimer != nil {
		p.refreshTimer.Stop()
		p.refreshTimer = nil
	}
	p.clearGitWatchersLocked()
	p.cachedBranch = nil
	p.cachedBranchSet = false
	p.gitPaths = findGitPaths(cwd)
	p.setupGitWatcherLocked()
	p.disposed = false
	callbacks := p.collectBranchChangeCallbacksLocked()
	p.mu.Unlock()
	for _, sub := range callbacks {
		sub.fn()
	}
}

// Dispose cleans up (internal).
func (p *FooterDataProvider) Dispose() {
	p.mu.Lock()
	p.disposed = true
	if p.refreshTimer != nil {
		p.refreshTimer.Stop()
		p.refreshTimer = nil
	}
	p.clearGitWatchersLocked()
	p.branchChangeCallbacks = nil
	p.mu.Unlock()
}

// scheduleRefreshLocked schedules a debounced async refresh. Caller holds
// p.mu.
func (p *FooterDataProvider) scheduleRefreshLocked() {
	if p.disposed || p.refreshTimer != nil {
		return
	}
	if p.refreshInFlight {
		p.refreshPending = true
		return
	}
	p.refreshTimer = time.AfterFunc(footerWatchDebounceMs, func() {
		p.mu.Lock()
		p.refreshTimer = nil
		p.mu.Unlock()
		p.refreshGitBranchAsync()
	})
}

// refreshGitBranchAsync resolves the branch off-thread and notifies on
// change (pi: refreshGitBranchAsync).
func (p *FooterDataProvider) refreshGitBranchAsync() {
	p.mu.Lock()
	if p.disposed {
		p.mu.Unlock()
		return
	}
	if p.refreshInFlight {
		p.refreshPending = true
		p.mu.Unlock()
		return
	}
	p.refreshInFlight = true
	gitPaths := p.gitPaths
	p.mu.Unlock()

	nextBranch := resolveGitBranchFromHead(gitPaths, footerResolveBranchAsync)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshInFlight = false
	if p.disposed {
		return
	}
	notify := p.cachedBranchSet && !branchEqual(p.cachedBranch, nextBranch)
	p.cachedBranch = nextBranch
	p.cachedBranchSet = true
	if notify {
		callbacks := p.collectBranchChangeCallbacksLocked()
		p.mu.Unlock()
		for _, sub := range callbacks {
			sub.fn()
		}
		p.mu.Lock()
	}
	if p.refreshPending && !p.disposed {
		p.refreshPending = false
		p.scheduleRefreshLocked()
	}
}

// collectBranchChangeCallbacksLocked snapshots the subscriber list. Caller
// holds p.mu.
func (p *FooterDataProvider) collectBranchChangeCallbacksLocked() []*footerBranchChangeSubscription {
	return append([]*footerBranchChangeSubscription{}, p.branchChangeCallbacks...)
}

func branchEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// resolveGitBranchFromHead reads HEAD and resolves the branch. The git
// resolver is only invoked for reftable (.invalid) HEADs.
func resolveGitBranchFromHead(gitPaths *GitPaths, resolveGit func(repoDir string) *string) *string {
	if gitPaths == nil {
		return nil
	}
	content, err := os.ReadFile(gitPaths.HeadPath)
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(content))
	if strings.HasPrefix(trimmed, "ref: refs/heads/") {
		branch := trimmed[len("ref: refs/heads/"):]
		if branch == ".invalid" {
			if resolved := resolveGit(gitPaths.RepoDir); resolved != nil {
				return resolved
			}
			detached := "detached"
			return &detached
		}
		return &branch
	}
	detached := "detached"
	return &detached
}

// clearGitWatchersLocked stops all watchers and retry timers. Caller holds
// p.mu.
func (p *FooterDataProvider) clearGitWatchersLocked() {
	utils.CloseWatcher(p.headWatcher)
	p.headWatcher = nil
	utils.CloseWatcher(p.wslHeadWatcher)
	p.wslHeadWatcher = nil
	utils.CloseWatcher(p.reftableWatcher)
	p.reftableWatcher = nil
	utils.CloseWatcher(p.reftableTablesWatcher)
	p.reftableTablesWatcher = nil
	if p.gitWatcherRetryTimer != nil {
		p.gitWatcherRetryTimer.Stop()
		p.gitWatcherRetryTimer = nil
	}
}

// handleGitWatcherError tears down and schedules a watcher retry (pi:
// handleGitWatcherError → scheduleGitWatcherRetry).
func (p *FooterDataProvider) handleGitWatcherError() {
	p.mu.Lock()
	if p.disposed {
		p.mu.Unlock()
		return
	}
	p.clearGitWatchersLocked()
	if p.gitWatcherRetryTimer != nil {
		p.mu.Unlock()
		return
	}
	p.gitWatcherRetryTimer = time.AfterFunc(utils.FSWatchRetryDelayMs*time.Millisecond, func() {
		p.mu.Lock()
		p.gitWatcherRetryTimer = nil
		p.mu.Unlock()
		p.mu.Lock()
		if p.disposed {
			p.mu.Unlock()
			return
		}
		p.setupGitWatcherLocked()
		p.mu.Unlock()
	})
	p.mu.Unlock()
}

// setupGitWatcherLocked installs the HEAD/reftable poll watchers. Caller
// holds p.mu.
func (p *FooterDataProvider) setupGitWatcherLocked() {
	p.clearGitWatchersLocked()
	if p.gitPaths == nil {
		return
	}

	pollGitHead := shouldPollGitHead(p.gitPaths.RepoDir)

	// Watch the directory containing HEAD, not HEAD itself: git uses atomic
	// writes (write temp, rename over HEAD), which changes the inode
	// (pi: watch(dirname(headPath))).
	headDir := filepath.Dir(p.gitPaths.HeadPath)
	p.headWatcher = utils.WatchWithErrorHandler(headDir, func() {
		p.mu.Lock()
		p.scheduleRefreshLocked()
		p.mu.Unlock()
	}, p.handleGitWatcherError)
	if pollGitHead {
		// pi: watchFile(headPath, { interval: 1000 }) — poll HEAD directly.
		p.wslHeadWatcher = utils.WatchWithErrorHandlerWithInterval(p.gitPaths.HeadPath, time.Second, func() {
			p.mu.Lock()
			p.scheduleRefreshLocked()
			p.mu.Unlock()
		}, p.handleGitWatcherError)
	}
	if p.headWatcher == nil && p.wslHeadWatcher == nil {
		return
	}

	// In reftable repos, branch switches update files in the reftable
	// directory instead of HEAD. Watch it separately so the footer picks up
	// those changes.
	reftableDir := filepath.Join(p.gitPaths.CommonGitDir, "reftable")
	if _, err := os.Stat(reftableDir); err == nil {
		p.reftableWatcher = utils.WatchWithErrorHandler(reftableDir, func() {
			p.mu.Lock()
			p.scheduleRefreshLocked()
			p.mu.Unlock()
		}, p.handleGitWatcherError)
		if p.reftableWatcher == nil {
			return
		}

		tablesListPath := filepath.Join(reftableDir, "tables.list")
		if _, err := os.Stat(tablesListPath); err == nil {
			p.reftableTablesWatcher = utils.WatchWithErrorHandlerWithInterval(tablesListPath, 250*time.Millisecond, func() {
				p.mu.Lock()
				p.scheduleRefreshLocked()
				p.mu.Unlock()
			}, p.handleGitWatcherError)
			if p.reftableTablesWatcher == nil {
				return
			}
		}
	}
}
