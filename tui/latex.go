// pi: packages/tui/src/latex.ts — LaTeX math → terminal-friendly Unicode
// text rendering (renderLatex + LatexParser + multi-line layout engine).
// Ported 2026-09-10 (was the renderLatexFn seam deferral in markdown.go,
// see docs/feat/LATEX.md). The seam below is wired in init(), matching pi
// where markdown.ts imports renderLatex directly.
//
// Porting notes (§3.4):
//   - TS operates on UTF-16 code units; the parser works on []rune (pig
//     convention). All marker characters (U+F0000–F0005) are single runes.
//   - The two JS lookaround spacing patterns
//     (NAMED_OPERATOR_LEFT/RIGHT_SPACING_PATTERN) have no RE2 equivalent —
//     replaced by manual rune scans (latexNamedOperatorSpacingLeft/Right).
//   - `\b` in the cases condition regex has no RE2 equivalent — replaced by
//     an explicit non-word-character class.
package tui

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/samber/lo"
)

var latexSymbols = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ϵ",
	"varepsilon": "ε", "zeta": "ζ", "eta": "η", "theta": "θ", "vartheta": "ϑ",
	"iota": "ι", "kappa": "κ", "varkappa": "ϰ", "lambda": "λ", "mu": "μ",
	"nu": "ν", "xi": "ξ", "pi": "π", "varpi": "ϖ", "rho": "ρ", "varrho": "ϱ",
	"sigma": "σ", "varsigma": "ς", "tau": "τ", "upsilon": "υ", "phi": "ϕ",
	"varphi": "φ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ",
	"Pi": "Π", "Sigma": "Σ", "Upsilon": "Υ", "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
	"pm": "±", "mp": "∓", "times": "×", "div": "÷", "cdot": "·", "ast": "∗",
	"star": "⋆", "circ": "∘", "bullet": "•", "oplus": "⊕", "ominus": "⊖",
	"otimes": "⊗", "oslash": "⊘", "odot": "⊙", "bigcirc": "○", "dagger": "†",
	"ddagger": "‡", "amalg": "⨿", "uplus": "⊎", "sqcap": "⊓", "sqcup": "⊔",
	"bowtie": "⋈", "Join": "⋈", "ltimes": "⋉", "rtimes": "⋊",
	"leftouterjoin": "⟕", "rightouterjoin": "⟖", "fullouterjoin": "⟗",
	"triangleleft": "◁", "triangleright": "▷", "wr": "≀", "cap": "∩", "cup": "∪",
	"bigcap": "⋂", "bigcup": "⋃", "bigwedge": "⋀", "bigvee": "⋁",
	"bigsqcup": "⨆", "biguplus": "⨄", "bigoplus": "⨁", "bigotimes": "⨂",
	"bigodot": "⨀", "setminus": "∖", "in": "∈", "notin": "∉", "ni": "∋",
	"subset": "⊂", "supset": "⊃", "subseteq": "⊆", "supseteq": "⊇",
	"sqsubset": "⊏", "sqsupset": "⊐", "sqsubseteq": "⊑", "sqsupseteq": "⊒",
	"prec": "≺", "preceq": "≼", "succ": "≻", "succeq": "≽", "ll": "≪",
	"gg": "≫", "le": "≤", "leq": "≤", "leqslant": "≤", "ge": "≥", "geq": "≥",
	"geqslant": "≥", "ne": "≠", "neq": "≠", "equiv": "≡", "approx": "≈",
	"sim": "∼", "simeq": "≃", "cong": "≅", "asymp": "≍", "doteq": "≐",
	"propto": "∝", "parallel": "∥", "perp": "⊥", "mid": "∣", "vdash": "⊢",
	"dashv": "⊣", "models": "⊨", "Vdash": "⊩", "Vvdash": "⊪", "nvdash": "⊬",
	"nvDash": "⊭", "forall": "∀", "exists": "∃", "nexists": "∄", "neg": "¬",
	"land": "∧", "wedge": "∧", "lor": "∨", "vee": "∨", "to": "→",
	"rightarrow": "→", "longrightarrow": "→", "leftarrow": "←",
	"longleftarrow": "←", "gets": "←", "leftrightarrow": "↔",
	"longleftrightarrow": "↔", "hookleftarrow": "↩", "hookrightarrow": "↪",
	"twoheadleftarrow": "↞", "twoheadrightarrow": "↠", "leftharpoonup": "↼",
	"leftharpoondown": "↽", "rightharpoonup": "⇀", "rightharpoondown": "⇁",
	"rightleftharpoons": "⇌", "leftrightharpoons": "⇋", "nearrow": "↗",
	"searrow": "↘", "swarrow": "↙", "nwarrow": "↖", "rightsquigarrow": "⇝",
	"leadsto": "⇝", "Rightarrow": "⇒", "Longrightarrow": "⇒", "Leftarrow": "⇐",
	"Longleftarrow": "⇐", "Leftrightarrow": "⇔", "Longleftrightarrow": "⇔",
	"implies": "⇒", "iff": "⇔", "mapsto": "↦", "longmapsto": "↦",
	"uparrow": "↑", "downarrow": "↓", "partial": "∂", "nabla": "∇", "int": "∫",
	"iint": "∬", "iiint": "∭", "oint": "∮", "sum": "∑", "prod": "∏",
	"coprod": "∐", "infty": "∞", "emptyset": "∅", "varnothing": "∅",
	"angle": "∠", "therefore": "∴", "because": "∵", "aleph": "ℵ", "beth": "ℶ",
	"gimel": "ℷ", "daleth": "ℸ", "top": "⊤", "bot": "⊥", "triangle": "△",
	"square": "□", "lozenge": "◊", "checkmark": "✓", "complement": "∁",
	"wp": "℘", "prime": "′", "ldots": "…", "dots": "…", "cdots": "⋯",
	"vdots": "⋮", "ddots": "⋱", "ell": "ℓ", "hbar": "ℏ", "Im": "ℑ", "Re": "ℜ",
	"langle": "⟨", "rangle": "⟩", "vert": "|", "lvert": "|", "rvert": "|",
	"Vert": "‖", "lVert": "‖", "rVert": "‖", "lbrace": "{", "rbrace": "}",
	"backslash": "\\", "lfloor": "⌊", "rfloor": "⌋", "lceil": "⌈",
	"rceil": "⌉", "colon": ":",
}

