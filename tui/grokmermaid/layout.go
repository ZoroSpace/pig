// Ported from pi node_modules/grok-mermaid/src/layout.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. Graph layout: rank, order, place, route, draw
// — the Sugiyama outline. BT/RL reuse the TD/LR layouts and flip the finished
// canvas, so text never ends up mirrored.
//
// pig: TS sorts are stable (V8 guarantee) → sort.SliceStable. Math.round →
// math.Floor(x+0.5) (JS rounds half toward +∞). The NodeExtra union becomes a
// single struct with a Kind discriminant; Span5 stays a [5]int array.
package grokmermaid

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// sat is saturating subtraction; Rust's usize arithmetic never goes negative.
func sat(a, b int) int { return max(0, a-b) }

// half is floor(n/2).
func half(n int) int { return n / 2 }

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Cells of padding between a box border and its text.
const pad = 1

// Minimum horizontal / vertical space between boxes.
const (
	gapX = 3
	gapY = 2
)

// maxCanvasCells: refuse to allocate a canvas larger than this many cells.
const maxCanvasCells = 1 << 21

// Placed is a placed box.
type Placed struct {
	X, Y, W, H int
	CX, CY     int
	Rank       int
}

// nodeSizes carries per-node dimensions. Lay* include room for self-edge loops
// and labels.
type nodeSizes struct {
	boxW       []int
	boxH       []int
	layW       []int
	layH       []int
	extraH     []int
	selfLabelW []int
}

// NodeExtra is what to draw inside a node box (pi: the NodeExtra union —
// single struct with Kind; Sub for "frame", Sections for "compartments").
type NodeExtra struct {
	Kind     string // "plain" | "frame" | "compartments"
	Sub      *Canvas
	Sections [][]string
}

// NodeExtraPlain constructs the "plain" variant.
func NodeExtraPlain() NodeExtra { return NodeExtra{Kind: "plain"} }

// routePlan is the computed routing geometry.
type routePlan struct {
	canvasW, canvasH int
	// bandEnd is the coordinate just past each rank's boxes, where its bus
	// rows begin.
	bandEnd []int
	// edgeBus is the bus track offset per edge.
	edgeBus []int
	// laneBase is the coordinate of the first lane track.
	laneBase int
	// edgeLane is the lane track offset per edge.
	edgeLane []int
}

// ------------------------------------------------------------------ ranking

// computeRanks does longest-path ranking over the graph's DAG.
//
// Back edges (those closing a cycle) are excluded by a DFS colouring pass, so
// `A --> B --> C --> A` still ranks 0, 1, 2 rather than diverging.
func computeRanks(graph *Graph) []int {
	n := len(graph.Nodes)
	children := make([][]int, n)
	indeg := make([]int, n)
	for _, e := range graph.Edges {
		if e.From != e.To {
			children[e.From] = append(children[e.From], e.To)
			indeg[e.To]++
		}
	}

	color := make([]uint8, n)
	dag := make([][]int, n)
	order := make([]int, 0, n)

	// Roots first so ranks grow from natural entry points, then any leftovers.
	roots := []int{}
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			roots = append(roots, i)
		}
	}
	rest := make([]int, n)
	for i := range rest {
		rest[i] = i
	}
	for _, start := range append(roots, rest...) {
		if color[start] == 0 {
			dfsDag(start, children, color, dag, &order)
		}
	}

	rank := make([]int, n)
	for i := len(order) - 1; i >= 0; i-- {
		u := order[i]
		for _, v := range dag[u] {
			rank[v] = max(rank[v], rank[u]+1)
		}
	}
	return rank
}

// dfsDag is an iterative DFS recording postorder and skipping edges back into
// the stack.
func dfsDag(start int, children [][]int, color []uint8, dag [][]int, order *[]int) {
	type frame struct {
		u int
		i int
	}
	stack := []frame{{u: start}}
	color[start] = 1
	for len(stack) > 0 {
		fr := &stack[len(stack)-1]
		u := fr.u
		if fr.i < len(children[u]) {
			v := children[u][fr.i]
			fr.i++
			if color[v] == 1 {
				continue // grey: a back edge, ignore it
			}
			dag[u] = append(dag[u], v)
			if color[v] == 0 {
				color[v] = 1
				stack = append(stack, frame{u: v})
			}
		} else {
			color[u] = 2
			*order = append(*order, u)
			stack = stack[:len(stack)-1]
		}
	}
}

