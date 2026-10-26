// pi: utils/streaming-json.ts — incremental, tolerant JSON parsing.
//
// StreamingJSONParser.Append reads each piece once, so a stream costs time and
// memory linear in its length, and Snapshot returns the value of the text read
// so far. Snapshots are plain JSON values and never change once returned: the
// parser copies the containers that are still open (the "open path") into every
// new snapshot and shares completed containers and strings. The rules for
// incomplete text follow ParseStreamingJSON:
//
//   - An unterminated string shows the characters read so far; an unterminated
//     escape is held back until complete.
//   - An unterminated container shows the members and elements read so far.
//   - A member whose key or value has not started is omitted.
//   - An unterminated literal shows its value once it is a prefix of true,
//     false or null; an unterminated number shows its value when the digits
//     read so far form a number (an exponent without digits is ignored).
//
// Malformed text is read leniently, the way RepairJSON and ParseStreamingJSON
// treat it: raw control characters in strings are kept, an invalid escape such
// as \d stays literal text, missing commas and colons are tolerated, values in
// invalid positions and unknown words are dropped, and text after the root
// value is ignored. The parser never panics. Callers that need the exact value
// of the finished text parse it once, e.g. with ParseToolCallArguments.
//
// Differences from ParseStreamingJSON on incomplete text: trailing whitespace
// inside an unterminated string is kept (it was trimmed), a string that
// contains a raw control character or an invalid escape stays visible while it
// streams (it was dropped until the document was complete), and NaN/Infinity
// words are dropped.
//
// Append also returns the changes the piece made, for a writer that keeps its
// own copy of the value and applies them instead of re-assigning snapshots. A
// container nested more than 32 levels deep is reported as one set of its
// whole value when it closes, so paths stay short.
package utils

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// JSONPath is a path from the root value: member keys and array indices (a
// string or an int element).
type JSONPath []any

// JSONChange is one change a piece of text made to the value (pi: JsonChange).
type JSONChange struct {
	// Kind is "set" or "append".
	Kind string
	// Path locates the changed value.
	Path JSONPath
	// Value is the new value for a "set" change.
	Value any
	// Text is the appended text for an "append" change.
	Text string
}

type jsonSlotKind int

const (
	jsonSlotRoot jsonSlotKind = iota
	jsonSlotMember
	jsonSlotElement
)

// jsonSlot is where a value sits in its parent; nil for a value in an invalid
// position, which is read but dropped.
type jsonSlot struct {
	kind  jsonSlotKind
	key   string
	index int
}

type jsonPhase string

const (
	jsonPhaseKey   jsonPhase = "key"
	jsonPhaseColon jsonPhase = "colon"
	jsonPhaseValue jsonPhase = "value"
	jsonPhaseComma jsonPhase = "comma"
)

// jsonFrame is an open object or array. Its container is never handed out
// while open; snapshots get copies.
type jsonFrame struct {
	container any // map[string]any or []any
	slot      *jsonSlot
	// path is where the container sits, when its changes are reported.
	path *JSONPath
	// reportsInside reports changes inside it; when false, its whole value is
	// reported as it closes.
	reportsInside bool
	// size is members or elements, what a copy of the container costs.
	size   int
	key    string
	hasKey bool
	phase  jsonPhase
}

// openString is an unterminated string.
type openString struct {
	target string // "key" or "value"
	slot   *jsonSlot
	text   string
	// escape is "" for none, otherwise a backslash and up to five more bytes.
	escape string
	// path is where a value string sits, when its changes are reported.
	path *JSONPath
	// change is the index of the change this string extends while it is the
	// newest change of the piece; -1 when none.
	change int
	// pendingHigh is a UTF-16 high surrogate awaiting its low half; -1 when
	// none. (JS strings can hold lone surrogates; Go strings cannot.)
	pendingHigh int
}

const maxReportedDepth = 32

// Copy credit per append and per appended character. The floor keeps small
// documents fresh at every snapshot.
const (
	creditPerAppend     = 256
	creditPerCharacter  = 8
	maxPendingScalarLen = 64
)

var jsonLiterals = []struct {
	token string
	value any
}{
	{"true", true},
	{"false", false},
	{"null", nil},
}

