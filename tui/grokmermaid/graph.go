// Ported from pi node_modules/grok-mermaid/src/graph.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. The shared diagram model. Flowchart, state,
// class and ER sources all parse into a Graph; only sequence diagrams have
// their own model.
package grokmermaid

// Caps that keep layout bounded; exceeding one drops the diagram to fallback.
const (
	MaxNodes      = 128
	MaxEdges      = 512
	MaxGroups     = 24
	MaxGroupDepth = 6
	// MaxMembers: class members / ER attributes listed per box before eliding
	// with `…`.
	MaxMembers = 8
)

// Shape is a node box shape.
type Shape = string

const (
	ShapeRect    Shape = "rect"
	ShapeRound   Shape = "round"
	ShapeDiamond Shape = "diamond"
)

// Head is a decoration at one end of an edge.
type Head = string

const (
	HeadNone        Head = "none"
	HeadArrow       Head = "arrow"
	HeadCircle      Head = "circle"
	HeadCross       Head = "cross"
	HeadTriangle    Head = "triangle"
	HeadDiamondFill Head = "diamondFill"
	HeadDiamondOpen Head = "diamondOpen"
)

// LineKind is an edge line style.
type LineKind = string

const (
	LineSolid  LineKind = "solid"
	LineDotted LineKind = "dotted"
	LineThick  LineKind = "thick"
)

// Dir is the flow direction.
type Dir = string

const (
	DirDown  Dir = "down"
	DirUp    Dir = "up"
	DirRight Dir = "right"
	DirLeft  Dir = "left"
)

// Node is a diagram node.
type Node struct {
	Label string
	Shape Shape
}

// Edge is a diagram edge.
type Edge struct {
	From     int
	To       int
	Label    *string // nil = null (pi: string | null)
	HeadTo   Head
	HeadFrom Head
	Line     LineKind
}

// Group is a subgraph.
type Group struct {
	ID     string
	Label  string
	Parent int // -1 = null (pi: number | null)
}

// ClassInfo is extra compartment content for class and ER boxes.
type ClassInfo struct {
	Annotation string // "" = null (pi: string | null; "" never renders)
	Attrs      []string
	Methods    []string
}

// emptyClassInfo mirrors pi's emptyClassInfo().
func emptyClassInfo() ClassInfo { return ClassInfo{Annotation: ""} }

// ParseDir parses LR/RL/BT as written in a header or direction statement;
// else down.
func ParseDir(token string) Dir {
	switch asciiUpper(token) {
	case "LR":
		return DirRight
	case "RL":
		return DirLeft
	case "BT":
		return DirUp
	default:
		return DirDown
	}
}

// Graph is the shared parse model.
type Graph struct {
	Nodes  []Node
	Edges  []Edge
	Index  map[string]int
	Groups []Group
	// NodeGroup is the innermost subgraph each node was declared in, parallel
	// to Nodes (-1 = null; pi: (number | null)[]).
	NodeGroup []int
	CurGroup  int // -1 = null (pi: number | null)
	// OverCap is set when a cap was hit; the caller abandons the parse.
	OverCap bool
	// Warnings is text the flowchart grammar could not read and silently
	// discarded.
	//
	// Flowchart parsing is deliberately lenient — a malformed statement
	// contributes whatever prefix parsed and the rest is dropped — so without
	// these the reader gets a clean diagram that is not what they wrote.
	Warnings []string
	Dir      Dir
}

// NewGraph constructs a Graph with the given direction.
func NewGraph(dir Dir) *Graph {
	return &Graph{
		Index:    map[string]int{},
		CurGroup: -1,
		Dir:      dir,
	}
}

// NodeIndex returns the index of id, creating the node if new. A later
// declaration carrying a label overwrites the placeholder one an edge created.
// Returns -1 once MaxNodes is reached (pi: null), which aborts the parse.
// A nil label means "no label given": the id stands in and an existing label
// is kept; a non-nil label — including the empty string — overwrites.
func (g *Graph) NodeIndex(id string, label *string, shape Shape) int {
	if existing, ok := g.Index[id]; ok {
		if label != nil {
			g.Nodes[existing].Label = *label
			g.Nodes[existing].Shape = shape
		}
		return existing
	}
	if len(g.Nodes) >= MaxNodes {
		g.OverCap = true
		return -1
	}
	g.Index[id] = len(g.Nodes)
	node := Node{Label: id, Shape: shape}
	if label != nil {
		node.Label = *label
	}
	g.Nodes = append(g.Nodes, node)
	g.NodeGroup = append(g.NodeGroup, g.CurGroup)
	return len(g.Nodes) - 1
}

// NodeLabel sets a node's label without disturbing its shape, creating it if
// new. Returns -1 (pi: null) when the node cannot be created.
func (g *Graph) NodeLabel(id string, label string) int {
	if existing, ok := g.Index[id]; ok {
		g.Nodes[existing].Label = label
		return existing
	}
	return g.NodeIndex(id, &label, ShapeRound)
}

// PushEdge appends an edge, or flags OverCap when MaxEdges is reached.
func (g *Graph) PushEdge(edge Edge) bool {
	if len(g.Edges) >= MaxEdges {
		g.OverCap = true
		return false
	}
	g.Edges = append(g.Edges, edge)
	return true
}
