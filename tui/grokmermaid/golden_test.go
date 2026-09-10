// Golden snapshot tests for package grokmermaid, generated against the real
// grok-mermaid v0.2.2 dist (node) — the jsdiff precedent. golden.json carries
// { plain, width, warnings } per case from `render`/`sourceBox`, plus the
// `diagramKind` map. Sources live inline in goldenSources below and must be
// kept in sync with the generator run recorded in docs/handoffs/T5b-4.md.
package grokmermaid

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// goldenSources are the diagram sources fed to node's render(); the name keys
// match golden.json.
var goldenSources = map[string]string{
	"flow_lr":             "flowchart LR\n  A[Start] --> B[Done]",
	"flow_tb_label":       "flowchart TD\n  A[One] -- yes --> B[Two]\n  B -- no --> C[Three]",
	"flow_subgraph":       "flowchart LR\n  A --> B\n  subgraph box [Inner]\n    B --> C\n  end\n  C --> D",
	"flow_styles":         "flowchart TD\n  A -. dotted .-> B\n  A === thick ===> B",
	"flow_cycle":          "flowchart TD\n  A --> B --> C --> A",
	"flow_rl":             "flowchart RL\n  A --> B --> C",
	"flow_warn":           "flowchart LR\n  A[Foo]:::highlight --> B[Bar]",
	"flow_shapes":         "flowchart LR\n  A([round]) --> B{diamond}\n  B --> C[rect]",
	"flow_selfloop":       "flowchart TD\n  A --> A\n  A --> B",
	"flow_open_edge":      "flowchart LR\n  A -- o B\n  B -- x C",
	"state":               "stateDiagram-v2\n  [*] --> Idle\n  Idle --> Running: start\n  Running --> [*]: stop",
	"state_choice":        "stateDiagram-v2\n  A --> B\n  B --> C\n  state B <<choice>>",
	"class":               "classDiagram\n  Animal <|-- Dog\n  Dog : bark()\n  Dog : +name",
	"class_generics":      "classDiagram\n  class List~T~\n  List~T~ : add(T)",
	"class_annotation":    "classDiagram\n  <<abstract>> Shape\n  Shape : area()",
	"er":                  "erDiagram\n  CUSTOMER ||--o{ ORDER : places\n  ORDER { string id }",
	"sequence":            "sequenceDiagram\n  Alice->>Bob: Hello\n  Bob-->>Alice: Hi",
	"sequence_notes":      "sequenceDiagram\n  participant A\n  participant B\n  Note over A,B: shared\n  A->x B: cross\n  loop every\n  A->>B: ping\n  end",
	"sequence_autonumber": "sequenceDiagram\n  autonumber\n  A->>B: one\n  B->>C: two",
	"cjk":                 "flowchart LR\n  开[开始] --> 结[结束]",
	"unsupported":         "pie\n  title Pets\n  \"Dogs\" : 4",
	"partial_stream":      "flowchart LR\n  A --> B",
	"empty":               "   ",
	"syntax_err":          "flowchart LR\n  -->",
	"width_table":         "flowchart LR\n  A[👨‍👩‍👧 flag🇯🇵 x] --> B[日本語テキスト]",
}

const sourceBoxSource = "flowchart LR\n  A --> B\n  pie"
const sourceBoxMaxWidth = 30

type goldenArt struct {
	Plain    []string `json:"plain"`
	Width    int      `json:"width"`
	Warnings []string `json:"warnings"`
}

type goldenFile struct {
	SourceBox   goldenArt             `json:"source_box"`
	DiagramKind map[string]string     `json:"diagram_kind"`
	Cases       map[string]*goldenArt `json:"-"`
}

func loadGolden(t *testing.T) map[string]*goldenArt {
	t.Helper()
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatalf("read golden.json: %v", err)
	}
	// diagram_kind is a different shape; strip it before decoding into cases.
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("decode golden.json: %v", err)
	}
	delete(all, "diagram_kind")
	out := map[string]*goldenArt{}
	for name, rawArt := range all {
		if string(rawArt) == "null" {
			out[name] = nil
			continue
		}
		var art goldenArt
		if err := json.Unmarshal(rawArt, &art); err != nil {
			t.Fatalf("decode golden case %s: %v", name, err)
		}
		out[name] = &art
	}
	return out
}

