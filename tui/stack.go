// tui/stack.go (pi: packages/tui/src/components/stack.ts)
//
// Stack flex-layout base shared by VStack and HStack (pi: abstract class
// Stack extends Container). Go has no abstract classes: the layoutType is a
// plain field set by the subclass constructors, and Stack itself is only
// embedded.
package tui

import "math"

// StackEntryOptions mirrors pi's StackEntryOptions. Shrink/MaxSize use
// pointers because an explicit 0 differs from absent (defaults 1 / max):
// Grow/MinSize collapse to the same default (0) either way, so plain ints.
type StackEntryOptions struct {
	Basis   any // nil = absent (intrinsic), int = fixed basis, "auto" = intrinsic
	Grow    int
	Shrink  *int
	MinSize int
	MaxSize *int
	Visible func(viewport LayoutViewport) bool
}

// StackEntry is pi's `interface StackEntry extends StackEntryOptions`.
type StackEntry struct {
	StackEntryOptions
	Component Component
}

// StackChild is pi's `Component | StackEntry` union: a bare Component value
// or a *StackEntry built with StackEntryOf.
type StackChild any

// StackEntryOf wraps a component with entry options (pi: the object-literal
// `{ component, ...options }` form in the children array).
func StackEntryOf(component Component, options *StackEntryOptions) *StackEntry {
	e := &StackEntry{Component: component}
	if options != nil {
		e.StackEntryOptions = *options
	}
	return e
}

// StackOptions mirrors pi's StackOptions.
type StackOptions struct {
	Gap   *int
	Align string // "stretch" | "start" | "center" | "end"; "" = default "stretch"
}

// Stack is the abstract base (pi: abstract class Stack).
type Stack struct {
	Container

	entries    []StackLayoutEntry
	gap        int
	align      string
	layoutType string // "vstack" | "hstack"; set by subclass constructors (pi: abstract)
}

// newStack builds the embedded base; layoutType corresponds to pi's abstract
// `protected readonly layoutType` provided by subclasses.
func newStack(layoutType string, children []StackChild, options *StackOptions) Stack {
	gap := 0
	align := "stretch"
	if options != nil {
		if options.Gap != nil {
			gap = normalizeStackSize(*options.Gap, 0)
		}
		if options.Align != "" {
			align = options.Align
		}
	}
	s := Stack{gap: gap, align: align, layoutType: layoutType}
	for _, child := range children {
		if entry, ok := child.(*StackEntry); ok {
			opts := entry.StackEntryOptions
			s.AddChildWithOptions(entry.Component, &opts)
		} else if component, ok := child.(Component); ok {
			s.AddChild(component)
		}
	}
	return s
}

// AddChild overrides Container.AddChild to also register a layout entry
// (pi: addChild override with default options).
func (s *Stack) AddChild(component Component) {
	s.AddChildWithOptions(component, nil)
}

// AddChildWithOptions is pi's addChild(component, options).
func (s *Stack) AddChildWithOptions(component Component, options *StackEntryOptions) {
	s.Container.AddChild(component)
	entry := StackLayoutEntry{
		Component: component,
		Grow:      0,
		Shrink:    1,
		MinSize:   0,
		MaxSize:   math.MaxInt,
	}
	if options != nil {
		entry.Basis = options.Basis
		entry.Grow = normalizeStackSize(options.Grow, 0)
		entry.Shrink = normalizeStackSize(derefInt(options.Shrink, 1), 1)
		entry.MinSize = normalizeStackSize(options.MinSize, 0)
		entry.MaxSize = normalizeStackSize(derefInt(options.MaxSize, math.MaxInt), math.MaxInt)
		entry.Visible = options.Visible
	}
	s.entries = append(s.entries, entry)
}

