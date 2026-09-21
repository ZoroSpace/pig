// pi: src/modes/interactive/bug-report.ts — the `/bug` flow: consent,
// optional model-written summary, then upload or zip export (pi v0.86.0).
// pig concurrency: the flow runs on the caller's goroutine inside
// mutateUI-serialized steps; dialog callbacks cross back through channels
// (pi resolves Promises from event-loop callbacks).
package interactive

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZoroSpace/pig/codingagent/cli"
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// bugReportDisclaimer is the consent description (pi: DISCLAIMER — pi names
// the product "Pi"; pig reports itself).
const bugReportDisclaimer = "This report goes to the Pi developers (Earendil) and is not shared publicly. It includes your pi version, operating system, the current model and provider configuration (without API keys), loaded extensions, settings, and provider error diagnostics from this session."

// bugReportTranscriptNote describes the transcript attachment (pi:
// TRANSCRIPT_NOTE).
const bugReportTranscriptNote = "The transcript contains your messages, model output, tool calls and their results, including file contents and command output read during this session."

// bugReportOptions is the promptForOptions result (pi: BugReportOptions).
type bugReportOptions struct {
	hint           string // "" = undefined
	includeSession bool
	includeSummary bool
	delivery       string // "upload" | "zip"
}

// handleBugCommand dispatches /bug (pi: handleBugCommand).
func (m *InteractiveMode) handleBugCommand(ctx context.Context, hint string) {
	m.reportBug(ctx, bugReportContext{
		session:         m.session(),
		showStatus:      m.showStatus,
		showError:       m.ShowError,
		editorContainer: m.editorContainer,
		editor:          m.editor,
		keybindings:     m.keybindings,
		ui:              m.ui,
	}, hint)
}

// bugReportContext is the reportBug context (pi: BugReportContext). editor
// is the active editor component; the overlay swap uses the container.
type bugReportContext struct {
	session         *core.AgentSession
	showStatus      func(string)
	showError       func(string)
	editorContainer *tui.Container
	editor          tui.Component
	keybindings     *core.KeybindingsManager
	ui              tui.TUI
}

// reportBug runs the /bug flow (pi: reportBug).
func (m *InteractiveMode) reportBug(ctx context.Context, bc bugReportContext, initialHint string) {
	options, ok := m.bugReportPromptForOptions(bc, initialHint)
	if !ok {
		bc.showStatus("Bug report cancelled")
		return
	}

	summary := ""
	if options.includeSummary {
		loader := m.bugReportShowLoader(bc, fmt.Sprintf("Writing summary with %s...", bugReportModelName(bc.session))+"...")
		summarizeCtx := loader.Signal()
		var summaryErr error
		summary, summaryErr = bc.session.SummarizeForBugReport(summarizeCtx, &core.BugReportSummaryOptions{Hint: options.hint})
		aborted := summarizeCtx.Err() != nil
		m.bugReportRestoreEditor(bc, loader)
		if summaryErr != nil {
			if aborted {
				bc.showStatus("Bug report cancelled")
			} else {
				bc.showError(fmt.Sprintf("Failed to write bug report summary: %s", errorMessageOf(summaryErr)))
			}
			return
		}
		if aborted {
			bc.showStatus("Bug report cancelled")
			return
		}
	}

	bundle, buildErr := m.bugReportBuildBundle(bc.session, options, summary)
	if buildErr != nil {
		bc.showError(fmt.Sprintf("Failed to build bug report: %s", errorMessageOf(buildErr)))
		return
	}

	if options.delivery == "upload" {
		failure, uploaded := m.bugReportUpload(ctx, bc, bundle)
		if uploaded {
			return
		}
		if failure == "" {
			bc.showStatus("Bug report cancelled")
			return
		}
		choice, ok := m.bugReportChoose(bc, "Upload failed",
			[]string{"Export as Zip", "Cancel"},
			fmt.Sprintf("%s\n\nExport the report as a zip archive instead?", failure))
		if !ok || choice != "Export as Zip" {
			bc.showStatus("Bug report cancelled")
			return
		}
	}
	m.bugReportExportZip(bc, bundle)
}

