// tui/layout.go (pi: packages/tui/src/layout.ts)
package tui

import (
	"math"
	"regexp"
	"strings"
)

var osc133ZonePrefixRegex = regexp.MustCompile(`^(?:\x1b\]133;[ABC](?:\x07|\x1b\\))+`)

// LayoutRect is an axis-aligned rectangle in screen coordinates.
type LayoutRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// LayoutBox is one laid-out component in the frame tree (pi: LayoutBox).
type LayoutBox struct {
	Component          Component
	Rect               LayoutRect
	Clip               LayoutRect
	Children           []*LayoutBox
	Parent             *LayoutBox
	Lines              []string // nil = container box
	LineOffset         int
	ScrollView         *ScrollView
	ScrollContentLines []string
	Layer              int
}

// LayoutFrame is the result of renderLayoutFrame (pi: LayoutFrame).
type LayoutFrame struct {
	Root              *LayoutBox
	Width             int
	Height            int
	Lines             []string
	PrimaryScrollView *ScrollView
}

// ScrollbarGeometry describes a painted scrollbar (pi: ScrollbarGeometry).
type ScrollbarGeometry struct {
	Column       int
	TrackTop     int
	TrackHeight  int
	ThumbTop     int
	ThumbHeight  int
	MaxScrollTop int
}

type layoutContext struct {
	viewport          LayoutViewport
	renderCache       map[Component]map[int][]string
	requestRender     func()
	primaryScrollView *ScrollView
}

func intersectRect(a LayoutRect, b LayoutRect) LayoutRect {
	x := maxInt(a.X, b.X)
	y := maxInt(a.Y, b.Y)
	right := minInt(a.X+a.Width, b.X+b.Width)
	bottom := minInt(a.Y+a.Height, b.Y+b.Height)
	return LayoutRect{X: x, Y: y, Width: maxInt(0, right-x), Height: maxInt(0, bottom-y)}
}

func renderCached(context *layoutContext, component Component, width int) []string {
	safeWidth := maxInt(1, width)
	widths := context.renderCache[component]
	if widths == nil {
		widths = map[int][]string{}
		context.renderCache[component] = widths
	}
	lines, ok := widths[safeWidth]
	if !ok {
		lines = component.Render(safeWidth)
		widths[safeWidth] = lines
	}
	return lines
}

func measureHeight(context *layoutContext, component Component, width int) int {
	return len(renderCached(context, component, width))
}

func measureWidth(context *layoutContext, component Component, width int) int {
	maxWidth := 0
	for _, line := range renderCached(context, component, width) {
		if w := VisibleWidth(line); w > maxWidth {
			maxWidth = w
		}
	}
	return maxWidth
}

func withParent(box *LayoutBox, parent *LayoutBox) *LayoutBox {
	box.Parent = parent
	return box
}

func translateBox(box *LayoutBox, deltaY int) {
	box.Rect.Y += deltaY
	for _, child := range box.Children {
		translateBox(child, deltaY)
	}
}

func updateClips(box *LayoutBox, parentClip LayoutRect) {
	box.Clip = intersectRect(parentClip, box.Rect)
	for _, child := range box.Children {
		updateClips(child, box.Clip)
	}
}