// StreamingJSONParser is an incremental, tolerant JSON parser; see the package
// comment above for its rules and costs (pi: StreamingJsonParser).
type StreamingJSONParser struct {
	root    any
	hasRoot bool
	done    bool
	stack   []*jsonFrame
	str     *openString
	scalar  string
	// scalarSlot is where the unterminated scalar goes.
	scalarSlot *jsonSlot
	// pathCost is the sum of size+1 over the open path: what a fresh snapshot
	// copies.
	pathCost    int
	credit      int
	dirty       bool
	snapshot    any
	hasSnapshot bool
	// changes are the changes of the piece being read.
	changes []JSONChange
}

// NewStreamingJSONParser returns an empty parser.
func NewStreamingJSONParser() *StreamingJSONParser {
	return &StreamingJSONParser{}
}

// Append reads the next piece of text; it returns the changes it made, in
// order. Costs time linear in len(text).
func (p *StreamingJSONParser) Append(text string) []JSONChange {
	p.credit += creditPerAppend + creditPerCharacter*len(text)
	p.changes = nil
	if p.str != nil {
		p.str.change = -1
	}
	for index := 0; index < len(text) && !p.done; {
		if open := p.str; open != nil {
			index = p.readString(open, text, index)
		} else if p.scalar != "" {
			index = p.readScalar(text, index)
		} else {
			p.structural(text[index])
			index++
		}
	}
	return p.changes
}

// Snapshot returns the value of the text read so far. has is false before a
// value starts. fresh is true when a new snapshot was built; while the text
// read since did not change the value, or while a fresh copy of the open path
// is not yet paid for, the previous snapshot is returned with fresh false.
func (p *StreamingJSONParser) Snapshot() (value any, has bool, fresh bool) {
	if !p.dirty {
		return p.snapshot, p.hasSnapshot, false
	}
	pending := p.pendingScalar()
	cost := p.pathCost
	if pending != nil {
		cost++
	}
	if cost > p.credit {
		return p.snapshot, p.hasSnapshot, false
	}
	p.credit -= cost
	p.dirty = false
	value, has = p.copyOpenPath(pending)
	p.snapshot = value
	p.hasSnapshot = has
	return value, has, true
}

func (p *StreamingJSONParser) readString(open *openString, text string, start int) int {
	runStart := start
	for index := start; index < len(text); index++ {
		code := text[index]
		if open.escape != "" {
			if p.continueEscape(open, text[index]) {
				runStart = index + 1
				continue
			}
			// Not part of the escape: read it as a normal string character below.
			runStart = index
		}
		if code == '"' {
			if index > runStart {
				p.appendString(open, text[runStart:index])
			}
			p.endString(open)
			return index + 1
		}
		if code == '\\' {
			if index > runStart {
				p.appendString(open, text[runStart:index])
			}
			open.escape = "\\"
			runStart = index + 1
		}
	}
	if len(text) > runStart {
		p.appendString(open, text[runStart:])
	}
	return len(text)
}

func (p *StreamingJSONParser) readScalar(text string, start int) int {
	end := start
	for end < len(text) && !isJSONDelimiter(text[end]) {
		end++
	}
	if end > start {
		p.scalar += text[start:end]
		if p.scalarSlot != nil {
			p.dirty = true
		}
	}
	if end < len(text) {
		p.endScalar()
	}
	return end
}

func (p *StreamingJSONParser) structural(char byte) {
	switch char {
	case ' ', '\t', '\n', '\r':
		return
	case '"':
		p.startString()
		return
	case '{', '[':
		if char == '{' {
			p.openContainer(map[string]any{})
		} else {
			p.openContainer([]any{})
		}
		return
	case '}', ']':
		p.closeContainer()
		return
	case ':':
		frame := p.topFrame()
		if frame != nil && !isJSONArray(frame.container) && frame.phase == jsonPhaseColon {
			frame.phase = jsonPhaseValue
		}
		return
	case ',':
		frame := p.topFrame()
		if frame != nil {
			if isJSONArray(frame.container) {
				frame.phase = jsonPhaseValue
			} else {
				frame.phase = jsonPhaseKey
			}
		}
		return
	default:
		p.scalar = string(char)
		p.scalarSlot = p.valueSlot()
		if p.scalarSlot != nil {
			p.dirty = true
		}
	}
}