// bugReportModelName is the display model name (pi: session.model?.name ??
// "the current model").
func bugReportModelName(session *core.AgentSession) string {
	if model := session.Model(); model != nil {
		return model.Name
	}
	return "the current model"
}

// bugReportPromptForOptions runs the consent dialogs (pi: promptForOptions).
// ok=false means cancelled.
func (m *InteractiveMode) bugReportPromptForOptions(bc bugReportContext, initialHint string) (bugReportOptions, bool) {
	hint, ok := m.bugReportInput(bc, "Report a bug",
		bugReportDisclaimer+"\n\nWhat went wrong? (optional)", initialHint)
	if !ok {
		return bugReportOptions{}, false
	}
	transcript, ok := m.bugReportChoose(bc, "Include the session transcript?",
		[]string{"Yes, include the transcript", "No"}, bugReportTranscriptNote)
	if !ok {
		return bugReportOptions{}, false
	}
	includeSession := transcript != "No"
	includeSummary := false
	if !includeSession {
		provider := "your provider"
		if model := bc.session.Model(); model != nil {
			provider = string(model.Provider)
		}
		summary, ok := m.bugReportChoose(bc,
			fmt.Sprintf("Attach a summary written by %s instead?", bugReportModelName(bc.session)),
			[]string{"Yes, generate a summary", "No"},
			fmt.Sprintf("The transcript is sent to %s with your credentials and tokens. Only the generated summary is attached; the transcript stays on your machine.", provider))
		if !ok {
			return bugReportOptions{}, false
		}
		includeSummary = summary != "No"
	}
	description := strings.TrimSpace(hint)
	delivery, ok := m.bugReportChoose(bc, "Bug report",
		[]string{"Upload Report", "Export as Zip", "Cancel"},
		fmt.Sprintf("Description: %s\nTranscript: %s\nSummary: %s\n\nUpload sends the report to %s. Export writes a zip archive to the current directory instead.",
			orNone(description),
			orIncluded(includeSession),
			summaryText(includeSummary, bugReportModelName(bc.session)),
			bugReportUploadHost()))
	if !ok || delivery == "Cancel" {
		return bugReportOptions{}, false
	}
	opts := bugReportOptions{
		hint:           description,
		includeSession: includeSession,
		includeSummary: includeSummary,
		delivery:       "zip",
	}
	if delivery == "Upload Report" {
		opts.delivery = "upload"
	}
	return opts, true
}

func orNone(text string) string {
	if text == "" {
		return "none"
	}
	return text
}

func orIncluded(include bool) string {
	if include {
		return "included"
	}
	return "not included"
}

func summaryText(include bool, modelName string) string {
	if include {
		return "written by " + modelName
	}
	return "none"
}

// bugReportUploadHost is the gateway host for the consent text (pi: new
// URL(getRadiusGatewayUrl()).host).
func bugReportUploadHost() string {
	parsed, err := url.Parse(core.GetRadiusGatewayUrl())
	if err != nil {
		return core.GetRadiusGatewayUrl()
	}
	return parsed.Host
}