var latexNamedOperators = latexStringSet(
	"arccos", "arcsin", "arctan", "arg", "cos", "cosh", "cot", "coth", "csc",
	"deg", "det", "dim", "exp", "gcd", "hom", "inf", "ker", "lg", "lim",
	"liminf", "limsup", "ln", "log", "max", "min", "Pr", "sec", "sin", "sinh",
	"sup", "tan", "tanh",
)

var latexLimitOperators = latexStringSet(
	"argmax", "argmin", "inf", "injlim", "lim", "liminf", "limsup", "max",
	"min", "projlim", "sup",
)

var latexDisplayLimitSymbols = latexStringSet(
	"bigcap", "bigcup", "bigodot", "bigoplus", "bigotimes", "bigsqcup",
	"biguplus", "bigvee", "bigwedge", "coprod", "int", "iint", "iiint", "oint",
	"prod", "sum",
)

var latexRelationCommands = latexStringSet(
	"Leftarrow", "Leftrightarrow", "Longleftarrow", "Longleftrightarrow",
	"Longrightarrow", "Rightarrow", "Join", "Vdash", "Vvdash", "approx",
	"asymp", "bowtie", "cong", "dashv", "fullouterjoin", "doteq", "downarrow",
	"equiv", "ge", "geq", "geqslant", "gets", "gg", "hookleftarrow",
	"hookrightarrow", "iff", "implies", "in", "leadsto", "le", "leftarrow",
	"leftharpoondown", "leftharpoonup", "leftrightarrow", "leftrightharpoons",
	"leftouterjoin", "leq", "leqslant", "ll", "longleftarrow",
	"longleftrightarrow", "longmapsto", "longrightarrow", "ltimes", "mapsto",
	"mid", "models", "ne", "nearrow", "neq", "ni", "notin", "nvdash", "nvDash",
	"nwarrow", "parallel", "perp", "prec", "preceq", "propto",
	"rightharpoondown", "rightharpoonup", "rightleftharpoons",
	"rightouterjoin", "rightarrow", "rightsquigarrow", "rtimes", "searrow",
	"sim", "simeq", "sqsubset", "sqsubseteq", "sqsupset", "sqsupseteq",
	"subset", "subseteq", "succ", "succeq", "supset", "supseteq", "swarrow",
	"to", "triangleleft", "triangleright", "twoheadleftarrow",
	"twoheadrightarrow", "uparrow", "vdash",
)

var latexNegatedSymbols = map[string]string{
	"<": "≮", ">": "≯", "=": "≠", "∈": "∉", "∋": "∌", "∣": "∤", "∥": "∦",
	"∼": "≁", "≃": "≄", "≅": "≇", "≈": "≉", "≡": "≢", "≤": "≰", "≥": "≱",
	"≺": "⊀", "≻": "⊁", "⊂": "⊄", "⊃": "⊅", "⊆": "⊈", "⊇": "⊉", "⊢": "⊬",
	"⊨": "⊭", "↔": "↮", "←": "↚", "→": "↛", "⇒": "⇏", "⇐": "⇍", "⇔": "⇎",
	"≼": "⋠", "≽": "⋡",
}

var latexBlackboard = map[rune]string{
	'C': "ℂ", 'H': "ℍ", 'N': "ℕ", 'P': "ℙ", 'Q': "ℚ", 'R': "ℝ", 'Z': "ℤ",
}

var latexSuperscripts = map[rune]string{
	'0': "⁰", '1': "¹", '2': "²", '3': "³", '4': "⁴", '5': "⁵", '6': "⁶",
	'7': "⁷", '8': "⁸", '9': "⁹", '+': "⁺", '-': "⁻", '=': "⁼", '(': "⁽",
	')': "⁾", 'a': "ᵃ", 'b': "ᵇ", 'c': "ᶜ", 'd': "ᵈ", 'e': "ᵉ", 'f': "ᶠ",
	'g': "ᵍ", 'h': "ʰ", 'i': "ⁱ", 'j': "ʲ", 'k': "ᵏ", 'l': "ˡ", 'm': "ᵐ",
	'n': "ⁿ", 'o': "ᵒ", 'p': "ᵖ", 'r': "ʳ", 's': "ˢ", 't': "ᵗ", 'u': "ᵘ",
	'v': "ᵛ", 'w': "ʷ", 'x': "ˣ", 'y': "ʸ", 'z': "ᶻ",
}

var latexSubscripts = map[rune]string{
	'0': "₀", '1': "₁", '2': "₂", '3': "₃", '4': "₄", '5': "₅", '6': "₆",
	'7': "₇", '8': "₈", '9': "₉", '+': "₊", '-': "₋", '=': "₌", '(': "₍",
	')': "₎", 'a': "ₐ", 'e': "ₑ", 'h': "ₕ", 'i': "ᵢ", 'j': "ⱼ", 'k': "ₖ",
	'l': "ₗ", 'm': "ₘ", 'n': "ₙ", 'o': "ₒ", 'p': "ₚ", 'r': "ᵣ", 's': "ₛ",
	't': "ₜ", 'u': "ᵤ", 'v': "ᵥ", 'x': "ₓ",
}

var latexSpacingCommands = latexStringSet(
	",", ":", ";", " ", ">", "enspace", "enskip", "medspace", "quad", "qquad",
	"thickspace", "thinspace",
)

var latexNegativeSpacingCommands = latexStringSet(
	"!", "negmedspace", "negthickspace", "negthinspace",
)

const latexNegativeSpace = "\x00"

var latexIgnoredCommands = latexStringSet(
	"displaystyle", "limits", "nolimits", "scriptstyle", "scriptscriptstyle",
	"textstyle",
)

