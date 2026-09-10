// pi: utils/json-parse.ts.
package utils

import (
	"encoding/json"
	"errors"
	"strings"
)

// repairJson repairs malformed JSON string literals by escaping raw control
// characters inside strings and doubling backslashes before invalid escape
// characters (pi: repairJson).
func RepairJSON(json string) string {
	var repaired strings.Builder
	inString := false
	for index := 0; index < len(json); index++ {
		char := json[index]
		if !inString {
			repaired.WriteByte(char)
			if char == '"' {
				inString = true
			}
			continue
		}
		if char == '"' {
			repaired.WriteByte(char)
			inString = false
			continue
		}
		if char == '\\' {
			if index+1 >= len(json) {
				repaired.WriteString("\\\\")
				continue
			}
			nextChar := json[index+1]
			if nextChar == 'u' {
				if index+6 <= len(json) && isHexDigits(json[index+2:index+6]) {
					repaired.WriteString(`\u`)
					repaired.WriteString(json[index+2 : index+6])
					index += 5
					continue
				}
			}
			if isValidJSONEscape(nextChar) {
				repaired.WriteByte('\\')
				repaired.WriteByte(nextChar)
				index++
				continue
			}
			repaired.WriteString("\\\\")
			continue
		}
		if isControlCharacter(char) {
			repaired.WriteString(escapeControlCharacter(char))
		} else {
			repaired.WriteByte(char)
		}
	}
	return repaired.String()
}

func isHexDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func isValidJSONEscape(c byte) bool {
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
		return true
	}
	return false
}

func isControlCharacter(c byte) bool { return c >= 0x00 && c <= 0x1f }

func escapeControlCharacter(c byte) string {
	switch c {
	case '\b':
		return "\\b"
	case '\f':
		return "\\f"
	case '\n':
		return "\\n"
	case '\r':
		return "\\r"
	case '\t':
		return "\\t"
	default:
		return `\u00` + hexByte(c)
	}
}

func hexByte(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&0x0f]})
}

// ParseJSONWithRepair parses JSON, repairing string literals on first failure
// (pi: parseJsonWithRepair).
func ParseJSONWithRepair(input string) (any, error) {
	var value any
	if err := json.Unmarshal([]byte(input), &value); err == nil {
		return value, nil
	}
	repaired := RepairJSON(input)
	if repaired != input {
		if err := json.Unmarshal([]byte(repaired), &value); err == nil {
			return value, nil
		}
	}
	var value2 any
	if err := json.Unmarshal([]byte(input), &value2); err == nil {
		return value2, nil
	}
	return nil, &malformedJSONError{msg: "cannot parse json: " + input}
}

// ParseStreamingJSON attempts to parse potentially incomplete JSON during
// streaming; it always returns a valid object, even when the JSON is
// incomplete (pi: parseStreamingJson, whose generic T is instantiated with
// Record<string, unknown> by every caller). An empty or blank input yields an
// empty map; an unparseable input also yields an empty map.
func ParseStreamingJSON(partialJSON *string) map[string]any {
	empty := map[string]any{}
	if partialJSON == nil || strings.TrimSpace(*partialJSON) == "" {
		return empty
	}
	if value, err := ParseJSONWithRepair(*partialJSON); err == nil {
		if parsed, ok := value.(map[string]any); ok {
			return parsed
		}
		return empty
	}
	if value, err := ParsePartialJSON(*partialJSON); err == nil {
		if parsed, ok := value.(map[string]any); ok {
			return parsed
		}
		return empty
	}
	if value, err := ParsePartialJSON(RepairJSON(*partialJSON)); err == nil {
		if parsed, ok := value.(map[string]any); ok {
			return parsed
		}
		return empty
	}
	return empty
}

type partialJSONError struct{ msg string }

func (e *partialJSONError) Error() string { return e.msg }

type malformedJSONError struct{ msg string }

func (e *malformedJSONError) Error() string { return e.msg }

// ParsePartialJSON is a port of the partial-json package's parseJSON with
// Allow.ALL (pi: partial-json's parse, used by parseStreamingJson). It parses
// incomplete JSON, returning what it can and swallowing PartialJSON errors.
// Malformed input yields a malformedJSONError.
func ParsePartialJSON(jsonString string) (any, error) {
	if strings.TrimSpace(jsonString) == "" {
		return nil, errors.New("empty")
	}
	p := &partialParser{s: strings.TrimSpace(jsonString)}
	value, err := p.parseAny()
	if err != nil {
		return nil, err
	}
	return value, nil
}

const (
	partialAllowSTR = 1 << iota
	partialAllowNUM
	partialAllowARR
	partialAllowOBJ
	partialAllowNULL
	partialAllowBOOL
	partialAllowNAN
	partialAllowINFINITY
	partialAllowInfinity
)

const partialAllowALL = partialAllowSTR | partialAllowNUM | partialAllowARR | partialAllowOBJ |
	partialAllowNULL | partialAllowBOOL | partialAllowNAN | partialAllowINFINITY | partialAllowInfinity

type partialParser struct {
	s     string
	index int
}

func (p *partialParser) markPartialJSON(msg string) error {
	return &partialJSONError{msg: msg + " at position " + itoaInt(p.index)}
}

func (p *partialParser) markMalformed(msg string) error {
	return &malformedJSONError{msg: msg + " at position " + itoaInt(p.index)}
}

func (p *partialParser) skipBlank() {
	for p.index < len(p.s) && strings.ContainsRune(" \n\r\t", rune(p.s[p.index])) {
		p.index++
	}
}

func (p *partialParser) at(i int) byte {
	if i < len(p.s) {
		return p.s[i]
	}
	return 0
}

