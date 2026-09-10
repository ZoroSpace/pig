// Ported from pi node_modules/grok-mermaid/src/parse.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. Source text to diagram model.
//
// Every ParseX returns nil when the source is not that kind of diagram, or
// when it exceeds a cap — Render tries each in turn and falls back to a framed
// copy of the source when they all decline.
//
// pig: TS `[...str]` (code point iteration) maps to []rune; indices are rune
// indices. JS null → nil pointer / -1 per field, as declared at each site.
package grokmermaid

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/samber/lo"
)

// pig: JS .trim()/.trimStart()/.trimEnd() map to strings.TrimSpace /
// TrimLeftFunc(unicode.IsSpace) / TrimRightFunc(unicode.IsSpace). The two
// whitespace sets differ only by U+0085/U+FEFF, which never appear in
// meaningful mermaid source.
//
// trimStartRune/trimEndRune mirror JS .trimStart()/.trimEnd().
func trimStartRune(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }
func trimEndRune(s string) string   { return strings.TrimRightFunc(s, unicode.IsSpace) }

// ---------------------------------------------------------------- statements

// flushStatement mirrors pi's flushStatement: trim cur, push when non-empty,
// return the reset accumulator.
func flushStatement(cur string, out *[]string) string {
	trimmed := strings.TrimSpace(cur)
	if trimmed != "" {
		*out = append(*out, trimmed)
	}
	return ""
}

// splitStatements splits one source line into statements on `;`, stopping at a
// %% comment.
//
// Quoted spans are opaque, so a label may contain `;` and `%%`.
func splitStatements(line string, out *[]string) {
	chars := []rune(line)
	cur := ""
	inQuotes := false
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		if inQuotes {
			if c == '"' {
				inQuotes = false
			}
			cur += string(c)
		} else if c == '"' {
			inQuotes = true
			cur += string(c)
		} else if c == '%' && i+1 < len(chars) && chars[i+1] == '%' {
			break
		} else if c == ';' {
			cur = flushStatement(cur, out)
		} else {
			cur += string(c)
		}
	}
	flushStatement(cur, out)
}

// statementsOf returns all statements in a source block, in order.
func statementsOf(src string) []string {
	var out []string
	for _, line := range srcLines(src) {
		splitStatements(line, &out)
	}
	return out
}

func firstWord(s string) string {
	if w := splitFields(s); len(w) > 0 {
		return w[0]
	}
	return ""
}

func words(s string) []string { return splitFields(s) }

// splitOnce splits on the first occurrence of sep, Rust's split_once. ok is
// false when sep is absent.
func splitOnce(s, sep string) (string, string, bool) {
	i := strings.Index(s, sep)
	if i == -1 {
		return "", "", false
	}
	return s[:i], s[i+len(sep):], true
}

// nonEmpty mirrors pi's nonEmpty: "" → null.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// headerKind returns the diagram kind from the header statement, lowercased.
func headerKind(statements []string) string {
	if len(statements) == 0 {
		return ""
	}
	kind := firstWord(statements[0])
	if kind == "" {
		return ""
	}
	return asciiLower(kind)
}

// DiagramKind is a diagram type this renderer draws.
type DiagramKind = string

const (
	KindFlowchart DiagramKind = "flowchart"
	KindState     DiagramKind = "state"
	KindClass     DiagramKind = "class"
	KindEr        DiagramKind = "er"
	KindSequence  DiagramKind = "sequence"
)

// DiagramKindOf returns the kind of diagram src declares, or "" (pi: null) if
// its header names no type this renderer draws.
//
// Reads the header only — it says nothing about whether the body parses. Pair
// it with Render to tell a source this renderer will never draw from one that
// is merely malformed:
//
//	Render(src) == nil && DiagramKindOf(src) != ""   // syntax error
//
// Each branch mirrors the header test in the matching ParseX, so the two
// always agree on what they recognise.
func DiagramKindOf(src string) DiagramKind {
	kind := headerKind(statementsOf(src))
	switch {
	case kind == "":
		return ""
	case kind == "graph" || kind == "flowchart":
		return KindFlowchart
	case strings.HasPrefix(kind, "statediagram"):
		return KindState
	case strings.HasPrefix(kind, "classdiagram"):
		return KindClass
	case kind == "erdiagram":
		return KindEr
	case kind == "sequencediagram":
		return KindSequence
	default:
		return ""
	}
}

// ----------------------------------------------------------------- flowchart

// ParseGraph parses a `graph`/`flowchart` source.
func ParseGraph(src string) *Graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind != "graph" && kind != "flowchart" {
		return nil
	}

	dir := DirDown
	if w := words(statements[0]); len(w) > 1 {
		dir = ParseDir(w[1])
	}
	graph := NewGraph(dir)
	var stack []int

	for _, st := range statements[1:] {
		switch asciiLower(firstWord(st)) {
		case "subgraph":
			if len(graph.Groups) >= MaxGroups || len(stack) >= MaxGroupDepth {
				return nil
			}
			id, label := parseSubgraphDecl(strings.TrimSpace(st[len("subgraph"):]))
			parent := -1
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			graph.Groups = append(graph.Groups, Group{ID: id, Label: label, Parent: parent})
			stack = append(stack, len(graph.Groups)-1)
			graph.CurGroup = stack[len(stack)-1]
			continue
		case "end":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if len(stack) > 0 {
				graph.CurGroup = stack[len(stack)-1]
			} else {
				graph.CurGroup = -1
			}
			continue
		case "classdef", "class", "style", "linkstyle", "click", "direction":
			continue
		default:
		}
		parseStatement(st, graph)
		if graph.OverCap {
			return nil
		}
	}

	if len(graph.Nodes) == 0 {
		return nil
	}
	return graph
}

