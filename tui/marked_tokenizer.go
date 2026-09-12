// tui/marked_tokenizer.go (marked: src/Lexer.js Tokenizer class — part of the
// marked.go port; see the header notes there)
package tui

import (
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

// markedTokenizer is marked's Tokenizer with pi's StrictStrikethrough del
// override baked into Del (pi: markdownParser.setOptions({tokenizer: new
// StrictStrikethroughTokenizer()}) replaces the builtin del entirely).
type markedTokenizer struct {
	lexer       *markedLexer
	other       *markedOther
	block       *markedBlockRules
	inlineRules *markedInlineRules
}

// ---------------------------------------------------------------------------
// block tokenizers
// ---------------------------------------------------------------------------

func (t *markedTokenizer) space(src string) *MarkedToken {
	if m := reExec(t.block.Newline, src); m != nil && m.Length > 0 {
		return &MarkedToken{Type: "space", Raw: m.String()}
	}
	return nil
}

func (t *markedTokenizer) code(src string) *MarkedToken {
	m := reExec(t.block.Code, src)
	if m == nil {
		return nil
	}
	// options.pedantic is always false in pig → always chop.
	n := markedChop(m.String())
	s := reReplaceAll(t.other.CodeRemoveIndent, n, "")
	return &MarkedToken{Type: "code", Raw: n, Text: s}
}

func (t *markedTokenizer) fences(src string) *MarkedToken {
	m := reExec(t.block.Fences, src)
	if m == nil {
		return nil
	}
	raw := m.String()
	compensated := markedIndentCodeCompensation(raw, mGroup(m, 3), t.other)
	lang := ""
	if m2 := mGroup(m, 2); m2 != "" {
		lang = reReplaceAll(t.inlineRules.AnyPunctuation, strings.TrimSpace(m2), "$1")
	}
	return &MarkedToken{Type: "code", Raw: raw, Lang: lang, Text: compensated}
}

func (t *markedTokenizer) heading(src string) *MarkedToken {
	m := reExec(t.block.Heading, src)
	if m == nil {
		return nil
	}
	n := strings.TrimSpace(mGroup(m, 2))
	if reTest(t.other.EndingHash, n) {
		s := markedRtrim(n, "#", false)
		// pedantic branch resolves to: !s || endingSpaceChar.test(s)
		if s == "" || reTest(t.other.EndingSpaceChar, s) {
			n = strings.TrimSpace(s)
		}
	}
	token := &MarkedToken{
		Type:  "heading",
		Raw:   markedRtrim(m.String(), "\n", false),
		Depth: jsRuneLen(mGroup(m, 1)),
		Text:  n,
	}
	t.lexer.queueInline(n, token)
	return token
}

func (t *markedTokenizer) hr(src string) *MarkedToken {
	m := reExec(t.block.Hr, src)
	if m == nil {
		return nil
	}
	return &MarkedToken{Type: "hr", Raw: markedRtrim(m.String(), "\n", false)}
}

func (t *markedTokenizer) blockquote(src string) *MarkedToken {
	m := reExec(t.block.Blockquote, src)
	if m == nil {
		return nil
	}
	lines := strings.Split(markedRtrim(m.String(), "\n", false), "\n")
	raw := ""
	text := ""
	tokens := []*MarkedToken{}
	for len(lines) > 0 {
		foundStart := false
		var quoted []string
		a := 0
		for a = 0; a < len(lines); a++ {
			if reTest(t.other.BlockquoteStart, lines[a]) {
				quoted = append(quoted, lines[a])
				foundStart = true
			} else if !foundStart {
				quoted = append(quoted, lines[a])
			} else {
				break
			}
		}
		lines = lines[a:]
		c := strings.Join(quoted, "\n")
		p := reReplaceAll(t.other.BlockquoteSetextReplace, c, "\n    $1")
		p = reReplaceAll(t.other.BlockquoteSetextReplace2, p, "")
		if raw != "" {
			raw += "\n" + c
		} else {
			raw = c
		}
		if text != "" {
			text += "\n" + p
		} else {
			text = p
		}
		prevTop := t.lexer.state.top
		t.lexer.state.top = true
		t.lexer.blockTokens(p, &tokens, true)
		t.lexer.state.top = prevTop
		if len(lines) == 0 {
			break
		}
		if len(tokens) == 0 {
			continue // pi: i.at(-1) undefined → neither branch fires
		}
		h := tokens[len(tokens)-1]
		if h.Type == "code" {
			break
		}
		if h.Type == "blockquote" {
			f := h.Raw + "\n" + strings.Join(lines, "\n")
			s := t.blockquote(f)
			tokens[len(tokens)-1] = s
			raw = jsSlice(raw, 0, jsRuneLen(raw)-jsRuneLen(h.Raw)) + s.Raw
			text = jsSlice(text, 0, jsRuneLen(text)-jsRuneLen(h.Text)) + s.Text
			break
		} else if h.Type == "list" {
			f := h.Raw + "\n" + strings.Join(lines, "\n")
			s := t.list(f)
			tokens[len(tokens)-1] = s
			raw = jsSlice(raw, 0, jsRuneLen(raw)-jsRuneLen(h.Raw)) + s.Raw
			text = jsSlice(text, 0, jsRuneLen(text)-jsRuneLen(h.Raw)) + s.Raw
			lines = strings.Split(jsSlice(f, jsRuneLen(s.Raw), jsRuneLen(f)), "\n")
			continue
		}
	}
	return &MarkedToken{Type: "blockquote", Raw: raw, Tokens: tokens, Text: text}
}

func (t *markedTokenizer) list(src string) *MarkedToken {
	m := reExec(t.block.List, src)
	if m == nil {
		return nil
	}
	bull := strings.TrimSpace(mGroup(m, 1))
	isordered := len(bull) > 1
	item := &MarkedToken{Type: "list", Ordered: isordered, Start: "", Items: []*MarkedToken{}}
	if isordered {
		if n, err := strconv.Atoi(bull[:len(bull)-1]); err == nil {
			item.Start = n
		}
	}
	if isordered {
		bull = "\\d{1,9}\\" + bull[len(bull)-1:]
	} else {
		bull = "\\" + bull
	}
	itemRegex := markedListItemRegex(bull)
	previousBlankLine := false
	for src != "" {
		checkBlank := false
		itemRaw := ""
		itemText := ""
		m = reExec(itemRegex, src)
		if m == nil || reTest(t.block.Hr, src) {
			break
		}
		itemRaw = m.String()
		src = jsSlice(src, jsRuneLen(itemRaw), jsRuneLen(src))
		outdent := markedOutdentTab(strings.Split(mGroup(m, 2), "\n")[0], jsRuneLen(mGroup(m, 1)))
		nextLine := strings.Split(src, "\n")[0]
		blankish := strings.TrimSpace(outdent) == ""
		indentAfterBullet := 0
		if blankish {
			indentAfterBullet = jsRuneLen(mGroup(m, 1)) + 1
		} else {
			indentAfterBullet = runeIndexOfRE(outdent, t.other.NonSpaceChar)
			if indentAfterBullet > 4 {
				indentAfterBullet = 1
			}
			itemText = jsSlice(outdent, indentAfterBullet, jsRuneLen(outdent))
			indentAfterBullet += jsRuneLen(mGroup(m, 1))
		}
		if blankish && reTest(t.other.BlankLine, nextLine) {
			itemRaw += nextLine + "\n"
			src = jsSlice(src, jsRuneLen(nextLine)+1, jsRuneLen(src))
			checkBlank = true
		}
		if !checkBlank {
			nextBullet := markedNextBulletRegex(indentAfterBullet)
			hr := markedHrRegex(indentAfterBullet)
			fencesBegin := markedFencesBeginRegex(indentAfterBullet)
			headingBegin := markedHeadingBeginRegex(indentAfterBullet)
			htmlBegin := markedHtmlBeginRegex(indentAfterBullet)
			blockquoteBegin := markedBlockquoteBeginRegex(indentAfterBullet)
			for src != "" {
				line := strings.Split(src, "\n")[0]
				h := line
				c := strings.ReplaceAll(h, "\t", "    ")
				if reTest(fencesBegin, h) || reTest(headingBegin, h) || reTest(htmlBegin, h) ||
					reTest(blockquoteBegin, h) || reTest(nextBullet, h) || reTest(hr, h) {
					break
				}
				if runeIndexOfRE(c, t.other.NonSpaceChar) >= indentAfterBullet || strings.TrimSpace(h) == "" {
					itemText += "\n" + jsSlice(c, indentAfterBullet, jsRuneLen(c))
				} else {
					if blankish ||
						runeIndexOfRE(strings.ReplaceAll(outdent, "\t", "    "), t.other.NonSpaceChar) >= 4 ||
						reTest(fencesBegin, outdent) || reTest(headingBegin, outdent) || reTest(hr, outdent) {
						break
					}
					itemText += "\n" + h
				}
				blankish = strings.TrimSpace(h) == ""
				itemRaw += line + "\n"
				src = jsSlice(src, jsRuneLen(line)+1, jsRuneLen(src))
				outdent = jsSlice(c, indentAfterBullet, jsRuneLen(c))
			}
		}
		if !item.Loose {
			if previousBlankLine {
				item.Loose = true
			} else if reTest(t.other.DoubleBlankLine, itemRaw) {
				previousBlankLine = true
			}
		}
		entry := &MarkedToken{
			Type:   "list_item",
			Raw:    itemRaw,
			Task:   reTest(t.other.ListIsTask, itemText),
			Text:   itemText,
			Tokens: []*MarkedToken{},
		}
		item.Items = append(item.Items, entry)
		item.Raw += itemRaw
	}
	if len(item.Items) == 0 {
		return nil
	}
	last := item.Items[len(item.Items)-1]
	last.Raw = markedTrimEnd(last.Raw)
	last.Text = markedTrimEnd(last.Text)
	item.Raw = markedTrimEnd(item.Raw)
	for _, entry := range item.Items {
		t.lexer.state.top = false
		t.lexer.blockTokens(entry.Text, &entry.Tokens, false)
		var first *MarkedToken
		if len(entry.Tokens) > 0 {
			first = entry.Tokens[0]
		}
		if entry.Task && (first != nil && (first.Type == "text" || first.Type == "paragraph")) {
			entry.Text = reReplaceAll(t.other.ListReplaceTask, entry.Text, "")
			first.Raw = reReplaceAll(t.other.ListReplaceTask, first.Raw, "")
			first.Text = reReplaceAll(t.other.ListReplaceTask, first.Text, "")
			for k := len(t.lexer.inlineQueue) - 1; k >= 0; k-- {
				if reTest(t.other.ListIsTask, t.lexer.inlineQueue[k].src) {
					t.lexer.inlineQueue[k].src = reReplaceAll(t.other.ListReplaceTask, t.lexer.inlineQueue[k].src, "")
					break
				}
			}
			if cm := reExec(t.other.ListTaskCheckbox, entry.Raw); cm != nil {
				checked := cm.String() != "[ ]"
				box := &MarkedToken{Type: "checkbox", Raw: cm.String() + " ", Checked: checked}
				entry.Checked = checked
				if item.Loose {
					if len(entry.Tokens) > 0 && (entry.Tokens[0].Type == "paragraph" || entry.Tokens[0].Type == "text") && entry.Tokens[0].Tokens != nil {
						head := entry.Tokens[0]
						head.Raw = box.Raw + head.Raw
						head.Text = box.Raw + head.Text
						head.Tokens = append([]*MarkedToken{box}, head.Tokens...)
					} else {
						entry.Tokens = append([]*MarkedToken{{Type: "paragraph", Raw: box.Raw, Text: box.Raw, Tokens: []*MarkedToken{box}}}, entry.Tokens...)
					}
				} else {
					entry.Tokens = append([]*MarkedToken{box}, entry.Tokens...)
				}
			}
		} else if entry.Task {
			entry.Task = false
		}
		if !item.Loose {
			spaceCount := 0
			anyLine := false
			for _, tok := range entry.Tokens {
				if tok.Type == "space" {
					spaceCount++
					if reTest(t.other.AnyLine, tok.Raw) {
						anyLine = true
					}
				}
			}
			item.Loose = spaceCount > 0 && anyLine
		}
	}
	if item.Loose {
		for _, entry := range item.Items {
			entry.Loose = true
			for _, tok := range entry.Tokens {
				if tok.Type == "text" {
					tok.Type = "paragraph"
				}
			}
		}
	}
	return item
}

// markedTrimEnd is JS String.prototype.trimEnd (ECMAScript WhiteSpace ∪
// LineTerminator — matches pig's JS-semantics trim helper usage elsewhere;
// here the ASCII+Unicode set via unicode.IsSpace is equivalent for the inputs
// marked trims).
func markedTrimEnd(s string) string {
	// JS String.prototype.trimEnd (ECMAScript WhiteSpace union LineTerminator).
	return strings.TrimRightFunc(s, func(r rune) bool {
		switch r {
		case 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0xFEFF:
			return true
		}
		return r >= 0x2000 && r <= 0x200A
	})
}

func (t *markedTokenizer) html(src string) *MarkedToken {
	m := reExec(t.block.Html, src)
	if m == nil {
		return nil
	}
	n := markedChop(m.String())
	return &MarkedToken{Type: "html", Block: true, Raw: n, Text: n}
}

func (t *markedTokenizer) def(src string) *MarkedToken {
	m := reExec(t.block.Def, src)
	if m == nil {
		return nil
	}
	tag := reReplaceAll(t.other.MultipleSpaceGlobal, strings.ToLower(mGroup(m, 1)), " ")
	href := mGroup(m, 2)
	if href != "" {
		href = reReplaceAll(t.inlineRules.AnyPunctuation, reReplaceAll(t.other.HrefBrackets, href, "$1"), "$1")
	}
	title := ""
	if m3 := mGroup(m, 3); m3 != "" {
		title = reReplaceAll(t.inlineRules.AnyPunctuation, jsSlice(m3, 1, jsRuneLen(m3)-1), "$1")
	}
	return &MarkedToken{
		Type:  "def",
		Tag:   tag,
		Raw:   markedRtrim(m.String(), "\n", false),
		Href:  href,
		Title: title,
	}
}

func (t *markedTokenizer) table(src string) *MarkedToken {
	m := reExec(t.block.Table, src)
	if m == nil || !reTest(t.other.TableDelimiter, mGroup(m, 2)) {
		return nil
	}
	header := markedSplitCells(mGroup(m, 1), 0, t.other)
	aligns := strings.Split(reReplaceAll(t.other.TableAlignChars, mGroup(m, 2), ""), "|")
	rows := []string{}
	if m3 := mGroup(m, 3); strings.TrimSpace(m3) != "" {
		rows = strings.Split(reReplaceAll(t.other.TableRowBlankLine, m3, ""), "\n")
	}
	item := &MarkedToken{
		Type: "table", Raw: markedRtrim(m.String(), "\n", false),
		HeaderCells: []*MarkedToken{}, Aligns: []string{}, Rows: [][]*MarkedToken{},
	}
	if len(header) != len(aligns) {
		return nil
	}
	for _, cell := range aligns {
		switch {
		case reTest(t.other.TableAlignRight, cell):
			item.Aligns = append(item.Aligns, "right")
		case reTest(t.other.TableAlignCenter, cell):
			item.Aligns = append(item.Aligns, "center")
		case reTest(t.other.TableAlignLeft, cell):
			item.Aligns = append(item.Aligns, "left")
		default:
			item.Aligns = append(item.Aligns, "")
		}
	}
	for i, h := range header {
		headerTrue := true
		cell := &MarkedToken{Text: h, Header: &headerTrue, Align: item.Aligns[i]}
		t.lexer.queueInline(h, cell)
		item.HeaderCells = append(item.HeaderCells, cell)
	}
	for _, row := range rows {
		cells := markedSplitCells(row, len(item.HeaderCells), t.other)
		out := []*MarkedToken{}
		for a, c := range cells {
			headerFalse := false
			align := ""
			if a < len(item.Aligns) {
				align = item.Aligns[a]
			}
			cell := &MarkedToken{Text: c, Header: &headerFalse, Align: align}
			t.lexer.queueInline(c, cell)
			out = append(out, cell)
		}
		item.Rows = append(item.Rows, out)
	}
	return item
}

func (t *markedTokenizer) lheading(src string) *MarkedToken {
	m := reExec(t.block.Lheading, src)
	if m == nil {
		return nil
	}
	n := strings.TrimSpace(mGroup(m, 1))
	depth := 2
	if strings.HasPrefix(mGroup(m, 2), "=") {
		depth = 1
	}
	token := &MarkedToken{Type: "heading", Raw: markedRtrim(m.String(), "\n", false), Depth: depth, Text: n}
	t.lexer.queueInline(n, token)
	return token
}

func (t *markedTokenizer) paragraph(src string) *MarkedToken {
	m := reExec(t.block.Paragraph, src)
	if m == nil {
		return nil
	}
	m1 := mGroup(m, 1)
	n := m1
	if strings.HasSuffix(m1, "\n") {
		n = jsSlice(m1, 0, jsRuneLen(m1)-1)
	}
	token := &MarkedToken{Type: "paragraph", Raw: m.String(), Text: n}
	t.lexer.queueInline(n, token)
	return token
}

func (t *markedTokenizer) text(src string) *MarkedToken {
	m := reExec(t.block.Text, src)
	if m == nil {
		return nil
	}
	token := &MarkedToken{Type: "text", Raw: m.String(), Text: m.String()}
	t.lexer.queueInline(m.String(), token)
	return token
}

// ---------------------------------------------------------------------------
// inline tokenizers
// ---------------------------------------------------------------------------

func (t *markedTokenizer) escape(src string) *MarkedToken {
	m := reExec(t.inlineRules.Escape, src)
	if m == nil {
		return nil
	}
	return &MarkedToken{Type: "escape", Raw: m.String(), Text: mGroup(m, 1)}
}

func (t *markedTokenizer) tag(src string) *MarkedToken {
	m := reExec(t.inlineRules.Tag, src)
	if m == nil {
		return nil
	}
	if !t.lexer.state.inLink && reTest(t.other.StartATag, m.String()) {
		t.lexer.state.inLink = true
	} else if t.lexer.state.inLink && reTest(t.other.EndATag, m.String()) {
		t.lexer.state.inLink = false
	}
	if !t.lexer.state.inRawBlock && reTest(t.other.StartPreScriptTag, m.String()) {
		t.lexer.state.inRawBlock = true
	} else if t.lexer.state.inRawBlock && reTest(t.other.EndPreScriptTag, m.String()) {
		t.lexer.state.inRawBlock = false
	}
	return &MarkedToken{Type: "html", Raw: m.String(), InLink: t.lexer.state.inLink,
		InRawBlock: t.lexer.state.inRawBlock, Block: false, Text: m.String()}
}

// markedLinkMatch carries the (possibly trimmed) match fields the link
// tokenizer uses after JS mutates match[0]/match[2]/match[3] in place.
type markedLinkMatch struct {
	full  string // match[0] after trimming
	label string // match[1]
	href  string // match[2] after trimming
	title string // match[3] after clearing
}

func (t *markedTokenizer) link(src string) *MarkedToken {
	m := reExec(t.inlineRules.Link, src)
	if m == nil {
		return nil
	}
	lm := markedLinkMatch{full: m.String(), label: mGroup(m, 1), href: mGroup(m, 2), title: mGroup(m, 3)}
	n := strings.TrimSpace(mGroup(m, 2))
	if reTest(t.other.StartAngleBracket, n) {
		if !reTest(t.other.EndAngleBracket, n) {
			return nil
		}
		i := markedRtrim(jsSlice(n, 0, jsRuneLen(n)-1), "\\", false)
		if (jsRuneLen(n)-jsRuneLen(i))%2 == 0 {
			return nil
		}
	} else {
		i := markedFindClosingBracket(mGroup(m, 2), "()")
		if i == -2 {
			return nil
		}
		if i > -1 {
			u := 4
			if strings.HasPrefix(m.String(), "!") {
				u = 5
			}
			u += jsRuneLen(mGroup(m, 1)) + i
			lm.href = jsSlice(mGroup(m, 2), 0, i)
			lm.full = strings.TrimSpace(jsSlice(m.String(), 0, u))
			lm.title = ""
		}
	}
	s := lm.href
	title := ""
	if lm.title != "" {
		title = jsSlice(lm.title, 1, jsRuneLen(lm.title)-1)
	}
	s = strings.TrimSpace(s)
	if reTest(t.other.StartAngleBracket, s) {
		// pedantic branch resolves to: s = s.slice(1, -1)
		s = jsSlice(s, 1, jsRuneLen(s)-1)
	}
	href := s
	if href != "" {
		href = reReplaceAll(t.inlineRules.AnyPunctuation, href, "$1")
	}
	titleOut := title
	if titleOut != "" {
		titleOut = reReplaceAll(t.inlineRules.AnyPunctuation, titleOut, "$1")
	}
	return t.createLink(lm, href, titleOut)
}

func (t *markedTokenizer) createLink(lm markedLinkMatch, href, title string) *MarkedToken {
	text := reReplaceAll(t.other.OutputLinkReplace, lm.label, "$1")
	tokenType := "link"
	if strings.HasPrefix(lm.full, "!") {
		tokenType = "image"
	}
	tok := &MarkedToken{Type: tokenType, Raw: lm.full, Href: href, Title: title, Text: text}
	t.lexer.state.inLink = true
	tok.Tokens = t.lexer.inlineTokensNow(text)
	t.lexer.state.inLink = false
	return tok
}

func (t *markedTokenizer) reflink(src string) *MarkedToken {
	m := reExec(t.inlineRules.Reflink, src)
	if m == nil {
		m = reExec(t.inlineRules.Nolink, src)
	}
	if m == nil {
		return nil
	}
	label := mGroup(m, 2)
	if label == "" {
		label = mGroup(m, 1)
	}
	label = reReplaceAll(t.other.MultipleSpaceGlobal, label, " ")
	ref, ok := t.lexer.links[strings.ToLower(label)]
	if !ok {
		head := firstRuneOf(m.String())
		return &MarkedToken{Type: "text", Raw: head, Text: head}
	}
	return t.createLink(markedLinkMatch{full: m.String(), label: mGroup(m, 1)}, ref.href, ref.title)
}

func (t *markedTokenizer) emStrong(src string, maskedSrc string, lastChar string) *MarkedToken {
	m := reExec(t.inlineRules.EmStrongLDelim, src)
	if m == nil {
		return nil
	}
	if mGroup(m, 1) == "" && mGroup(m, 2) == "" && mGroup(m, 3) == "" && mGroup(m, 4) == "" {
		return nil
	}
	if mGroup(m, 4) != "" && lastChar != "" && reTest(t.other.UnicodeAlphaNumeric, lastChar) {
		return nil
	}
	g1 := mGroup(m, 1)
	g3 := mGroup(m, 3)
	if !((g1 == "" && g3 == "") || lastChar == "" || reExec(t.inlineRules.Punctuation, lastChar) != nil) {
		return nil
	}
	delimRuneLen := jsRuneLen(m.String()) - 1
	after := delimRuneLen
	counter := 0
	var rdelim *regexp2.Regexp
	if strings.HasPrefix(m.String(), "*") {
		rdelim = t.inlineRules.EmStrongRDelimAst
	} else {
		rdelim = t.inlineRules.EmStrongRDelimUnd
	}
	tail := jsSlice(maskedSrc, jsRuneLen(maskedSrc)-jsRuneLen(src)+delimRuneLen, jsRuneLen(maskedSrc))
	for s := reExec(rdelim, tail); s != nil; s = nextMatch(rdelim, s) {
		enders := ""
		for _, gi := range []int{1, 2, 3, 4, 5, 6} {
			if g := mGroup(s, gi); g != "" {
				enders = g
				break
			}
		}
		if enders == "" {
			continue
		}
		endersLen := jsRuneLen(enders)
		if mGroup(s, 3) != "" || mGroup(s, 4) != "" {
			after += endersLen
			continue
			// marked: else if ((s[5] || s[6]) && i % 3 && !((i + u) % 3)) — the
			// closer candidate is rejected only when (i+u) is a multiple of 3
			// (CommonMark multiple-of-3 rule). The != here was ported inverted
			// (2026-09-12 fix): punct-flanked closers (`）*。`) were wrongly
			// rejected and the run fell back to literal text.
		} else if (mGroup(s, 5) != "" || mGroup(s, 6) != "") && delimRuneLen%3 != 0 && (delimRuneLen+endersLen)%3 == 0 {
			counter += endersLen
			continue
		}
		after -= endersLen
		if after > 0 {
			continue
		}
		endersLen = min(endersLen, endersLen+after+counter)
		firstRuneLen := jsRuneLen(firstRuneOf(s.String()))
		end := delimRuneLen + s.Index + firstRuneLen + endersLen
		h := jsSlice(src, 0, end)
		if min(delimRuneLen, endersLen)%2 != 0 {
			text := jsSlice(h, 1, jsRuneLen(h)-1)
			return &MarkedToken{Type: "em", Raw: h, Text: text, Tokens: t.lexer.inlineTokensNow(text)}
		}
		text := jsSlice(h, 2, jsRuneLen(h)-2)
		return &MarkedToken{Type: "strong", Raw: h, Text: text, Tokens: t.lexer.inlineTokensNow(text)}
	}
	return nil
}

func (t *markedTokenizer) codespan(src string) *MarkedToken {
	m := reExec(t.inlineRules.Code, src)
	if m == nil {
		return nil
	}
	n := reReplaceAll(t.other.NewLineCharGlobal, mGroup(m, 2), " ")
	hasNonSpace := reTest(t.other.NonSpaceChar, n)
	leading := reTest(t.other.StartingSpaceChar, n)
	trailing := reTest(t.other.EndingSpaceChar, n)
	if hasNonSpace && leading && trailing {
		n = jsSlice(n, 1, jsRuneLen(n)-1)
	}
	return &MarkedToken{Type: "codespan", Raw: m.String(), Text: n}
}

func (t *markedTokenizer) br(src string) *MarkedToken {
	m := reExec(t.inlineRules.Br, src)
	if m == nil {
		return nil
	}
	return &MarkedToken{Type: "br", Raw: m.String()}
}

func (t *markedTokenizer) del(src string, maskedSrc string, lastChar string) *MarkedToken {
	m := reExec(t.inlineRules.DelLDelim, src)
	if m == nil {
		return nil
	}
	// nextChar = match[1] (punct adjacency); when absent the del always
	// proceeds (pi: if (!nextChar || !prevChar || punctuation.exec(prevChar))).
	nextChar := mGroup(m, 1)
	if !(nextChar == "" || lastChar == "" || reExec(t.inlineRules.Punctuation, lastChar) != nil) {
		return nil
	}
	delimRuneLen := jsRuneLen(m.String()) - 1
	after := delimRuneLen
	rdelim := t.inlineRules.DelRDelim
	tail := jsSlice(maskedSrc, jsRuneLen(maskedSrc)-jsRuneLen(src)+delimRuneLen, jsRuneLen(maskedSrc))
	for s := reExec(rdelim, tail); s != nil; s = nextMatch(rdelim, s) {
		enders := ""
		for _, gi := range []int{1, 2, 3, 4, 5, 6} {
			if g := mGroup(s, gi); g != "" {
				enders = g
				break
			}
		}
		if enders == "" || jsRuneLen(enders) != delimRuneLen {
			continue
		}
		if mGroup(s, 3) != "" || mGroup(s, 4) != "" {
			after += jsRuneLen(enders)
			continue
		}
		after -= jsRuneLen(enders)
		if after > 0 {
			continue
		}
		endersLen := min(jsRuneLen(enders), jsRuneLen(enders)+after)
		firstRuneLen := jsRuneLen(firstRuneOf(s.String()))
		end := delimRuneLen + s.Index + firstRuneLen + endersLen
		k := jsSlice(src, 0, end)
		text := jsSlice(k, delimRuneLen, jsRuneLen(k)-delimRuneLen)
		return &MarkedToken{Type: "del", Raw: k, Text: text, Tokens: t.lexer.inlineTokensNow(text)}
	}
	return nil
}

func (t *markedTokenizer) autolink(src string) *MarkedToken {
	m := reExec(t.inlineRules.Autolink, src)
	if m == nil {
		return nil
	}
	text := mGroup(m, 1)
	href := text
	if mGroup(m, 2) == "@" {
		href = "mailto:" + text
	}
	return &MarkedToken{Type: "link", Raw: m.String(), Text: text, Href: href,
		Tokens: []*MarkedToken{{Type: "text", Raw: text, Text: text}}}
}

func (t *markedTokenizer) url(src string) *MarkedToken {
	m := reExec(t.inlineRules.Url, src)
	if m == nil {
		return nil
	}
	var text, href string
	if mGroup(m, 2) == "@" {
		text = m.String()
		href = "mailto:" + text
	} else {
		// do-while backpedal: repeatedly trim trailing punctuation.
		cur := m.String()
		for {
			bm := reExec(t.inlineRules._backpedal, cur)
			next := ""
			if bm != nil {
				next = bm.String()
			}
			if next == cur {
				break
			}
			cur = next
		}
		text = cur
		if mGroup(m, 1) == "www." {
			href = "http://" + text
		} else {
			href = text
		}
	}
	return &MarkedToken{Type: "link", Raw: text, Text: text, Href: href,
		Tokens: []*MarkedToken{{Type: "text", Raw: text, Text: text}}}
}

func (t *markedTokenizer) inlineText(src string) *MarkedToken {
	m := reExec(t.inlineRules.Text, src)
	if m == nil {
		return nil
	}
	return &MarkedToken{Type: "text", Raw: m.String(), Text: m.String(), Escaped: t.lexer.state.inRawBlock}
}