// orderRanks reorders nodes within each rank to minimise edge crossings
// (barycenter sweeps): alternate down/up passes sort each rank by the mean
// position of its neighbours, keeping whichever ordering crossed least.
func orderRanks(byRank [][]int, edges []Edge, ranks []int) {
	n := len(ranks)
	if len(byRank) < 2 || n < 3 {
		return
	}

	parents := make([][]int, n)
	children := make([][]int, n)
	for _, e := range edges {
		if e.From != e.To && ranks[e.To] > ranks[e.From] {
			parents[e.To] = append(parents[e.To], e.From)
			children[e.From] = append(children[e.From], e.To)
		}
	}

	pos := make([]int, n)
	reindex := func(row []int) {
		for i, v := range row {
			pos[v] = i
		}
	}
	for _, row := range byRank {
		reindex(row)
	}

	best := make([][]int, len(byRank))
	for i, row := range byRank {
		best[i] = append([]int{}, row...)
	}
	bestCrossings := countCrossings(edges, ranks, pos)
	if bestCrossings == 0 {
		return
	}

	for it := 0; it < 8; it++ {
		// Alternate sweeping down (sort by parents) and up (sort by children).
		var rows [][]int
		var neigh [][]int
		if it%2 == 0 {
			rows = byRank[1:]
			neigh = parents
		} else {
			rev := byRank[:len(byRank)-1]
			rows = make([][]int, len(rev))
			for i, row := range rev {
				rows[len(rev)-1-i] = row
			}
			neigh = children
		}
		for _, row := range rows {
			sortByBarycenter(row, neigh, pos)
			reindex(row)
		}
		crossings := countCrossings(edges, ranks, pos)
		if crossings < bestCrossings {
			bestCrossings = crossings
			for i, row := range byRank {
				best[i] = append([]int{}, row...)
			}
		}
		if bestCrossings == 0 {
			break
		}
	}

	for i := range byRank {
		copy(byRank[i], best[i])
	}
}

func sortByBarycenter(row []int, neigh [][]int, pos []int) {
	type keyed struct {
		key float64
		v   int
	}
	keyedRows := make([]keyed, len(row))
	for i, v := range row {
		k := float64(pos[v])
		if len(neigh[v]) > 0 {
			sum := 0
			for _, u := range neigh[v] {
				sum += pos[u]
			}
			k = float64(sum) / float64(len(neigh[v]))
		}
		keyedRows[i] = keyed{key: k, v: v}
	}
	sort.SliceStable(keyedRows, func(a, b int) bool { return keyedRows[a].key < keyedRows[b].key })
	for i, k := range keyedRows {
		row[i] = k.v
	}
}

// countCrossings counts crossings between adjacent-rank edges.
func countCrossings(edges []Edge, ranks []int, pos []int) int {
	type adj struct {
		rank, fromPos, toPos int
	}
	var adjacent []adj
	for _, e := range edges {
		if e.From != e.To && ranks[e.To] == ranks[e.From]+1 {
			adjacent = append(adjacent, adj{ranks[e.From], pos[e.From], pos[e.To]})
		}
	}
	crossings := 0
	for i := range adjacent {
		a := adjacent[i]
		for j := i + 1; j < len(adjacent); j++ {
			b := adjacent[j]
			if a.rank == b.rank &&
				((a.fromPos < b.fromPos && a.toPos > b.toPos) ||
					(a.fromPos > b.fromPos && a.toPos < b.toPos)) {
				crossings++
			}
		}
	}
	return crossings
}

// assignPositions assigns a cross-axis centre to every node so nodes line up
// under their neighbours: each node drifts toward the average of its
// neighbours while ranks keep their order and boxes keep sep between them.
func assignPositions(byRank [][]int, size []int, sep int, edges []Edge, ranks []int) []int {
	n := len(size)
	parents := make([][]int, n)
	children := make([][]int, n)
	for _, e := range edges {
		if e.From != e.To && ranks[e.To] > ranks[e.From] {
			parents[e.To] = append(parents[e.To], e.From)
			children[e.From] = append(children[e.From], e.To)
		}
	}

	pos := make([]float64, n)
	for _, row := range byRank {
		x := 0.0
		for _, v := range row {
			h := float64(size[v]) / 2
			x += h
			pos[v] = x
			x += h + float64(sep)
		}
	}

	for it := 0; it < 10; it++ {
		var rows [][]int
		neigh := parents
		if it%2 == 0 {
			rows = byRank
		} else {
			rows = make([][]int, len(byRank))
			for i, row := range byRank {
				rows[len(byRank)-1-i] = row
			}
			neigh = children
		}
		for _, row := range rows {
			relaxRank(row, neigh, pos, size, sep)
		}
	}

	minLeft := math.Inf(1)
	for v := 0; v < n; v++ {
		minLeft = math.Min(minLeft, pos[v]-float64(size[v])/2)
	}
	if math.IsInf(minLeft, 1) {
		minLeft = 0
	}
	out := make([]int, n)
	for v := 0; v < n; v++ {
		// math.Floor(x+0.5) matches JS Math.round (half toward +∞).
		out[v] = max(0, int(math.Floor(pos[v]-minLeft+0.5)))
	}
	return out
}

func relaxRank(nodes []int, neigh [][]int, pos []float64, size []int, sep int) {
	n := len(nodes)
	if n == 0 {
		return
	}

	desired := make([]float64, n)
	for i, v := range nodes {
		if len(neigh[v]) == 0 {
			desired[i] = pos[v]
		} else {
			sum := 0.0
			for _, u := range neigh[v] {
				sum += pos[u]
			}
			desired[i] = sum / float64(len(neigh[v]))
		}
	}
	halfOf := func(i int) float64 { return float64(size[nodes[i]]) / 2 }

	// Sweep right then left, then take the midpoint: this centres a node
	// between the tightest packing that respects order from either side.
	left := make([]float64, n)
	for i := 0; i < n; i++ {
		if i == 0 {
			left[i] = desired[i]
		} else {
			left[i] = math.Max(desired[i], left[i-1]+halfOf(i-1)+float64(sep)+halfOf(i))
		}
	}
	right := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		if i == n-1 {
			right[i] = desired[i]
		} else {
			right[i] = math.Min(desired[i], right[i+1]-halfOf(i+1)-float64(sep)-halfOf(i))
		}
	}
	for i := 0; i < n; i++ {
		pos[nodes[i]] = (left[i] + right[i]) / 2
	}
	for i := 1; i < n; i++ {
		minP := pos[nodes[i-1]] + halfOf(i-1) + float64(sep) + halfOf(i)
		if pos[nodes[i]] < minP {
			pos[nodes[i]] = minP
		}
	}
}

