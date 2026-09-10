// tui/v_stack.go (pi: packages/tui/src/components/v-stack.ts)
package tui

// VStack lays out children vertically (pi: class VStack extends Stack).
type VStack struct {
	Stack
}

// NewVStack mirrors pi's constructor; options may be nil.
func NewVStack(children []StackChild, options *StackOptions) *VStack {
	return &VStack{Stack: newStack("vstack", children, options)}
}

func (v *VStack) Render(width int) []string {
	viewport := LayoutViewport{Width: max(1, width), Height: int(^uint(0) >> 1)}
	entries := VisibleStackEntries(v.entries, viewport)
	rendered := make([][]string, len(entries))
	intrinsicSizes := make([]int, len(entries))
	for i, entry := range entries {
		rendered[i] = entry.Component.Render(viewport.Width)
		intrinsicSizes[i] = len(rendered[i])
	}
	sizes := AllocateStackSizes(entries, intrinsicSizes, nil, v.gap)
	var lines []string
	for index := range entries {
		if index > 0 {
			for gap := 0; gap < v.gap; gap++ {
				lines = append(lines, "")
			}
		}
		childLines := rendered[index]
		if len(childLines) > sizes[index] {
			childLines = childLines[:sizes[index]]
		}
		lines = append(lines, childLines...)
		for padding := len(childLines); padding < sizes[index]; padding++ {
			lines = append(lines, "")
		}
	}
	return lines
}
