// pi: src/config.ts — install/package detection, package asset paths, app
// config constants, and user config paths.
//
// Segment deviations:
//   - APP_NAME / PACKAGE_NAME / VERSION: pi reads pkg.piConfig from
//     package.json (build-time injection); pig is a Go binary with no
//     package.json — the constants are pinned. VERSION ldflags injection
//     arrives with the packaging batch (Phase 8) if ever needed.
//   - Package Detection (isBunBinary/isBunRuntime) and the whole Install
//     Method / self-update segment (detectInstallMethod, getSelfUpdateCommand,
//     getUpdateInstruction, ...) are Node package-manager machinery (npm /
//     pnpm / yarn / bun) with no Go counterpart: pig ships as a single static
//     binary, where "update" means re-downloading from releases. Deferred to
//     the packaging batch; the segment position is kept below.
//   - getPackageDir: pi distinguishes Bun binary (dirname(execPath)) vs Node
//     (findNodePackageDir(__dirname)); pig is always a compiled binary →
//     dirname(os.Executable()), with the PI_PACKAGE_DIR override kept. The
//     src/dist distinction in getThemesDir/getExportTemplateDir/
//     getInteractiveAssetsDir collapses to the Bun-binary branch shape
//     (assets shipped next to the executable).
//   - process.env access is direct os.Getenv.
package codingagent

import (
	"os"
	"path/filepath"
)

// =============================================================================
// Package Detection
// =============================================================================

// pi: isBunBinary / isBunRuntime — Bun compiled-binary and runtime detection.
// No Go counterpart: pig is always a compiled binary. Segment position kept
// (see header); the deferred self-update segment keyed off these flags.

// =============================================================================
// Install Method Detection (deferred)
// =============================================================================

// pi's InstallMethod / SelfUpdateCommand / detectInstallMethod /
// getSelfUpdateCommand / getSelfUpdateUnavailableInstruction /
// getUpdateInstruction segment is deferred: it exists to compute npm/pnpm/
// yarn/bun reinstall command lines for a Node-hosted install. A Go binary has
// no package-manager-managed install; a release-downloader self-update (if
// any) arrives with the packaging batch (Phase 8). Callers arrive with the
// CLI batches and use the deferred segment's position as reference.

// =============================================================================
// Package Asset Paths (shipped with executable)
// =============================================================================

// FindNodePackageDir returns the base directory for resolving package assets
// (pi: findNodePackageDir). Kept for parity: walks up from startDir for a
// package.json, skipping Bun binary metadata copied into dist/. A Go binary
// typically has no package.json anywhere, so this returns startDir.
func FindNodePackageDir(startDir string) string {
	dir := startDir
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			// build:binary places Bun's metadata inside dist/. Node still needs
			// the package root so its dist-relative asset paths do not become
			// dist/dist/.
			if filepath.Base(dir) == "dist" {
				if _, err := os.Stat(filepath.Join(parent, "package.json")); err == nil {
					return parent
				}
			}
			return dir
		}
		dir = parent
	}
	return startDir
}

// GetPackageDir returns the directory the executable ships in (pi:
// getPackageDir). PI_PACKAGE_DIR overrides (useful for Nix/Guix where store
// paths tokenize poorly).
func GetPackageDir() string {
	if envDir := os.Getenv("PI_PACKAGE_DIR"); envDir != "" {
		return NormalizePackageDir(envDir)
	}
	if execPath, err := os.Executable(); err == nil {
		return filepath.Dir(execPath)
	}
	return "."
}

// NormalizePackageDir normalizes a package dir override (pi:
// normalizePath(envDir) → utils/paths.ts; pig uses the shared tilde/URL
// expansion).
func NormalizePackageDir(path string) string {
	return ExpandTildePath(path)
}

// GetThemesDir returns the built-in themes directory (pi: getThemesDir,
// Bun-binary branch shape: theme/ next to the executable).
func GetThemesDir() string {
	return filepath.Join(GetPackageDir(), "theme")
}

// GetExportTemplateDir returns the HTML export template directory (pi:
// getExportTemplateDir, Bun-binary branch shape: export-html/ next to the
// executable).
func GetExportTemplateDir() string {
	return filepath.Join(GetPackageDir(), "export-html")
}

// GetPackageJsonPath returns the path to package.json (pi:
// getPackageJsonPath). pig has no package.json; kept for path parity.
func GetPackageJsonPath() string {
	return filepath.Join(GetPackageDir(), "package.json")
}

// GetReadmePath returns the path to README.md (pi: getReadmePath;
// path.resolve → absolute).
func GetReadmePath() string {
	path, _ := filepath.Abs(filepath.Join(GetPackageDir(), "README.md"))
	return path
}

// GetDocsPath returns the path to the docs directory (pi: getDocsPath).
func GetDocsPath() string {
	path, _ := filepath.Abs(filepath.Join(GetPackageDir(), "docs"))
	return path
}

// GetExamplesPath returns the path to the examples directory (pi:
// getExamplesPath).
func GetExamplesPath() string {
	path, _ := filepath.Abs(filepath.Join(GetPackageDir(), "examples"))
	return path
}