// ------------------------------------------------------------------- tracks

// span5 is a span competing for a track: [start, end, from, to, edgeIndex].
type span5 [5]int

// assignTracks packs spans into as few parallel tracks as possible.
//
// Two spans share a track when they are two cells apart, or when they share an
// endpoint — edges fanning out of one node deliberately reuse a single row so
// a merge draws one arrowhead rather than a stack of them.
func assignTracks(spans []span5) (assigned [][2]int, count int) {
	sorted := append([]span5{}, spans...)
	sort.SliceStable(sorted, func(a, b int) bool {
		for i := 0; i < 5; i++ {
			if sorted[a][i] != sorted[b][i] {
				return sorted[a][i] < sorted[b][i]
			}
		}
		return false
	})
	var tracks [][][4]int
	assigned = [][2]int{}
	for _, sp := range sorted {
		s, e, f, t, idx := sp[0], sp[1], sp[2], sp[3], sp[4]
		slot := -1
		for si, members := range tracks {
			ok := true
			for _, m := range members {
				s2, e2, f2, t2 := m[0], m[1], m[2], m[3]
				if !(e2+2 <= s || e+2 <= s2 || f2 == f || t2 == t) {
					ok = false
					break
				}
			}
			if ok {
				slot = si
				break
			}
		}
		if slot == -1 {
			tracks = append(tracks, nil)
			slot = len(tracks) - 1
		}
		tracks[slot] = append(tracks[slot], [4]int{s, e, f, t})
		assigned = append(assigned, [2]int{idx, slot})
	}
	return assigned, len(tracks)
}

// busSpans returns edges from rank r to r+1 that must jog sideways, so need a
// bus row.
func busSpans(graph *Graph, ranks []int, centers []int, r int, exact bool) []span5 {
	var out []span5
	for i := range graph.Edges {
		e := &graph.Edges[i]
		jogs := false
		if exact {
			jogs = centers[e.From] != centers[e.To]
		} else {
			jogs = abs(centers[e.From]-centers[e.To]) > 1
		}
		if e.From != e.To && ranks[e.From] == r && ranks[e.To] == r+1 && jogs {
			out = append(out, span5{
				min(centers[e.From], centers[e.To]),
				max(centers[e.From], centers[e.To]),
				e.From, e.To, i,
			})
		}
	}
	return out
}

// laneSpans returns edges skipping a rank or running backwards; these go
// around in a lane.
func laneSpans(graph *Graph, ranks []int, placed []Placed, vertical bool) []span5 {
	var out []span5
	for i := range graph.Edges {
		e := &graph.Edges[i]
		if e.From == e.To || ranks[e.To] == ranks[e.From]+1 {
			continue
		}
		pf := &placed[e.From]
		pt := &placed[e.To]
		var a, b int
		if vertical {
			a, b = min(pf.CY, pt.CY), max(pf.CY, pt.CY)
		} else {
			a, b = min(pf.CX, pt.CX), max(pf.CX, pt.CX)
		}
		out = append(out, span5{a, b, e.From, e.To, i})
	}
	return out
}

// ----------------------------------------------------------------- placement

func placeTd(ranks []int, maxRank int, byRank [][]int, sizes nodeSizes, graph *Graph, placed []Placed) routePlan {
	centers := assignPositions(byRank, sizes.layW, gapX, graph.Edges, ranks)

	edgeBus := make([]int, len(graph.Edges))
	busTracks := make([]int, maxRank+1)
	for r := 0; r < maxRank; r++ {
		spans := busSpans(graph, ranks, centers, r, false)
		if len(spans) == 0 {
			continue
		}
		assigned, count := assignTracks(spans)
		for _, a := range assigned {
			edgeBus[a[0]] = a[1]
		}
		busTracks[r] = count
	}

	rankH := make([]int, len(byRank))
	for r, row := range byRank {
		if len(row) == 0 {
			rankH[r] = 3
			continue
		}
		m := 0
		for _, i := range row {
			m = max(m, sizes.boxH[i]+sizes.extraH[i])
		}
		rankH[r] = m
	}
	rankY := make([]int, maxRank+1)
	for r := 1; r <= maxRank; r++ {
		rankY[r] = rankY[r-1] + rankH[r-1] + max(gapY, busTracks[r-1]+1)
	}
	canvasH := rankY[maxRank] + rankH[maxRank]
	bandEnd := make([]int, maxRank+1)
	for r := 0; r <= maxRank; r++ {
		bandEnd[r] = rankY[r] + rankH[r]
	}

	diagramW := 1
	for r, row := range byRank {
		for _, idx := range row {
			w := sizes.boxW[idx]
			h := sizes.boxH[idx]
			cx := centers[idx]
			x := sat(cx, half(w))
			y := rankY[r] + half(rankH[r]-h-sizes.extraH[idx])
			placed[idx] = Placed{X: x, Y: y, W: w, H: h, CX: cx, CY: y + half(h), Rank: r}
			diagramW = max(diagramW, x+w)
			if sizes.extraH[idx] > 0 && sizes.selfLabelW[idx] > 0 {
				diagramW = max(diagramW, x+w+2+sizes.selfLabelW[idx])
			}
		}
	}

	contentW := diagramW
	for i := range graph.Edges {
		e := &graph.Edges[i]
		if e.From == e.To || e.Label == nil {
			continue
		}
		lw := min(stringWidth(*e.Label), MaxLabel)
		if ranks[e.To] == ranks[e.From]+1 {
			contentW = max(contentW, placed[e.To].CX+2+lw)
		} else {
			contentW = max(contentW, diagramW+lw+1)
		}
	}

	edgeLane := make([]int, len(graph.Edges))
	lanes := laneSpans(graph, ranks, placed, true)
	canvasW := contentW
	laneBase := 0
	if len(lanes) > 0 {
		assigned, count := assignTracks(lanes)
		for _, a := range assigned {
			edgeLane[a[0]] = a[1]
		}
		canvasW = contentW + 1 + count
		laneBase = contentW + 1
	}

	return routePlan{canvasW: canvasW, canvasH: canvasH, bandEnd: bandEnd,
		edgeBus: edgeBus, laneBase: laneBase, edgeLane: edgeLane}
}

