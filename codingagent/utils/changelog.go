// changelog.go (pi: src/utils/changelog.ts) — parse CHANGELOG.md entries and
// rewrite relative markdown links into tag-pinned GitHub source links.
package utils

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// ChangelogEntry is one parsed `## [x.y.z]` section (pi: ChangelogEntry).
type ChangelogEntry struct {
	Major   int
	Minor   int
	Patch   int
	Content string
}

const (
	changelogGithubRepo           = "earendil-works/pi"
	changelogLinkBasePath         = "packages/coding-agent"
	changelogURLSchemePattern     = `^[a-z][a-z0-9+.-]*:`
	changelogInlineMarkdownLinkRe = `(!?\[[^\]\n]+\]\()` + `([^\s)]+)` + `((?:\s+[^)]*)?\))`
)

var (
	// RE2 has no lookahead; the legacy-repo rewrite matches the host prefix
	// and checks the next rune is '/' or end-of-string (pig adaptation).
	changelogLegacyRepoRe = regexp.MustCompile(`^https://github\.com/(?:badlogic|earendil-works)/pi-mono`)
	changelogURLSchemeRe  = regexp.MustCompile(`(?i)` + changelogURLSchemePattern)
	// InlineMarkdownLinkRe matches inline markdown links (pi:
	// INLINE_MARKDOWN_LINK_RE, global).
	InlineMarkdownLinkRe = regexp.MustCompile(changelogInlineMarkdownLinkRe)
)

// rewriteLegacyRepoURL replaces the pi-mono host prefix with the new repo
// (pi: LEGACY_REPO_RE with (?=/|$) lookahead).
func rewriteLegacyRepoURL(target string) string {
	idx := changelogLegacyRepoRe.FindStringIndex(target)
	if idx == nil || idx[0] != 0 {
		return target
	}
	rest := target[idx[1]:]
	if rest != "" && rest[0] != '/' {
		return target
	}
	return "https://github.com/" + changelogGithubRepo + rest
}

func changelogEntryVersion(entry ChangelogEntry) string {
	return fmt.Sprintf("%d.%d.%d", entry.Major, entry.Minor, entry.Patch)
}

