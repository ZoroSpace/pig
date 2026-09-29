// interactive_mode_rebind_footer_test.go — regression for the /resume footer
// staleness bug (docs/fix/2026-09-29-footer-stale-after-session-replacement.md).
package interactive

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// rebindTestTUI extends the render stub with the UI surface
// applyRuntimeSettings / updateTerminalTitle touch.
type rebindTestTUI struct {
	stubRequestRenderTUI
}

func (s *rebindTestTUI) SetShowHardwareCursor(bool) {}
func (s *rebindTestTUI) SetClearOnShrink(bool)      {}
func (s *rebindTestTUI) Terminal() tui.Terminal     { return tui.NewVirtualTerminal(80, 24) }

// sessionWithModel builds a test session whose active model is
// provider/modelID (requires the provider to be registered first).
func sessionWithModel(t *testing.T, modelID string) *core.AgentSession {
	t.Helper()
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	registerTestProvider(t, session, "testprov", []core.ProviderConfigInputModel{{
		ID: modelID, Name: modelID, API: "openai-completions", BaseURL: "http://localhost:0",
	}})
	model, ok := session.GetModelRuntime().GetModel("testprov", modelID)
	if !ok {
		t.Fatalf("test model %q not registered", modelID)
	}
	if err := session.SetModel(context.Background(), &model, nil); err != nil {
		t.Fatalf("SetModel(%s): %v", modelID, err)
	}
	return session
}

// TestRebindCurrentSessionRebindsFooterAfterSessionReplacement locks the
// /resume bug: rebindCurrentSession must re-point the footer at the
// replacement session. Before the fix the footer kept rendering the previous
// session, so after /resume the right-hand model froze on the old value while
// /model switches actually applied to the live session.
//
// This locks both halves of the fix: rebindCurrentSession calling
// applyRuntimeSettings (pi parity) and FooterComponent.SetSession rebuilding
// its view (Render reads through that seam, unlike pi which reads
// this.session directly).
func TestRebindCurrentSessionRebindsFooterAfterSessionReplacement(t *testing.T) {
	theme.SetGlobalTheme(newSubmitTestTheme(t))

	sessionA := sessionWithModel(t, "model-a")
	sessionB := sessionWithModel(t, "model-b")

	m := newSeamTestMode(sessionA, nil)
	m.keybindings = core.CreateKeybindingsManager("")
	m.defaultEditor = newSeamTestEditor()
	m.editor = m.defaultEditor
	m.statusContainer = &tui.Container{}
	m.ui = &rebindTestTUI{}
	m.footerDataProviderView = core.NewFooterDataProvider(sessionA.SessionManager.GetCwd())
	m.footer = components.NewFooterComponent(sessionA, m.footerDataProviderView)

	if rendered := strings.Join(m.footer.Render(120), "\n"); !strings.Contains(rendered, "model-a") {
		t.Fatalf("footer before swap = %q, want model-a", rendered)
	}

	// The runtime replaces the session before the rebind hook runs (what
	// /resume, /new, /fork and /import all do).
	m.runtimeHost = core.NewAgentSessionRuntime(sessionB, nil, nil, nil, "")
	if err := m.rebindCurrentSession(false); err != nil {
		t.Fatalf("rebindCurrentSession: %v", err)
	}

	rendered := strings.Join(m.footer.Render(120), "\n")
	if !strings.Contains(rendered, "model-b") {
		t.Fatalf("footer after rebind = %q, want model-b (stale view keeps the old session)", rendered)
	}
	if strings.Contains(rendered, "model-a") {
		t.Fatalf("footer after rebind = %q, still shows the replaced session's model", rendered)
	}
}
