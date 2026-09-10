// pi: src/core/project-trust.ts — app mode type and project trust
// resolution.
//
// Segment deviations:
//   - The extension project_trust event branch (pi: emitProjectTrustEvent
//     from extensions/runner.ts with LoadExtensionsResult and
//     ProjectTrustContext) is deferred to the extensions batch — the
//     extensions runtime does not exist yet. The options field
//     ExtensionsResult is declared but always nil until then.
//   - ProjectTrustContext (pi: extensions/types.ts, carrying ui.select and
//     hasUI) is deferred with the extensions batch; pig takes the
//     hasUI bit as ProjectTrustHasUI. The interactive select branch runs
//     only when that bit is set (and errors until the UI surface exists) —
//     for the headless/print path pi also returns false here.
package core

import (
	"fmt"

	"github.com/ZoroSpace/pig/codingagent"
)

// AppMode selects the coding-agent mode (pi: AppMode = "interactive" |
// "print" | "json" | "rpc").
type AppMode = string

// App mode values (pi: declaration order kept).
const (
	AppModeInteractive AppMode = "interactive"
	AppModePrint       AppMode = "print"
	AppModeJSON        AppMode = "json"
	AppModeRPC         AppMode = "rpc"
)

// ResolveProjectTrustedOptions carries the resolveProjectTrusted options
// (pi: ResolveProjectTrustedOptions).
type ResolveProjectTrustedOptions struct {
	Cwd                 string
	TrustStore          *ProjectTrustStore
	TrustOverride       *bool
	DefaultProjectTrust ProjectTrustSetting
	// ExtensionsResult is pi's LoadExtensionsResult for the project_trust
	// extension event; always nil until the extensions batch lands (declared
	// above).
	ExtensionsResult any
	// ProjectTrustHasUI is pi's projectTrustContext.hasUI.
	ProjectTrustHasUI bool
	// OnExtensionError receives extension event errors (unused until the
	// extensions branch lands).
	OnExtensionError func(message string)
}

// ProjectTrustSetting is the defaultProjectTrust setting value (pi:
// DefaultProjectTrust from settings-manager.ts).
type ProjectTrustSetting = DefaultProjectTrust

// formatProjectTrustPrompt renders the trust prompt (pi:
// formatProjectTrustPrompt).
func formatProjectTrustPrompt(cwd string) string {
	return fmt.Sprintf("Trust project folder?\n%s\n\nThis allows %s to load %s settings and resources, install missing project packages, and execute project extensions.",
		cwd, codingagent.AppName, codingagent.ConfigDirName)
}

// pi: selectProjectTrustOption + saveProjectTrustPromptResult — the
// interactive trust prompt. Deferred with the UI surface (see header); the
// option objects themselves are available via GetProjectTrustOptions.

// ResolveProjectTrusted resolves whether the project at cwd is trusted (pi:
// resolveProjectTrusted; async throws → bool + error).
func ResolveProjectTrusted(options ResolveProjectTrustedOptions) (bool, error) {
	if options.TrustOverride != nil {
		return *options.TrustOverride, nil
	}
	if !HasTrustRequiringProjectResources(options.Cwd) {
		return true, nil
	}

	// pi: extensions project_trust event branch — deferred with the
	// extensions batch (header declaration). With no extensions runtime the
	// flow continues to the stored decision exactly as pi does when no
	// extension answers.

	if options.TrustStore != nil {
		decision, err := options.TrustStore.Get(options.Cwd)
		if err != nil {
			return false, err
		}
		if decision != nil {
			return *decision, nil
		}
	}

	switch options.DefaultProjectTrust {
	case DefaultProjectTrustAlways:
		return true, nil
	case DefaultProjectTrustNever:
		return false, nil
	case DefaultProjectTrustAsk, "":
	}

	// pi: interactive select branch — requires projectTrustContext.hasUI.
	// The headless path (hasUI=false) returns false; the interactive UI
	// select arrives with the TUI batch.
	if !options.ProjectTrustHasUI {
		return false, nil
	}
	_ = formatProjectTrustPrompt(options.Cwd)
	return false, fmt.Errorf("interactive project trust selection is not available yet (deferred to the UI batch)")
}