var latexSizeCommands = latexStringSet(
	"big", "Big", "bigg", "Bigg", "bigl", "Bigl", "biggl", "Biggl", "bigr",
	"Bigr", "biggr", "Biggr",
)

var latexPlainWrappers = latexStringSet(
	"emph", "mathcal", "mathbf", "mathfrak", "mathit", "mathrm", "mathnormal",
	"mathscr", "mathsf", "mathtt", "mathup", "mbox", "overbrace", "pmb",
	"smash", "substack", "text", "textbf", "textit", "textmd", "textnormal",
	"textrm", "textsc", "textsf", "textsl", "texttt", "textup", "underbrace",
	"bm", "boldsymbol",
)

var latexAccents = map[string]string{
	"acute": "\u0301", "bar": "\u0305", "breve": "\u0306", "check": "\u030c",
	"ddot": "\u0308", "dot": "\u0307", "grave": "\u0300", "hat": "\u0302",
	"mathring": "\u030a", "overleftarrow": "\u20d6",
	"overleftrightarrow": "\u20e1", "overline": "\u0305",
	"overrightarrow": "\u20d7", "tilde": "\u0303", "underline": "\u0332",
	"vec": "\u20d7", "widehat": "\u0302", "widetilde": "\u0303",
}

func latexStringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

// Layout node kinds (pi: FractionNode | OperatorNode | MatrixNode).
type latexLayoutNode interface{ latexLayoutNode() }

type latexFractionNode struct {
	numerator   string
	denominator string
}

func (latexFractionNode) latexLayoutNode() {}

type latexOperatorNode struct {
	operator string
	lower    *string
	upper    *string
}

func (latexOperatorNode) latexLayoutNode() {}

type latexMatrixNode struct {
	lines    []string
	baseline int
}

func (latexMatrixNode) latexLayoutNode() {}

type latexLayout struct {
	lines    []string
	width    int
	baseline int
}

const (
	latexLayoutMarkerStart  = '\U000F0000'
	latexLayoutMarkerEnd    = '\U000F0001'
	latexProtectedSpace     = '\U000F0002'
	latexNamedOperatorStart = '\U000F0004'
	latexNamedOperatorEnd   = '\U000F0005'
)

var (
	latexLayoutMarkerPattern        = regexp.MustCompile(`\x{F0000}([0-9]+)\x{F0001}`)
	latexTrailingLayoutMarkerParse  = regexp.MustCompile(`\x{F0000}([0-9]+)\x{F0001}$`)
	latexSpacesTabsPattern          = regexp.MustCompile(`[ \t]+`)
	latexScriptEqualsPlusMinus      = regexp.MustCompile(`\s*([=+-])\s*`)
	latexSimpleNumeratorPattern     = regexp.MustCompile(`^[\p{L}\p{N}.]+$`)
	latexSimpleDenominatorPattern   = regexp.MustCompile(`^[\p{N}.]+$`)
	latexWordSubscriptPattern       = regexp.MustCompile(`^[A-Za-z]+$`)
	latexLeadingGroupPattern        = regexp.MustCompile(`^\s*\{[^}]*\}`)
	latexTrailingCommaPattern       = regexp.MustCompile(`,\s*$`)
	latexCasesConditionPrefixParse  = regexp.MustCompile(`(?i)^(?:if|when|for|otherwise)(?:$|[^A-Za-z0-9_])`)
	latexEnvironmentRowSplitPattern = regexp.MustCompile(`\\\\(?:\[[^\]\n]*\])?`)
	latexLimitsModifierPattern      = func() *regexp.Regexp {
		// pi: /^\\(limits|nolimits)(?![A-Za-z])/ — the lookahead has no RE2
		// equivalent; the caller verifies the following character manually.
		return regexp.MustCompile(`^\\(limits|nolimits)`)
	}()
)

