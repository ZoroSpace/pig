// pi: src/utils/tools-manager.ts — availability and GitHub-release download
// of the external search binaries (fd, ripgrep).
//
// Segment deviations:
//   - process.platform/arch → runtime.GOOS/runtime.GOARCH; pi's "x64" arch
//     string maps from GOARCH "amd64" (fd's darwin x64 pin included).
//   - The tar/zip extraction shells out to tar/unzip/powershell exactly as
//     pi does (spawnSync → exec.Command).
//   - fetchWithRetry carries the caller ctx; pi relies on AbortSignal
//     timeouts.
package utils

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/codingagent"
)

const (
	networkTimeoutMs  = 10_000
	downloadTimeoutMs = 120_000
)

// toolsDir is the managed binaries directory (pi: TOOLS_DIR = getBinDir()).
func toolsDir() string { return codingagent.GetBinDir() }

// isOfflineModeEnabled reports whether downloads are disabled (pi:
// isOfflineModeEnabled).
func isOfflineModeEnabled() bool {
	value := os.Getenv("PI_OFFLINE")
	if value == "" {
		return false
	}
	return value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
}

// toolConfig describes one downloadable tool (pi: ToolConfig).
type toolConfig struct {
	name              string
	repo              string
	binaryName        string
	systemBinaryNames []string
	tagPrefix         string
	getAssetName      func(version string, plat string, architecture string) string
}

var toolsCatalog = map[string]toolConfig{
	"fd": {
		name:              "fd",
		repo:              "sharkdp/fd",
		binaryName:        "fd",
		systemBinaryNames: []string{"fd", "fdfind"},
		tagPrefix:         "v",
		getAssetName: func(version string, plat string, architecture string) string {
			if plat == "darwin" {
				archStr := "x86_64"
				if architecture == "arm64" {
					archStr = "aarch64"
				}
				return fmt.Sprintf("fd-v%s-%s-apple-darwin.tar.gz", version, archStr)
			} else if plat == "linux" {
				archStr := "x86_64"
				if architecture == "arm64" {
					archStr = "aarch64"
				}
				return fmt.Sprintf("fd-v%s-%s-unknown-linux-gnu.tar.gz", version, archStr)
			} else if plat == "win32" {
				archStr := "x86_64"
				if architecture == "arm64" {
					archStr = "aarch64"
				}
				return fmt.Sprintf("fd-v%s-%s-pc-windows-msvc.zip", version, archStr)
			}
			return ""
		},
	},
	"rg": {
		name:       "ripgrep",
		repo:       "BurntSushi/ripgrep",
		binaryName: "rg",
		tagPrefix:  "",
		getAssetName: func(version string, plat string, architecture string) string {
			if plat == "darwin" {
				archStr := "x86_64"
				if architecture == "arm64" {
					archStr = "aarch64"
				}
				return fmt.Sprintf("ripgrep-%s-%s-apple-darwin.tar.gz", version, archStr)
			} else if plat == "linux" {
				if architecture == "arm64" {
					return fmt.Sprintf("ripgrep-%s-aarch64-unknown-linux-gnu.tar.gz", version)
				}
				return fmt.Sprintf("ripgrep-%s-x86_64-unknown-linux-musl.tar.gz", version)
			} else if plat == "win32" {
				archStr := "x86_64"
				if architecture == "arm64" {
					archStr = "aarch64"
				}
				return fmt.Sprintf("ripgrep-%s-%s-pc-windows-msvc.zip", version, archStr)
			}
			return ""
		},
	},
}

// goArch maps GOARCH to pi's os.arch() strings.
func goArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

// goPlatform maps GOOS to pi's process.platform strings.
func goPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	case "windows":
		return "win32"
	default:
		return runtime.GOOS
	}
}

// commandExists checks if a command exists in PATH by trying to run it (pi:
// commandExists).
func commandExists(cmd string) bool {
	proc := exec.Command(cmd, "--version")
	return proc.Run() == nil
}

// GetToolPath gets the path to a tool (system-wide or in our tools dir) (pi:
// getToolPath); "" = null.
func GetToolPath(tool string) string {
	config, ok := toolsCatalog[tool]
	if !ok {
		return ""
	}
	binaryExt := ""
	if runtime.GOOS == "windows" {
		binaryExt = ".exe"
	}
	localPath := filepath.Join(toolsDir(), config.binaryName+binaryExt)
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}
	systemBinaryNames := config.systemBinaryNames
	if systemBinaryNames == nil {
		systemBinaryNames = []string{config.binaryName}
	}
	for _, systemBinaryName := range systemBinaryNames {
		if commandExists(systemBinaryName) {
			return systemBinaryName
		}
	}
	return ""
}

// getLatestVersion fetches the latest release version from GitHub (pi:
// getLatestVersion).
func getLatestVersion(ctx context.Context, repo string) (string, error) {
	timeout := int64(networkTimeoutMs)
	resp, err := FetchWithRetry(ctx, http.MethodGet,
		fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo),
		map[string]string{"User-Agent": codingagent.AppName + "-coding-agent"},
		&FetchRetryOptions{TimeoutMs: &timeout})
	if err != nil {
		return "", err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}
	tag := extractJSONString(string(body), "tag_name")
	if tag == "" {
		return "", fmt.Errorf("GitHub API error: missing tag_name")
	}
	return strings.TrimPrefix(tag, "v"), nil
}