func (s *Stack) RemoveChild(component Component) {
	s.Container.RemoveChild(component)
	for i, entry := range s.entries {
		if entry.Component == component {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

func (s *Stack) Clear() {
	s.Container.Clear()
	s.entries = nil
}

// LayoutNode implements LayoutComponent (pi: [LAYOUT_NODE]()).
func (s *Stack) LayoutNode() LayoutNode {
	return &StackLayoutNode{
		Type:    s.layoutType,
		Entries: s.entries,
		Gap:     s.gap,
		Align:   s.align,
	}
}

// normalizeStackSize is pi's normalizeSize.
func normalizeStackSize(value int, fallback int) int {
	return max(0, value)
}

func derefInt(v *int, fallback int) int {
	if v == nil {
		return fallback
	}
	return *v
}

// VisibleStackEntries filters entries by their visibility predicates
// (pi: visibleStackEntries).
func VisibleStackEntries(entries []StackLayoutEntry, viewport LayoutViewport) []StackLayoutEntry {
	var result []StackLayoutEntry
	for _, entry := range entries {
		if entry.Visible != nil && !entry.Visible(viewport) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

// clampStackSize is pi's clampSize.
func clampStackSize(size int, entry StackLayoutEntry) int {
	minSize := max(0, entry.MinSize)
	maxSize := max(minSize, entry.MaxSize)
	return max(minSize, min(maxSize, max(0, size)))
}

// distributeStackSpace is pi's distribute.
func distributeStackSpace(sizes []int, entries []StackLayoutEntry, amount int, mode string) {
	remaining := amount
	for remaining > 0 {
		type candidate struct {
			entry StackLayoutEntry
			index int
		}
		var candidates []candidate
		for index, entry := range entries {
			if mode == "grow" {
				if entry.Grow > 0 && sizes[index] < entry.MaxSize {
					candidates = append(candidates, candidate{entry, index})
				}
			} else {
				if entry.Shrink > 0 && sizes[index] > entry.MinSize {
					candidates = append(candidates, candidate{entry, index})
				}
			}
		}
		if len(candidates) == 0 {
			return
		}

		totalWeight := 0
		for _, c := range candidates {
			if mode == "grow" {
				totalWeight += c.entry.Grow
			} else {
				totalWeight += c.entry.Shrink * max(1, sizes[c.index])
			}
		}
		distributed := 0
		for _, c := range candidates {
			if remaining <= 0 {
				break
			}
			var weight int
			if mode == "grow" {
				weight = c.entry.Grow
			} else {
				weight = c.entry.Shrink * max(1, sizes[c.index])
			}
			proposed := max(1, remaining*weight/totalWeight)
			var capacity int
			if mode == "grow" {
				capacity = c.entry.MaxSize - sizes[c.index]
			} else {
				capacity = sizes[c.index] - c.entry.MinSize
			}
			delta := min(remaining, min(proposed, capacity))
			if delta <= 0 {
				continue
			}
			if mode == "grow" {
				sizes[c.index] += delta
			} else {
				sizes[c.index] -= delta
			}
			remaining -= delta
			distributed += delta
		}
		if distributed == 0 {
			return
		}
	}
}

// AllocateStackSizes computes per-entry sizes (pi: allocateStackSizes).
// availableSize is nil where pi passes undefined (VStack).
func AllocateStackSizes(entries []StackLayoutEntry, intrinsicSizes []int, availableSize *int, gap int) []int {
	sizes := make([]int, len(entries))
	for index, entry := range entries {
		intrinsic := 0
		if index < len(intrinsicSizes) {
			intrinsic = intrinsicSizes[index]
		}
		basis := intrinsic
		if size, ok := entry.Basis.(int); ok {
			basis = size
		}
		sizes[index] = clampStackSize(basis, entry)
	}
	if availableSize == nil {
		return sizes
	}

	contentSize := max(0, *availableSize-max(0, len(entries)-1)*gap)
	total := 0
	for _, size := range sizes {
		total += size
	}
	if total < contentSize {
		distributeStackSpace(sizes, entries, contentSize-total, "grow")
	} else if total > contentSize {
		distributeStackSpace(sizes, entries, total-contentSize, "shrink")
	}
	return sizes
}
