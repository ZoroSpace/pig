// settings_submenu.go (pi: components/settings-submenu.ts) — the reusable
// submenu building blocks behind the settings selector: SelectSubmenu
// (single-step titled select list, optionally fuzzy-searchable) and
// SteppedSubmenu (generic N-step selector with a shared selection context).
package components

import (
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

var submenuSelectListLayout = tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: 12,
	MaxPrimaryColumnWidth: 32,
}

// minInt/maxInt are the local min/max helpers (tui's are unexported).
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SelectSubmenuOptions configure a SelectSubmenu (pi: SelectSubmenuOptions).
type SelectSubmenuOptions struct {
	// Searchable enables type-to-search fuzzy filtering.
	Searchable bool
	// Layout overrides the select list layout (column widths). Nil = default.
	Layout *tui.SelectListLayoutOptions
}

// SelectSubmenu is a single-step submenu that shows a titled select list
// (pi: SelectSubmenu extends Container). With Searchable, typing filters the
// list using fuzzy matching.
type SelectSubmenu struct {
	tui.Container

	selectList     *tui.SelectList
	listChildIndex int
	allOptions     []tui.SelectItem
	listLayout     tui.SelectListLayoutOptions
	searchInput    *tui.Input
	onSelectCb     func(value string)
	onCancelCb     func()
	onSelectionCb  func(value string)
	hasSelectionCb bool
	searchable     bool
}

// NewSelectSubmenu builds the submenu (pi: constructor).
func NewSelectSubmenu(
	title string,
	description string,
	options []tui.SelectItem,
	currentValue string,
	onSelect func(value string),
	onCancel func(),
	onSelectionChange func(value string),
	submenuOptions *SelectSubmenuOptions,
) *SelectSubmenu {
	s := &SelectSubmenu{
		allOptions:     options,
		onSelectCb:     onSelect,
		onCancelCb:     onCancel,
		onSelectionCb:  onSelectionChange,
		hasSelectionCb: onSelectionChange != nil,
	}
	if submenuOptions != nil {
		if submenuOptions.Layout != nil {
			s.listLayout = *submenuOptions.Layout
		} else {
			s.listLayout = submenuSelectListLayout
		}
		s.searchable = submenuOptions.Searchable
	} else {
		s.listLayout = submenuSelectListLayout
	}

	currentTheme := theme.Current()

	// Title
	s.AddChild(tui.NewText(currentTheme.Bold(currentTheme.Fg("accent", title)), 0, 0))

	// Description
	if description != "" {
		s.AddChild(tui.NewSpacer(1))
		s.AddChild(tui.NewText(currentTheme.Fg("muted", description), 0, 0))
	}

	// Search input
	if s.searchable {
		s.AddChild(tui.NewSpacer(1))
		s.searchInput = tui.NewInput()
		s.searchInput.OnSubmit = func(string) {
			s.selectList.HandleInput("\r")
		}
		s.AddChild(s.searchInput)
	}

	// Spacer
	s.AddChild(tui.NewSpacer(1))

	// Select list
	s.selectList = s.buildSelectList(options, currentValue)
	s.listChildIndex = len(s.Children)
	s.AddChild(s.selectList)

	// Hint
	s.AddChild(tui.NewSpacer(1))
	hint := "  Enter to select · Esc to go back"
	if s.searchable {
		hint = "  Type to filter · Enter to select · Esc to go back"
	}
	s.AddChild(tui.NewText(currentTheme.Fg("dim", hint), 0, 0))

	return s
}

func (s *SelectSubmenu) buildSelectList(options []tui.SelectItem, preselect string) *tui.SelectList {
	list := tui.NewSelectList(options, minInt(len(options), 10), theme.GetSelectListTheme(), &s.listLayout)

	for i, o := range options {
		if o.Value == preselect {
			list.SetSelectedIndex(i)
			break
		}
	}

	list.OnSelect = func(item tui.SelectItem) { s.onSelectCb(item.Value) }
	list.OnCancel = s.onCancelCb
	if s.hasSelectionCb {
		onSelectionChange := s.onSelectionCb
		list.OnSelectionChange = func(item tui.SelectItem) { onSelectionChange(item.Value) }
	}

	return list
}

func (s *SelectSubmenu) applyFilter(query string) {
	var filtered []tui.SelectItem
	if query != "" {
		filtered = tui.FuzzyFilter(s.allOptions, query, func(item tui.SelectItem) string {
			return item.Label + " " + item.Description
		})
	} else {
		filtered = s.allOptions
	}

	newList := s.buildSelectList(filtered, "")
	s.Children[s.listChildIndex] = newList
	s.selectList = newList
}

// HandleInput implements InputHandler (pi: handleInput).
func (s *SelectSubmenu) HandleInput(data string) {
	if s.searchInput != nil {
		kb := tui.GetKeybindings()
		isNav := kb.Matches(data, "tui.select.up") ||
			kb.Matches(data, "tui.select.down") ||
			kb.Matches(data, "tui.select.confirm") ||
			kb.Matches(data, "tui.select.cancel")
		if isNav {
			s.selectList.HandleInput(data)
		} else {
			s.searchInput.HandleInput(data)
			s.applyFilter(s.searchInput.GetValue())
		}
	} else {
		s.selectList.HandleInput(data)
	}
}