// parseSubgraphDecl parses `subgraph id[Title]`, `subgraph "Title"`, or a bare
// title.
func parseSubgraphDecl(rest string) (string, string) {
	if strings.HasPrefix(rest, "\"") {
		if close := strings.Index(rest[1:], "\""); close != -1 {
			label := rest[1 : 1+close]
			return label, decodeHtmlEntities(label)
		}
	}
	if open := strings.Index(rest, "["); open != -1 {
		id := strings.TrimSpace(rest[:open])
		label := cleanLabel(strings.TrimSpace(strings.TrimRight(rest[open+1:], "]")))
		if id != "" && label != "" {
			return id, label
		}
	}
	return rest, rest
}

// parseStatement parses a chain of `node link node link node ...`, each link
// fanning out over `&`.
//
// Parses as far as it can and keeps the prefix, matching upstream and
// mermaid.js. Whatever it could not read is recorded in graph.warnings rather
// than failing the diagram — see the note on that field.
func parseStatement(st string, graph *Graph) {
	chars := []rune(st)
	i := 0

	head := parseNodeGroup(chars, i, graph)
	if head == nil {
		graph.Warnings = append(graph.Warnings, "dropped, does not start with a node: \""+st+"\"")
		return
	}
	prev := head.group
	i = head.next

	for {
		i = skipSpaces(chars, i)
		if i >= len(chars) {
			break
		}
		link := parseLink(chars, i)
		if link == nil {
			graph.Warnings = append(graph.Warnings,
				"dropped, expected a link: \""+string(chars[i:])+"\"")
			break
		}
		i = skipSpaces(chars, link.next)
		target := parseNodeGroup(chars, i, graph)
		if target == nil {
			graph.Warnings = append(graph.Warnings, "dropped, link has no target: \""+st+"\"")
			break
		}
		i = target.next
		for _, f := range prev {
			for _, t := range target.group {
				// `A <-- B` reads right-to-left: swap the endpoints so the
				// arrow that was written on the left becomes a normal forward
				// head.
				reversed := link.left == HeadArrow && link.right != HeadArrow
				edge := Edge{
					From:     f,
					To:       t,
					HeadTo:   link.right,
					HeadFrom: link.left,
					Line:     link.line,
					Label:    link.label,
				}
				if reversed {
					edge.From = t
					edge.To = f
					edge.HeadTo = HeadArrow
					edge.HeadFrom = link.right
				}
				if !graph.PushEdge(edge) {
					return
				}
			}
		}
		prev = target.group
	}
}

// nodeGroupResult is the outcome of parseNodeGroup.
type nodeGroupResult struct {
	group []int
	next  int
}

// parseNodeGroup reads one or more nodes joined by `&`, which fan out into a
// cross product.
func parseNodeGroup(chars []rune, start int, graph *Graph) *nodeGroupResult {
	first := parseNode(chars, start, graph)
	if first == nil {
		return nil
	}
	group := []int{first.index}
	i := first.next
	for {
		j := skipSpaces(chars, i)
		if j >= len(chars) || chars[j] != '&' {
			break
		}
		next := parseNode(chars, j+1, graph)
		if next == nil {
			return nil
		}
		group = append(group, next.index)
		i = next.next
	}
	return &nodeGroupResult{group: group, next: i}
}

func skipSpaces(chars []rune, i int) int {
	for i < len(chars) && (chars[i] == ' ' || chars[i] == '\t') {
		i++
	}
	return i
}

// nodeResult is the outcome of parseNode.
type nodeResult struct {
	index int
	next  int
}

func parseNode(chars []rune, start int, graph *Graph) *nodeResult {
	i := skipSpaces(chars, start)
	idStart := i
	for i < len(chars) && isIdChar(string(chars[i])) {
		i++
	}
	if i == idStart {
		return nil
	}
	id := string(chars[idStart:i])

	shaped := readShapeAt(chars, i)
	if shaped.unclosed != "" {
		graph.Warnings = append(graph.Warnings,
			"node \""+id+"\": label is missing its closing `"+shaped.unclosed+"`")
	}
	index := graph.NodeIndex(id, shaped.label, shaped.shape)
	if index == -1 {
		return nil
	}
	return &nodeResult{index: index, next: shaped.after}
}

// shapedResult is what a shape bracket yielded. Closer is set when the bracket
// never closed.
type shapedResult struct {
	shape    Shape
	label    *string // nil = null (pi: string | null)
	after    int
	unclosed string // "" = none (pi: unclosed?)
}

// readShapeAt dispatches on the bracket following an id to pick shape and
// closing token.
func readShapeAt(chars []rune, i int) shapedResult {
	var c, n rune
	if i < len(chars) {
		c = chars[i]
	}
	if i+1 < len(chars) {
		n = chars[i+1]
	}
	switch {
	case c == '[':
		if n == '[' {
			return readShape(chars, i+2, "]]", ShapeRect)
		}
		if n == '(' {
			return readShape(chars, i+2, ")]", ShapeRound)
		}
		return readShape(chars, i+1, "]", ShapeRect)
	case c == '(':
		if n == '(' {
			return readShape(chars, i+2, "))", ShapeRound)
		}
		if n == '[' {
			return readShape(chars, i+2, "])", ShapeRound)
		}
		return readShape(chars, i+1, ")", ShapeRound)
	case c == '{':
		if n == '{' {
			return readShape(chars, i+2, "}}", ShapeDiamond)
		}
		return readShape(chars, i+1, "}", ShapeDiamond)
	case c == '>':
		return readShape(chars, i+1, "]", ShapeRect)
	default:
		return shapedResult{shape: ShapeRect, label: nil, after: i}
	}
}

