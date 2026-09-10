// pi: src/core/trust-manager.ts — project trust decisions (store, nearest-
// parent lookup, trust-requiring resource detection).
//
// Segment deviations:
//   - proper-lockfile → O_EXCL "<trust.json>.lock" lockfile with the same
//     10×20ms retry loop (settings-manager batch precedent). pi locks the
//     trust dir with an explicit lockfilePath of `${path}.lock`; pig locks
//     the same path directly.
//   - TrustFile: Record<string, boolean|null> → map[string]*bool (nil =
//     null).
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// ProjectTrustDecision is a trust decision: true, false, or null (pi:
// ProjectTrustDecision = boolean | null).
type ProjectTrustDecision = *bool

// ProjectTrustStoreEntry is one stored trust decision (pi:
// ProjectTrustStoreEntry).
type ProjectTrustStoreEntry struct {
	Path     string
	Decision bool
}

// ProjectTrustUpdate is one pending trust-store write (pi:
// ProjectTrustUpdate).
type ProjectTrustUpdate struct {
	Path     string
	Decision ProjectTrustDecision
}

// ProjectTrustOption is one trust prompt option (pi: ProjectTrustOption;
// savedPath "" = undefined).
type ProjectTrustOption struct {
	Label     string
	Trusted   bool
	Updates   []ProjectTrustUpdate
	SavedPath string
}

// trustFile is the parsed trust.json shape (pi: TrustFile).
type trustFile = map[string]*bool

// trustRequiringProjectConfigResources lists the .pi entries that gate
// project trust (pi: TRUST_REQUIRING_PROJECT_CONFIG_RESOURCES).
var trustRequiringProjectConfigResources = []string{
	"settings.json",
	"extensions",
	"skills",
	"prompts",
	"themes",
	"SYSTEM.md",
	"APPEND_SYSTEM.md",
}

// normalizeTrustCwd canonicalizes the cwd for store keys (pi:
// normalizeCwd).
func normalizeTrustCwd(cwd string) string {
	return utils.CanonicalizePath(utils.ResolvePath(cwd, "", nil))
}

// findNearestTrustEntry walks up from cwd for the nearest decision (pi:
// findNearestTrustEntry); nil = none.
func findNearestTrustEntry(data trustFile, cwd string) *ProjectTrustStoreEntry {
	currentDir := normalizeTrustCwd(cwd)
	for {
		if value, ok := data[currentDir]; ok && value != nil {
			return &ProjectTrustStoreEntry{Path: currentDir, Decision: *value}
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return nil
		}
		currentDir = parentDir
	}
}

// GetProjectTrustParentPath returns the parent of the normalized cwd (pi:
// getProjectTrustParentPath; "" = undefined at the filesystem root).
func GetProjectTrustParentPath(cwd string) string {
	trustPath := normalizeTrustCwd(cwd)
	parentDir := filepath.Dir(trustPath)
	if parentDir == trustPath {
		return ""
	}
	return parentDir
}

// GetProjectTrustOptions builds the trust prompt options (pi:
// getProjectTrustOptions).
func GetProjectTrustOptions(cwd string, options *struct{ IncludeSessionOnly bool }) []ProjectTrustOption {
	trustPath := normalizeTrustCwd(cwd)
	trustOptions := []ProjectTrustOption{
		{Label: "Trust", Trusted: true, Updates: []ProjectTrustUpdate{{Path: trustPath, Decision: lo.ToPtr(true)}}, SavedPath: trustPath},
	}
	parentPath := GetProjectTrustParentPath(cwd)
	if parentPath != "" {
		trustOptions = append(trustOptions, ProjectTrustOption{
			Label:   fmt.Sprintf("Trust parent folder (%s)", parentPath),
			Trusted: true,
			Updates: []ProjectTrustUpdate{
				{Path: parentPath, Decision: lo.ToPtr(true)},
				{Path: trustPath, Decision: nil},
			},
			SavedPath: parentPath,
		})
	}
	if options != nil && options.IncludeSessionOnly {
		trustOptions = append(trustOptions, ProjectTrustOption{Label: "Trust (this session only)", Trusted: true})
	}
	trustOptions = append(trustOptions, ProjectTrustOption{
		Label:     "Do not trust",
		Trusted:   false,
		Updates:   []ProjectTrustUpdate{{Path: trustPath, Decision: lo.ToPtr(false)}},
		SavedPath: trustPath,
	})
	if options != nil && options.IncludeSessionOnly {
		trustOptions = append(trustOptions, ProjectTrustOption{Label: "Do not trust (this session only)", Trusted: false})
	}
	return trustOptions
}

// readTrustFile reads and validates the trust store (pi: readTrustFile).
func readTrustFile(path string) (trustFile, error) {
	if _, err := os.Stat(path); err != nil {
		return trustFile{}, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read trust store %s: %s", path, err.Error())
	}
	var parsed any
	if err := json.Unmarshal([]byte(utils.StripBom(string(content))), &parsed); err != nil {
		return nil, fmt.Errorf("Failed to read trust store %s: %s", path, err.Error())
	}

	parsedMap, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Invalid trust store %s: expected an object", path)
	}

	data := trustFile{}
	for key, value := range parsedMap {
		boolValue, isBool := value.(bool)
		if !isBool && value != nil {
			return nil, fmt.Errorf("Invalid trust store %s: value for %s must be true, false, or null",
				path, strconv.Quote(key))
		}
		if isBool {
			data[key] = &boolValue
		} else {
			data[key] = nil
		}
	}
	return data, nil
}