// latexNamedOperatorSpacingLeft replaces latexNamedOperatorStart with a space
// when preceded by a letter/digit/) ] } / end marker (pi:
// NAMED_OPERATOR_LEFT_SPACING_PATTERN lookbehind — manual rune scan).
func latexNamedOperatorSpacingLeft(value string) string {
	runes := []rune(value)
	var b strings.Builder
	for i, r := range runes {
		if r == latexNamedOperatorStart && i > 0 {
			prev := runes[i-1]
			if unicode.IsLetter(prev) || unicode.IsDigit(prev) ||
				prev == ')' || prev == ']' || prev == '}' || prev == latexLayoutMarkerEnd {
				b.WriteRune(' ')
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// latexNamedOperatorSpacingRight replaces latexNamedOperatorEnd with a space
// when followed by a letter/digit/√/layout marker (pi:
// NAMED_OPERATOR_RIGHT_SPACING_PATTERN lookahead — manual rune scan).
func latexNamedOperatorSpacingRight(value string) string {
	runes := []rune(value)
	var b strings.Builder
	for i, r := range runes {
		if r == latexNamedOperatorEnd && i+1 < len(runes) {
			next := runes[i+1]
			if unicode.IsLetter(next) || unicode.IsDigit(next) ||
				next == '√' || next == latexLayoutMarkerStart {
				b.WriteRune(' ')
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func latexNormalizeOutput(value string) string {
	value = latexNamedOperatorSpacingLeft(value)
	value = strings.ReplaceAll(value, string(latexNamedOperatorStart), "")
	value = latexNamedOperatorSpacingRight(value)
	value = strings.ReplaceAll(value, string(latexNamedOperatorEnd), "")
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(latexSpacesTabsPattern.ReplaceAllString(line, " "))
	}
	filtered := make([]string, 0, len(lines))
	for i, line := range lines {
		if len(line) > 0 || (i > 0 && i < len(lines)-1) {
			filtered = append(filtered, line)
		}
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func latexReplaceCharacters(value string, replacements map[rune]string) (string, bool) {
	var b strings.Builder
	for _, character := range value {
		replacement, ok := replacements[character]
		if !ok {
			return "", false
		}
		b.WriteString(replacement)
	}
	return b.String(), true
}

func latexFormatScript(value string, kind string) string {
	value = strings.TrimSpace(value)
	replacements := latexSuperscripts
	if kind == "sub" {
		replacements = latexSubscripts
	}
	collapsed := latexScriptEqualsPlusMinus.ReplaceAllString(value, "$1")
	if unicode, ok := latexReplaceCharacters(collapsed, replacements); ok {
		return unicode
	}
	prefix := "^"
	if kind == "sub" {
		prefix = "_"
	}
	if len([]rune(value)) == 1 || (kind == "sub" && latexWordSubscriptPattern.MatchString(value)) {
		return prefix + value
	}
	return prefix + "(" + value + ")"
}

func latexFormatFraction(numerator string, denominator string) string {
	numerator = strings.TrimSpace(numerator)
	denominator = strings.TrimSpace(denominator)
	simpleNumerator := latexSimpleNumeratorPattern.MatchString(numerator)
	simpleDenominator := latexSimpleDenominatorPattern.MatchString(denominator) ||
		len([]rune(denominator)) == 1
	if !simpleNumerator {
		numerator = "(" + numerator + ")"
	}
	if !simpleDenominator {
		denominator = "(" + denominator + ")"
	}
	return numerator + "/" + denominator
}

func latexFormatRoot(value string, symbol string) string {
	value = strings.TrimSpace(value)
	if latexSimpleNumeratorPattern.MatchString(value) {
		return symbol + value
	}
	return symbol + "(" + value + ")"
}

func latexPadLayoutLine(line string, width int, centered bool) string {
	padding := width - VisibleWidth(line)
	if padding < 0 {
		padding = 0
	}
	left := 0
	if centered {
		left = padding / 2
	}
	return strings.Repeat(" ", left) + line + strings.Repeat(" ", padding-left)
}

func latexJoinLayouts(layouts []*latexLayout) *latexLayout {
	if len(layouts) == 0 {
		return &latexLayout{lines: []string{""}, width: 0, baseline: 0}
	}
	baseline := 0
	for _, layout := range layouts {
		if layout.baseline > baseline {
			baseline = layout.baseline
		}
	}
	below := 0
	for _, layout := range layouts {
		if n := len(layout.lines) - layout.baseline - 1; n > below {
			below = n
		}
	}
	lines := make([]string, 0, baseline+below+1)
	for row := 0; row <= baseline+below; row++ {
		var b strings.Builder
		for _, layout := range layouts {
			sourceRow := row - baseline + layout.baseline
			if sourceRow >= 0 && sourceRow < len(layout.lines) {
				b.WriteString(latexPadLayoutLine(layout.lines[sourceRow], layout.width, false))
			} else {
				b.WriteString(strings.Repeat(" ", layout.width))
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " \t\n\r"))
	}
	width := 0
	for _, layout := range layouts {
		width += layout.width
	}
	return &latexLayout{lines: lines, width: width, baseline: baseline}
}

// latexScanLayoutMarkers finds non-overlapping layout marker tokens
// (start + digits + end) in a rune slice, returning (start, end, nodeIndex)
// triples with rune offsets (pi: LAYOUT_MARKER_PATTERN matchAll).
func latexScanLayoutMarkers(runes []rune) [][3]int {
	var matches [][3]int
	for i := 0; i < len(runes); i++ {
		if runes[i] != latexLayoutMarkerStart {
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] >= '0' && runes[j] <= '9' {
			j++
		}
		if j == i+1 || j >= len(runes) || runes[j] != latexLayoutMarkerEnd {
			continue
		}
		index, err := strconv.Atoi(string(runes[i+1 : j]))
		if err != nil {
			continue
		}
		matches = append(matches, [3]int{i, j + 1, index})
		i = j
	}
	return matches
}

func latexRenderLayout(source string, nodes []latexLayoutNode) *latexLayout {
	renderedLines := []string{}
	firstBaseline := 0
	for _, sourceLine := range strings.Split(source, "\n") {
		runes := []rune(sourceLine)
		layouts := []*latexLayout{}
		position := 0
		var previousNode latexLayoutNode
		for _, match := range latexScanLayoutMarkers(runes) {
			start, end, index := match[0], match[1], match[2]
			if index >= len(nodes) {
				continue
			}
			node := nodes[index]
			if start > position {
				sliced := string(runes[position:start])
				var trimmed string
				if previousNode != nil {
					trimmed = strings.TrimLeftFunc(sliced, unicode.IsSpace)
				} else {
					trimmed = sliced
				}
				trimmed = strings.TrimRightFunc(trimmed, unicode.IsSpace)
				_, isPrevMatrix := previousNode.(latexMatrixNode)
				_, isMatrix := node.(latexMatrixNode)
				preserveLeadingSpace := isPrevMatrix && len(sliced) > 0 && unicode.IsSpace([]rune(sliced)[0])
				preserveTrailingSpace := isMatrix && len(sliced) > 0 && unicode.IsSpace([]rune(sliced)[len([]rune(sliced))-1])
				var text string
				switch {
				case trimmed != "":
					if preserveLeadingSpace {
						text += " "
					}
					text += trimmed
					if preserveTrailingSpace {
						text += " "
					}
				case preserveLeadingSpace || preserveTrailingSpace:
					text = " "
				}
				layouts = append(layouts, &latexLayout{lines: []string{text}, width: VisibleWidth(text), baseline: 0})
			}
			switch n := node.(type) {
			case latexFractionNode:
				numerator := latexRenderLayout(n.numerator, nodes)
				denominator := latexRenderLayout(n.denominator, nodes)
				contentWidth := max(numerator.width, denominator.width, 1)
				width := contentWidth + 2
				lines := make([]string, 0, len(numerator.lines)+len(denominator.lines)+1)
				for _, line := range numerator.lines {
					lines = append(lines, latexPadLayoutLine(line, width, true))
				}
				lines = append(lines, " "+strings.Repeat("─", contentWidth)+" ")
				for _, line := range denominator.lines {
					lines = append(lines, latexPadLayoutLine(line, width, true))
				}
				layouts = append(layouts, &latexLayout{lines: lines, width: width, baseline: len(numerator.lines)})
			case latexOperatorNode:
				contentWidth := VisibleWidth(n.operator)
				if n.lower != nil {
					contentWidth = max(contentWidth, VisibleWidth(*n.lower))
				}
				if n.upper != nil {
					contentWidth = max(contentWidth, VisibleWidth(*n.upper))
				}
				var lines []string
				if n.upper != nil {
					lines = append(lines, latexPadLayoutLine(*n.upper, contentWidth, true)+" ")
				}
				lines = append(lines, latexPadLayoutLine(n.operator, contentWidth, true)+" ")
				if n.lower != nil {
					lines = append(lines, latexPadLayoutLine(*n.lower, contentWidth, true)+" ")
				}
				baseline := 0
				if n.upper != nil {
					baseline = 1
				}
				layouts = append(layouts, &latexLayout{lines: lines, width: contentWidth + 1, baseline: baseline})
			default:
				matrix := n.(latexMatrixNode)
				width := 0
				for _, line := range matrix.lines {
					width = max(width, VisibleWidth(line))
				}
				lines := make([]string, len(matrix.lines))
				for i, line := range matrix.lines {
					lines[i] = latexPadLayoutLine(line, width, false)
				}
				layouts = append(layouts, &latexLayout{lines: lines, width: width, baseline: matrix.baseline})
			}
			position = end
			previousNode = node
		}
		if position < len(runes) {
			sliced := string(runes[position:])
			var text string
			if previousNode != nil {
				trimmed := strings.TrimLeftFunc(sliced, unicode.IsSpace)
				_, isPrevMatrix := previousNode.(latexMatrixNode)
				if isPrevMatrix && len(sliced) > 0 && unicode.IsSpace([]rune(sliced)[0]) {
					text = " " + trimmed
				} else {
					text = trimmed
				}
			} else {
				text = sliced
			}
			layouts = append(layouts, &latexLayout{lines: []string{text}, width: VisibleWidth(text), baseline: 0})
		}
		lineLayout := latexJoinLayouts(layouts)
		if len(renderedLines) == 0 {
			firstBaseline = lineLayout.baseline
		}
		renderedLines = append(renderedLines, lineLayout.lines...)
	}
	width := 0
	for _, line := range renderedLines {
		width = max(width, VisibleWidth(line))
	}
	return &latexLayout{lines: renderedLines, width: width, baseline: firstBaseline}
}

type latexParser struct {
	source         []rune
	layoutNodes    *[]latexLayoutNode
	display        bool
	position       int
	supported      bool
	stackFractions bool
}

func newLatexParser(source string, layoutNodes *[]latexLayoutNode, display bool) *latexParser {
	return &latexParser{
		source:         []rune(source),
		layoutNodes:    layoutNodes,
		display:        display,
		supported:      true,
		stackFractions: true,
	}
}

func (p *latexParser) addNode(node latexLayoutNode) int {
	*p.layoutNodes = append(*p.layoutNodes, node)
	return len(*p.layoutNodes) - 1
}

func (p *latexParser) marker(index int) string {
	return string(latexLayoutMarkerStart) + strconv.Itoa(index) + string(latexLayoutMarkerEnd)
}

func (p *latexParser) render() (string, bool) {
	rendered := p.parseSequence(0)
	if !p.supported || p.position != len(p.source) {
		return "", false
	}
	return latexNormalizeOutput(rendered), true
}

func (p *latexParser) parseSequence(endCharacter rune) string {
	result := ""
	for p.position < len(p.source) {
		character := p.source[p.position]
		if endCharacter != 0 && character == endCharacter {
			p.position++
			return result
		}

		if character == '}' {
			p.supported = false
			return result
		}

		if character == '{' {
			p.position++
			result += p.parseSequence('}')
			continue
		}

		if character == '\\' {
			command := p.parseCommand()
			if command == latexNegativeSpace {
				result = strings.TrimRightFunc(result, unicode.IsSpace)
				if strings.HasSuffix(result, string(latexNamedOperatorEnd)) {
					result = result[:len(result)-len(string(latexNamedOperatorEnd))]
				}
			} else {
				result += command
			}
			continue
		}

		if character == '^' || character == '_' {
			p.position++
			kind := "sup"
			if character == '_' {
				kind = "sub"
			}
			result = strings.TrimRightFunc(result, unicode.IsSpace)
			script := latexFormatScript(p.parseRequiredArgument(false), kind)
			if strings.HasSuffix(result, string(latexNamedOperatorEnd)) {
				result = result[:len(result)-len(string(latexNamedOperatorEnd))] + script + string(latexNamedOperatorEnd)
			} else {
				result += script
			}
			continue
		}

		if unicode.IsSpace(character) {
			result += p.parseWhitespace()
			continue
		}

		if character == '=' || character == '<' || character == '>' {
			result = strings.TrimRightFunc(result, unicode.IsSpace) + " " + string(character) + " "
			p.position++
			continue
		}

		if character == '&' {
			p.position++
			continue
		}

		if character == '~' {
			p.position++
			result += " "
			continue
		}

		if character == '.' {
			matches := latexTrailingLayoutMarkerParse.FindStringSubmatch(result)
			if matches != nil {
				index, err := strconv.Atoi(matches[1])
				if err == nil && index < len(*p.layoutNodes) {
					if node, ok := (*p.layoutNodes)[index].(latexMatrixNode); ok {
						last := len(node.lines) - 1
						node.lines[last] = node.lines[last] + "."
						(*p.layoutNodes)[index] = node
						p.position++
						continue
					}
				}
			}
		}

		result += string(character)
		p.position++
	}

	if endCharacter != 0 {
		p.supported = false
	}
	return result
}

func (p *latexParser) parseWhitespace() string {
	for p.position < len(p.source) && unicode.IsSpace(p.source[p.position]) {
		p.position++
	}
	return " "
}

func latexIsASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func (p *latexParser) parseCommand() string {
	p.position++
	if p.position >= len(p.source) {
		p.supported = false
		return ""
	}

	var command string
	first := p.source[p.position]
	if first == '\n' || first == '\r' {
		p.position++
		if first == '\r' && p.position < len(p.source) && p.source[p.position] == '\n' {
			p.position++
		}
		return " "
	}
	if latexIsASCIILetter(first) {
		start := p.position
		for p.position < len(p.source) && latexIsASCIILetter(p.source[p.position]) {
			p.position++
		}
		command = string(p.source[start:p.position])
	} else {
		command = string(first)
		p.position++
	}

	if command == "\\" {
		return "\n"
	}
	if latexSpacingCommands[command] {
		return " "
	}
	if latexNegativeSpacingCommands[command] {
		return latexNegativeSpace
	}
	if latexIgnoredCommands[command] {
		return ""
	}
	if command == "{" || command == "}" || command == "$" || command == "%" ||
		command == "#" || command == "_" || command == "&" {
		return command
	}
	if command == "|" {
		return "‖"
	}
	if command == "not" {
		value := strings.TrimSpace(p.parseRequiredArgument(false))
		if negated, ok := latexNegatedSymbols[value]; ok {
			return " " + negated + " "
		}
		characters := []rune(value)
		if len(characters) == 0 {
			p.supported = false
			return ""
		}
		return " " + string(characters[0]) + "\u0338" + string(characters[1:]) + " "
	}
	if latexLimitOperators[command] {
		return p.parseOperator(command, "bracket", true, true)
	}

	if symbol, ok := latexSymbols[command]; ok {
		if latexDisplayLimitSymbols[command] {
			return p.parseOperator(symbol, "script", true, false)
		}
		if command == "cdot" || command == "times" || latexRelationCommands[command] {
			return " " + symbol + " "
		}
		return symbol
	}
	if latexNamedOperators[command] {
		return string(latexNamedOperatorStart) + command + string(latexNamedOperatorEnd)
	}
	if latexSizeCommands[command] {
		return ""
	}
	if command == "left" || command == "middle" || command == "right" {
		if p.position < len(p.source) && p.source[p.position] == '.' {
			p.position++
		}
		return ""
	}
	if command == "frac" || command == "dfrac" || command == "tfrac" {
		shouldStack := p.display && p.stackFractions && command != "tfrac"
		numerator := p.parseRequiredArgument(!shouldStack)
		denominator := p.parseRequiredArgument(!shouldStack)
		if shouldStack {
			index := p.addNode(latexFractionNode{
				numerator:   latexNormalizeOutput(numerator),
				denominator: latexNormalizeOutput(denominator),
			})
			return p.marker(index)
		}
		return latexFormatFraction(numerator, denominator)
	}
	if command == "sqrt" {
		degree, hasDegree := p.parseOptionalArgument()
		value := p.parseRequiredArgument(true)
		if !hasDegree || degree == "2" {
			return latexFormatRoot(value, "√")
		}
		if degree == "3" {
			return latexFormatRoot(value, "∛")
		}
		if degree == "4" {
			return latexFormatRoot(value, "∜")
		}
		return latexFormatScript(degree, "sup") + latexFormatRoot(value, "√")
	}
	if command == "boxed" || command == "fbox" {
		return "[" + strings.TrimSpace(p.parseRequiredArgument(true)) + "]"
	}
	if command == "binom" || command == "dbinom" || command == "tbinom" {
		return "(" + p.parseRequiredArgument(true) + " choose " + p.parseRequiredArgument(true) + ")"
	}
	if accent, ok := latexAccents[command]; ok {
		value := p.parseRequiredArgument(true)
		if len([]rune(value)) == 1 {
			return value + accent
		}
		return command + "(" + value + ")"
	}
	if command == "mathbb" {
		value := p.parseRequiredArgument(true)
		var b strings.Builder
		for _, character := range value {
			if blackboard, ok := latexBlackboard[character]; ok {
				b.WriteString(blackboard)
			} else {
				b.WriteRune(character)
			}
		}
		return b.String()
	}
	if command == "operatorname" {
		starred := p.position < len(p.source) && p.source[p.position] == '*'
		if starred {
			p.position++
		}
		operator := strings.TrimSpace(latexNormalizeOutput(p.parseRequiredArgument(true)))
		return p.parseOperator(operator, "bracket", starred, true)
	}
	if command == "mod" || command == "bmod" {
		return " mod "
	}
	if command == "pmod" || command == "pod" {
		value := strings.TrimSpace(p.parseRequiredArgument(true))
		if command == "pmod" {
			return " (mod " + value + ")"
		}
		return " (" + value + ")"
	}
	if command == "overset" || command == "stackrel" {
		upper := p.parseRequiredArgument(true)
		value := strings.TrimSpace(p.parseRequiredArgument(true))
		return value + latexFormatScript(upper, "sup")
	}
	if command == "underset" {
		lower := p.parseRequiredArgument(true)
		value := strings.TrimSpace(p.parseRequiredArgument(true))
		return value + latexFormatScript(lower, "sub")
	}
	if latexPlainWrappers[command] {
		value := p.parseRequiredArgument(true)
		if strings.HasPrefix(command, "text") || command == "mbox" {
			return value
		}
		return strings.TrimSpace(value)
	}
	if command == "begin" {
		return p.parseEnvironment()
	}
	if command == "end" {
		p.supported = false
		return ""
	}

	p.supported = false
	return "\\" + command
}

func (p *latexParser) parseOperator(operator string, inlineLowerStyle string, displayLimits bool, spaced bool) string {
	useDisplayLimits := displayLimits
	modifierPosition := p.position
	for modifierPosition < len(p.source) && (p.source[modifierPosition] == ' ' || p.source[modifierPosition] == '\t') {
		modifierPosition++
	}
	rest := string(p.source[modifierPosition:])
	if matches := latexLimitsModifierPattern.FindStringSubmatch(rest); matches != nil {
		runes := []rune(rest)
		following := len(matches[0])
		// pi lookahead (?![A-Za-z]): at end-of-string the modifier applies.
		if following >= len(runes) || !latexIsASCIILetter(runes[following]) {
			useDisplayLimits = matches[1] == "limits"
			p.position = modifierPosition + len(matches[0])
		}
	}

	var lower, upper *string
	for {
		scriptPosition := p.position
		for scriptPosition < len(p.source) && (p.source[scriptPosition] == ' ' || p.source[scriptPosition] == '\t') {
			scriptPosition++
		}
		if scriptPosition >= len(p.source) {
			break
		}
		kind := p.source[scriptPosition]
		if kind != '_' && kind != '^' {
			break
		}
		p.position = scriptPosition + 1
		value := strings.ReplaceAll(latexNormalizeOutput(p.parseRequiredArgument(false)), " ", "")
		if kind == '_' {
			if lower != nil {
				p.supported = false
			}
			lower = lo.ToPtr(value)
		} else {
			if upper != nil {
				p.supported = false
			}
			upper = lo.ToPtr(value)
		}
	}

	if p.display && useDisplayLimits && (lower != nil || upper != nil) {
		index := p.addNode(latexOperatorNode{operator: operator, lower: lower, upper: upper})
		return p.marker(index)
	}

	rendered := operator
	if lower != nil {
		if inlineLowerStyle == "bracket" {
			rendered += "[" + *lower + "]"
		} else {
			rendered += latexFormatScript(*lower, "sub")
		}
	}
	if upper != nil {
		rendered += latexFormatScript(*upper, "sup")
	}
	if spaced {
		return " " + rendered + " "
	}
	return rendered
}

func (p *latexParser) parseRequiredArgument(stackFractions bool) string {
	previousStackFractions := p.stackFractions
	p.stackFractions = previousStackFractions && stackFractions
	value := p.parseRequiredArgumentValue()
	p.stackFractions = previousStackFractions
	return value
}

func (p *latexParser) parseRequiredArgumentValue() string {
	for p.position < len(p.source) && unicode.IsSpace(p.source[p.position]) {
		p.position++
	}
	if p.position >= len(p.source) {
		p.supported = false
		return ""
	}
	if p.source[p.position] == '{' {
		p.position++
		return p.parseSequence('}')
	}
	if p.source[p.position] == '\\' {
		return p.parseCommand()
	}
	value := string(p.source[p.position])
	p.position++
	return value
}

func (p *latexParser) parseOptionalArgument() (string, bool) {
	for p.position < len(p.source) && (p.source[p.position] == ' ' || p.source[p.position] == '\t') {
		p.position++
	}
	if p.position >= len(p.source) || p.source[p.position] != '[' {
		return "", false
	}
	end := -1
	for i := p.position + 1; i < len(p.source); i++ {
		if p.source[i] == ']' {
			end = i
			break
		}
	}
	if end < 0 {
		p.supported = false
		return "", false
	}
	value := string(p.source[p.position+1 : end])
	p.position = end + 1
	return p.renderNested(value, true), true
}

func (p *latexParser) readRawGroup() (string, bool) {
	for p.position < len(p.source) && (p.source[p.position] == ' ' || p.source[p.position] == '\t') {
		p.position++
	}
	if p.position >= len(p.source) || p.source[p.position] != '{' {
		p.supported = false
		return "", false
	}

	p.position++
	start := p.position
	depth := 1
	for p.position < len(p.source) {
		character := p.source[p.position]
		if character == '\\' {
			p.position += 2
			continue
		}
		if character == '{' {
			depth++
		}
		if character == '}' {
			depth--
		}
		if depth == 0 {
			value := string(p.source[start:p.position])
			p.position++
			return value, true
		}
		p.position++
	}
	p.supported = false
	return "", false
}

func (p *latexParser) splitEnvironmentRows(body string) []string {
	return latexEnvironmentRowSplitPattern.Split(body, -1)
}

func (p *latexParser) parseEnvironment() string {
	environment, ok := p.readRawGroup()
	if !ok {
		return ""
	}
	endMarker := "\\end{" + environment + "}"
	rest := string(p.source[p.position:])
	end := strings.Index(rest, endMarker)
	if end < 0 {
		p.supported = false
		return ""
	}
	body := string(p.source[p.position : p.position+end])
	p.position += end + len(endMarker)

	if environment == "equation" || environment == "equation*" || environment == "displaymath" {
		return strings.TrimSpace(p.renderNested(body, true))
	}

	switch environment {
	case "aligned", "align", "align*", "alignedat", "alignat", "alignat*",
		"gather", "gathered", "multline", "multline*", "split":
		alignedAt := environment == "alignedat" || environment == "alignat" || environment == "alignat*"
		alignedBody := body
		if alignedAt {
			alignedBody = latexLeadingGroupPattern.ReplaceAllString(body, "")
		}
		rows := p.splitEnvironmentRows(alignedBody)
		rendered := make([]string, 0, len(rows))
		for _, row := range rows {
			cells := strings.Split(row, "&")
			var source string
			if alignedAt {
				pairs := make([]string, 0, (len(cells)+1)/2)
				for index := 0; index*2 < len(cells); index++ {
					end := index*2 + 2
					if end > len(cells) {
						end = len(cells)
					}
					pairs = append(pairs, strings.Join(cells[index*2:end], ""))
				}
				source = strings.Join(pairs, " ")
			} else {
				source = strings.Join(cells, "")
			}
			value := strings.TrimSpace(p.renderNested(source, true))
			if value != "" {
				rendered = append(rendered, value)
			}
		}
		return strings.Join(rendered, "\n")
	}

	if environment == "cases" || environment == "cases*" {
		rows := p.splitEnvironmentRows(body)
		kept := make([][][]rune, 0, len(rows))
		for _, row := range rows {
			cells := strings.Split(row, "&")
			rendered := make([][]rune, len(cells))
			hasContent := false
			for i, cell := range cells {
				rendered[i] = []rune(strings.TrimSpace(p.renderNested(cell, false)))
				if len(rendered[i]) > 0 {
					hasContent = true
				}
			}
			if hasContent {
				kept = append(kept, rendered)
			}
		}
		lines := make([]string, 0, len(kept))
		for index, row := range kept {
			first := ""
			if len(row) > 0 {
				first = string(row[0])
			}
			value := latexTrailingCommaPattern.ReplaceAllString(first, "")
			condition := ""
			if len(row) > 1 {
				condition = string(row[1])
			}
			delimiter := "⎨"
			if index == 0 {
				delimiter = "⎧"
			} else if index == len(kept)-1 {
				delimiter = "⎩"
			}
			conditionPrefix := " if "
			if latexCasesConditionPrefixParse.MatchString(condition) {
				conditionPrefix = " "
			}
			line := delimiter + " " + value
			if condition != "" {
				line += conditionPrefix + condition
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n")
	}

	switch environment {
	case "array", "matrix", "smallmatrix", "pmatrix", "bmatrix", "Bmatrix",
		"vmatrix", "Vmatrix":
		matrixBody := body
		if environment == "array" {
			matrixBody = latexLeadingGroupPattern.ReplaceAllString(body, "")
		}
		return p.renderMatrix(environment, matrixBody)
	}

	p.supported = false
	return body
}

func (p *latexParser) renderMatrix(environment string, body string) string {
	matrix := make([][][]rune, 0)
	for _, row := range p.splitEnvironmentRows(body) {
		cells := strings.Split(row, "&")
		rendered := make([][]rune, len(cells))
		hasContent := false
		for i, cell := range cells {
			rendered[i] = []rune(strings.TrimSpace(p.renderNested(cell, false)))
			if len(rendered[i]) > 0 {
				hasContent = true
			}
		}
		if hasContent {
			matrix = append(matrix, rendered)
		}
	}
	columnCount := 0
	for _, row := range matrix {
		if len(row) > columnCount {
			columnCount = len(row)
		}
	}
	columnWidths := make([]int, columnCount)
	for column := 0; column < columnCount; column++ {
		for _, row := range matrix {
			cell := ""
			if column < len(row) {
				cell = string(row[column])
			}
			columnWidths[column] = max(columnWidths[column], VisibleWidth(cell))
		}
	}
	rows := make([]string, len(matrix))
	for i, row := range matrix {
		cells := make([]string, columnCount)
		for column := 0; column < columnCount; column++ {
			cell := ""
			if column < len(row) {
				cell = string(row[column])
			}
			pad := columnWidths[column] - VisibleWidth(cell)
			if pad < 0 {
				pad = 0
			}
			cells[column] = cell + strings.Repeat(string(latexProtectedSpace), pad)
		}
		rows[i] = strings.Join(cells, " │ ")
	}

	var lines []string
	if environment == "array" || environment == "matrix" || environment == "smallmatrix" {
		lines = rows
	} else {
		delimiters := map[string][6]string{
			"pmatrix": {"⎛", "⎞", "⎜", "⎟", "⎝", "⎠"},
			"bmatrix": {"⎡", "⎤", "⎢", "⎥", "⎣", "⎦"},
			"Bmatrix": {"⎧", "⎫", "⎨", "⎬", "⎩", "⎭"},
			"vmatrix": {"│", "│", "│", "│", "│", "│"},
			"Vmatrix": {"║", "║", "║", "║", "║", "║"},
		}
		delimiter, ok := delimiters[environment]
		if !ok {
			p.supported = false
			return strings.Join(rows, "\n")
		}
		lines = make([]string, len(rows))
		for index, row := range rows {
			left, right := delimiter[2], delimiter[3]
			if index == 0 {
				left, right = delimiter[0], delimiter[1]
			} else if index == len(rows)-1 {
				left, right = delimiter[4], delimiter[5]
			}
			lines[index] = left + " " + row + " " + right
		}
	}

	if len(lines) <= 1 {
		if len(lines) == 0 {
			return ""
		}
		return lines[0]
	}
	index := p.addNode(latexMatrixNode{lines: lines, baseline: 0})
	return p.marker(index)
}

func (p *latexParser) renderNested(source string, stackFractions bool) string {
	rendered, ok := newLatexParser(source, p.layoutNodes, p.display && stackFractions).render()
	if !ok {
		p.supported = false
		return source
	}
	return rendered
}

// RenderLatex renders a basic LaTeX math expression as terminal-friendly
// Unicode text (pi: renderLatex). ok is false when the expression contains
// unsupported or malformed syntax (pi: undefined). pi's RenderLatexOptions
// has a single display option — collapsed into the display parameter (the
// markdown seam's signature).
func RenderLatex(source string, display bool) (string, bool) {
	var layoutNodes []latexLayoutNode
	rendered, ok := newLatexParser(source, &layoutNodes, display).render()
	if !ok {
		return "", false
	}
	if len(layoutNodes) == 0 {
		return strings.ReplaceAll(rendered, string(latexProtectedSpace), " "), true
	}
	lines := latexRenderLayout(rendered, layoutNodes).lines
	indentation := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := utf8RuneCount(line) - utf8RuneCount(strings.TrimLeftFunc(line, unicode.IsSpace))
		if indentation < 0 || indent < indentation {
			indentation = indent
		}
	}
	if indentation < 0 {
		indentation = 0
	}
	var b strings.Builder
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) > indentation {
			runes = runes[indentation:]
		} else {
			runes = nil
		}
		b.WriteString(strings.TrimRightFunc(string(runes), unicode.IsSpace))
		b.WriteByte('\n')
	}
	return strings.ReplaceAll(strings.TrimRight(b.String(), "\n"), string(latexProtectedSpace), " "), true
}

func utf8RuneCount(s string) int { return len([]rune(s)) }

// Wire the markdown seam (pi: markdown.ts imports renderLatex directly).
func init() {
	renderLatexFn = func(text string, display bool) (string, bool) {
		return RenderLatex(text, display)
	}
}
