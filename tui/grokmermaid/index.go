// Ported from pi node_modules/grok-mermaid/src/index.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. The Render entry point.
package grokmermaid

import "strings"

// Render renders a Mermaid source block as Unicode box-drawing art.
//
// Supported: graph/flowchart (including subgraph), stateDiagram,
// classDiagram, erDiagram and sequenceDiagram.
//
// The diagram is laid out at whatever size it needs; art.Width reports the
// columns that turned out to be. Deciding what to do when that exceeds the
// space at hand is the caller's — SourceBox is the usual answer:
//
//	art := Render(src)
//	if art != nil && art.Width <= cols { show(art) } else { show(SourceBox(src, &cols)) }
//
// nil means there is no art to show: blank input, a syntax error, a diagram
// type this renderer does not draw, or one large enough that laying it out is
// refused. DiagramKindOf separates the middle two.
//
// Rendering is best-effort. A flowchart keeps whatever parsed; the stricter
// grammars additionally get one retry without their final line, which is what
// keeps a streaming diagram on screen while its last statement is half-typed.
// Everything given up on is listed in art.Warnings — advisory only, never a
// reason to withhold the art.
func Render(src string) *MermaidArt {
	src = stripControls(src)
	if strings.TrimSpace(src) == "" {
		return nil
	}
	drawn := attempt(src)
	if drawn == nil {
		return nil
	}
	plain, styled, width := drawn.canvas.ToLines()
	art := &MermaidArt{Plain: plain, Styled: styled, Width: width}
	art.Warnings = append(art.Warnings, drawn.warnings...)
	return art
}

// drawn is the internal draw outcome.
type drawn struct {
	canvas   *Canvas
	warnings []string
}

// attempt draws src, retrying once without its last line if the grammar
// rejects it.
//
// State, class, ER and sequence fail a whole diagram on one unreadable
// statement, and while a source is streaming its last line is usually still
// being typed — so without this a diagram alternates with the source box all
// the way in. Only the final line is dropped, and doing so is always reported,
// so a finished document with a bad last line still says what it lost rather
// than quietly rendering short.
func attempt(src string) *drawn {
	d := draw(src)
	if d != nil {
		return d
	}

	body := strings.TrimRightFunc(src, isJSWhitespaceRune)
	cut := strings.LastIndex(body, "\n")
	if cut == -1 {
		return nil
	}
	salvaged := draw(body[:cut])
	if salvaged == nil {
		return nil
	}

	dropped := strings.TrimSpace(body[cut+1:])
	return &drawn{
		canvas: salvaged.canvas,
		warnings: append(append([]string{}, salvaged.warnings...),
			"dropped, unreadable final line: \""+dropped+"\""),
	}
}

// draw dispatches on the declared diagram type; nil means nothing was drawn.
func draw(src string) *drawn {
	plain := func(canvas *Canvas) *drawn {
		if canvas == nil {
			return nil
		}
		return &drawn{canvas: canvas}
	}

	switch DiagramKindOf(src) {
	case KindFlowchart:
		graph := ParseGraph(src)
		if graph == nil {
			return nil
		}
		var canvas *Canvas
		if len(graph.Groups) == 0 {
			canvas = layoutFlowchart(graph)
		} else {
			canvas = layoutGrouped(graph)
		}
		if canvas == nil {
			return nil
		}
		return &drawn{canvas: canvas, warnings: graph.Warnings}
	case KindState:
		state := ParseState(src)
		if state == nil {
			return nil
		}
		return plain(layoutFlowchart(state))
	case KindClass:
		cls := ParseClass(src)
		if cls == nil {
			return nil
		}
		return plain(layoutClass(cls.graph, cls.infos))
	case KindEr:
		er := ParseEr(src)
		if er == nil {
			return nil
		}
		return plain(layoutClass(er.graph, er.infos))
	case KindSequence:
		seq := ParseSequence(src)
		if seq == nil {
			return nil
		}
		return plain(layoutSequence(seq))
	default:
		return nil
	}
}