func placeLr(ranks []int, maxRank int, byRank [][]int, sizes nodeSizes, graph *Graph, placed []Placed) routePlan {
	colW := make([]int, len(byRank))
	for r, row := range byRank {
		if len(row) == 0 {
			continue
		}
		m := 0
		for _, i := range row {
			m = max(m, sizes.boxW[i])
		}
		colW[r] = m
	}

	// Left-to-right edge labels sit in the gap between columns, so the gap has
	// to be wide enough for the widest of them.
	maxLabel := 0
	for i := range graph.Edges {
		e := &graph.Edges[i]
		if (e.From == e.To || ranks[e.To] == ranks[e.From]+1) && e.Label != nil {
			maxLabel = max(maxLabel, min(stringWidth(*e.Label), MaxLabel))
		}
	}
	baseGap := max(gapX+1, maxLabel+3)

	centers := assignPositions(byRank, sizes.layH, 1, graph.Edges, ranks)

	edgeBus := make([]int, len(graph.Edges))
	busTracks := make([]int, maxRank+1)
	for r := 0; r < maxRank; r++ {
		spans := busSpans(graph, ranks, centers, r, true)
		if len(spans) == 0 {
			continue
		}
		assigned, count := assignTracks(spans)
		for _, a := range assigned {
			edgeBus[a[0]] = a[1]
		}
		busTracks[r] = count
	}

	rankX := make([]int, maxRank+1)
	for r := 1; r <= maxRank; r++ {
		rankX[r] = rankX[r-1] + colW[r-1] + max(baseGap, busTracks[r-1]+1)
	}
	selfTails := []int{}
	for _, i := range byRank[maxRank] {
		if sizes.extraH[i] > 0 && sizes.selfLabelW[i] > 0 {
			selfTails = append(selfTails, 2+sizes.selfLabelW[i])
		}
	}
	canvasW := rankX[maxRank] + colW[maxRank]
	if len(selfTails) > 0 {
		m := 0
		for _, t := range selfTails {
			m = max(m, t)
		}
		canvasW += m
	}
	bandEnd := make([]int, maxRank+1)
	for r := 0; r <= maxRank; r++ {
		bandEnd[r] = rankX[r] + colW[r]
	}

	diagramH := 1
	for r, row := range byRank {
		x := rankX[r]
		for _, idx := range row {
			w := sizes.boxW[idx]
			h := sizes.boxH[idx]
			cy := centers[idx]
			y := sat(cy, half(h+sizes.extraH[idx]))
			placed[idx] = Placed{X: x, Y: y, W: w, H: h, CX: x + half(w), CY: y + half(h), Rank: r}
			diagramH = max(diagramH, y+h+sizes.extraH[idx])
		}
	}

	edgeLane := make([]int, len(graph.Edges))
	lanes := laneSpans(graph, ranks, placed, false)
	canvasH := diagramH
	laneBase := 0
	if len(lanes) > 0 {
		assigned, count := assignTracks(lanes)
		for _, a := range assigned {
			edgeLane[a[0]] = a[1]
		}
		canvasH = diagramH + 1 + count
		laneBase = diagramH + 1
	}

	return routePlan{canvasW: canvasW, canvasH: canvasH, bandEnd: bandEnd,
		edgeBus: edgeBus, laneBase: laneBase, edgeLane: edgeLane}
}

// -------------------------------------------------------------------- canvas

