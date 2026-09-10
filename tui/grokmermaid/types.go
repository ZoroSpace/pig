// Ported from pi node_modules/grok-mermaid/src/types.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. Semantic classes, spans and the rendered art.
package grokmermaid

// Cls is the semantic class of a run of cells. The renderer never knows about
// colour; consumers map these to their own theme (see ToAnsi for the common
// case).
//
//   - "border"     box outlines, subgraph frames, compartment rules
//   - "text"       node / participant / compartment labels
//   - "edge"       connector lines and arrowheads
//   - "edgeLabel"  text sitting on an edge
//   - "title"      the `mermaid: <kind>` header of a source box
//   - "none"       blank filler
type Cls = string

const (
	ClsBorder    Cls = "border"
	ClsText      Cls = "text"
	ClsEdge      Cls = "edge"
	ClsEdgeLabel Cls = "edgeLabel"
	ClsTitle     Cls = "title"
	ClsNone      Cls = "none"
)

// Span is a run of adjacent cells sharing one semantic class.
type Span struct {
	Text string
	Cls  Cls
}

// MermaidArt is a rendered diagram. Plain[i] and Styled[i] describe the same
// row: Plain is right-trimmed for display width and copy/paste, Styled keeps
// the run structure needed to colour it.
//
// Width is the display columns the widest row needs — the number to compare
// against the space you have. It cannot be recovered from Plain, whose rows
// are strings of code points, not columns.
//
// Warnings lists source the flowchart grammar could not read and dropped.
// Non-empty means the art is real but incomplete — some of what was written is
// not in it. Only flowcharts warn; the other grammars refuse the whole diagram
// instead, and Render returns nil.
//
// They are advisory. Do not gate rendering on them: the art is the best
// drawing of the source either way, and a diagram being typed or streamed
// warns at nearly every intermediate state. Show them alongside, or once it
// settles.
type MermaidArt struct {
	Plain    []string
	Styled   [][]Span
	Width    int
	Warnings []string
}