func (p *StreamingJSONParser) valueSlot() *jsonSlot {
	frame := p.topFrame()
	if frame == nil {
		if !p.hasRoot {
			return &jsonSlot{kind: jsonSlotRoot}
		}
		return nil
	}
	if elements, ok := frame.container.([]any); ok {
		return &jsonSlot{kind: jsonSlotElement, index: len(elements)}
	}
	// A missing colon is tolerated: {"a" 1} reads as {"a": 1}.
	if (frame.phase == jsonPhaseValue || frame.phase == jsonPhaseColon) && frame.hasKey {
		return &jsonSlot{kind: jsonSlotMember, key: frame.key}
	}
	return nil
}

func (p *StreamingJSONParser) startString() {
	frame := p.topFrame()
	// A missing comma is tolerated: {"a": 1 "b": 2} reads the second member.
	if frame != nil && !isJSONArray(frame.container) &&
		(frame.phase == jsonPhaseKey || frame.phase == jsonPhaseComma) {
		p.str = &openString{target: "key", change: -1, pendingHigh: -1}
		return
	}
	slot := p.valueSlot()
	path := p.reportedPath(slot)
	p.str = &openString{target: "value", slot: slot, path: path, change: -1, pendingHigh: -1}
	p.place(slot, "")
	if path != nil {
		p.str.change = p.report(JSONChange{Kind: "set", Path: append(JSONPath{}, *path...), Value: ""})
	}
}

func (p *StreamingJSONParser) reportedPath(slot *jsonSlot) *JSONPath {
	if slot == nil {
		return nil
	}
	if slot.kind == jsonSlotRoot {
		path := JSONPath{}
		return &path
	}
	frame := p.topFrame()
	if !frame.reportsInside {
		return nil
	}
	path := append(JSONPath{}, (*frame.path)...)
	if slot.kind == jsonSlotMember {
		path = append(path, slot.key)
	} else {
		path = append(path, slot.index)
	}
	return &path
}

func (p *StreamingJSONParser) report(change JSONChange) int {
	p.changes = append(p.changes, change)
	return len(p.changes) - 1
}

func (p *StreamingJSONParser) continueEscape(open *openString, char byte) bool {
	if open.escape == "\\" {
		if decoded, ok := jsonSimpleEscapes[char]; ok {
			open.escape = ""
			p.appendString(open, decoded)
			return true
		}
		if char == 'u' {
			open.escape = "\\u"
			return true
		}
		// Invalid escape: keep the backslash as text and read the character
		// normally, as RepairJSON does.
		open.escape = ""
		p.appendString(open, "\\")
		return false
	}
	if isJSONHexDigit(char) {
		open.escape += string(char)
		if len(open.escape) < 6 {
			return true
		}
		code, err := strconv.ParseUint(open.escape[2:], 16, 16)
		open.escape = ""
		if err != nil {
			code = 0xFFFD
		}
		p.appendUnicode(open, rune(code))
		return true
	}
	// \u without four hex digits: keep it as text and read the character
	// normally, as RepairJSON does.
	literal := open.escape
	open.escape = ""
	p.appendString(open, literal)
	return false
}

func (p *StreamingJSONParser) appendUnicode(open *openString, code rune) {
	if code >= 0xD800 && code <= 0xDBFF {
		p.flushPendingHigh(open)
		open.pendingHigh = int(code)
		return
	}
	if code >= 0xDC00 && code <= 0xDFFF {
		if open.pendingHigh >= 0 {
			decoded := utf16.DecodeRune(rune(open.pendingHigh), code)
			open.pendingHigh = -1
			p.appendStringRaw(open, string(decoded))
			return
		}
		p.appendStringRaw(open, "\uFFFD")
		return
	}
	p.flushPendingHigh(open)
	p.appendStringRaw(open, string(code))
}

func (p *StreamingJSONParser) flushPendingHigh(open *openString) {
	if open.pendingHigh >= 0 {
		open.pendingHigh = -1
		p.appendStringRaw(open, "\uFFFD")
	}
}

func (p *StreamingJSONParser) appendString(open *openString, text string) {
	p.flushPendingHigh(open)
	p.appendStringRaw(open, text)
}