// layoutCanvas ranks, places, draws and routes a graph onto a fresh canvas.
// Returns nil when the diagram is empty or over the cell cap.
func layoutCanvas(graph *Graph, extras []NodeExtra) *Canvas {
	n := len(graph.Nodes)
	if n == 0 {
		return nil
	}

	ranks := computeRanks(graph)
	maxRank := 0
	for _, r := range ranks {
		maxRank = max(maxRank, r)
	}

	byRank := make([][]int, maxRank+1)
	for idx, r := range ranks {
		byRank[r] = append(byRank[r], idx)
	}
	orderRanks(byRank, graph.Edges, ranks)

	wrapped := make([][]string, n)
	for i, node := range graph.Nodes {
		wrapped[i] = wrapLabel(node.Label, WrapWidth, MaxLines)
	}
	widest := func(lines []string) int {
		w := 1
		for _, l := range lines {
			w = max(w, stringWidth(l))
		}
		return w
	}

	boxW := make([]int, n)
	boxH := make([]int, n)
	for i := range extras {
		switch extras[i].Kind {
		case "frame":
			boxW[i] = max(extras[i].Sub.w+2,
				stringWidth(fitLabel(graph.Nodes[i].Label, WrapWidth))+4)
			boxH[i] = extras[i].Sub.h + 2
		case "compartments":
			var flat []string
			for _, sec := range extras[i].Sections {
				flat = append(flat, sec...)
			}
			boxW[i] = widest(flat) + 2*pad + 2
			filled := 0
			total := 0
			for _, sec := range extras[i].Sections {
				total += len(sec)
				if len(sec) > 0 {
					filled++
				}
			}
			boxH[i] = total + sat(filled, 1) + 2
		default: // "plain"
			boxW[i] = widest(wrapped[i]) + 2*pad + 2
			boxH[i] = len(wrapped[i]) + 2
		}
	}

	// A self-edge needs two rows below its box, and room beside it for a
	// label.
	extraH := make([]int, n)
	selfLabelW := make([]int, n)
	for i := range graph.Edges {
		e := &graph.Edges[i]
		if e.From != e.To {
			continue
		}
		extraH[e.From] = 2
		if e.Label != nil {
			selfLabelW[e.From] = max(selfLabelW[e.From], min(stringWidth(*e.Label), MaxLabel))
		}
	}
	for i := 0; i < n; i++ {
		if extraH[i] > 0 {
			boxW[i] = max(boxW[i], 7)
		}
	}

	sizes := nodeSizes{
		boxW:       boxW,
		boxH:       boxH,
		layW:       make([]int, n),
		layH:       make([]int, n),
		extraH:     extraH,
		selfLabelW: selfLabelW,
	}
	for i := 0; i < n; i++ {
		if selfLabelW[i] > 0 {
			sizes.layW[i] = boxW[i] + 2*(selfLabelW[i]+3)
		} else {
			sizes.layW[i] = boxW[i]
		}
		sizes.layH[i] = boxH[i] + extraH[i]
	}

	placed := make([]Placed, n)

	vertical := graph.Dir == DirDown || graph.Dir == DirUp
	var plan routePlan
	if vertical {
		plan = placeTd(ranks, maxRank, byRank, sizes, graph, placed)
	} else {
		plan = placeLr(ranks, maxRank, byRank, sizes, graph, placed)
	}

	if plan.canvasW*plan.canvasH > maxCanvasCells {
		return nil
	}

	canvas := NewCanvas(plan.canvasW, plan.canvasH)
	for idx := 0; idx < n; idx++ {
		extra := &extras[idx]
		switch extra.Kind {
		case "frame":
			drawFrame(canvas, placed[idx], graph.Nodes[idx].Label, extra.Sub)
		case "compartments":
			drawClassBox(canvas, placed[idx], extra.Sections)
		default:
			drawBox(canvas, placed[idx], wrapped[idx], graph.Nodes[idx].Shape)
		}
	}

	for i := range graph.Edges {
		edge := &graph.Edges[i]
		switch edge.Line {
		case LineDotted:
			canvas.curStyle = StyDot
		case LineThick:
			canvas.curStyle = StyThick
		default:
			canvas.curStyle = StySolid
		}
		if edge.From == edge.To {
			routeSelf(canvas, placed[edge.From], edge)
			continue
		}
		from := placed[edge.From]
		to := placed[edge.To]
		adjacent := to.Rank == from.Rank+1
		bus := plan.bandEnd[from.Rank] + plan.edgeBus[i]
		lane := plan.laneBase + plan.edgeLane[i]
		if vertical {
			if adjacent {
				routeForward(canvas, from, to, edge, bus)
			} else {
				routeBack(canvas, from, to, edge, lane)
			}
		} else if adjacent {
			routeForwardLr(canvas, from, to, edge, bus)
		} else {
			routeBackLr(canvas, from, to, edge, lane)
		}
	}

	canvas.FinalizeMask()
	return canvas
}

// orient applies the direction flip a finished canvas needs for BT / RL.
func orient(canvas *Canvas, graph *Graph) *Canvas {
	if graph.Dir == DirUp {
		canvas.FlipVertical()
	} else if graph.Dir == DirLeft {
		canvas.FlipHorizontal()
	}
	return canvas
}

// layoutFlowchart lays out flowchart and state diagrams: plain boxes, no extra
// content.
func layoutFlowchart(graph *Graph) *Canvas {
	extras := make([]NodeExtra, len(graph.Nodes))
	for i := range extras {
		extras[i] = NodeExtraPlain()
	}
	canvas := layoutCanvas(graph, extras)
	if canvas == nil {
		return nil
	}
	return orient(canvas, graph)
}

// layoutClass lays out class and ER diagrams: boxes divided into title /
// attribute / method rows.
func layoutClass(graph *Graph, infos []ClassInfo) *Canvas {
	extras := make([]NodeExtra, len(graph.Nodes))
	for i := range graph.Nodes {
		var title []string
		if infos[i].Annotation != "" {
			title = append(title, "«"+infos[i].Annotation+"»")
		}
		title = append(title, displayGenerics(graph.Nodes[i].Label))
		extras[i] = NodeExtra{Kind: "compartments",
			Sections: [][]string{title, infos[i].Attrs, infos[i].Methods}}
	}
	canvas := layoutCanvas(graph, extras)
	if canvas == nil {
		return nil
	}
	return orient(canvas, graph)
}

