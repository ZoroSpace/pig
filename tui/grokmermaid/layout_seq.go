// Ported from pi node_modules/grok-mermaid/src/layout-seq.ts (grok-mermaid
// v0.2.2, Apache-2.0) as part of T5b-4. Sequence diagram layout.
//
// Participants get one column each, with lifelines running the full height and
// a box repeated at top and bottom. Column gaps are solved from the widest
// thing that has to fit between any two columns — a message label, a note, a
// self-message stub — then items stack down the canvas in source order.
package grokmermaid

import "sort"

// Sequence diagram layout constants.
const (
	seqPad          = 1
	seqGap          = 5 // Minimum columns between adjacent lifelines.
	seqMaxCanvasCel = 1 << 21
)

// noteGeometry is where a note box sits, given the lifeline positions.
func noteGeometry(xs []int, anchor NoteAnchor, textW int) (int, int) {
	if anchor.Kind == "over" {
		center := half(xs[anchor.From] + xs[anchor.To])
		w := max(xs[anchor.To]-xs[anchor.From]+5, textW+2*seqPad+2)
		return sat(center, half(w)), w
	}
	w := textW + 2*seqPad + 2
	if anchor.Kind == "left" {
		return sat(xs[anchor.At], 2+w-1), w
	}
	return xs[anchor.At] + 2, w
}

func itemTextW(text *string) int {
	if text == nil {
		return 0
	}
	return stringWidth(*text)
}

// layoutSequence lays out a parsed sequence diagram. Returns nil (pi: null)
// over the cell cap.
func layoutSequence(seq *Sequence) *Canvas {
	n := len(seq.Labels)
	labels := make([]string, n)
	for i, l := range seq.Labels {
		labels[i] = fitLabel(l, WrapWidth)
	}
	boxW := make([]int, n)
	for i, l := range labels {
		boxW[i] = max(1, stringWidth(l)) + 2*seqPad + 2
	}
	boxH := 3

	gaps := make([]int, sat(n, 1))
	for i := range gaps {
		gaps[i] = max(seqGap, (boxW[i]+1)/2+(boxW[i+1]+1)/2+1)
	}

	// Each requirement is "columns l..r together need at least need cells".
	type req struct{ l, r, need int }
	var reqs []req
	for _, item := range seq.Items {
		switch it := item.(type) {
		case *SeqMessage:
			tw := itemTextW(it.Text)
			if it.From != it.To {
				reqs = append(reqs, req{min(it.From, it.To), max(it.From, it.To), max(tw+2, 4)})
			} else if it.From+1 < n {
				reqs = append(reqs, req{it.From, it.From + 1, 5 + tw + 2})
			}
		case *SeqNote:
			tw := stringWidth(it.Text)
			a := it.Anchor
			if a.Kind == "over" && a.From < a.To {
				reqs = append(reqs, req{a.From, a.To, sat(tw, 1)})
			} else if a.Kind == "over" {
				need := (tw+4+1)/2 + 2
				if a.From > 0 {
					reqs = append(reqs, req{a.From - 1, a.From, need})
				}
				if a.From+1 < n {
					reqs = append(reqs, req{a.From, a.From + 1, need})
				}
			} else if a.Kind == "left" && a.At > 0 {
				reqs = append(reqs, req{a.At - 1, a.At, tw + 7})
			} else if a.Kind == "right" && a.At+1 < n {
				reqs = append(reqs, req{a.At, a.At + 1, tw + 7})
			}
		}
	}
	// Narrowest spans first, so a wide requirement absorbs what they already
	// gave.
	sort.SliceStable(reqs, func(a, b int) bool { return reqs[a].r-reqs[a].l < reqs[b].r-reqs[b].l })
	for _, rq := range reqs {
		cur := 0
		for i := rq.l; i < rq.r; i++ {
			cur += gaps[i]
		}
		if cur < rq.need {
			gaps[rq.r-1] += rq.need - cur
		}
	}

	xs := make([]int, n)
	xs[0] = half(boxW[0])
	for i := 1; i < n; i++ {
		xs[i] = xs[i-1] + gaps[i-1]
	}

	canvasW := xs[n-1] + (boxW[n-1]+1)/2 + 1
	for _, item := range seq.Items {
		switch it := item.(type) {
		case *SeqMessage:
			if it.From == it.To {
				canvasW = max(canvasW, xs[it.From]+5+itemTextW(it.Text)+1)
			}
		case *SeqNote:
			gx, gw := noteGeometry(xs, it.Anchor, stringWidth(it.Text))
			canvasW = max(canvasW, gx+gw+1)
		case *SeqDivider:
			canvasW = max(canvasW, stringWidth(it.Text)+4)
		}
	}

	rows := make([]int, len(seq.Items))
	y := boxH + 1
	for k, item := range seq.Items {
		rows[k] = y
		y += seqRowHeight(item)
	}
	bottomTop := y
	canvasH := bottomTop + boxH

	if canvasW*canvasH > seqMaxCanvasCel {
		return nil
	}

	canvas := NewCanvas(canvasW, canvasH)

	for i := 0; i < n; i++ {
		for _, by := range []int{0, bottomTop} {
			drawBox(canvas, seqBox(sat(xs[i], half(boxW[i])), by, boxW[i], boxH), []string{labels[i]}, ShapeRect)
		}
	}
	for k, item := range seq.Items {
		note, ok := item.(*SeqNote)
		if !ok {
			continue
		}
		gx, gw := noteGeometry(xs, note.Anchor, stringWidth(note.Text))
		drawBox(canvas, seqBox(gx, rows[k], gw, 3), []string{note.Text}, ShapeRect)
	}

	for _, x := range xs {
		canvas.Junction(x, boxH-1, BitD)
		canvas.SegV(x, boxH, bottomTop-1)
		canvas.Junction(x, bottomTop, BitU)
	}

	for k, item := range seq.Items {
		r := rows[k]
		switch it := item.(type) {
		case *SeqMessage:
			drawMessage(canvas, it, xs, r)
		case *SeqDivider:
			drawDivider(canvas, it.Text, r, canvasW)
		}
	}

	canvas.FinalizeMask()
	return canvas
}