func (p *StreamingJSONParser) appendStringRaw(open *openString, text string) {
	open.text += text
	if open.target != "value" {
		return
	}
	p.place(open.slot, open.text)
	if open.path == nil {
		return
	}
	// Extend this string's newest change while nothing came after it;
	// otherwise report an append.
	if open.change >= 0 && open.change == len(p.changes)-1 {
		change := &p.changes[open.change]
		if change.Kind == "set" {
			if existing, ok := change.Value.(string); ok {
				change.Value = existing + text
			} else {
				change.Value = text
			}
		} else {
			change.Text += text
		}
		return
	}
	open.change = p.report(JSONChange{Kind: "append", Path: append(JSONPath{}, (*open.path)...), Text: text})
}

func (p *StreamingJSONParser) endString(open *openString) {
	p.flushPendingHigh(open)
	p.str = nil
	frame := p.topFrame()
	if open.target == "key" {
		if frame == nil {
			return
		}
		frame.key = open.text
		frame.hasKey = true
		frame.phase = jsonPhaseColon
		return
	}
	p.valueEnded(frame)
}

func (p *StreamingJSONParser) endScalar() {
	token := p.scalar
	slot := p.scalarSlot
	p.scalar = ""
	p.scalarSlot = nil
	if value, ok := scalarJSONValue(token); ok {
		p.place(slot, value)
		if path := p.reportedPath(slot); path != nil {
			p.report(JSONChange{Kind: "set", Path: append(JSONPath{}, (*path)...), Value: value})
		}
	}
	p.valueEnded(p.topFrame())
}

// pendingScalar is the value of the unterminated scalar at the end of the text,
// if it shows.
func (p *StreamingJSONParser) pendingScalar() *struct {
	slot  *jsonSlot
	value any
} {
	slot := p.scalarSlot
	token := p.scalar
	if slot == nil || token == "" || len(token) > maxPendingScalarLen {
		return nil
	}
	value, ok := pendingScalarJSONValue(token)
	if !ok {
		return nil
	}
	return &struct {
		slot  *jsonSlot
		value any
	}{slot: slot, value: value}
}

func (p *StreamingJSONParser) openContainer(container any) {
	slot := p.valueSlot()
	path := p.reportedPath(slot)
	p.place(slot, container)
	frame := &jsonFrame{
		container:     container,
		slot:          slot,
		path:          path,
		reportsInside: path != nil && len(*path) < maxReportedDepth,
		phase:         jsonPhaseKey,
	}
	if isJSONArray(container) {
		frame.phase = jsonPhaseValue
	}
	p.stack = append(p.stack, frame)
	p.pathCost++
	p.dirty = true
	// A fresh container: the working one keeps changing, and a change never
	// does.
	if path != nil {
		var fresh any
		if isJSONArray(container) {
			fresh = []any{}
		} else {
			fresh = map[string]any{}
		}
		p.report(JSONChange{Kind: "set", Path: append(JSONPath{}, (*path)...), Value: fresh})
	}
}

func (p *StreamingJSONParser) closeContainer() {
	frame := p.popFrame()
	// A stray closer outside any container is ignored.
	if frame == nil {
		return
	}
	p.pathCost -= frame.size + 1
	p.dirty = true
	// Too deep to report inside: the whole value, final now that it is closed.
	if frame.path != nil && !frame.reportsInside {
		p.report(JSONChange{Kind: "set", Path: append(JSONPath{}, (*frame.path)...), Value: frame.container})
	}
	p.valueEnded(p.topFrame())
}

// valueEnded marks a value in frame, or the root value when there is no frame,
// as ended.
func (p *StreamingJSONParser) valueEnded(frame *jsonFrame) {
	if frame == nil {
		// Text after the root value is ignored, but only once a root value
		// exists.
		if p.hasRoot {
			p.done = true
		}
		return
	}
	frame.phase = jsonPhaseComma
}