// -------------------------------------------------------------------- groups

// itemKey identifies a laid-out item: "n<i>" for a node, "g<i>" for a group.
type itemKey = string

func nodeKey(i int) itemKey { return "n" + strconv.Itoa(i) }

func groupKey(i int) itemKey { return "g" + strconv.Itoa(i) }

// edgeRef is one edge bucketed by scope: endpoint keys plus the edge index.
type edgeRef struct {
	f, t itemKey
	ei   int
}

// layoutGrouped lays out a flowchart that uses subgraph.
//
// Each subgraph becomes a framed box holding its own independently laid-out
// canvas. An edge is drawn in the innermost scope containing both endpoints;
// one crossing a subgraph boundary attaches to the frame instead of the node.
func layoutGrouped(graph *Graph) *Canvas {
	// A node whose id matches a subgraph id stands in for that subgraph.
	proxy := map[int]int{}
	for gi, g := range graph.Groups {
		if ni, ok := graph.Index[g.ID]; ok {
			proxy[ni] = gi
		}
	}

	groupChain := func(g int) []int {
		var chain []int
		cur := g
		for cur != -1 {
			chain = append(chain, cur)
			cur = graph.Groups[cur].Parent
		}
		// reverse
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
		return chain
	}
	endpoint := func(n int) (itemKey, []int) {
		if gi, ok := proxy[n]; ok {
			return groupKey(gi), groupChain(graph.Groups[gi].Parent)
		}
		return nodeKey(n), groupChain(graph.NodeGroup[n])
	}

	// Edges bucketed by the scope that draws them; scopeNull (-1) is the top
	// level.
	const scopeNull = -1
	scopeEdges := map[int][]edgeRef{}
	referenced := make([]bool, len(graph.Groups))
	for ei := range graph.Edges {
		e := &graph.Edges[ei]
		fKey, fChain := endpoint(e.From)
		tKey, tChain := endpoint(e.To)
		k := 0
		for k < len(fChain) && k < len(tChain) && fChain[k] == tChain[k] {
			k++
		}
		scope := scopeNull
		if k > 0 {
			scope = fChain[k-1]
		}
		fOut := fKey
		if len(fChain) > k {
			fOut = groupKey(fChain[k])
		}
		tOut := tKey
		if len(tChain) > k {
			tOut = groupKey(tChain[k])
		}
		for _, key := range []itemKey{fOut, tOut} {
			if strings.HasPrefix(key, "g") {
				n, _ := strconv.Atoi(key[1:])
				referenced[n] = true
			}
		}
		scopeEdges[scope] = append(scopeEdges[scope], edgeRef{f: fOut, t: tOut, ei: ei})
	}

	directNodes := map[int][]int{}
	for ni, g := range graph.NodeGroup {
		if _, ok := proxy[ni]; ok {
			continue
		}
		directNodes[g] = append(directNodes[g], ni)
	}

	// Drop empty subgraphs, but keep any that an edge attaches to.
	keep := make([]bool, len(graph.Groups))
	for gi := len(graph.Groups) - 1; gi >= 0; gi-- {
		hasNodes := len(directNodes[gi]) > 0
		hasChildren := false
		for c, g := range graph.Groups {
			if g.Parent == gi && keep[c] {
				hasChildren = true
				break
			}
		}
		keep[gi] = hasNodes || hasChildren || referenced[gi]
	}

	canvas := buildScope(graph, scopeNull, scopeEdges, directNodes, keep)
	if canvas == nil {
		return nil
	}
	return orient(canvas, graph)
}

func buildScope(
	graph *Graph,
	scope int,
	scopeEdges map[int][]edgeRef,
	directNodes map[int][]int,
	keep []bool,
) *Canvas {
	var items []itemKey
	for _, ni := range directNodes[scope] {
		items = append(items, nodeKey(ni))
	}
	for gi := range graph.Groups {
		if graph.Groups[gi].Parent == scope && keep[gi] {
			items = append(items, groupKey(gi))
		}
	}

	if len(items) == 0 {
		return NewCanvas(1, 1)
	}

	indexOf := map[itemKey]int{}
	var nodes []Node
	extras := []NodeExtra{}
	for _, item := range items {
		indexOf[item] = len(nodes)
		i, _ := strconv.Atoi(item[1:])
		if strings.HasPrefix(item, "n") {
			nodes = append(nodes, Node{Label: graph.Nodes[i].Label, Shape: graph.Nodes[i].Shape})
			extras = append(extras, NodeExtraPlain())
		} else {
			sub := buildScope(graph, i, scopeEdges, directNodes, keep)
			if sub == nil {
				return nil
			}
			nodes = append(nodes, Node{Label: graph.Groups[i].Label, Shape: ShapeRect})
			extras = append(extras, NodeExtra{Kind: "frame", Sub: sub})
		}
	}

	var edges []Edge
	for _, ref := range scopeEdges[scope] {
		fi, okF := indexOf[ref.f]
		ti, okT := indexOf[ref.t]
		if !okF || !okT {
			continue
		}
		e := graph.Edges[ref.ei]
		edges = append(edges, Edge{
			From: fi, To: ti, Label: e.Label,
			HeadTo: e.HeadTo, HeadFrom: e.HeadFrom, Line: e.Line,
		})
	}

	// Layout only reads nodes/edges/dir, so a bare Graph carrying those is
	// enough.
	synth := NewGraph(graph.Dir)
	synth.Nodes = nodes
	synth.Edges = edges
	return layoutCanvas(synth, extras)
}