// readShape reads label text up to closer.
//
// Quoting is decided by the first non-space character: inside a quoted label
// the closer is ignored until the quote closes, so A["a] b"] is one node.
// An unquoted label ends at the first closer, so A[5" pipe] keeps its quote.
func readShape(chars []rune, start int, closer string, shape Shape) shapedResult {
	j := start
	for j < len(chars) && (chars[j] == ' ' || chars[j] == '\t') {
		j++
	}
	quoted := j < len(chars) && chars[j] == '"'

	i := start
	var text strings.Builder
	inQuotes := false
	for i < len(chars) {
		c := chars[i]
		if quoted && c == '"' {
			inQuotes = !inQuotes
			text.WriteRune(c)
			i++
			continue
		}
		if !inQuotes && i+len([]rune(closer)) <= len(chars) && string(chars[i:i+len([]rune(closer))]) == closer {
			label := cleanLabel(text.String())
			return shapedResult{shape: shape, label: &label, after: i + len(closer)}
		}
		text.WriteRune(c)
		i++
	}
	// Ran off the end still looking for the closer: everything after the
	// opening bracket became label text, so any link operator in it was
	// swallowed.
	label := cleanLabel(text.String())
	return shapedResult{shape: shape, label: &label, after: len(chars), unclosed: closer}
}

func isLinkChar(c rune) bool {
	return c == '-' || c == '.' || c == '=' || c == '<' || c == '>'
}

// linkResult is a parsed link operator with its label.
type linkResult struct {
	left  Head
	right Head
	line  LineKind
	label *string // nil = null
	next  int
}

// parseLink reads a link operator and its label.
//
// Labels come in two forms: `-->|text|` and the inline `-- text -->`, the
// latter only when the first operator carried no head.
func parseLink(chars []rune, start int) *linkResult {
	i := skipSpaces(chars, start)
	left := HeadNone
	// A leading `o`/`x` decorates the tail, but only directly before an
	// operator.
	if i < len(chars) && (chars[i] == 'o' || chars[i] == 'x') &&
		i+1 < len(chars) && (chars[i+1] == '-' || chars[i+1] == '.' || chars[i+1] == '=') {
		if chars[i] == 'o' {
			left = HeadCircle
		} else {
			left = HeadCross
		}
		i++
	}

	opStart := i
	for i < len(chars) && isLinkChar(chars[i]) {
		i++
	}
	if i == opStart {
		return nil
	}
	op1 := string(chars[opStart:i])
	if left == HeadNone && strings.HasPrefix(op1, "<") {
		left = HeadArrow
	}

	line := lineKind(op1)
	right := HeadNone
	if strings.Contains(op1, ">") {
		right = HeadArrow
	}
	if right == HeadNone {
		if trailing := trailingHead(chars, i); trailing != nil {
			right = trailing.head
			i = trailing.next
		}
	}

	if i < len(chars) && chars[i] == '|' {
		i++
		lStart := i
		for i < len(chars) && chars[i] != '|' {
			i++
		}
		label := cleanLabel(string(chars[lStart:i]))
		if i < len(chars) && chars[i] == '|' {
			i++
		}
		return &linkResult{left: left, right: right, line: line, label: nonEmpty(label), next: i}
	}

	if right == HeadNone {
		textStart := skipSpaces(chars, i)
		j := textStart
		for j < len(chars) && !isLinkChar(chars[j]) {
			j++
		}
		if j < len(chars) && j > textStart && chars[j] != '<' {
			text := string(chars[textStart:j])
			op2Start := j
			for j < len(chars) && isLinkChar(chars[j]) {
				j++
			}
			op2 := string(chars[op2Start:j])
			if strings.Contains(op2, ">") {
				right = HeadArrow
			} else if trailing := trailingHead(chars, j); trailing != nil {
				right = trailing.head
				j = trailing.next
			}
			if line == LineSolid {
				line = lineKind(op2)
			}
			return &linkResult{left: left, right: right, line: line,
				label: nonEmpty(cleanLabel(text)), next: j}
		}
	}

	return &linkResult{left: left, right: right, line: line, label: nil, next: i}
}

func lineKind(op string) LineKind {
	if strings.Contains(op, "=") {
		return LineThick
	}
	if strings.Contains(op, ".") {
		return LineDotted
	}
	return LineSolid
}

// trailingHead reads a trailing `o`/`x` head, only when followed by a
// statement boundary.
func trailingHead(chars []rune, i int) *headNext {
	var head Head
	if i < len(chars) && chars[i] == 'o' {
		head = HeadCircle
	} else if i < len(chars) && chars[i] == 'x' {
		head = HeadCross
	} else {
		return nil
	}
	boundary := i+1 >= len(chars) ||
		chars[i+1] == ' ' || chars[i+1] == '\t' ||
		chars[i+1] == '|' || chars[i+1] == '&' || chars[i+1] == ';'
	if !boundary {
		return nil
	}
	return &headNext{head: head, next: i + 1}
}

type headNext struct {
	head Head
	next int
}

// --------------------------------------------------------------------- state

// ParseState parses a `stateDiagram-v2`/`stateDiagram` source.
func ParseState(src string) *Graph {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !strings.HasPrefix(kind, "statediagram") {
		return nil
	}

	graph := NewGraph(DirDown)
	inNote := false

	for _, st := range statements[1:] {
		if inNote {
			if asciiLower(st) == "end note" {
				inNote = false
			}
			continue
		}
		first := asciiLower(firstWord(st))
		if first == "direction" {
			w := words(st)
			if len(w) > 1 {
				graph.Dir = ParseDir(w[1])
			} else {
				graph.Dir = ParseDir("")
			}
		} else if first == "note" {
			// A single-line `note ... : text` needs no terminator.
			if !strings.Contains(st, ":") {
				inNote = true
			}
		} else if first == "state" {
			if !parseStateDecl(st, graph) {
				return nil
			}
		} else if first == "classdef" || first == "class" || first == "hide" ||
			first == "scale" || first == "}" || first == "--" {
			// Styling and composite-state punctuation carry no layout meaning.
		} else if strings.Contains(st, "-->") {
			if !parseTransition(st, graph) {
				return nil
			}
		} else if !parseStateDesc(st, graph) {
			return nil
		}
		if graph.OverCap {
			return nil
		}
	}

	if len(graph.Nodes) == 0 {
		return nil
	}
	return graph
}