// TestRenderGolden checks render() against node-produced goldens and verifies
// the styled span structure decodes back to the plain rows.
func TestRenderGolden(t *testing.T) {
	golden := loadGolden(t)
	for name, src := range goldenSources {
		t.Run(name, func(t *testing.T) {
			want := golden[name]
			got := Render(src)
			if want == nil {
				if got != nil {
					t.Fatalf("expected nil, got art:\n%s", strings.Join(got.Plain, "\n"))
				}
				return
			}
			if got == nil {
				t.Fatalf("expected art, got nil")
			}
			if got.Width != want.Width {
				t.Errorf("width: got %d, want %d", got.Width, want.Width)
			}
			if len(got.Warnings) != len(want.Warnings) {
				t.Errorf("warnings: got %v, want %v", got.Warnings, want.Warnings)
			} else {
				for i := range want.Warnings {
					if got.Warnings[i] != want.Warnings[i] {
						t.Errorf("warning[%d]: got %q, want %q", i, got.Warnings[i], want.Warnings[i])
					}
				}
			}
			if len(got.Plain) != len(want.Plain) {
				t.Fatalf("rows: got %d, want %d\n got:\n%s\nwant:\n%s",
					len(got.Plain), len(want.Plain),
					strings.Join(got.Plain, "\n"), strings.Join(want.Plain, "\n"))
			}
			for i := range want.Plain {
				if got.Plain[i] != want.Plain[i] {
					t.Errorf("row %d:\n got %q\nwant %q", i, got.Plain[i], want.Plain[i])
				}
			}
			// Styled rows must spell the same (untrimmed) rows as plain.
			if len(got.Styled) != len(got.Plain) {
				t.Errorf("styled rows %d != plain rows %d", len(got.Styled), len(got.Plain))
			}
			for i, row := range got.Styled {
				var b strings.Builder
				for _, span := range row {
					b.WriteString(span.Text)
				}
				if strings.TrimRight(b.String(), " ") != got.Plain[i] {
					t.Errorf("styled row %d %q does not trim to plain %q", i, b.String(), got.Plain[i])
				}
			}
		})
	}
}

// TestSourceBoxGolden checks sourceBox() against the node golden.
func TestSourceBoxGolden(t *testing.T) {
	golden := loadGolden(t)
	want := golden["source_box"]
	if want == nil {
		t.Fatal("missing source_box golden")
	}
	mw := sourceBoxMaxWidth
	got := SourceBox(sourceBoxSource, &mw)
	if got == nil {
		t.Fatal("expected art")
	}
	if got.Width != want.Width {
		t.Errorf("width: got %d, want %d", got.Width, want.Width)
	}
	if len(got.Plain) != len(want.Plain) {
		t.Fatalf("rows: got %d, want %d", len(got.Plain), len(want.Plain))
	}
	for i := range want.Plain {
		if got.Plain[i] != want.Plain[i] {
			t.Errorf("row %d:\n got %q\nwant %q", i, got.Plain[i], want.Plain[i])
		}
	}
}

// TestDiagramKindGolden checks diagramKind() against the node golden.
func TestDiagramKindGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var all struct {
		DiagramKind map[string]*string `json:"diagram_kind"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	for name, want := range all.DiagramKind {
		src, ok := goldenSources[name]
		if !ok {
			continue // source_box is not a render case
		}
		got := DiagramKindOf(src)
		if want == nil {
			if got != "" {
				t.Errorf("%s: got %q, want null", name, got)
			}
			continue
		}
		if got != *want {
			t.Errorf("%s: got %q, want %q", name, got, *want)
		}
	}
}

// TestToAnsi locks the ANSI convenience mapping.
func TestToAnsi(t *testing.T) {
	art := &MermaidArt{
		Styled: [][]Span{{
			{Text: "┌", Cls: ClsBorder},
			{Text: "hi", Cls: ClsText},
			{Text: "┐", Cls: ClsBorder},
		}},
	}
	lines := ToAnsi(art, nil)
	want := "\x1b[2m┌\x1b[0mhi\x1b[2m┐\x1b[0m"
	if lines[0] != want {
		t.Errorf("got %q, want %q", lines[0], want)
	}
}
