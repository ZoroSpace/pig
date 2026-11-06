// pi: src/utils/paths.ts — path normalization and resolution helpers.
//
// Segment deviations:
//   - node path.resolve/join/relative/basename/isAbsolute/sep → path/filepath
//     (POSIX separators; Windows-specific branches keyed on runtime.GOOS are
//     ported but the drive-letter nuances of node's win32 path module are
//     approximated by filepath — declared here).
//   - fileURLToPath → net/url (same approximation as the env package).
//   - spawnProcessSync for the cloud-sync attribute marking → os/exec.
package utils

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var unicodeSpacesRe = regexp.MustCompile("[\u00A0\u2000-\u200A\u202F\u205F\u3000]")

// PathInputOptions parameterize normalizePath (pi: PathInputOptions).
type PathInputOptions struct {
	// Trim strips leading/trailing whitespace before normalization; nil =
	// pi's undefined (false).
	Trim *bool
	// ExpandTilde expands a leading `~` to a home directory. Defaults to
	// true; nil = default.
	ExpandTilde *bool
	// HomeDir is the home directory used for `~` expansion; empty = pi's
	// os.homedir() default.
	HomeDir string
	// StripAtPrefix strips a leading `@`, used for CLI @file paths; nil =
	// pi's undefined (false).
	StripAtPrefix *bool
	// NormalizeUnicodeSpaces normalizes unicode space variants to regular
	// spaces; nil = pi's undefined (false).
	NormalizeUnicodeSpaces *bool
}

// CanonicalizePath resolves a path to its canonical (real) form, following
// symlinks, falling back to the raw path when resolution fails (pi:
// canonicalizePath).
func CanonicalizePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// GetPathComparisonKey returns the key used to compare filesystem paths: the
// resolved absolute path, lowercased on Windows because Windows paths are
// case-insensitive. It does not touch the filesystem; call CanonicalizePath
// first when symlinks must compare equal. Use only for comparison, never for
// display or filesystem access (pi: getPathComparisonKey).
func GetPathComparisonKey(path string) string {
	resolved, err := filepath.Abs(path)
	if err != nil {
		resolved = filepath.Clean(path)
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(resolved)
	}
	return resolved
}

// PathsEqual reports whether both paths name the same location (pi:
// pathsEqual).
func PathsEqual(left string, right string) bool {
	return GetPathComparisonKey(left) == GetPathComparisonKey(right)
}

// IsPathWithin reports whether child is parent or inside it, matching whole
// path segments (pi: isPathWithin).
func IsPathWithin(parent string, child string) bool {
	parentKey := GetPathComparisonKey(parent)
	childKey := GetPathComparisonKey(child)
	if childKey == parentKey {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(parentKey, sep) {
		parentKey += sep
	}
	return strings.HasPrefix(childKey, parentKey)
}

// ShortenHomePath replaces a leading home directory with "~" for display
// (pi: shortenHomePath). An optional homeDir overrides os.UserHomeDir.
func ShortenHomePath(path string, homeDir ...string) string {
	home := ""
	for _, candidate := range homeDir {
		if candidate != "" {
			home = candidate
			break
		}
	}
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if home == "" || !filepath.IsAbs(path) || !IsPathWithin(home, path) {
		return path
	}
	relativePath, err := filepath.Rel(home, path)
	if err != nil || relativePath == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + relativePath
}

// GetFileRevision returns a change-detection token for a file (pi:
// getFileRevision), "" when the path does not exist (pi: undefined). pi
// includes mtimeNs/ctimeNs; pig uses the modtime nanos only (the ctime is
// not portable), which preserves the change-detection contract.
func GetFileRevision(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	dev, ino := fileIdentity(info)
	return fmt.Sprintf("%d:%d:%d:%d", dev, ino, info.Size(), info.ModTime().UnixNano())
}

// IsLocalPath reports whether the value is NOT a package source (npm:, git:,
// etc.) or a remote URL protocol (pi: isLocalPath).
func IsLocalPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	for _, prefix := range []string{"npm:", "git:", "github:", "http:", "https:", "ssh:"} {
		if strings.HasPrefix(trimmed, prefix) {
			return false
		}
	}
	return true
}