// extractJSONString reads one top-level string field without a full JSON
// struct (the only field consumed is tag_name).
func extractJSONString(body string, field string) string {
	key := "\"" + field + "\""
	idx := strings.Index(body, key)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(key):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	rest = strings.TrimLeft(rest[colon+1:], " \t\r\n")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return ""
	}
	return rest[1 : 1+end]
}

// downloadFile downloads url to dest (pi: downloadFile).
func downloadFile(ctx context.Context, url string, dest string) error {
	timeout := int64(downloadTimeoutMs)
	resp, err := FetchWithRetry(ctx, http.MethodGet, url, nil, &FetchRetryOptions{TimeoutMs: &timeout})
	if err != nil {
		return err
	}
	defer resp.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Failed to download: %d", resp.StatusCode)
	}
	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}

// findBinaryRecursively walks a directory tree looking for the binary (pi:
// findBinaryRecursively).
func findBinaryRecursively(rootDir string, binaryFileName string) string {
	stack := []string{rootDir}
	for len(stack) > 0 {
		currentDir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(currentDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			fullPath := filepath.Join(currentDir, entry.Name())
			if entry.Type().IsRegular() && entry.Name() == binaryFileName {
				return fullPath
			}
			if entry.IsDir() {
				stack = append(stack, fullPath)
			}
		}
	}
	return ""
}

// runExtractionCommand runs an extraction command and reports the failure as
// a message (pi: runExtractionCommand); "" = success.
func runExtractionCommand(command string, args []string) string {
	cmd := exec.Command(command, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("%s: %s", command, FormatSpawnFailure(err, stderr.String(), "", 0))
	}
	return ""
}

// extractTarGzArchive extracts a tar.gz archive (pi: extractTarGzArchive).
func extractTarGzArchive(archivePath string, extractDir string, assetName string) error {
	if failure := runExtractionCommand("tar", []string{"xzf", archivePath, "-C", extractDir}); failure != "" {
		return fmt.Errorf("Failed to extract %s: %s", assetName, failure)
	}
	return nil
}

// getWindowsTarCommand prefers the System32 bsdtar (pi: getWindowsTarCommand).
func getWindowsTarCommand() string {
	for _, envKey := range []string{"SystemRoot", "WINDIR"} {
		if systemRoot := os.Getenv(envKey); systemRoot != "" {
			systemTar := filepath.Join(systemRoot, "System32", "tar.exe")
			if _, err := os.Stat(systemTar); err == nil {
				return systemTar
			}
		}
	}
	return "tar.exe"
}

// extractZipArchive extracts a zip archive (pi: extractZipArchive).
func extractZipArchive(archivePath string, extractDir string, assetName string) error {
	var failures []string
	if runtime.GOOS == "windows" {
		// Windows ships bsdtar as tar.exe, which supports zip files. Prefer
		// the System32 binary over Git Bash's GNU tar, which does not handle
		// zip archives.
		if failure := runExtractionCommand(getWindowsTarCommand(), []string{"xf", archivePath, "-C", extractDir}); failure == "" {
			return nil
		} else {
			failures = append(failures, failure)
		}
		script := "& { param($archive, $destination) $ErrorActionPreference = 'Stop'; Expand-Archive -LiteralPath $archive -DestinationPath $destination -Force }"
		powershellFailure := runExtractionCommand("powershell.exe", []string{
			"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
			script, archivePath, extractDir,
		})
		if powershellFailure == "" {
			return nil
		}
		failures = append(failures, powershellFailure)
	} else {
		if failure := runExtractionCommand("unzip", []string{"-q", archivePath, "-d", extractDir}); failure == "" {
			return nil
		} else {
			failures = append(failures, failure)
		}
		if failure := runExtractionCommand("tar", []string{"xf", archivePath, "-C", extractDir}); failure == "" {
			return nil
		} else {
			failures = append(failures, failure)
		}
	}
	return fmt.Errorf("Failed to extract %s: %s", assetName, strings.Join(failures, "; "))
}

// extractZipGoFallback is not used; zip extraction shells out per pi.