// normalizeChangelogTag prefixes "v" (pi: normalizeTag).
func normalizeChangelogTag(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

func changelogVersionString(version any) string {
	switch v := version.(type) {
	case string:
		return v
	case ChangelogEntry:
		return changelogEntryVersion(v)
	case *ChangelogEntry:
		return changelogEntryVersion(*v)
	}
	return ""
}

type changelogSplitTarget struct {
	fragment string
	pathPart string
	query    string
}

func splitLocalTarget(target string) changelogSplitTarget {
	hashIndex := strings.Index(target, "#")
	var beforeHash, fragment string
	if hashIndex == -1 {
		beforeHash = target
	} else {
		beforeHash = target[:hashIndex]
		fragment = target[hashIndex:]
	}
	queryIndex := strings.Index(beforeHash, "?")
	if queryIndex == -1 {
		return changelogSplitTarget{fragment: fragment, pathPart: beforeHash}
	}
	return changelogSplitTarget{
		fragment: fragment,
		pathPart: beforeHash[:queryIndex],
		query:    beforeHash[queryIndex:],
	}
}

func normalizePathPart(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

// resolveRepositoryPath maps a link target onto a repo-relative path (pi:
// resolveRepositoryPath); ok=false = outside the repository.
func resolveRepositoryPath(targetPath string) (string, bool) {
	normalizedTarget := normalizePathPart(targetPath)
	trailingSlash := strings.HasSuffix(normalizedTarget, "/")
	var joined string
	if strings.HasPrefix(normalizedTarget, "/") {
		joined = path.Clean(strings.TrimLeft(normalizedTarget, "/"))
	} else {
		joined = path.Clean(path.Join(changelogLinkBasePath, normalizedTarget))
	}
	// node path.posix.normalize preserves a trailing slash; Go path.Clean
	// strips it (pig adaptation).
	if trailingSlash && !strings.HasSuffix(joined, "/") {
		joined += "/"
	}

	if joined == "." || strings.HasPrefix(joined, "../") || joined == ".." {
		return "", false
	}
	return joined, true
}

func isDirectoryTarget(originalPath string, repositoryPath string) bool {
	if strings.HasSuffix(originalPath, "/") {
		return true
	}
	basename := path.Base(repositoryPath)
	return !strings.Contains(basename, ".")
}

func normalizeChangelogLinkTarget(target string, tag string) string {
	canonicalTarget := rewriteLegacyRepoURL(target)
	repoURL := "https://github.com/" + changelogGithubRepo

	for _, route := range []string{"blob", "tree"} {
		for _, branch := range []string{"main", "master"} {
			floatingRefPrefix := repoURL + "/" + route + "/" + branch + "/"
			if strings.HasPrefix(canonicalTarget, floatingRefPrefix) {
				canonicalTarget = repoURL + "/" + route + "/" + tag + "/" + canonicalTarget[len(floatingRefPrefix):]
			}
		}
	}

	if strings.HasPrefix(canonicalTarget, "#") || strings.HasPrefix(canonicalTarget, "//") ||
		changelogURLSchemeRe.MatchString(canonicalTarget) {
		return canonicalTarget
	}

	split := splitLocalTarget(canonicalTarget)
	if split.pathPart == "" {
		return canonicalTarget
	}

	repositoryPath, ok := resolveRepositoryPath(split.pathPart)
	if !ok {
		return canonicalTarget
	}

	route := "blob"
	if isDirectoryTarget(split.pathPart, repositoryPath) {
		route = "tree"
	}
	return repoURL + "/" + route + "/" + tag + "/" + encodeURIPath(repositoryPath) + split.query + split.fragment
}

// encodeURIPath percent-encodes a path like JS encodeURI (keeps the RFC 2396
// unreserved + reserved-mark set, escapes the rest).
func encodeURIPath(p string) string {
	const unreserved = "-_.~!#$&'()*+,/:;=?@"
	var b strings.Builder
	for _, r := range []byte(p) {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' ||
			strings.IndexByte(unreserved, r) >= 0 {
			b.WriteByte(r)
		} else {
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}

// NormalizeChangelogLinks rewrites relative links in changelog markdown into
// tag-pinned GitHub links (pi: normalizeChangelogLinks). version is a string
// or ChangelogEntry.
func NormalizeChangelogLinks(markdown string, version any) string {
	tag := normalizeChangelogTag(changelogVersionString(version))
	return InlineMarkdownLinkRe.ReplaceAllStringFunc(markdown, func(match string) string {
		groups := InlineMarkdownLinkRe.FindStringSubmatch(match)
		prefix, target, suffix := groups[1], groups[2], groups[3]
		return prefix + normalizeChangelogLinkTarget(target, tag) + suffix
	})
}

// ParseChangelog parses changelog entries from CHANGELOG.md. Scans for ##
// lines and collects content until the next ## or EOF (pi: parseChangelog).
func ParseChangelog(changelogPath string) []ChangelogEntry {
	content, err := os.ReadFile(changelogPath)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: Could not parse changelog: %v\n", err)
		}
		return nil
	}
	lines := strings.Split(string(content), "\n")
	var entries []ChangelogEntry

	var currentLines []string
	var currentVersion *ChangelogEntry

	flush := func() {
		if currentVersion != nil && len(currentLines) > 0 {
			entry := *currentVersion
			entry.Content = strings.TrimSpace(strings.Join(currentLines, "\n"))
			entries = append(entries, entry)
		}
	}

	versionRe := regexp.MustCompile(`##\s+\[?(\d+)\.(\d+)\.(\d+)\]?`)
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			if m := versionRe.FindStringSubmatch(line); m != nil {
				major, _ := strconv.Atoi(m[1])
				minor, _ := strconv.Atoi(m[2])
				patch, _ := strconv.Atoi(m[3])
				currentVersion = &ChangelogEntry{Major: major, Minor: minor, Patch: patch}
				currentLines = []string{line}
			} else {
				currentVersion = nil
				currentLines = nil
			}
		} else if currentVersion != nil {
			currentLines = append(currentLines, line)
		}
	}
	flush()
	return entries
}

// CompareVersions returns -1/0/1 (pi: compareVersions).
func CompareVersions(v1, v2 ChangelogEntry) int {
	if v1.Major != v2.Major {
		return signInt(v1.Major - v2.Major)
	}
	if v1.Minor != v2.Minor {
		return signInt(v1.Minor - v2.Minor)
	}
	return signInt(v1.Patch - v2.Patch)
}

// GetNewEntries returns entries newer than lastVersion (pi: getNewEntries).
func GetNewEntries(entries []ChangelogEntry, lastVersion string) []ChangelogEntry {
	parts := strings.Split(lastVersion, ".")
	atoi := func(i int) int {
		if i < len(parts) {
			v, err := strconv.Atoi(strings.TrimSpace(parts[i]))
			if err == nil {
				return v
			}
		}
		return 0
	}
	last := ChangelogEntry{Major: atoi(0), Minor: atoi(1), Patch: atoi(2)}
	var out []ChangelogEntry
	for _, entry := range entries {
		if CompareVersions(entry, last) > 0 {
			out = append(out, entry)
		}
	}
	return out
}

func signInt(v int) int {
	if v < 0 {
		return -1
	}
	if v > 0 {
		return 1
	}
	return 0
}