func layoutComponent(
	context *layoutContext,
	component Component,
	x int,
	y int,
	width int,
	height *int,
	clip LayoutRect,
) *LayoutBox {
	safeWidth := maxInt(1, width)
	node := GetLayoutNode(component)
	if node == nil {
		lines := renderCached(context, component, safeWidth)
		allocatedHeight := len(lines)
		if height != nil {
			allocatedHeight = maxInt(0, *height)
		}
		lineOffset := 0
		if len(lines) > allocatedHeight && allocatedHeight > 0 {
			cursorLine := -1
			for i, line := range lines {
				if strings.Contains(line, CursorMarker) {
					cursorLine = i
					break
				}
			}
			if cursorLine >= allocatedHeight {
				lineOffset = cursorLine - allocatedHeight + 1
			}
		}
		return &LayoutBox{
			Component:  component,
			Rect:       LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight},
			Clip:       intersectRect(clip, LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}),
			Children:   nil,
			Lines:      lines,
			LineOffset: lineOffset,
			Layer:      0,
		}
	}

	switch n := node.(type) {
	case *ScrollLayoutNode:
		scrollState := n.State
		scrollView, _ := scrollState.(*ScrollView)
		previousScrollTop := scrollState.ScrollTop()
		contentWidth := scrollState.GetContentWidth(safeWidth)
		childBox := layoutComponent(context, n.Component, x, y-previousScrollTop, contentWidth, nil, clip)
		contentHeight := childBox.Rect.Height
		viewportHeight := contentHeight
		if height != nil {
			viewportHeight = maxInt(0, *height)
		}
		scrollState.UpdateLayout(contentHeight, viewportHeight, context.requestRender)
		translateBox(childBox, previousScrollTop-scrollState.ScrollTop())
		if scrollState.Primary() || context.primaryScrollView == nil {
			context.primaryScrollView = scrollView
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: viewportHeight}
		childClip := intersectRect(clip, rect)
		box := &LayoutBox{
			Component:          component,
			Rect:               rect,
			Clip:               childClip,
			Children:           []*LayoutBox{childBox},
			ScrollView:         scrollView,
			ScrollContentLines: renderCached(context, n.Component, contentWidth),
			Layer:              0,
		}
		childBox.Parent = box
		updateClips(childBox, childClip)
		return box
	}

	stackNode := node.(*StackLayoutNode)
	entries := VisibleStackEntries(stackNode.Entries, context.viewport)
	gapTotal := maxInt(0, len(entries)-1) * stackNode.Gap
	if stackNode.Type == "vstack" {
		intrinsicHeights := make([]int, len(entries))
		for i, entry := range entries {
			if basis, ok := entry.Basis.(int); ok {
				intrinsicHeights[i] = basis
			} else {
				intrinsicHeights[i] = measureHeight(context, entry.Component, safeWidth)
			}
		}
		var available *int
		if height != nil {
			available = height
		}
		sizes := AllocateStackSizes(entries, intrinsicHeights, available, stackNode.Gap)
		naturalHeight := gapTotal
		for _, size := range sizes {
			naturalHeight += size
		}
		allocatedHeight := naturalHeight
		if height != nil {
			allocatedHeight = maxInt(0, *height)
		}
		rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
		box := &LayoutBox{
			Component: component,
			Rect:      rect,
			Clip:      intersectRect(clip, rect),
			Children:  nil,
			Layer:     0,
		}
		childY := y
		for index := range entries {
			box.Children = append(box.Children, withParent(
				layoutComponent(context, entries[index].Component, x, childY, safeWidth, intPtr(sizes[index]), box.Clip),
				box,
			))
			childY += sizes[index] + stackNode.Gap
		}
		return box
	}

	intrinsicWidths := make([]int, len(entries))
	for i, entry := range entries {
		if basis, ok := entry.Basis.(int); ok {
			intrinsicWidths[i] = basis
		} else {
			intrinsicWidths[i] = measureWidth(context, entry.Component, safeWidth)
		}
	}
	widths := AllocateStackSizes(entries, intrinsicWidths, intPtr(safeWidth), stackNode.Gap)
	intrinsicHeights := make([]int, len(entries))
	for index, entry := range entries {
		intrinsicHeights[index] = measureHeight(context, entry.Component, maxInt(1, widths[index]))
	}
	allocatedHeight := 0
	if height == nil {
		for _, childHeight := range intrinsicHeights {
			allocatedHeight = maxInt(allocatedHeight, childHeight)
		}
	} else {
		allocatedHeight = maxInt(0, *height)
	}
	rect := LayoutRect{X: x, Y: y, Width: safeWidth, Height: allocatedHeight}
	box := &LayoutBox{
		Component: component,
		Rect:      rect,
		Clip:      intersectRect(clip, rect),
		Children:  nil,
		Layer:     0,
	}
	childX := x
	for index := range entries {
		naturalChildHeight := intrinsicHeights[index]
		childHeight := naturalChildHeight
		if stackNode.Align == "stretch" {
			childHeight = allocatedHeight
		} else {
			childHeight = minInt(allocatedHeight, naturalChildHeight)
		}
		childY := y
		if stackNode.Align == "center" {
			childY += (allocatedHeight - childHeight) / 2
		} else if stackNode.Align == "end" {
			childY += allocatedHeight - childHeight
		}
		childWidth := widths[index]
		if childWidth == 0 {
			box.Children = append(box.Children, &LayoutBox{
				Component: entries[index].Component,
				Rect:      LayoutRect{X: childX, Y: childY, Width: 0, Height: childHeight},
				Clip:      LayoutRect{X: childX, Y: childY, Width: 0, Height: 0},
				Children:  nil,
				Parent:    box,
				Layer:     0,
			})
		} else {
			box.Children = append(box.Children, withParent(
				layoutComponent(context, entries[index].Component, childX, childY, childWidth, intPtr(childHeight), box.Clip),
				box,
			))
		}
		childX += childWidth + stackNode.Gap
	}
	return box
}