// ------------------------------------------------------------------- drawing

// drawBox draws a node box with its label lines.
func drawBox(canvas *Canvas, p Placed, lines []string, shape Shape) {
	x, y, w, h := p.X, p.Y, p.W, p.H
	right := x + w - 1
	bottom := y + h - 1

	rounded := shape == ShapeRound || shape == ShapeDiamond
	tl, tr, bl, br := "┌", "┐", "└", "┘"
	if rounded {
		tl, tr, bl, br = "╭", "╮", "╰", "╯"
	}
	canvas.Set(x, y, tl, ClsBorder)
	canvas.Set(right, y, tr, ClsBorder)
	canvas.Set(x, bottom, bl, ClsBorder)
	canvas.Set(right, bottom, br, ClsBorder)

	// The perimeter is drawn as bits so edges can tee into it, but it is the
	// box outline, so it claims border rather than edge.
	for cx := x + 1; cx < right; cx++ {
		canvas.AddBits(cx, y, BitL|BitR, ClsBorder)
		canvas.AddBits(cx, bottom, BitL|BitR, ClsBorder)
	}
	for cy := y + 1; cy < bottom; cy++ {
		canvas.AddBits(x, cy, BitU|BitD, ClsBorder)
		canvas.AddBits(right, cy, BitU|BitD, ClsBorder)
	}

	for cy := y; cy <= bottom; cy++ {
		for cx := x; cx <= right; cx++ {
			canvas.occupied[canvas.idx(cx, cy)] = 1
		}
	}

	inner := max(1, sat(w, 2*pad+2))
	for li, line := range lines {
		text := fitLabel(line, inner)
		textX := x + 1 + pad + half(sat(inner, stringWidth(text)))
		DrawText(canvas, text, textX, y+1+li, ClsText)
	}
}

// drawClassBox draws a class or ER box: sections separated by horizontal
// rules, title centred.
func drawClassBox(canvas *Canvas, p Placed, sections [][]string) {
	drawBox(canvas, p, nil, ShapeRect)
	inner := max(1, sat(p.W, 2*pad+2))
	row := p.Y + 1
	first := true
	for si, section := range sections {
		if len(section) == 0 {
			continue
		}
		if !first {
			canvas.Set(p.X, row, "├", ClsBorder)
			for x := p.X + 1; x < p.X+p.W-1; x++ {
				canvas.Set(x, row, "─", ClsBorder)
			}
			canvas.Set(p.X+p.W-1, row, "┤", ClsBorder)
			row++
		}
		first = false
		for _, line := range section {
			text := fitLabel(line, inner)
			tx := p.X + 1 + pad
			if si == 0 {
				tx = p.X + 1 + pad + half(sat(inner, stringWidth(text)))
			}
			DrawTextOverEdges(canvas, text, tx, row, ClsText)
			row++
		}
	}
}

// drawFrame draws a subgraph frame: a titled box with a finished sub-canvas
// centred inside.
func drawFrame(canvas *Canvas, p Placed, title string, sub *Canvas) {
	drawBox(canvas, p, nil, ShapeRect)
	t := fitLabel(title, sat(p.W, 4))
	DrawTextOverEdges(canvas, " "+t+" ", p.X+1, p.Y, ClsText)
	canvas.Blit(sub, p.X+1+half(p.W-2-sub.w), p.Y+1+half(p.H-2-sub.h))
}

// ------------------------------------------------------------------- routing

func headGlyph(head Head, arrow string) string {
	switch head {
	case HeadCircle:
		return "o"
	case HeadCross:
		return "×"
	case HeadDiamondFill:
		return "◆"
	case HeadDiamondOpen:
		return "◇"
	case HeadTriangle:
		switch arrow {
		case "▼":
			return "▽"
		case "▲":
			return "△"
		case "◄":
			return "◁"
		case "▶":
			return "▷"
		default:
			return arrow
		}
	default:
		return arrow
	}
}

// routeForward routes adjacent ranks, top-down: drop, jog along the bus row,
// drop into the head.
func routeForward(canvas *Canvas, from Placed, to Placed, edge *Edge, bus int) {
	tx := to.CX
	// A jog of one column reads as a kink; snap straight instead.
	bx := from.CX
	if abs(from.CX-tx) <= 1 {
		bx = tx
	}
	by := from.Y + from.H - 1
	headRow := to.Y - 1

	canvas.Junction(bx, by, BitD)
	canvas.SegV(bx, by, bus)
	if bx == tx {
		canvas.SegV(bx, bus, headRow)
	} else {
		canvas.SegH(bus, bx, tx)
		canvas.SegV(tx, bus, headRow)
	}

	if edge.HeadTo == HeadNone {
		canvas.AddBits(tx, headRow, BitU, ClsEdge)
	} else {
		canvas.Set(tx, headRow, headGlyph(edge.HeadTo, "▼"), ClsEdge)
	}
	if edge.HeadFrom != HeadNone {
		canvas.Set(bx, by, headGlyph(edge.HeadFrom, "▲"), ClsEdge)
	}

	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, headRow, tx+1)
	}
}