// bugReportBuildBundle assembles the report (pi: buildBundle).
func (m *InteractiveMode) bugReportBuildBundle(session *core.AgentSession, options bugReportOptions, summary string) (*core.BugReportBundle, error) {
	extensions := session.GetResourceLoader().GetExtensions()
	extensionErrors := make([]core.LoadExtensionError, 0, len(extensions.Errors))
	extensionErrors = append(extensionErrors, extensions.Errors...)
	loadedExtensions := make([]*core.Extension, 0, len(extensions.Extensions))
	loadedExtensions = append(loadedExtensions, extensions.Extensions...)

	sessionJsonl := ""
	if options.includeSession {
		serialized, err := core.SerializeSessionBranch(session.SessionManager, func(parentID *string, timestamp string) []any {
			return createShareTrailingEntries(session, parentID, timestamp)
		})
		if err != nil {
			return nil, err
		}
		sessionJsonl = serialized
	}
	return &core.BugReportBundle{
		Metadata: core.CollectBugReportMetadata(core.CollectBugReportMetadataOptions{
			Hint:            options.hint,
			SessionID:       session.SessionID(),
			Cwd:             session.SessionManager.GetCwd(),
			IncludeSession:  options.includeSession,
			IncludeSummary:  summary != "",
			MessageCount:    len(session.Messages()),
			Model:           session.Model(),
			ModelRuntime:    session.GetModelRuntime(),
			ThinkingLevel:   session.ThinkingLevel(),
			Extensions:      loadedExtensions,
			ExtensionErrors: extensionErrors,
			GlobalSettings:  session.SettingsManager.GetGlobalSettings(),
			ProjectSettings: session.SettingsManager.GetProjectSettings(),
		}),
		Diagnostics:  core.CollectBugReportDiagnostics(session.SessionManager, core.ReadCrashLog("")),
		Summary:      summary,
		SessionJsonl: sessionJsonl,
	}, nil
}

// bugReportUpload uploads the bundle (pi: upload). Returns (failure,
// uploaded). failure "" with uploaded=false means cancelled; uploaded=true
// means the report was delivered.
func (m *InteractiveMode) bugReportUpload(ctx context.Context, bc bugReportContext, bundle *core.BugReportBundle) (string, bool) {
	loader := m.bugReportShowLoader(bc, "Uploading bug report...")
	uploadCtx := loader.Signal()

	failure := func() string {
		var token string
		if provider := bc.session.GetModelRuntime().GetProvider(core.RADIUS_PROVIDER_ID); provider != nil {
			authResult, err := bc.session.GetModelRuntime().GetAuth(ctx, core.RADIUS_PROVIDER_ID, &core.ModelRuntimeAuthOverrides{MinOAuthValidityMs: 5 * 60_000})
			if err == nil && authResult != nil {
				token = cli.GetAuthCredential(authResult)
			}
		}
		id, err := core.UploadBugReport(uploadCtx, bundle, core.UploadBugReportOptions{Token: token})
		if err != nil {
			return err.Error()
		}
		m.bugReportRestoreEditor(bc, loader)
		bugReportRecordInSession(bc.session, bundle, "upload", "")
		bc.showStatus(fmt.Sprintf("Bug report uploaded. Report ID: %s", id))
		return ""
	}()

	if failure == "" {
		return "", true
	}
	m.bugReportRestoreEditor(bc, loader)
	if uploadCtx.Err() != nil {
		return "", false // cancelled
	}
	return failure, false
}

// bugReportExportZip writes the archive into the process cwd (pi:
// exportZip).
func (m *InteractiveMode) bugReportExportZip(bc bugReportContext, bundle *core.BugReportBundle) {
	archivePath := filepath.Join(workingDirOrDot(), core.BugReportArchiveFileName(bundle.Metadata.ID))
	if err := core.WriteBugReportArchive(bundle, archivePath); err != nil {
		bc.showError(fmt.Sprintf("Failed to write bug report: %s", errorMessageOf(err)))
		return
	}
	bugReportRecordInSession(bc.session, bundle, "zip", archivePath)
	bc.showStatus(fmt.Sprintf("Bug report exported to: %s\nReport ID: %s", archivePath, bundle.Metadata.ID))
}