// intPtr allocates a pointer to v. pi's layout uses `number | undefined`
// heights; nil expresses the undefined case.
func intPtr(v int) *int { return &v }

func styleScrollbarCell(line string, column int, totalWidth int, style func(text string) string) string {
	if IsImageLine(line) {
		return line
	}

	start := column
	end := column + 1
	if graphemeRange := GetGraphemeCellRange(line, column); graphemeRange != nil {
		start = graphemeRange.Start
		end = graphemeRange.End
	}
	before := SliceByColumn(line, 0, start, true)
	target := SliceByColumn(line, start, end-start, true)
	after := SliceByColumn(line, end, maxInt(0, totalWidth-end), true)

	var targetPrefix strings.Builder
	targetIndex := 0
	for targetIndex < len(target) {
		ansi := ExtractAnsiCode(target, targetIndex)
		if ansi == nil {
			break
		}
		targetPrefix.WriteString(ansi.Code)
		targetIndex += ansi.Length
	}
	targetText := target[targetIndex:]
	if targetText == "" {
		targetText = strings.Repeat(" ", end-start)
	}
	beforePadding := strings.Repeat(" ", maxInt(0, start-VisibleWidth(before)))
	return before + beforePadding + targetPrefix.String() + style(targetText) + after
}

// GetScrollbarGeometry computes the scrollbar geometry for a laid-out
// scroll view box (pi: getScrollbarGeometry). Returns nil when no scrollbar
// paints.
func GetScrollbarGeometry(box *LayoutBox) *ScrollbarGeometry {
	if box.ScrollView == nil || !box.ScrollView.IsScrollbarVisible() || box.Rect.Width <= 0 || box.Rect.Height <= 0 {
		return nil
	}

	contentHeight := 0
	if len(box.Children) > 0 {
		contentHeight = box.Children[0].Rect.Height
	} else {
		contentHeight = len(box.ScrollContentLines)
	}
	trackHeight := box.Rect.Height

	minThumbHeight := minInt(2, trackHeight)
	thumbHeight := maxInt(
		minThumbHeight,
		minInt(trackHeight, int(math.Round(float64(trackHeight*trackHeight)/float64(contentHeight)))),
	)
	maxScrollTop := maxInt(0, contentHeight-trackHeight)
	maxThumbTop := trackHeight - thumbHeight
	thumbOffset := 0
	if maxScrollTop != 0 {
		thumbOffset = int(math.Round(float64(box.ScrollView.ScrollTop()) / float64(maxScrollTop) * float64(maxThumbTop)))
	}
	column := box.Rect.X + box.Rect.Width - 1
	if column < box.Clip.X || column >= box.Clip.X+box.Clip.Width {
		return nil
	}

	return &ScrollbarGeometry{
		Column:       column,
		TrackTop:     box.Rect.Y,
		TrackHeight:  trackHeight,
		ThumbTop:     box.Rect.Y + thumbOffset,
		ThumbHeight:  thumbHeight,
		MaxScrollTop: maxScrollTop,
	}
}

func paintScrollbar(box *LayoutBox, screen []string, totalWidth int) {
	geometry := GetScrollbarGeometry(box)
	if geometry == nil || box.ScrollView == nil {
		return
	}

	for offset := 0; offset < geometry.ThumbHeight; offset++ {
		row := geometry.ThumbTop + offset
		if row < box.Clip.Y || row >= box.Clip.Y+box.Clip.Height || row < 0 || row >= len(screen) {
			continue
		}
		line := ""
		if row < len(screen) {
			line = screen[row]
		}
		screen[row] = styleScrollbarCell(line, geometry.Column, totalWidth, box.ScrollView.scrollbarStyle)
	}
}