// NormalizeWindowsShellPath converts Git Bash, MSYS, Cygwin, and WSL drive
// paths to a form native Windows APIs accept (pi: normalizeWindowsShellPath).
func NormalizeWindowsShellPath(filePath string) string {
	if !strings.HasPrefix(filePath, "/") || strings.HasPrefix(filePath, "//") || strings.Contains(filePath, "\\") {
		return filePath
	}
	re := regexp.MustCompile(`(?i)^/(?:mnt/|cygdrive/)?([a-z])(?:/(.*))?$`)
	match := re.FindStringSubmatch(filePath)
	if match == nil {
		return filePath
	}
	suffix := strings.ReplaceAll(match[2], "/", "\\")
	return strings.ToUpper(match[1]) + ":\\" + suffix
}

// NormalizePath normalizes user path input (pi: normalizePath).
func NormalizePath(input string, options *PathInputOptions) string {
	trim := options != nil && options.Trim != nil && *options.Trim
	normalized := input
	if trim {
		normalized = strings.TrimSpace(normalized)
	}
	if options != nil && options.NormalizeUnicodeSpaces != nil && *options.NormalizeUnicodeSpaces {
		normalized = unicodeSpacesRe.ReplaceAllString(normalized, " ")
	}
	if options != nil && options.StripAtPrefix != nil && *options.StripAtPrefix && strings.HasPrefix(normalized, "@") {
		normalized = normalized[1:]
	}
	if runtime.GOOS == "windows" {
		normalized = NormalizeWindowsShellPath(normalized)
	}

	expandTilde := options == nil || options.ExpandTilde == nil || *options.ExpandTilde
	if expandTilde {
		home := ""
		if options != nil && options.HomeDir != "" {
			home = options.HomeDir
		} else if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
		if normalized == "~" {
			return home
		}
		if strings.HasPrefix(normalized, "~/") {
			return filepath.Join(home, normalized[2:])
		}
	}

	if strings.HasPrefix(normalized, "file://") {
		return fileURLToPath(normalized)
	}

	return normalized
}

// ResolvePath resolves input against baseDir (pi: resolvePath; baseDir ""
// = process.cwd()).
func ResolvePath(input string, baseDir string, options *PathInputOptions) string {
	normalized := NormalizePath(input, options)
	normalizedBaseDir := NormalizePath(baseDir, nil)
	if filepath.IsAbs(normalized) {
		return filepath.Clean(normalized)
	}
	return filepath.Clean(filepath.Join(normalizedBaseDir, normalized))
}

// GetCwdRelativePath returns the path relative to cwd when inside it (pi:
// getCwdRelativePath), "" when outside (pi: undefined).
func GetCwdRelativePath(filePath string, cwd string) string {
	resolvedCwd := ResolvePath(cwd, "", nil)
	resolvedPath := ResolvePath(filePath, resolvedCwd, nil)
	rel, err := filepath.Rel(resolvedCwd, resolvedPath)
	if err != nil {
		return ""
	}
	isInsideCwd := rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
	if isInsideCwd {
		if rel == "" {
			return "."
		}
		return rel
	}
	return ""
}

// FormatPathRelativeToCwdOrAbsolute formats a path relative to cwd when
// inside it, otherwise absolute, with posix separators (pi:
// formatPathRelativeToCwdOrAbsolute).
func FormatPathRelativeToCwdOrAbsolute(filePath string, cwd string) string {
	absolutePath := ResolvePath(filePath, cwd, nil)
	if rel := GetCwdRelativePath(absolutePath, cwd); rel != "" {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(absolutePath)
}

// MarkPathIgnoredByCloudSync marks a path ignored by common cloud-sync
// providers (pi: markPathIgnoredByCloudSync). Failures are silent.
func MarkPathIgnoredByCloudSync(path string) {
	var attrs []string
	switch runtime.GOOS {
	case "darwin":
		attrs = []string{"com.dropbox.ignored", "com.apple.fileprovider.ignore#P"}
	case "linux":
		attrs = []string{"user.com.dropbox.ignored"}
	default:
		return
	}
	for _, attr := range attrs {
		if runtime.GOOS == "darwin" {
			_ = exec.Command("xattr", "-w", attr, "1", path).Run()
		} else {
			_ = exec.Command("setfattr", "-n", attr, "-v", "1", path).Run()
		}
	}
}

// fileURLToPath approximates node's fileURLToPath for POSIX paths.
func fileURLToPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "file" {
		return raw
	}
	decoded, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return raw
	}
	return decoded
}