// place sets the value at slot in the open path; nothing happens for a value in
// an invalid position.
func (p *StreamingJSONParser) place(slot *jsonSlot, value any) {
	if slot == nil {
		return
	}
	p.dirty = true
	if slot.kind == jsonSlotRoot {
		p.root = value
		p.hasRoot = true
		return
	}
	frame := p.topFrame()
	if slot.kind == jsonSlotElement {
		elements := frame.container.([]any)
		if slot.index == len(elements) {
			frame.size++
			p.pathCost++
			elements = append(elements, value)
			// Slices reallocate when they grow; repoint the container that
			// holds this frame at the new backing array.
			frame.container = elements
			p.repointFrameContainer(frame, elements)
		} else if slot.index < len(elements) {
			elements[slot.index] = value
		}
		return
	}
	members := frame.container.(map[string]any)
	if _, ok := members[slot.key]; !ok {
		frame.size++
		p.pathCost++
	}
	members[slot.key] = value
}

// copyOpenPath returns a fresh snapshot: copies of the open containers,
// innermost first, each holding the copy of its open child.
func (p *StreamingJSONParser) copyOpenPath(pending *struct {
	slot  *jsonSlot
	value any
}) (any, bool) {
	if len(p.stack) == 0 {
		if pending != nil && pending.slot.kind == jsonSlotRoot {
			return pending.value, true
		}
		return p.root, p.hasRoot
	}
	var child any
	var childSlot *jsonSlot
	for depth := len(p.stack) - 1; depth >= 0; depth-- {
		frame := p.stack[depth]
		var copyContainer any
		if elements, ok := frame.container.([]any); ok {
			copied := make([]any, len(elements))
			copy(copied, elements)
			copyContainer = copied
		} else {
			members := make(map[string]any, len(frame.container.(map[string]any)))
			for key, value := range frame.container.(map[string]any) {
				members[key] = value
			}
			copyContainer = members
		}
		if depth == len(p.stack)-1 && pending != nil {
			setJSONSlot(copyContainer, pending.slot, pending.value)
		}
		if childSlot != nil {
			setJSONSlot(copyContainer, childSlot, child)
		}
		child = copyContainer
		childSlot = frame.slot
	}
	if childSlot != nil && childSlot.kind == jsonSlotRoot {
		return child, true
	}
	return p.root, p.hasRoot
}

// repointFrameContainer updates the parent slot after a slice growth.
func (p *StreamingJSONParser) repointFrameContainer(frame *jsonFrame, container []any) {
	if frame.slot == nil {
		return
	}
	if frame.slot.kind == jsonSlotRoot {
		p.root = container
		return
	}
	if len(p.stack) < 2 {
		return
	}
	parent := p.stack[len(p.stack)-2]
	switch parentContainer := parent.container.(type) {
	case map[string]any:
		if frame.slot.kind == jsonSlotMember {
			parentContainer[frame.slot.key] = container
		}
	case []any:
		if frame.slot.kind == jsonSlotElement && frame.slot.index < len(parentContainer) {
			parentContainer[frame.slot.index] = container
		}
	}
}

func (p *StreamingJSONParser) topFrame() *jsonFrame {
	if len(p.stack) == 0 {
		return nil
	}
	return p.stack[len(p.stack)-1]
}

func (p *StreamingJSONParser) popFrame() *jsonFrame {
	if len(p.stack) == 0 {
		return nil
	}
	frame := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	return frame
}

func setJSONSlot(container any, slot *jsonSlot, value any) {
	if slot == nil {
		return
	}
	if elements, ok := container.([]any); ok {
		if slot.kind == jsonSlotElement && slot.index < len(elements) {
			elements[slot.index] = value
		}
		return
	}
	if members, ok := container.(map[string]any); ok && slot.kind == jsonSlotMember {
		members[slot.key] = value
	}
}

func isJSONArray(value any) bool {
	_, ok := value.([]any)
	return ok
}