func (p *partialParser) substring(start, end int) string {
	if start < 0 {
		start = 0
	}
	if end < start {
		return ""
	}
	if end > len(p.s) {
		end = len(p.s)
	}
	return p.s[start:end]
}

func (p *partialParser) parseAny() (any, error) {
	p.skipBlank()
	if p.index >= len(p.s) {
		return nil, p.markPartialJSON("Unexpected end of input")
	}
	c := p.s[p.index]
	if c == '"' {
		return p.parseStr()
	}
	if c == '{' {
		return p.parseObj()
	}
	if c == '[' {
		return p.parseArr()
	}
	if p.substring(p.index, p.index+4) == "null" ||
		(partialAllowNULL&partialAllowALL != 0 && len(p.s)-p.index < 4 && strings.HasPrefix("null", p.substring(p.index, len(p.s)))) {
		p.index += 4
		return nil, nil
	}
	if p.substring(p.index, p.index+4) == "true" ||
		(partialAllowBOOL&partialAllowALL != 0 && len(p.s)-p.index < 4 && strings.HasPrefix("true", p.substring(p.index, len(p.s)))) {
		p.index += 4
		return true, nil
	}
	if p.substring(p.index, p.index+5) == "false" ||
		(partialAllowBOOL&partialAllowALL != 0 && len(p.s)-p.index < 5 && strings.HasPrefix("false", p.substring(p.index, len(p.s)))) {
		p.index += 5
		return false, nil
	}
	return p.parseNum()
}

func (p *partialParser) parseStr() (any, error) {
	start := p.index
	escape := false
	p.index++ // skip initial quote
	for p.index < len(p.s) && (p.s[p.index] != '"' || (escape && p.at(p.index-1) == '\\')) {
		if p.s[p.index] == '\\' {
			escape = !escape
		} else {
			escape = false
		}
		p.index++
	}
	if p.at(p.index) == '"' {
		p.index++
		end := p.index
		if escape {
			end--
		}
		return parseJSONLiteral(p.substring(start, end))
	}
	if partialAllowSTR&partialAllowALL != 0 {
		end := p.index
		if escape {
			end--
		}
		literal := p.substring(start, end) + `"`
		if v, err := parseJSONLiteral(literal); err == nil {
			return v, nil
		}
		// pi scans the whole input for the last backslash (jsonString.lastIndexOf).
		lastBackslash := strings.LastIndex(p.s, "\\")
		if lastBackslash >= 0 {
			if v, err := parseJSONLiteral(p.substring(start, lastBackslash) + `"`); err == nil {
				return v, nil
			}
		}
		return nil, p.markMalformed("Invalid escape sequence")
	}
	return nil, p.markPartialJSON("Unterminated string literal")
}

func (p *partialParser) parseObj() (any, error) {
	p.index++ // skip initial brace
	p.skipBlank()
	obj := map[string]any{}
	for p.at(p.index) != '}' {
		p.skipBlank()
		if p.index >= len(p.s) && partialAllowOBJ&partialAllowALL != 0 {
			return obj, nil
		}
		key, err := p.parseStr()
		if err != nil {
			if partialAllowOBJ&partialAllowALL != 0 {
				return obj, nil
			}
			return nil, err
		}
		p.skipBlank()
		p.index++ // skip colon
		value, err := p.parseAny()
		if err != nil {
			if partialAllowOBJ&partialAllowALL != 0 {
				return obj, nil
			}
			return nil, err
		}
		obj[key.(string)] = value
		p.skipBlank()
		if p.at(p.index) == ',' {
			p.index++ // skip comma
		}
	}
	p.index++ // skip final brace
	return obj, nil
}

func (p *partialParser) parseArr() (any, error) {
	p.index++ // skip initial bracket
	arr := []any{}
	for p.at(p.index) != ']' {
		if p.index >= len(p.s) {
			if partialAllowARR&partialAllowALL != 0 {
				return arr, nil
			}
			return nil, p.markPartialJSON("Expected ']' at end of array")
		}
		value, err := p.parseAny()
		if err != nil {
			if partialAllowARR&partialAllowALL != 0 {
				return arr, nil
			}
			return nil, err
		}
		arr = append(arr, value)
		p.skipBlank()
		if p.at(p.index) == ',' {
			p.index++ // skip comma
		}
	}
	p.index++ // skip final bracket
	return arr, nil
}

func (p *partialParser) parseNum() (any, error) {
	if p.index == 0 {
		if p.s == "-" {
			return nil, p.markMalformed("Not sure what '-' is")
		}
		if v, err := parseJSONNumber(p.s); err == nil {
			return v, nil
		}
		if partialAllowNUM&partialAllowALL != 0 {
			if v, err := parseJSONNumber(p.substring(0, strings.LastIndex(p.s, "e"))); err == nil {
				return v, nil
			}
		}
		return nil, p.markMalformed("cannot parse number")
	}
	start := p.index
	if p.at(p.index) == '-' {
		p.index++
	}
	for p.index < len(p.s) && !strings.ContainsRune(",]}", rune(p.s[p.index])) {
		p.index++
	}
	token := p.substring(start, p.index)
	if v, err := parseJSONNumber(token); err == nil {
		return v, nil
	}
	if token == "-" {
		return nil, p.markPartialJSON("Not sure what '-' is")
	}
	if v, err := parseJSONNumber(p.substring(start, strings.LastIndex(token, "e"))); err == nil {
		return v, nil
	}
	return nil, p.markMalformed("cannot parse number")
}

func parseJSONLiteral(literal string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(literal), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func parseJSONNumber(literal string) (any, error) {
	if literal == "" {
		return nil, errors.New("empty number")
	}
	var v float64
	if err := json.Unmarshal([]byte(literal), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