// GetChangelogPath returns the path to CHANGELOG.md (pi: getChangelogPath).
func GetChangelogPath() string {
	path, _ := filepath.Abs(filepath.Join(GetPackageDir(), "CHANGELOG.md"))
	return path
}

// GetInteractiveAssetsDir returns the built-in interactive assets directory
// (pi: getInteractiveAssetsDir, Bun-binary branch shape: assets/ next to the
// executable).
func GetInteractiveAssetsDir() string {
	return filepath.Join(GetPackageDir(), "assets")
}

// GetBundledInteractiveAssetPath returns the path to one bundled interactive
// asset (pi: getBundledInteractiveAssetPath).
func GetBundledInteractiveAssetPath(name string) string {
	return filepath.Join(GetInteractiveAssetsDir(), name)
}

// =============================================================================
// App Config (from package.json piConfig in pi; pinned in pig)
// =============================================================================

// PackageName is the package identity (pi: PACKAGE_NAME = pkg.name ||
// "@earendil-works/pi-coding-agent"; pig pins the pi value — the Go module
// has no npm name).
const PackageName = "@earendil-works/pi-coding-agent"

// AppName is the product name (pi: APP_NAME = piConfig.name || "pi").
const AppName = "pi"

// AppTitle is the display title (pi: APP_TITLE = piConfig.name ? APP_NAME :
// "π"; pig has no piConfig rename hook → the "π" branch).
const AppTitle = "π"

// ConfigDirName is the per-project config directory (pi: CONFIG_DIR_NAME =
// piConfig.configDir || ".pi"; pig deviates: ".pig" — per user decision
// 2026-09-10, pig uses its own config dir instead of sharing pi's).
const ConfigDirName = ".pig"

// Version is the product version (pi: VERSION = pkg.version || "0.0.0";
// ldflags injection arrives with the packaging batch).
const Version = "0.0.0"

// EnvAgentDir is the env var overriding the agent config dir (pi:
// ENV_AGENT_DIR = `${APP_NAME.toUpperCase()}_CODING_AGENT_DIR`; pig deviates:
// PIG_ prefix — no data migration, pig has its own config dir).
const EnvAgentDir = "PIG_CODING_AGENT_DIR"

// EnvSessionDir is the env var overriding the session dir (pi:
// ENV_SESSION_DIR = `${APP_NAME.toUpperCase()}_CODING_AGENT_SESSION_DIR`).
const EnvSessionDir = "PIG_CODING_AGENT_SESSION_DIR"

// ExpandTildePath expands a leading ~ (pi: expandTildePath → normalizePath's
// tilde expansion).
func ExpandTildePath(path string) string {
	return expandTilde(path)
}

const defaultShareViewerURL = "https://pi.dev/session/"

// GetShareViewerURL returns the share viewer URL for a gist ID (pi:
// getShareViewerUrl).
func GetShareViewerURL(gistID string) string {
	baseURL := os.Getenv("PI_SHARE_VIEWER_URL")
	if baseURL == "" {
		baseURL = defaultShareViewerURL
	}
	return baseURL + "#" + gistID
}

// =============================================================================
// User Config Paths (~/.pi/agent/*)
// =============================================================================

// GetAgentDir returns the agent config directory (pi: getAgentDir).
func GetAgentDir() string {
	if envDir := os.Getenv(EnvAgentDir); envDir != "" {
		return ExpandTildePath(envDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(home, ConfigDirName, "agent")
}

// GetCustomThemesDir returns the user's custom themes directory (pi:
// getCustomThemesDir).
func GetCustomThemesDir() string {
	return filepath.Join(GetAgentDir(), "themes")
}

// GetModelsPath returns the path to models.json (pi: getModelsPath).
func GetModelsPath() string {
	return filepath.Join(GetAgentDir(), "models.json")
}

// GetAuthPath returns the path to auth.json (pi: getAuthPath).
func GetAuthPath() string {
	return filepath.Join(GetAgentDir(), "auth.json")
}

// GetSettingsPath returns the path to settings.json (pi: getSettingsPath).
func GetSettingsPath() string {
	return filepath.Join(GetAgentDir(), "settings.json")
}

// GetToolsDir returns the path to the tools directory (pi: getToolsDir).
func GetToolsDir() string {
	return filepath.Join(GetAgentDir(), "tools")
}

// GetBinDir returns the managed binaries directory (pi: getBinDir).
func GetBinDir() string {
	return filepath.Join(GetAgentDir(), "bin")
}

// GetPromptsDir returns the path to the prompt templates directory (pi:
// getPromptsDir).
func GetPromptsDir() string {
	return filepath.Join(GetAgentDir(), "prompts")
}

// GetSessionsDir returns the root directory holding all per-project session
// directories (pi: getSessionsDir).
func GetSessionsDir() string {
	return filepath.Join(GetAgentDir(), "sessions")
}

// GetDebugLogPath returns the path to the debug log file (pi:
// getDebugLogPath).
func GetDebugLogPath() string {
	return filepath.Join(GetAgentDir(), AppName+"-debug.log")
}

// expandTilde expands a leading `~`/`~/` to the home directory (pi:
// normalizePath's tilde branch).
func expandTilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if len(path) >= 2 && path[0] == '~' && (path[1] == '/') {
		return filepath.Join(home, path[2:])
	}
	return path
}