// writeTrustFile writes the trust store with sorted keys (pi:
// writeTrustFile).
func writeTrustFile(path string, data trustFile) error {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sorted := trustFile{}
	// pi keeps true/false/null; the map form already excludes other shapes.
	for _, key := range keys {
		sorted[key] = data[key]
	}
	native, err := json.MarshalIndent(sorted, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(native, '\n'), 0o644)
}

// acquireTrustLockSync locks the trust store path (pi: acquireTrustLockSync
// — proper-lockfile with lockfilePath `${path}.lock`, ELOCKED retried 10×
// every 20ms). Returns the release func.
func acquireTrustLockSync(path string) (func(), error) {
	trustDir := filepath.Dir(path)
	if err := os.MkdirAll(trustDir, 0o755); err != nil {
		return nil, err
	}
	const maxAttempts = 10
	const delayMs = 20
	lockPath := path + ".lock"

	var lastError error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			// pi: only ELOCKED is retried; other errors throw immediately.
			return nil, err
		}
		lastError = err
		if attempt == maxAttempts {
			break
		}
		time.Sleep(delayMs * time.Millisecond)
	}

	if lastError != nil {
		return nil, lastError
	}
	return nil, errors.New("Failed to acquire trust store lock")
}

// withTrustFileLock runs fn under the trust store lock (pi:
// withTrustFileLock; throw → error).
func withTrustFileLock(path string, fn func() error) error {
	release, err := acquireTrustLockSync(path)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

// HasTrustRequiringProjectResources returns true when cwd has project-local
// resources that must be gated by project trust: trust-requiring entries
// under cwd/.pi, or .agents/skills in cwd or one of its ancestors. Returns
// false when no such project resources exist. The user/global
// ~/.agents/skills directory is always treated as a trusted user resource
// and is ignored here, even when cwd is $HOME. (pi:
// hasTrustRequiringProjectResources.)
func HasTrustRequiringProjectResources(cwd string) bool {
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = ""
		}
		homeDir = home
	}
	userHomeDir := utils.CanonicalizePath(utils.ResolvePath(homeDir, "", nil))
	userAgentsSkillsDir := filepath.Join(userHomeDir, ".agents", "skills")
	currentDir := utils.CanonicalizePath(utils.ResolvePath(cwd, "", nil))

	configDir := filepath.Join(currentDir, codingagent.ConfigDirName)
	for _, entry := range trustRequiringProjectConfigResources {
		if _, err := os.Stat(filepath.Join(configDir, entry)); err == nil {
			return true
		}
	}

	for {
		agentsSkillsDir := filepath.Join(currentDir, ".agents", "skills")
		if agentsSkillsDir != userAgentsSkillsDir {
			if _, err := os.Stat(agentsSkillsDir); err == nil {
				return true
			}
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return false
		}
		currentDir = parentDir
	}
}

// ProjectTrustStore persists trust decisions in trust.json (pi:
// ProjectTrustStore).
type ProjectTrustStore struct {
	trustPath string
}

// NewProjectTrustStore builds the store for one agent dir (pi:
// constructor).
func NewProjectTrustStore(agentDir string) *ProjectTrustStore {
	return &ProjectTrustStore{trustPath: filepath.Join(utils.ResolvePath(agentDir, "", nil), "trust.json")}
}

// Get returns the nearest stored decision (pi: get; nil = none; throw →
// error).
func (s *ProjectTrustStore) Get(cwd string) (ProjectTrustDecision, error) {
	entry, err := s.GetEntry(cwd)
	if err != nil || entry == nil {
		return nil, err
	}
	decision := entry.Decision
	return &decision, nil
}

// GetEntry returns the nearest trust entry (pi: getEntry; nil = none).
func (s *ProjectTrustStore) GetEntry(cwd string) (*ProjectTrustStoreEntry, error) {
	var entry *ProjectTrustStoreEntry
	err := withTrustFileLock(s.trustPath, func() error {
		data, err := readTrustFile(s.trustPath)
		if err != nil {
			return err
		}
		entry = findNearestTrustEntry(data, cwd)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// Set stores one decision (pi: set; nil decision = delete).
func (s *ProjectTrustStore) Set(cwd string, decision ProjectTrustDecision) error {
	return s.SetMany([]ProjectTrustUpdate{{Path: cwd, Decision: decision}})
}

// SetMany stores multiple decisions (pi: setMany).
func (s *ProjectTrustStore) SetMany(decisions []ProjectTrustUpdate) error {
	return withTrustFileLock(s.trustPath, func() error {
		data, err := readTrustFile(s.trustPath)
		if err != nil {
			return err
		}
		for _, update := range decisions {
			key := normalizeTrustCwd(update.Path)
			if update.Decision == nil {
				delete(data, key)
			} else {
				data[key] = update.Decision
			}
		}
		return writeTrustFile(s.trustPath, data)
	})
}