// parseStateDecl parses `state "Label" as id`, `state id <<choice>>`, or
// `state id {`. Returns false where pi returns null.
func parseStateDecl(st string, graph *Graph) bool {
	rest := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(st[len("state"):]), "{"))
	if rest == "" {
		return true
	}

	if strings.HasPrefix(rest, "\"") {
		close := strings.Index(rest[1:], "\"")
		if close == -1 {
			return false
		}
		label := rest[1 : 1+close]
		after := strings.TrimSpace(rest[1+close+1:])
		id := label
		if strings.HasPrefix(after, "as") {
			id = strings.TrimSpace(after[2:])
		}
		return graph.NodeLabel(id, decodeHtmlEntities(label)) != -1
	}

	shape := ShapeRound
	id := rest
	stereotyped := false
	pos := strings.Index(rest, "<<")
	if pos != -1 {
		stereo := strings.TrimSpace(strings.TrimSuffix(rest[pos+2:], ">>"))
		if stereo == "choice" {
			shape = ShapeDiamond
		}
		id = strings.TrimSpace(rest[:pos])
		stereotyped = true
	}
	if id == "" || strings.ContainsFunc(id, isJSWhitespaceRune) {
		return false
	}
	if stereotyped {
		return graph.NodeIndex(id, &id, shape) != -1
	}
	return graph.NodeIndex(id, nil, shape) != -1
}

// parseTransition parses `A --> B: label`, including chains `A --> B --> C`.
func parseTransition(st string, graph *Graph) bool {
	rest := st
	prev := -1

	for {
		lhs, rhs, ok := splitOnce(rest, "-->")
		if !ok {
			break
		}

		// pi: lhs.trimEnd().replace(/-+$/, '').trim()
		fromId := strings.TrimSpace(strings.TrimRight(trimEndRune(lhs), "-"))
		var from int
		if prev != -1 {
			// Mid-chain: the source is the previous target, so nothing may
			// precede.
			if fromId != "" {
				return false
			}
			from = prev
		} else {
			if fromId == "" {
				return false
			}
			f := stateEndpoint(graph, fromId, true)
			if f == -1 {
				return false
			}
			from = f
		}

		nextArrow := strings.Index(rhs, "-->")
		toPartRaw := rhs
		tail := ""
		if nextArrow != -1 {
			toPartRaw = rhs[:nextArrow]
			tail = rhs[nextArrow:]
		}

		toPart := toPartRaw
		var labelText *string
		if c0, c1, ok := splitOnce(toPartRaw, ":"); ok {
			toPart = c0
			labelText = nonEmpty(decodeHtmlEntities(strings.TrimSpace(c1)))
		}

		// pi: toPart.trimStart().replace(/^>+/, '').trimEnd().replace(/-+$/, '').trim()
		toId := strings.TrimLeft(trimStartRune(toPart), ">")
		toId = strings.TrimRight(trimEndRune(toId), "-")
		toId = strings.TrimSpace(toId)
		if toId == "" {
			return false
		}
		to := stateEndpoint(graph, toId, false)
		if to == -1 {
			return false
		}

		if !graph.PushEdge(Edge{From: from, To: to, Label: labelText,
			HeadTo: HeadArrow, HeadFrom: HeadNone, Line: LineSolid}) {
			return true
		}
		prev = to
		rest = tail
	}
	return true
}

// stateEndpoint: `[*]` is start or end depending on which side of the arrow it
// sits. Returns -1 (pi: null) when the node cannot be created.
func stateEndpoint(graph *Graph, id string, isSource bool) int {
	if id == "[*]" {
		name := "[*]end"
		if isSource {
			name = "[*]start"
		}
		dot := "●"
		return graph.NodeIndex(name, &dot, ShapeRound)
	}
	return graph.NodeIndex(id, nil, ShapeRound)
}

// parseStateDesc parses `id: description`, or a bare state name.
func parseStateDesc(st string, graph *Graph) bool {
	if id, desc, ok := splitOnce(st, ":"); ok {
		id = strings.TrimSpace(id)
		desc = strings.TrimSpace(desc)
		if id == "" || strings.ContainsFunc(id, isJSWhitespaceRune) || desc == "" {
			return false
		}
		return graph.NodeLabel(id, decodeHtmlEntities(desc)) != -1
	}
	if strings.ContainsFunc(st, isJSWhitespaceRune) {
		return false
	}
	return graph.NodeIndex(st, nil, ShapeRound) != -1
}

