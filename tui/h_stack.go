// tui/h_stack.go (pi: packages/tui/src/components/h-stack.ts)
package tui

// HStack lays out children horizontally (pi: class HStack extends Stack).
type HStack struct {
	Stack
}

// NewHStack mirrors pi's constructor; options may be nil.
func NewHStack(children []StackChild, options *StackOptions) *HStack {
	h := &HStack{Stack: newStack("hstack", children, options)}
	return h
}

func (h *HStack) Render(width int) []string {
	safeWidth := max(1, width)
	viewport := LayoutViewport{Width: safeWidth, Height: int(^uint(0) >> 1)}
	entries := VisibleStackEntries(h.entries, viewport)
	if len(entries) == 0 {
		return []string{}
	}

	intrinsicWidths := make([]int, len(entries))
	for i, entry := range entries {
		lines := entry.Component.Render(safeWidth)
		lineMax := 0
		for _, line := range lines {
			lineMax = max(lineMax, VisibleWidth(line))
		}
		intrinsicWidths[i] = lineMax
	}
	widths := AllocateStackSizes(entries, intrinsicWidths, &safeWidth, h.gap)
	rendered := make([][]string, len(entries))
	for i, entry := range entries {
		if widths[i] == 0 {
			rendered[i] = []string{}
			continue
		}
		rendered[i] = entry.Component.Render(widths[i])
	}
	height := 0
	for _, lines := range rendered {
		height = max(height, len(lines))
	}
	result := make([]string, height)
	x := 0
	for index, lines := range rendered {
		childWidth := widths[index]
		offset := 0
		if h.align == "center" {
			offset = (height - len(lines)) / 2
		} else if h.align == "end" {
			offset = height - len(lines)
		}
		for row, line := range lines {
			target := row + offset
			if target < 0 || target >= len(result) {
				continue
			}
			result[target] = CompositeTuiLine(result[target], line, x, childWidth, safeWidth)
		}
		x += childWidth + h.gap
	}
	return result
}