// =============================================================================
// SteppedSubmenu — reusable multi-step selector
// =============================================================================

// SteppedSubmenuStep is one step in a SteppedSubmenu (pi:
// SteppedSubmenuStep). Title/Description are the string variants; the
// function variants (pi: `string | ((context) => string)`) go in TitleFunc /
// DescriptionFunc — exactly one of each pair must be set.
type SteppedSubmenuStep struct {
	// Key is the unique key — the selected value is stored in the result
	// context under this key.
	Key string
	// Title is shown at the top of the step (pi: string variant).
	Title string
	// TitleFunc receives prior selections (pi: function variant).
	TitleFunc func(context map[string]string) string
	// Description is shown below the title (pi: string variant).
	Description string
	// DescriptionFunc receives prior selections (pi: function variant).
	DescriptionFunc func(context map[string]string) string
	// Options builds the option list for this step, fresh each time the step
	// is shown.
	Options func(context map[string]string) []tui.SelectItem
	// Preselect optionally pre-selects a value when entering this step
	// (pi: preselect?; nil/"" = none).
	Preselect func(context map[string]string) string
	// Searchable enables type-to-search fuzzy filtering for this step.
	Searchable bool
	// Layout overrides the select list layout (column widths) for this step.
	Layout *tui.SelectListLayoutOptions
}

// SteppedSubmenuOptions carries pi's SteppedSubmenuOptions.
type SteppedSubmenuOptions struct {
	// StartAtStep starts at this step index (0-based), skipping earlier
	// steps. Requires InitialContext for skipped keys.
	StartAtStep int
	// InitialContext pre-fills selections for skipped steps.
	InitialContext map[string]string
	// Loop loops back to step 0 after completing the last step instead of
	// closing.
	Loop bool
}

// SteppedSubmenu is a generic N-step submenu built on top of SelectSubmenu
// (pi: SteppedSubmenu extends Container).
//
// Each step's options can depend on prior selections via the shared context.
// Esc goes back one step; Esc at step 0 cancels. With Loop, completing the
// final step invokes onComplete then returns to step 0.
type SteppedSubmenu struct {
	steps      []SteppedSubmenuStep
	onComplete func(context map[string]string)
	onCancel   func()
	opts       SteppedSubmenuOptions
	active     tui.Component
	context    map[string]string
}

// NewSteppedSubmenu builds the stepped submenu (pi: constructor; opts
// optional — pass nil for defaults).
func NewSteppedSubmenu(
	steps []SteppedSubmenuStep,
	onComplete func(context map[string]string),
	onCancel func(),
	opts *SteppedSubmenuOptions,
) *SteppedSubmenu {
	s := &SteppedSubmenu{
		steps:      steps,
		onComplete: onComplete,
		onCancel:   onCancel,
	}
	if opts != nil {
		s.opts = *opts
	}
	s.context = map[string]string{}
	for k, v := range s.opts.InitialContext {
		s.context[k] = v
	}
	s.active = s.buildStep(s.opts.StartAtStep)
	return s
}

func (s *SteppedSubmenu) buildStep(stepIndex int) tui.Component {
	step := s.steps[stepIndex]
	total := len(s.steps)
	stepLabel := ""
	if total > 1 {
		stepLabel = "Step " + itoa(stepIndex+1) + "/" + itoa(total) + " · "
	}

	title := step.Title
	if step.TitleFunc != nil {
		title = step.TitleFunc(s.context)
	}
	desc := step.Description
	if step.DescriptionFunc != nil {
		desc = step.DescriptionFunc(s.context)
	}
	items := step.Options(s.context)
	preselect := ""
	if step.Preselect != nil {
		preselect = step.Preselect(s.context)
	}

	var submenuOptions *SelectSubmenuOptions
	if step.Searchable || step.Layout != nil {
		submenuOptions = &SelectSubmenuOptions{Searchable: step.Searchable, Layout: step.Layout}
	}

	return NewSelectSubmenu(
		title,
		stepLabel+desc,
		items,
		preselect,
		func(value string) {
			s.context[step.Key] = value

			if stepIndex < total-1 {
				// Advance to next step
				s.active = s.buildStep(stepIndex + 1)
			} else {
				// Final step — deliver result
				completed := map[string]string{}
				for k, v := range s.context {
					completed[k] = v
				}
				s.onComplete(completed)

				if s.opts.Loop {
					s.context = map[string]string{}
					s.active = s.buildStep(0)
				} else {
					s.onCancel()
				}
			}
		},
		func() {
			if stepIndex > 0 {
				delete(s.context, step.Key)
				s.active = s.buildStep(stepIndex - 1)
			} else {
				s.onCancel()
			}
		},
		nil,
		submenuOptions,
	)
}

// Render implements Component (pi: render delegates to activeComponent).
func (s *SteppedSubmenu) Render(width int) []string {
	return s.active.Render(width)
}

// HandleInput implements InputHandler (pi: handleInput).
func (s *SteppedSubmenu) HandleInput(data string) {
	if handler, ok := s.active.(tui.InputHandler); ok {
		handler.HandleInput(data)
	}
}

// Invalidate implements Component (pi: invalidate).
func (s *SteppedSubmenu) Invalidate() {
	if inv, ok := s.active.(interface{ Invalidate() }); ok {
		inv.Invalidate()
	}
}