// isJSWhitespaceRune matches the JS `\s` class (used via /\s/.test).
func isJSWhitespaceRune(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680,
		0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// --------------------------------------------------------------------- class

// classOp is one entry of classOps.
type classOp struct {
	op       string
	headFrom Head
	headTo   Head
	line     LineKind
}

// classOps lists relation operators, longest-first so `--|>` wins over `--`.
var classOps = []classOp{
	{"<|--", HeadTriangle, HeadNone, LineSolid},
	{"--|>", HeadNone, HeadTriangle, LineSolid},
	{"<|..", HeadTriangle, HeadNone, LineDotted},
	{"..|>", HeadNone, HeadTriangle, LineDotted},
	{"*--", HeadDiamondFill, HeadNone, LineSolid},
	{"--*", HeadNone, HeadDiamondFill, LineSolid},
	{"o--", HeadDiamondOpen, HeadNone, LineSolid},
	{"--o", HeadNone, HeadDiamondOpen, LineSolid},
	{"<--", HeadArrow, HeadNone, LineSolid},
	{"-->", HeadNone, HeadArrow, LineSolid},
	{"<..", HeadArrow, HeadNone, LineDotted},
	{"..>", HeadNone, HeadArrow, LineDotted},
	{"--", HeadNone, HeadNone, LineSolid},
	{"..", HeadNone, HeadNone, LineDotted},
}

const maxClassOp = 4

// classParseResult mirrors pi's { graph, infos } return.
type classParseResult struct {
	graph *Graph
	infos []ClassInfo
}

// ParseClass parses a `classDiagram` source.
func ParseClass(src string) *classParseResult {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !strings.HasPrefix(kind, "classdiagram") {
		return nil
	}

	graph := NewGraph(DirDown)
	infos := []ClassInfo{}
	sync := func() {
		for len(infos) < len(graph.Nodes) {
			infos = append(infos, emptyClassInfo())
		}
	}
	// declare declares a class, keeping infos aligned with graph.nodes.
	declare := func(name string) int {
		idx := graph.NodeIndex(name, nil, ShapeRect)
		sync()
		return idx
	}
	curClass := -1

	for _, st := range statements[1:] {
		if curClass != -1 {
			if st == "}" {
				curClass = -1
			} else {
				PushMember(&infos[curClass], st)
			}
			continue
		}

		first := asciiLower(firstWord(st))
		if first == "direction" {
			w := words(st)
			if len(w) > 1 {
				graph.Dir = ParseDir(w[1])
			} else {
				graph.Dir = ParseDir("")
			}
			continue
		}
		switch first {
		case "note", "callback", "click", "link", "style", "cssclass",
			"classdef", "namespace", "}":
			continue
		}
		if first == "class" {
			rest := strings.TrimSpace(st[len("class"):])
			open := strings.HasSuffix(rest, "{")
			name := rest
			if open {
				name = strings.TrimSpace(rest[:len(rest)-1])
			}
			if name == "" || strings.ContainsFunc(name, isJSWhitespaceRune) {
				return nil
			}
			idx := declare(name)
			if idx == -1 {
				return nil
			}
			if open {
				curClass = idx
			}
			continue
		}

		if strings.HasPrefix(st, "<<") {
			body, name, ok := splitOnce(st[2:], ">>")
			if !ok {
				return nil
			}
			name = strings.TrimSpace(name)
			if name == "" || strings.ContainsFunc(name, isJSWhitespaceRune) {
				return nil
			}
			idx := declare(name)
			if idx == -1 {
				return nil
			}
			infos[idx].Annotation = strings.TrimSpace(body)
			continue
		}

		if rel := parseClassRelation(st); rel != nil {
			f := declare(rel.from)
			if f == -1 {
				return nil
			}
			t := declare(rel.to)
			if t == -1 {
				return nil
			}
			if len(graph.Edges) >= MaxEdges {
				return nil
			}
			graph.Edges = append(graph.Edges, Edge{
				From: f, To: t, Label: rel.label,
				HeadTo: rel.headTo, HeadFrom: rel.headFrom, Line: rel.line,
			})
			continue
		}

		if id, text, ok := splitOnce(st, ":"); ok {
			id = strings.TrimSpace(id)
			text = strings.TrimSpace(text)
			if id == "" || strings.ContainsFunc(id, isJSWhitespaceRune) || text == "" {
				return nil
			}
			idx := declare(id)
			if idx == -1 {
				return nil
			}
			PushMember(&infos[idx], text)
			continue
		}
		return nil
	}

	if len(graph.Nodes) == 0 {
		return nil
	}
	sync()
	return &classParseResult{graph: graph, infos: infos}
}

// PushMember adds a member to the attribute or method compartment, eliding
// past the cap.
func PushMember(info *ClassInfo, raw string) {
	if strings.HasPrefix(raw, "<<") {
		if body, name, ok := splitOnce(raw[2:], ">>"); ok {
			info.Annotation = strings.TrimSpace(body)
			_ = name
		}
		return
	}
	member := decodeHtmlEntities(displayGenerics(strings.TrimSpace(raw)))
	if strings.Contains(member, "(") {
		if len(info.Methods) < MaxMembers {
			info.Methods = append(info.Methods, member)
		} else if len(info.Methods) == MaxMembers {
			info.Methods = append(info.Methods, "…")
		}
		return
	}
	if len(info.Attrs) < MaxMembers {
		info.Attrs = append(info.Attrs, member)
	} else if len(info.Attrs) == MaxMembers {
		info.Attrs = append(info.Attrs, "…")
	}
}

// classRelation is a parsed class relation.
type classRelation struct {
	from     string
	to       string
	headFrom Head
	headTo   Head
	line     LineKind
	label    *string
}

func parseClassRelation(st string) *classRelation {
	chars := []rune(st)
	type foundOp struct {
		pos      int
		op       string
		headFrom Head
		headTo   Head
		line     LineKind
	}
	var found *foundOp

outer:
	for pos := 0; pos < len(chars); pos++ {
		end := pos + maxClassOp
		if end > len(chars) {
			end = len(chars)
		}
		tail := string(chars[pos:end])
		for _, candidate := range classOps {
			if !strings.HasPrefix(tail, candidate.op) {
				continue
			}
			// `o` is also an identifier character: skip a match glued to a
			// name.
			if strings.HasPrefix(candidate.op, "o") && pos > 0 && isIdChar(string(chars[pos-1])) {
				continue
			}
			after := rune(0)
			hasAfter := false
			if p := pos + len([]rune(candidate.op)); p < len(chars) {
				after = chars[p]
				hasAfter = true
			}
			if strings.HasSuffix(candidate.op, "o") && hasAfter && isIdChar(string(after)) {
				continue
			}
			found = &foundOp{pos: pos, op: candidate.op,
				headFrom: candidate.headFrom, headTo: candidate.headTo, line: candidate.line}
			break outer
		}
	}
	if found == nil {
		return nil
	}

	lhsRaw := strings.TrimSpace(string(chars[:found.pos]))
	rhsRaw := strings.TrimSpace(string(chars[found.pos+len([]rune(found.op)):]))

	lhs, cardFrom := stripCardinalitySuffix(lhsRaw)
	rhs, cardTo := stripCardinalityPrefix(rhsRaw)

	toId := rhs
	relLabel := ""
	if split, label, ok := splitOnce(rhs, ":"); ok {
		toId = split
		relLabel = strings.TrimSpace(label)
	}
	toId = strings.TrimSpace(toId)
	if relLabel != "" {
		relLabel = decodeHtmlEntities(relLabel)
	}

	if lhs == "" || toId == "" || strings.ContainsFunc(lhs, isJSWhitespaceRune) ||
		strings.ContainsFunc(toId, isJSWhitespaceRune) {
		return nil
	}

	var parts []string
	if cardFrom != "" {
		parts = append(parts, cardFrom)
	}
	if relLabel != "" {
		parts = append(parts, relLabel)
	}
	if cardTo != "" {
		parts = append(parts, cardTo)
	}
	joined := strings.Join(parts, " ")
	return &classRelation{
		from: lhs, to: toId,
		headFrom: found.headFrom, headTo: found.headTo, line: found.line,
		label: nonEmpty(joined),
	}
}

// stripCardinalitySuffix: `Class "1"` — a quoted cardinality trailing the
// left-hand name.
func stripCardinalitySuffix(s string) (string, string) {
	t := trimEndRune(s)
	if strings.HasSuffix(t, "\"") {
		rest := t[:len(t)-1]
		if q := strings.LastIndex(rest, "\""); q != -1 {
			return trimEndRune(rest[:q]), rest[q+1:]
		}
	}
	return t, ""
}

// stripCardinalityPrefix: `"0..*" Class` — a quoted cardinality leading the
// right-hand name.
func stripCardinalityPrefix(s string) (string, string) {
	t := trimStartRune(s)
	if strings.HasPrefix(t, "\"") {
		rest := t[1:]
		if q := strings.Index(rest, "\""); q != -1 {
			return trimStartRune(rest[q+1:]), rest[:q]
		}
	}
	return t, ""
}

// displayGenerics: mermaid writes generics as List~T~; show them as List<T>.
func displayGenerics(s string) string {
	var out strings.Builder
	open := false
	for _, c := range s {
		if c == '~' {
			if open {
				out.WriteByte('>')
			} else {
				out.WriteByte('<')
			}
			open = !open
		} else {
			out.WriteRune(c)
		}
	}
	return out.String()
}

// ------------------------------------------------------------------------ ER

// ParseEr parses an `erDiagram` source.
func ParseEr(src string) *classParseResult {
	statements := statementsOf(src)
	if headerKind(statements) != "erdiagram" {
		return nil
	}

	graph := NewGraph(DirDown)
	infos := []ClassInfo{}
	syncInfos := func() {
		for len(infos) < len(graph.Nodes) {
			infos = append(infos, emptyClassInfo())
		}
	}
	curEntity := -1

	for _, st := range statements[1:] {
		if curEntity != -1 {
			if st == "}" {
				curEntity = -1
			} else {
				syncInfos()
				PushErAttribute(&infos[curEntity], st)
			}
			continue
		}

		if rel := splitErRelationship(st); rel != nil {
			tokens := words(rel.rel)
			if len(tokens) != 3 {
				return nil
			}
			op := parseErOp(tokens[1])
			if op == nil {
				return nil
			}
			f := erEntity(graph, &infos, tokens[0])
			if f == -1 {
				return nil
			}
			t := erEntity(graph, &infos, tokens[2])
			if t == -1 {
				return nil
			}
			if len(graph.Edges) >= MaxEdges {
				return nil
			}
			relLabel := ""
			if rel.label != nil {
				relLabel = cleanLabel(*rel.label)
			}
			var parts []string
			if op.cardL != "" {
				parts = append(parts, op.cardL)
			}
			if relLabel != "" {
				parts = append(parts, relLabel)
			}
			if op.cardR != "" {
				parts = append(parts, op.cardR)
			}
			graph.Edges = append(graph.Edges, Edge{
				From: f, To: t,
				Label:    nonEmpty(strings.Join(parts, " ")),
				HeadTo:   HeadNone,
				HeadFrom: HeadNone,
				Line:     op.line,
			})
			continue
		}

		open := strings.HasSuffix(st, "{")
		decl := st
		if open {
			decl = strings.TrimSpace(st[:len(st)-1])
		}
		if decl == "" || len(words(decl)) != 1 {
			return nil
		}
		idx := erEntity(graph, &infos, decl)
		if idx == -1 {
			return nil
		}
		if open {
			curEntity = idx
		}
	}

	if len(graph.Nodes) == 0 {
		return nil
	}
	for len(infos) < len(graph.Nodes) {
		infos = append(infos, emptyClassInfo())
	}
	return &classParseResult{graph: graph, infos: infos}
}

func erEntity(graph *Graph, infos *[]ClassInfo, token string) int {
	open := strings.Index(token, "[")
	var idx int
	if open != -1 {
		id := token[:open]
		label := cleanLabel(strings.TrimRight(token[open+1:], "]"))
		if id == "" || label == "" {
			return -1
		}
		idx = graph.NodeLabel(id, label)
	} else {
		idx = graph.NodeIndex(token, nil, ShapeRect)
	}
	if idx == -1 {
		return -1
	}
	for len(*infos) < len(graph.Nodes) {
		*infos = append(*infos, emptyClassInfo())
	}
	return idx
}

func splitErRelationship(st string) *erRel {
	rel := st
	var label *string
	if a, b, ok := splitOnce(st, ":"); ok {
		rel = a
		label = lo.ToPtr(strings.TrimSpace(b))
	}
	for _, t := range words(rel) {
		if parseErOp(t) != nil {
			return &erRel{rel: rel, label: label}
		}
	}
	return nil
}

type erRel struct {
	rel   string
	label *string
}

func isAscii(s string) bool {
	for _, r := range s {
		if r > 0x7f {
			return false
		}
	}
	return true
}

// parseErOp parses a crow's-foot operator: two cardinality glyphs around `--`
// or `..`.
func parseErOp(tok string) *erCardOp {
	if len(tok) != 6 || !isAscii(tok) {
		return nil
	}
	mid := tok[2:4]
	var line LineKind
	switch mid {
	case "--":
		line = LineSolid
	case "..":
		line = LineDotted
	default:
		return nil
	}
	cardL := erCard(tok[0:2])
	cardR := erCard(tok[4:6])
	if cardL == "" || cardR == "" {
		return nil
	}
	return &erCardOp{cardL: cardL, cardR: cardR, line: line}
}

type erCardOp struct {
	cardL string
	cardR string
	line  LineKind
}

func erCard(tok string) string {
	switch tok {
	case "|o", "o|":
		return "0..1"
	case "||":
		return "1"
	case "}o", "o{":
		return "0..*"
	case "}|", "|{":
		return "1..*"
	default:
		return ""
	}
}

// PushErAttribute pushes an ER attribute: `type name`; a trailing quoted
// comment is dropped.
func PushErAttribute(info *ClassInfo, raw string) {
	var parts []string
	for _, tok := range words(raw) {
		if strings.HasPrefix(tok, "\"") {
			break
		}
		parts = append(parts, tok)
	}
	if len(parts) == 0 {
		return
	}
	line := decodeHtmlEntities(strings.Join(parts, " "))
	if len(info.Attrs) < MaxMembers {
		info.Attrs = append(info.Attrs, line)
	} else if len(info.Attrs) == MaxMembers {
		info.Attrs = append(info.Attrs, "…")
	}
}

// ------------------------------------------------------------------ sequence

// SeqHead is a message arrowhead.
type SeqHead = string

const (
	SeqHeadArrow SeqHead = "arrow"
	SeqHeadCross SeqHead = "cross"
)

// seqOp is one entry of seqOps.
type seqOp struct {
	op     string
	dashed bool
	head   SeqHead
}

// seqOps lists message operators, longest-first so `-->>` wins over `-->`.
var seqOps = []seqOp{
	{"-->>", true, SeqHeadArrow},
	{"->>", false, SeqHeadArrow},
	{"--x", true, SeqHeadCross},
	{"-x", false, SeqHeadCross},
	{"--)", true, SeqHeadArrow},
	{"-)", false, SeqHeadArrow},
	{"-->", true, SeqHeadArrow},
	{"->", false, SeqHeadArrow},
}

const maxSeqOp = 4

// NoteAnchor describes where a note sits (pi: the NoteAnchor union — single
// struct with Kind; From/To for "over", At for "left"/"right").
type NoteAnchor struct {
	Kind string // "over" | "left" | "right"
	From int
	To   int
	At   int
}

// SeqItem is one row of a sequence diagram (pi: the SeqItem union).
type SeqItem interface{ seqItemKind() string }

// SeqMessage is a message between two participants.
type SeqMessage struct {
	From   int
	To     int
	Text   *string // nil = null
	Dashed bool
	Head   SeqHead
}

// SeqNote is a note box.
type SeqNote struct {
	Anchor NoteAnchor
	Text   string
}

// SeqDivider is a full-width rule labelling a block boundary.
type SeqDivider struct {
	Text string
}

func (*SeqMessage) seqItemKind() string { return "message" }
func (*SeqNote) seqItemKind() string    { return "note" }
func (*SeqDivider) seqItemKind() string { return "divider" }

// Sequence is the parsed sequence diagram model.
type Sequence struct {
	Labels []string
	Index  map[string]int
	Items  []SeqItem
}

func newSequence() *Sequence {
	return &Sequence{Index: map[string]int{}}
}

// participant registers (or updates) a participant; -1 = over cap.
func (s *Sequence) participant(id string, label *string) int {
	if existing, ok := s.Index[id]; ok {
		if label != nil {
			s.Labels[existing] = *label
		}
		return existing
	}
	if len(s.Labels) >= MaxNodes {
		return -1
	}
	s.Index[id] = len(s.Labels)
	l := id
	if label != nil {
		l = *label
	}
	s.Labels = append(s.Labels, l)
	return len(s.Labels) - 1
}

// ParseSequence parses a `sequenceDiagram` source.
func ParseSequence(src string) *Sequence {
	statements := statementsOf(src)
	if headerKind(statements) != "sequencediagram" {
		return nil
	}

	seq := newSequence()
	autonumber := false
	msgCount := 0
	// blocks: one entry per open block; true when it draws a divider on end.
	var blocks []bool

	for _, st := range statements[1:] {
		first := firstWord(st)
		lower := asciiLower(first)

		if lower == "participant" || lower == "actor" {
			rest := strings.TrimSpace(st[len(first):])
			if rest == "" {
				return nil
			}
			if id, as, ok := splitOnce(rest, " as "); ok {
				if seq.participant(strings.TrimSpace(id), lo.ToPtr(cleanLabel(as))) == -1 {
					return nil
				}
			} else if seq.participant(rest, nil) == -1 {
				return nil
			}
			continue
		}
		if lower == "autonumber" {
			autonumber = true
			continue
		}
		switch lower {
		case "activate", "deactivate", "create", "destroy", "title",
			"acctitle", "accdescr", "links", "link", "properties":
			continue
		}
		if lower == "note" {
			note := parseNoteAnchor(strings.TrimSpace(st[len(first):]), seq)
			if note == nil {
				return nil
			}
			if len(seq.Items) >= MaxEdges {
				return nil
			}
			seq.Items = append(seq.Items, &SeqNote{Anchor: note.anchor, Text: note.text})
			continue
		}
		switch lower {
		case "loop", "alt", "opt", "par", "critical", "break", "else", "and", "option":
			switch lower {
			case "else", "and", "option":
				// A continuation only divides a block that opened one.
				if len(blocks) == 0 || !blocks[len(blocks)-1] {
					continue
				}
			default:
				blocks = append(blocks, true)
			}
			if len(seq.Items) >= MaxEdges {
				return nil
			}
			seq.Items = append(seq.Items, &SeqDivider{Text: decodeHtmlEntities(st)})
			continue
		}
		if lower == "rect" || lower == "box" {
			blocks = append(blocks, false)
			continue
		}
		if lower == "end" {
			popped := false
			if len(blocks) > 0 {
				popped = blocks[len(blocks)-1]
				blocks = blocks[:len(blocks)-1]
			}
			if popped {
				if len(seq.Items) >= MaxEdges {
					return nil
				}
				seq.Items = append(seq.Items, &SeqDivider{Text: "end"})
			}
			continue
		}

		msg := parseSeqMessage(st, seq)
		if msg == nil {
			return nil
		}
		text := msg.text
		if autonumber {
			msgCount++
			if text == nil {
				text = lo.ToPtr(strconv.Itoa(msgCount) + ".")
			} else {
				text = lo.ToPtr(strconv.Itoa(msgCount) + ". " + *text)
			}
		}
		if len(seq.Items) >= MaxEdges {
			return nil
		}
		seq.Items = append(seq.Items, &SeqMessage{
			From: msg.from, To: msg.to, Text: text,
			Dashed: msg.dashed, Head: msg.head,
		})
	}

	if len(seq.Labels) == 0 {
		return nil
	}
	return seq
}

// noteAnchorResult is the outcome of parseNoteAnchor.
type noteAnchorResult struct {
	text   string
	anchor NoteAnchor
}

func parseNoteAnchor(rest string, seq *Sequence) *noteAnchorResult {
	lower := asciiLower(rest)
	var kind string
	var idsAndText string
	switch {
	case strings.HasPrefix(lower, "over "):
		kind = "over"
		idsAndText = rest[len("over "):]
	case strings.HasPrefix(lower, "left of "):
		kind = "left"
		idsAndText = rest[len("left of "):]
	case strings.HasPrefix(lower, "right of "):
		kind = "right"
		idsAndText = rest[len("right of "):]
	default:
		return nil
	}

	ids, textRaw, ok := splitOnce(idsAndText, ":")
	if !ok {
		return nil
	}
	text := decodeHtmlEntities(strings.TrimSpace(textRaw))
	var parts []string
	for _, s := range strings.Split(ids, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return nil
	}
	a := seq.participant(parts[0], nil)
	if a == -1 {
		return nil
	}

	if kind != "over" {
		return &noteAnchorResult{text: text, anchor: NoteAnchor{Kind: kind, At: a}}
	}
	b := a
	if len(parts) > 1 {
		second := seq.participant(parts[1], nil)
		if second == -1 {
			return nil
		}
		b = second
	}
	return &noteAnchorResult{text: text, anchor: NoteAnchor{
		Kind: "over", From: min(a, b), To: max(a, b)}}
}

// seqMessageResult is the outcome of parseSeqMessage.
type seqMessageResult struct {
	from   int
	to     int
	text   *string
	dashed bool
	head   SeqHead
}

func parseSeqMessage(st string, seq *Sequence) *seqMessageResult {
	chars := []rune(st)
	type foundOp struct {
		pos    int
		op     string
		dashed bool
		head   SeqHead
	}
	var found *foundOp
outer:
	for pos := 0; pos < len(chars); pos++ {
		end := pos + maxSeqOp
		if end > len(chars) {
			end = len(chars)
		}
		tail := string(chars[pos:end])
		for _, candidate := range seqOps {
			if strings.HasPrefix(tail, candidate.op) {
				found = &foundOp{pos: pos, op: candidate.op,
					dashed: candidate.dashed, head: candidate.head}
				break outer
			}
		}
	}
	if found == nil {
		return nil
	}

	fromId := strings.TrimSpace(string(chars[:found.pos]))
	if fromId == "" {
		return nil
	}
	// `+`/`-` activate and deactivate the target; they carry no layout
	// meaning.
	rest := string(chars[found.pos+len([]rune(found.op)):])
	rest = strings.TrimLeft(strings.TrimLeft(trimStartRune(rest), " "), "+-")

	toId := rest
	var text *string
	if a, b, ok := splitOnce(rest, ":"); ok {
		toId = a
		text = nonEmpty(decodeHtmlEntities(strings.TrimSpace(b)))
	}
	toId = strings.TrimSpace(toId)
	if toId == "" {
		return nil
	}

	from := seq.participant(fromId, nil)
	if from == -1 {
		return nil
	}
	to := seq.participant(toId, nil)
	if to == -1 {
		return nil
	}
	return &seqMessageResult{from: from, to: to, text: text,
		dashed: found.dashed, head: found.head}
}