// downloadTool downloads and installs a tool (pi: downloadTool).
func downloadTool(ctx context.Context, tool string) (string, error) {
	config, ok := toolsCatalog[tool]
	if !ok {
		return "", fmt.Errorf("Unknown tool: %s", tool)
	}
	plat := goPlatform()
	architecture := goArch()

	version, err := getLatestVersion(ctx, config.repo)
	if err != nil {
		return "", err
	}
	if tool == "fd" && plat == "darwin" && architecture == "x64" {
		version = "10.3.0"
	}

	assetName := config.getAssetName(version, plat, architecture)
	if assetName == "" {
		return "", fmt.Errorf("Unsupported platform: %s/%s", plat, architecture)
	}

	if err := os.MkdirAll(toolsDir(), 0o755); err != nil {
		return "", err
	}

	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s%s/%s", config.repo, config.tagPrefix, version, assetName)
	archivePath := filepath.Join(toolsDir(), assetName)
	binaryExt := ""
	if plat == "win32" {
		binaryExt = ".exe"
	}
	binaryPath := filepath.Join(toolsDir(), config.binaryName+binaryExt)

	if err := downloadFile(ctx, downloadURL, archivePath); err != nil {
		return "", err
	}

	// Extract into a unique temp directory. fd and rg downloads can run
	// concurrently during startup, so sharing a fixed directory causes
	// races.
	extractDir := filepath.Join(toolsDir(), fmt.Sprintf("extract_tmp_%s_%d_%d_%s", config.binaryName, os.Getpid(), timeNowUnixMilli(), randomSuffix(8)))
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(extractDir)
	defer os.Remove(archivePath)

	switch {
	case strings.HasSuffix(assetName, ".tar.gz"):
		if err := extractTarGzArchive(archivePath, extractDir, assetName); err != nil {
			return "", err
		}
	case strings.HasSuffix(assetName, ".zip"):
		if err := extractZipArchive(archivePath, extractDir, assetName); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("Unsupported archive format: %s", assetName)
	}

	// Find the binary in extracted files. Some archives contain files
	// directly at root, others nest under a versioned subdirectory.
	binaryFileName := config.binaryName + binaryExt
	extractedDir := filepath.Join(extractDir, trimArchiveExtension(assetName))
	extractedBinary := ""
	for _, candidate := range []string{filepath.Join(extractedDir, binaryFileName), filepath.Join(extractDir, binaryFileName)} {
		if _, err := os.Stat(candidate); err == nil {
			extractedBinary = candidate
			break
		}
	}
	if extractedBinary == "" {
		extractedBinary = findBinaryRecursively(extractDir, binaryFileName)
	}
	if extractedBinary == "" {
		return "", fmt.Errorf("Binary not found in archive: expected %s under %s", binaryFileName, extractDir)
	}
	if err := os.Rename(extractedBinary, binaryPath); err != nil {
		return "", err
	}

	// Make executable (Unix only)
	if plat != "win32" {
		if err := os.Chmod(binaryPath, 0o755); err != nil {
			return "", err
		}
	}
	return binaryPath, nil
}

// timeNowUnixMilli returns the current unix time in millis.
func timeNowUnixMilli() int64 { return time.Now().UnixMilli() }

// randomSuffix produces a short base36 random suffix (pi:
// Math.random().toString(36).slice(2, 10)).
func randomSuffix(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}

// trimArchiveExtension strips .tar.gz/.zip (pi: assetName.replace(/\.(tar\.gz|zip)$/, "")).
func trimArchiveExtension(assetName string) string {
	for _, ext := range []string{".tar.gz", ".zip"} {
		if strings.HasSuffix(assetName, ext) {
			return strings.TrimSuffix(assetName, ext)
		}
	}
	return assetName
}

// termuxPackages are the Termux package names for the tools (pi:
// TERMUX_PACKAGES).
var termuxPackages = map[string]string{"fd": "fd", "rg": "ripgrep"}

// ToolStatus is one progress report (pi: ToolStatus).
type ToolStatus struct {
	// Type is "info" or "warning".
	Type string
	// Message is the progress message.
	Message string
}

// EnsureTool ensures a tool is available, downloading if necessary (pi:
// ensureTool). Progress reports go through onStatus; "" = undefined when
// unavailable.
func EnsureTool(ctx context.Context, tool string, onStatus func(status ToolStatus)) string {
	if existingPath := GetToolPath(tool); existingPath != "" {
		return existingPath
	}
	config, ok := toolsCatalog[tool]
	if !ok {
		return ""
	}
	report := func(status ToolStatus) {
		if onStatus != nil {
			onStatus(status)
		}
	}
	if isOfflineModeEnabled() {
		report(ToolStatus{Type: "warning", Message: fmt.Sprintf("%s not found. Offline mode enabled, skipping download.", config.name)})
		return ""
	}
	// On Android/Termux, Linux binaries don't work due to Bionic libc
	// incompatibility. Users must install via pkg.
	if runtime.GOOS == "android" {
		pkgName := tool
		if name, ok := termuxPackages[tool]; ok {
			pkgName = name
		}
		report(ToolStatus{Type: "warning", Message: fmt.Sprintf("%s not found. Install with: pkg install %s", config.name, pkgName)})
		return ""
	}
	report(ToolStatus{Type: "info", Message: fmt.Sprintf("%s not found. Downloading...", config.name)})
	path, err := downloadTool(ctx, tool)
	if err != nil {
		report(ToolStatus{Type: "warning", Message: fmt.Sprintf("Failed to download %s: %v", config.name, err)})
		return ""
	}
	report(ToolStatus{Type: "info", Message: fmt.Sprintf("%s installed to %s", config.name, path)})
	return path
}