func paintBox(box *LayoutBox, screen []string, totalWidth int) {
	if box.Lines != nil {
		offset := box.LineOffset
		firstRow := maxInt(maxInt(box.Rect.Y, box.Clip.Y), 0)
		lastRow := minInt(minInt(box.Rect.Y+box.Rect.Height, box.Clip.Y+box.Clip.Height), len(screen))
		for row := firstRow; row < lastRow; row++ {
			sourceIndex := offset + row - box.Rect.Y
			if sourceIndex < 0 || sourceIndex >= len(box.Lines) {
				continue
			}
			sourceLine := box.Lines[sourceIndex]
			line := osc133ZonePrefixRegex.ReplaceAllString(sourceLine, "")
			imageMetadata := GetKittyImageMetadata(line)
			if imageMetadata != nil {
				clipBottom := minInt(len(screen), box.Clip.Y+box.Clip.Height)
				visibleRows := minInt(imageMetadata.Rows, clipBottom-row)
				if visibleRows < imageMetadata.Rows {
					line = CropKittyImageLine(line, 0, visibleRows)
				}
			}
			// Fast path: a full-width box painting onto an untouched row can use the
			// source line reference directly. Compositing here would rebuild the row
			// string through ANSI/grapheme segmentation every frame; padding is
			// unnecessary because rows are written with erase-line and the final
			// width clamp still truncates over-wide lines.
			if box.Rect.X == 0 && box.Rect.Width >= totalWidth && (IsImageLine(line) || screen[row] == "") {
				screen[row] = line
			} else {
				base := ""
				if row < len(screen) {
					base = screen[row]
				}
				screen[row] = CompositeTuiLine(base, line, box.Rect.X, box.Rect.Width, totalWidth)
			}
		}
	}
	for _, child := range box.Children {
		paintBox(child, screen, totalWidth)
	}

	if box.ScrollView != nil && box.ScrollContentLines != nil && box.ScrollView.ScrollTop() > 0 && box.Rect.Height > 0 {
		for imageRow := box.ScrollView.ScrollTop() - 1; imageRow >= 0; imageRow-- {
			imageLine := ""
			if imageRow < len(box.ScrollContentLines) {
				imageLine = box.ScrollContentLines[imageRow]
			}
			metadata := GetKittyImageMetadata(imageLine)
			if metadata != nil {
				hiddenRows := box.ScrollView.ScrollTop() - imageRow
				if hiddenRows < metadata.Rows {
					visibleRows := minInt(box.Rect.Height, metadata.Rows-hiddenRows)
					cropped := CropKittyImageLine(imageLine, hiddenRows, visibleRows)
					if box.Rect.X == 0 && box.Rect.Width >= totalWidth {
						screen[box.Rect.Y] = cropped
					}
				}
				break
			}
			if imageLine != "" {
				break
			}
		}
	}

	paintScrollbar(box, screen, totalWidth)
}

// RenderLayoutFrame lays out the component tree and paints it into a screen
// buffer of height rows (pi: renderLayoutFrame).
func RenderLayoutFrame(root Component, width int, height int, requestRender func()) *LayoutFrame {
	safeWidth := maxInt(1, width)
	safeHeight := maxInt(1, height)
	context := &layoutContext{
		viewport:      LayoutViewport{Width: safeWidth, Height: safeHeight},
		renderCache:   map[Component]map[int][]string{},
		requestRender: requestRender,
	}
	rootBox := layoutComponent(context, root, 0, 0, safeWidth, intPtr(safeHeight),
		LayoutRect{X: 0, Y: 0, Width: safeWidth, Height: safeHeight})
	lines := make([]string, safeHeight)
	for i := range lines {
		lines[i] = ""
	}
	paintBox(rootBox, lines, safeWidth)
	return &LayoutFrame{
		Root:              rootBox,
		Width:             safeWidth,
		Height:            safeHeight,
		Lines:             lines,
		PrimaryScrollView: context.primaryScrollView,
	}
}

func containsPoint(rect LayoutRect, x int, y int) bool {
	return x >= rect.X && x < rect.X+rect.Width && y >= rect.Y && y < rect.Y+rect.Height
}

// GetScrollViewBox finds the LayoutBox for a scroll view in the frame (pi:
// getScrollViewBox).
func GetScrollViewBox(frame *LayoutFrame, scrollView *ScrollView) *LayoutBox {
	var visit func(box *LayoutBox) *LayoutBox
	visit = func(box *LayoutBox) *LayoutBox {
		if box.ScrollView == scrollView {
			return box
		}
		for _, child := range box.Children {
			if match := visit(child); match != nil {
				return match
			}
		}
		return nil
	}
	return visit(frame.Root)
}

// GetScrollViewsAt returns the scroll views whose rects contain the point,
// deepest first (pi: getScrollViewsAt).
func GetScrollViewsAt(frame *LayoutFrame, x int, y int) []*ScrollView {
	type entry struct {
		scrollView *ScrollView
		depth      int
	}
	var result []entry
	var visit func(box *LayoutBox, depth int)
	visit = func(box *LayoutBox, depth int) {
		if !containsPoint(box.Clip, x, y) {
			return
		}
		if box.ScrollView != nil && containsPoint(box.Rect, x, y) {
			result = append(result, entry{scrollView: box.ScrollView, depth: depth})
		}
		for _, child := range box.Children {
			visit(child, depth+1)
		}
	}
	visit(frame.Root, 0)
	// sort by depth descending
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j].depth > result[j-1].depth; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	scrollViews := make([]*ScrollView, len(result))
	for i, e := range result {
		scrollViews[i] = e.scrollView
	}
	return scrollViews
}