func seqRowHeight(item SeqItem) int {
	switch it := item.(type) {
	case *SeqNote:
		return 4
	case *SeqDivider:
		return 2
	case *SeqMessage:
		if it.From == it.To {
			return 4
		}
		if it.Text != nil {
			return 3
		}
		return 2
	}
	return 2
}

// seqBox builds geometry for a box drawn by position and size; ranks are
// irrelevant here.
func seqBox(x, y, w, h int) Placed {
	return Placed{X: x, Y: y, W: w, H: h, CX: x + half(w), CY: y + 1, Rank: 0}
}

func drawMessage(canvas *Canvas, item *SeqMessage, xs []int, r int) {
	lineCh := "─"
	if item.Dashed {
		lineCh = "╌"
	}

	if item.From == item.To {
		// A stub that leaves the lifeline and returns two rows down.
		x := xs[item.From]
		canvas.Junction(x, r, BitR)
		canvas.Set(x+1, r, lineCh, ClsEdge)
		canvas.Set(x+2, r, lineCh, ClsEdge)
		canvas.Set(x+3, r, "╮", ClsEdge)
		canvas.Set(x+3, r+1, "│", ClsEdge)
		head := "◄"
		if item.Head == SeqHeadCross {
			head = "×"
		}
		canvas.Set(x+1, r+2, head, ClsEdge)
		canvas.Set(x+2, r+2, lineCh, ClsEdge)
		canvas.Set(x+3, r+2, "╯", ClsEdge)
		if item.Text != nil {
			DrawTextOverEdges(canvas, *item.Text, x+5, r+1, ClsText)
		}
		return
	}

	x0 := xs[item.From]
	x1 := xs[item.To]
	rightward := x1 > x0
	// A labelled message writes its text on r and draws the arrow below it.
	arrowRow := r
	if item.Text != nil {
		arrowRow = r + 1
	}
	lo := min(x0, x1)
	hi := max(x0, x1)

	if rightward {
		canvas.Junction(x0, arrowRow, BitR)
	} else {
		canvas.Junction(x0, arrowRow, BitL)
	}
	for x := lo + 1; x < hi; x++ {
		canvas.Set(x, arrowRow, lineCh, ClsEdge)
	}
	headCh := "▶"
	if !rightward {
		headCh = "◄"
	}
	if item.Head == SeqHeadCross {
		headCh = "×"
	}
	if rightward {
		canvas.Set(x1-1, arrowRow, headCh, ClsEdge)
	} else {
		canvas.Set(x1+1, arrowRow, headCh, ClsEdge)
	}

	if item.Text != nil {
		span := hi - lo - 1
		t := fitLabel(*item.Text, max(1, span))
		DrawTextOverEdges(canvas, t, lo+1+half(sat(span, stringWidth(t))), r, ClsText)
	}
}

// drawDivider draws a full-width rule labelling a loop / alt / opt block
// boundary.
func drawDivider(canvas *Canvas, text string, r, canvasW int) {
	for x := 0; x < canvasW; x++ {
		canvas.Set(x, r, "─", ClsEdge)
	}
	DrawTextOverEdges(canvas, " "+fitLabel(text, sat(canvasW, 4))+" ", 2, r, ClsEdgeLabel)
}