// bugReportRecordInSession records the delivery in the session (pi:
// recordInSession).
func bugReportRecordInSession(session *core.AgentSession, bundle *core.BugReportBundle, delivery string, path string) {
	var hint *string
	if bundle.Metadata.Hint != nil {
		hint = bundle.Metadata.Hint
	}
	_, _ = session.SessionManager.AppendCustomEntry(core.BugReportCustomEntryType, core.BugReportSessionEntryData{
		ID:              bundle.Metadata.ID,
		CreatedAt:       bundle.Metadata.CreatedAt,
		Hint:            hint,
		SessionIncluded: bundle.Metadata.Session.Included,
		SummaryIncluded: bundle.Metadata.Session.SummaryIncluded,
		Delivery:        delivery,
		Path:            path,
	})
	if len(bundle.Diagnostics.Crashes) > 0 {
		core.ClearCrashLog("")
	}
}

// bugReportInput shows the text prompt overlay (pi: input). ok=false =
// cancelled.
func (m *InteractiveMode) bugReportInput(bc bugReportContext, title string, description string, initialValue string) (string, bool) {
	resultCh := make(chan struct{}, 1)
	var value string
	ok := false
	m.mutateUI(func() {
		bc.editorContainer.Clear()
		// pi v0.86.1: ExtensionInputComponent → ExtensionEditorComponent so
		// pasted multiline descriptions survive (d1230ea20).
		component := components.NewExtensionEditorComponent(
			bc.ui,
			bc.keybindings,
			title,
			initialValue,
			func(submitted string) {
				value = submitted
				ok = true
				resultCh <- struct{}{}
			}, func() {
				resultCh <- struct{}{}
			}, &components.ExtensionEditorOptions{
				Description: description,
			},
			bc.session.SettingsManager.GetExternalEditorCommand(),
		)
		bc.editorContainer.AddChild(component)
		bc.ui.SetFocus(component)
		bc.ui.RequestRender(false)
		go func() {
			<-resultCh
			m.mutateUI(func() {
				bc.editorContainer.Clear()
				bc.editorContainer.AddChild(bc.editor)
				bc.ui.SetFocus(bc.editor)
				bc.ui.RequestRender(false)
			})
		}()
	})
	<-resultCh
	return value, ok
}

// bugReportChoose shows the option list overlay (pi: choose). ok=false =
// cancelled.
func (m *InteractiveMode) bugReportChoose(bc bugReportContext, title string, options []string, description string) (string, bool) {
	resultCh := make(chan struct{}, 1)
	var choice string
	ok := false
	m.mutateUI(func() {
		bc.editorContainer.Clear()
		component := components.NewExtensionSelectorComponent(title, options, func(selected string) {
			choice = selected
			ok = true
			resultCh <- struct{}{}
		}, func() {
			resultCh <- struct{}{}
		}, &components.ExtensionSelectorOptions{
			Tui:         bc.ui,
			Description: description,
		})
		bc.editorContainer.AddChild(component)
		bc.ui.SetFocus(component)
		bc.ui.RequestRender(false)
		go func() {
			<-resultCh
			m.mutateUI(func() {
				bc.editorContainer.Clear()
				bc.editorContainer.AddChild(bc.editor)
				bc.ui.SetFocus(bc.editor)
				bc.ui.RequestRender(false)
			})
		}()
	})
	<-resultCh
	return choice, ok
}

// bugReportShowLoader shows the spinner overlay (pi: showLoader).
func (m *InteractiveMode) bugReportShowLoader(bc bugReportContext, message string) *components.BorderedLoader {
	loader := components.NewBorderedLoader(bc.ui, theme.Current(), message, nil)
	m.mutateUI(func() {
		bc.editorContainer.Clear()
		bc.editorContainer.AddChild(loader)
		bc.ui.SetFocus(loader)
		bc.ui.RequestRender(false)
	})
	return loader
}

// bugReportRestoreEditor puts the editor back (pi: restoreEditor).
func (m *InteractiveMode) bugReportRestoreEditor(bc bugReportContext, component *components.BorderedLoader) {
	m.mutateUI(func() {
		component.Dispose()
		bc.editorContainer.Clear()
		bc.editorContainer.AddChild(bc.editor)
		bc.ui.SetFocus(bc.editor)
		bc.ui.RequestRender(false)
	})
}

func workingDirOrDot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