var jsonSimpleEscapes = map[byte]string{
	'"':  `"`,
	'\\': `\`,
	'/':  "/",
	'b':  "\b",
	'f':  "\f",
	'n':  "\n",
	'r':  "\r",
	't':  "\t",
}

func isJSONDelimiter(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', ',', ':', '{', '}', '[', ']', '"':
		return true
	}
	return false
}

func isJSONHexDigit(char byte) bool {
	return (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')
}

// scalarJSONValue is the value of a complete number or literal token; ok is
// false for anything else, which is dropped.
func scalarJSONValue(token string) (any, bool) {
	for _, literal := range jsonLiterals {
		if token == literal.token {
			return literal.value, true
		}
	}
	if isJSONNumber(token) {
		value, err := strconv.ParseFloat(token, 64)
		if err == nil {
			return value, true
		}
	}
	return nil, false
}

// pendingScalarJSONValue is the value an unterminated number or literal token
// shows.
func pendingScalarJSONValue(token string) (any, bool) {
	for _, literal := range jsonLiterals {
		if strings.HasPrefix(literal.token, token) {
			return literal.value, true
		}
	}
	if isJSONNumber(token) {
		value, err := strconv.ParseFloat(token, 64)
		if err == nil {
			return value, true
		}
	}
	// An exponent without digits is ignored: show the mantissa.
	if index := strings.IndexAny(token, "eE"); index >= 0 && isJSONNumber(token[:index]) {
		value, err := strconv.ParseFloat(token[:index], 64)
		if err == nil {
			return value, true
		}
	}
	return nil, false
}

func isJSONNumber(token string) bool {
	if token == "" {
		return false
	}
	index := 0
	if token[index] == '-' {
		index++
		if index == len(token) {
			return false
		}
	}
	if token[index] == '0' {
		index++
	} else if token[index] >= '1' && token[index] <= '9' {
		for index < len(token) && token[index] >= '0' && token[index] <= '9' {
			index++
		}
	} else {
		return false
	}
	if index < len(token) && token[index] == '.' {
		index++
		start := index
		for index < len(token) && token[index] >= '0' && token[index] <= '9' {
			index++
		}
		if index == start {
			return false
		}
	}
	if index < len(token) && (token[index] == 'e' || token[index] == 'E') {
		index++
		if index < len(token) && (token[index] == '+' || token[index] == '-') {
			index++
		}
		start := index
		for index < len(token) && token[index] >= '0' && token[index] <= '9' {
			index++
		}
		if index == start {
			return false
		}
	}
	return index == len(token)
}

// ToolCallArgumentsStream is the arguments of one streaming tool call: the raw
// text, for the one full parse when the call ends, and a StreamingJSONParser
// for the partial arguments in between (pi: ToolCallArgumentsStream).
type ToolCallArgumentsStream struct {
	text        string
	parser      *StreamingJSONParser
	snapshot    any
	hasSnapshot bool
	arguments   map[string]any
}

// NewToolCallArgumentsStream returns an empty stream.
func NewToolCallArgumentsStream() *ToolCallArgumentsStream {
	return &ToolCallArgumentsStream{
		parser:    NewStreamingJSONParser(),
		arguments: map[string]any{},
	}
}

// Text is the raw argument text received so far.
func (s *ToolCallArgumentsStream) Text() string { return s.text }

// Append appends a piece of argument text; it returns the partial arguments, a
// new object whenever they changed.
func (s *ToolCallArgumentsStream) Append(delta string) map[string]any {
	s.text += delta
	s.parser.Append(delta)
	return s.partial()
}

// Replace takes the provider's complete argument text. When it continues the
// text so far, it returns the missing piece as delta; otherwise it starts over
// with text. Either way it returns the partial arguments of text.
func (s *ToolCallArgumentsStream) Replace(text string) (delta string, hasDelta bool, arguments map[string]any) {
	if strings.HasPrefix(text, s.text) {
		delta = text[len(s.text):]
		if delta == "" {
			return "", false, s.partial()
		}
		return delta, true, s.Append(delta)
	}
	s.text = ""
	s.parser = NewStreamingJSONParser()
	s.snapshot = nil
	s.hasSnapshot = false
	s.arguments = map[string]any{}
	return "", false, s.Append(text)
}

// Finish returns the final arguments: one full parse of the complete text,
// which reports malformed arguments (pi: finish).
func (s *ToolCallArgumentsStream) Finish() (map[string]any, string) {
	return ParseToolCallArguments(s.text)
}

func (s *ToolCallArgumentsStream) partial() map[string]any {
	snapshot, has, fresh := s.parser.Snapshot()
	if !fresh {
		if s.arguments == nil {
			s.arguments = map[string]any{}
		}
		return s.arguments
	}
	s.snapshot = snapshot
	s.hasSnapshot = has
	// Tool arguments are an object; any other partial value shows as no
	// arguments yet.
	if members, ok := snapshot.(map[string]any); ok && has {
		s.arguments = members
	} else {
		s.arguments = map[string]any{}
	}
	return s.arguments
}