// routeSelf draws a self-edge: a stub loop hanging below the box.
func routeSelf(canvas *Canvas, p Placed, edge *Edge) {
	bottom := p.Y + p.H - 1
	exitX := p.CX + 1
	retX := p.X + p.W - 2
	if retX <= exitX || bottom+2 >= canvas.h {
		return
	}

	v, h, bLeft, bRight := "│", "─", "╰", "╯"
	switch edge.Line {
	case LineDotted:
		v, h, bLeft, bRight = "╎", "╌", "╰", "╯"
	case LineThick:
		v, h, bLeft, bRight = "┃", "━", "┗", "┛"
	}

	canvas.Junction(exitX, bottom, BitD)
	canvas.Set(exitX, bottom+1, v, ClsEdge)
	canvas.Set(exitX, bottom+2, bLeft, ClsEdge)
	for x := exitX + 1; x < retX; x++ {
		canvas.Set(x, bottom+2, h, ClsEdge)
	}
	canvas.Set(retX, bottom+2, bRight, ClsEdge)
	canvas.Set(retX, bottom+1, headGlyph(edge.HeadTo, "▲"), ClsEdge)
	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, bottom+1, p.X+p.W+1)
	}
}

// routeBack routes skip or back edges, top-down: out the right side, up a
// lane, back in.
func routeBack(canvas *Canvas, from Placed, to Placed, edge *Edge, laneX int) {
	sx := from.X + from.W - 1
	sy := from.CY
	tx := to.X + to.W - 1
	tyc := to.CY

	canvas.Junction(sx, sy, BitR)
	canvas.SegH(sy, sx, laneX)
	canvas.SegV(laneX, sy, tyc)
	canvas.SegH(tyc, tx+1, laneX)

	if edge.HeadTo == HeadNone {
		canvas.AddBits(tx+1, tyc, BitR, ClsEdge)
	} else {
		canvas.Set(tx+1, tyc, headGlyph(edge.HeadTo, "◄"), ClsEdge)
	}
	if edge.HeadFrom != HeadNone {
		canvas.Set(sx, sy, headGlyph(edge.HeadFrom, "◄"), ClsEdge)
	}

	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, sat(tyc, 1), sat(laneX, stringWidth(*edge.Label)+1))
	}
}

// routeForwardLr routes adjacent ranks, left-to-right: out the right side, jog
// on the bus column.
func routeForwardLr(canvas *Canvas, from Placed, to Placed, edge *Edge, bus int) {
	rx := from.X + from.W - 1
	ry := from.CY
	ly := to.CY
	headCol := to.X - 1

	canvas.Junction(rx, ry, BitR)
	canvas.SegH(ry, rx, bus)
	if ry == ly {
		canvas.SegH(ry, bus, headCol)
	} else {
		canvas.SegV(bus, ry, ly)
		canvas.SegH(ly, bus, headCol)
	}

	if edge.HeadTo == HeadNone {
		canvas.AddBits(headCol, ly, BitR, ClsEdge)
	} else {
		canvas.Set(headCol, ly, headGlyph(edge.HeadTo, "▶"), ClsEdge)
	}
	if edge.HeadFrom != HeadNone {
		canvas.Set(rx, ry, headGlyph(edge.HeadFrom, "◄"), ClsEdge)
	}

	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, sat(ly, 1), bus+1)
	}
}

// routeBackLr routes skip or back edges, left-to-right: down out the bottom,
// along a lane, back up.
func routeBackLr(canvas *Canvas, from Placed, to Placed, edge *Edge, laneY int) {
	sx := from.CX
	sy := from.Y + from.H - 1
	tx := to.CX
	ty := to.Y + to.H - 1

	canvas.Junction(sx, sy, BitD)
	canvas.SegV(sx, sy, laneY)
	canvas.SegH(laneY, sx, tx)
	canvas.SegV(tx, laneY, ty+1)

	if edge.HeadTo == HeadNone {
		canvas.AddBits(tx, ty+1, BitD, ClsEdge)
	} else {
		canvas.Set(tx, ty+1, headGlyph(edge.HeadTo, "▲"), ClsEdge)
	}
	if edge.HeadFrom != HeadNone {
		canvas.Set(sx, sy, headGlyph(edge.HeadFrom, "▲"), ClsEdge)
	}

	if edge.Label != nil {
		placeLabel(canvas, *edge.Label, sat(laneY, 1), half(sx+tx))
	}
}

// placeLabel writes an edge label, stopping at the first cell already
// occupied.
func placeLabel(canvas *Canvas, label string, row, startX int) {
	if row >= canvas.h {
		return
	}
	text := fitLabel(label, MaxLabel)
	x := startX
	for _, ch := range measured(text) {
		if ch.w == 0 {
			continue
		}
		if x+ch.w > canvas.w {
			break
		}
		blocked := false
		for k := 0; k < ch.w; k++ {
			i := canvas.idx(x+k, row)
			if canvas.ch[i] != " " || canvas.mask[i] != 0 || canvas.occupied[i] != 0 {
				blocked = true
			}
		}
		if blocked {
			break
		}
		canvas.Set(x, row, ch.text, ClsEdgeLabel)
		for k := 1; k < ch.w; k++ {
			canvas.Set(x+k, row, CONT, ClsEdgeLabel)
		}
		x += ch.w
	}
}
