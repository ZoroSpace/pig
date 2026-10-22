// tui/autocomplete.go — ported from pi: packages/tui/src/autocomplete.ts
//
// Declared deviations:
//   - GetSuggestions/getArgumentCompletions return Promise in TS → synchronous
//     Go signatures (repo §3.4 rule); cancellation travels via the ctx
//     parameter (pi: options.signal AbortSignal → context rule).
//   - walkDirectoryWithFd's promise + abort listener → exec.CommandContext
//     (SIGKILL on ctx cancel, matching pi's child.kill("SIGKILL")); the
//     resolved-once finish() latch collapses into plain returns.
//   - `Awaitable<T>` argument completions → synchronous calls (the editor
//     request machinery serializes them off the input path, mirroring the
//     microtask hop in TS).
//   - readdirSync order (OS order) → os.ReadDir (sorted); harmless because
//     suggestions are re-sorted (directories first, then label) before
//     returning.
//   - label localeCompare → byte-order strings.Compare (pi sorts ASCII
//     filenames, both orders agree — repo convention, see harness batch).
//   - Path math uses the POSIX `path` package for both display strings and FS
//     access (pi imports the posix `path` module even for FS ops); Windows
//     backslash separators are display-normalized only, same as pi.
//   - pi v0.86.0's CJK-aware trigger/debounce/boundary regexes use lookahead
//     and tempered-dot constructs with no RE2 equivalent → hand-rolled code
//     point matching (autocompleteContextPattern in editor.go, separator
//     helpers in utils.go); JS `\s` semantics covered by isJsSpaceRune.

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// PATH_DELIMITERS (pi: const Set).
var pathDelimiters = map[byte]bool{' ': true, '\t': true, '"': true, '\'': true, '=': true}

func toDisplayPath(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

func escapeRegex(value string) string {
	parts := strings.Split(value, "")
	for i, part := range parts {
		switch part {
		case ".", "*", "+", "?", "^", "$", "{", "}", "(", ")", "|", "[", "]", "\\":
			parts[i] = "\\" + part
		}
	}
	return strings.Join(parts, "")
}

func buildFdPathQuery(query string) string {
	normalized := toDisplayPath(query)
	if !strings.Contains(normalized, "/") {
		return escapeRegex(normalized)
	}

	hasTrailingSeparator := strings.HasSuffix(normalized, "/")
	trimmed := strings.Trim(normalized, "/")
	if trimmed == "" {
		return normalized
	}

	separatorPattern := "[\\\\/]"
	var segments []string
	for _, segment := range strings.Split(trimmed, "/") {
		if segment != "" {
			segments = append(segments, escapeRegex(segment))
		}
	}
	pattern := strings.Join(segments, separatorPattern)
	if hasTrailingSeparator {
		pattern += separatorPattern
	}
	return pattern
}

// pathWrappers maps opening wrappers that may precede a path in prose to
// their closing counterpart (pi: PATH_WRAPPERS, v0.99.1).
var pathWrappers = map[rune]rune{
	'(': ')',
	'[': ']',
	'{': '}',
	'<': '>',
	'`': '`',
}

// stripLeadingWrappers strips opening wrappers before a path, e.g. "(~/Dev" →
// "~/Dev" or "`src/ma" → "src/ma". A wrapper is kept when the token also
// contains its closer, e.g. "app/[slug]/pa" or "(group)/pa" (pi:
// stripLeadingWrappers, v0.99.1).
func stripLeadingWrappers(token string) string {
	for len(token) > 0 {
		first, size := utf8.DecodeRuneInString(token)
		closer, ok := pathWrappers[first]
		if !ok || strings.ContainsRune(token[size:], closer) {
			break
		}
		token = token[size:]
	}
	return token
}

func findLastDelimiter(text string) int {
	// pi v0.86.0 iterates code points and also accepts autocomplete separator
	// characters (whitespace or CJK punctuation) as delimiters; indices are
	// UTF-16 code units in TS → BYTE offsets of the delimiter rune here.
	// Consumers must skip the full rune (see delimiterRuneEnd).
	lastDelimiter := -1
	index := 0
	for _, character := range text {
		if (character < utf8.RuneSelf && pathDelimiters[byte(character)]) || autocompleteSeparatorRune(character) {
			lastDelimiter = index
		}
		index += utf8.RuneLen(character)
	}
	return lastDelimiter
}

// delimiterRuneEnd returns the byte offset just past the rune starting at
// index (pi: lastDelimiterIndex + 1 — UTF-16 delimiters are single units;
// multi-byte runes need the full rune width here).
func delimiterRuneEnd(text string, index int) int {
	if index < 0 || index >= len(text) {
		return index
	}
	_, size := utf8.DecodeRuneInString(text[index:])
	return index + size
}

func findUnclosedQuoteStart(text string) *int {
	inQuotes := false
	quoteStart := -1

	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteStart = i
			}
		}
	}

	if inQuotes {
		return &quoteStart
	}
	return nil
}

func isTokenStart(text string, index int) bool {
	// pi v0.99.1: skip opening wrappers before the start, e.g. "(@src" or
	// "`@src" still start a token (pi: the while loop over PATH_WRAPPERS).
	start := index
	for start > 0 {
		previous, size := utf8.DecodeLastRuneInString(text[:start])
		if _, ok := pathWrappers[previous]; !ok {
			break
		}
		start -= size
	}
	// pi v0.86.0: PATH_DELIMITERS.has(text[index - 1] ?? "") ||
	// tokenStartRegex.test(text.slice(0, index)) — the second check is true
	// for an empty prefix (index == 0) or when the code point before the
	// index is a whitespace/CJK-punctuation separator.
	if start == 0 {
		return true
	}
	if pathDelimiters[text[start-1]] {
		return true
	}
	return isAutocompleteSeparatorAtEnd(text[:start])
}

func extractQuotedPrefix(text string) *string {
	quoteStartPtr := findUnclosedQuoteStart(text)
	if quoteStartPtr == nil {
		return nil
	}
	quoteStart := *quoteStartPtr

	if quoteStart > 0 && text[quoteStart-1] == '@' {
		if !isTokenStart(text, quoteStart-1) {
			return nil
		}
		out := text[quoteStart-1:]
		return &out
	}

	if !isTokenStart(text, quoteStart) {
		return nil
	}

	out := text[quoteStart:]
	return &out
}

// parsedPathPrefix is the result of parsePathPrefix (pi: inline object).
type parsedPathPrefix struct {
	rawPrefix      string
	isAtPrefix     bool
	isQuotedPrefix bool
}

func parsePathPrefix(prefix string) parsedPathPrefix {
	if strings.HasPrefix(prefix, "@\"") {
		return parsedPathPrefix{rawPrefix: prefix[2:], isAtPrefix: true, isQuotedPrefix: true}
	}
	if strings.HasPrefix(prefix, "\"") {
		return parsedPathPrefix{rawPrefix: prefix[1:], isAtPrefix: false, isQuotedPrefix: true}
	}
	if strings.HasPrefix(prefix, "@") {
		return parsedPathPrefix{rawPrefix: prefix[1:], isAtPrefix: true, isQuotedPrefix: false}
	}
	return parsedPathPrefix{rawPrefix: prefix, isAtPrefix: false, isQuotedPrefix: false}
}

// buildCompletionValueOptions mirrors the inline options object of pi's
// buildCompletionValue.
type buildCompletionValueOptions struct {
	isDirectory    bool
	isAtPrefix     bool
	isQuotedPrefix bool
}

func buildCompletionValue(pathValue string, options buildCompletionValueOptions) string {
	// needsQuotes: quoted prefix, or the path contains whitespace or CJK
	// punctuation (pi v0.86.0: autocompleteSeparatorRegex.test(path)).
	needsQuotes := options.isQuotedPrefix || containsAutocompleteSeparator(pathValue)
	prefix := ""
	if options.isAtPrefix {
		prefix = "@"
	}

	if !needsQuotes {
		return fmt.Sprintf("%s%s", prefix, pathValue)
	}

	openQuote := fmt.Sprintf("%s\"", prefix)
	closeQuote := "\""
	return fmt.Sprintf("%s%s%s", openQuote, pathValue, closeQuote)
}

// fdEntry is one entry produced by walkDirectoryWithFd (pi: inline
// { path, isDirectory } objects).
type fdEntry struct {
	path        string
	isDirectory bool
}

// walkDirectoryWithFd walks the directory tree with fd (fast, respects
// .gitignore). maxDepth optionally limits recursion depth (pi: maxDepth?,
// --max-depth).
func walkDirectoryWithFd(ctx context.Context, baseDir string, fdPath string, query string, maxResults int, maxDepth ...int) []fdEntry {
	args := []string{
		"--base-directory", baseDir,
		"--max-results", fmt.Sprintf("%d", maxResults),
		"--type", "f",
		"--type", "d",
		"--follow",
		"--hidden",
		"--exclude", ".git",
		"--exclude", ".git/*",
		"--exclude", ".git/**",
	}

	if len(maxDepth) > 0 {
		args = append(args, "--max-depth", fmt.Sprintf("%d", maxDepth[0]))
	}

	if strings.Contains(toDisplayPath(query), "/") {
		args = append(args, "--full-path")
	}

	if query != "" {
		args = append(args, buildFdPathQuery(query))
	}

	if ctx.Err() != nil {
		return []fdEntry{}
	}

	cmd := exec.CommandContext(ctx, fdPath, args...)
	stdout, err := cmd.Output()
	if err != nil {
		// spawn error, non-zero exit (pi: code !== 0), or killed on abort —
		// pi resolves [] in every failure branch.
		return []fdEntry{}
	}
	if ctx.Err() != nil || len(stdout) == 0 {
		return []fdEntry{}
	}

	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	results := []fdEntry{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		displayLine := toDisplayPath(line)
		hasTrailingSeparator := strings.HasSuffix(displayLine, "/")
		normalizedPath := displayLine
		if hasTrailingSeparator {
			normalizedPath = displayLine[:len(displayLine)-1]
		}
		if normalizedPath == ".git" || strings.HasPrefix(normalizedPath, ".git/") || strings.Contains(normalizedPath, "/.git/") {
			continue
		}

		results = append(results, fdEntry{path: displayLine, isDirectory: hasTrailingSeparator})
	}

	return results
}

// countPathSegments counts non-empty slash-separated segments (pi:
// path.split("/").filter(Boolean).length). Note: pi's final tiebreak is
// localeCompare; pig uses bytewise < (ordering stable within the ASCII
// filename surface).
func countPathSegments(path string) int {
	count := 0
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			count++
		}
	}
	return count
}

// AutocompleteItem is one suggestion entry (pi: AutocompleteItem).
type AutocompleteItem struct {
	Value string
	Label string
	// Description is optional (pi: description?); "" means absent. TS falsy
	// checks treat "" the same as undefined.
	Description string
}

// isSlashCommandOrItem marks AutocompleteItem as a member of the
// SlashCommandOrItem union (pi: SlashCommand | AutocompleteItem).
func (AutocompleteItem) isSlashCommandOrItem() {}

// SlashCommand describes a slash command with optional argument completions
// (pi: SlashCommand).
type SlashCommand struct {
	Name         string
	Description  string
	ArgumentHint string
	// GetArgumentCompletions returns argument completions for this command;
	// nil means no argument completion is available (pi: Awaitable<
	// AutocompleteItem[] | null> → synchronous nil, see file header).
	GetArgumentCompletions func(argumentPrefix string) []AutocompleteItem
}

// isSlashCommandOrItem marks SlashCommand as a member of the
// SlashCommandOrItem union (pi: SlashCommand | AutocompleteItem).
func (SlashCommand) isSlashCommandOrItem() {}

// SlashCommandOrItem is the union of slash commands and plain items accepted
// by CombinedAutocompleteProvider (pi: SlashCommand | AutocompleteItem).
// Discrimination follows pi's `"name" in cmd` probe via a type switch.
type SlashCommandOrItem interface {
	isSlashCommandOrItem()
}

// AutocompleteSuggestions is a suggestion result (pi: AutocompleteSuggestions).
type AutocompleteSuggestions struct {
	Items []AutocompleteItem
	// Prefix is what we're matching against (e.g. "/" or "src/").
	Prefix string
}

// AutocompleteGetSuggestionsOptions carries getSuggestions options; the abort
// signal moved to the ctx parameter (repo AbortSignal → context rule).
type AutocompleteGetSuggestionsOptions struct {
	Force bool
}

// AutocompleteApplyResult is the result of applyCompletion (pi: inline
// { lines, cursorLine, cursorCol }).
type AutocompleteApplyResult struct {
	Lines      []string
	CursorLine int
	CursorCol  int
}

// AutocompleteProvider provides autocomplete suggestions for the editor
// (pi: AutocompleteProvider).
type AutocompleteProvider interface {
	// GetSuggestions returns suggestions for the current text/cursor
	// position, or nil if none are available. lines/cursor* arrive as a
	// stable snapshot (pi reads live state on the event loop; see editor.go
	// file header for the goroutine adaptation).
	GetSuggestions(ctx context.Context, lines []string, cursorLine int, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions

	// ApplyCompletion applies the selected item, returning the new text and
	// cursor position.
	ApplyCompletion(lines []string, cursorLine int, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult
}

// FileCompletionTriggerer is the optional capability for providers that
// control explicit Tab file-completion triggering (pi: optional
// shouldTriggerFileCompletion member → capability interface, repo rule).
type FileCompletionTriggerer interface {
	// ShouldTriggerFileCompletion reports whether file completion should
	// trigger (called on Tab key).
	ShouldTriggerFileCompletion(lines []string, cursorLine int, cursorCol int) bool
}

// TriggerCharactersProvider is the optional capability exposing the natural
// trigger characters (pi: optional triggerCharacters member → capability
// interface, repo rule).
type TriggerCharactersProvider interface {
	// TriggerCharacters returns the characters that should naturally trigger
	// this provider at token boundaries.
	TriggerCharacters() []string
}

// CombinedAutocompleteProvider handles both slash commands and file paths
// (pi: CombinedAutocompleteProvider).
type CombinedAutocompleteProvider struct {
	commands []SlashCommandOrItem
	basePath string
	// fdPath is "" when fd is unavailable (pi: fdPath: string | null).
	fdPath string
	// triggerCharacters is pi's optional triggerCharacters member; set by
	// interactive-mode's wrapper-chain merge (T5b-2).
	triggerCharacters []string
}

// TriggerCharacters implements TriggerCharactersProvider.
func (p *CombinedAutocompleteProvider) TriggerCharacters() []string { return p.triggerCharacters }

// SetTriggerCharacters installs the merged trigger characters (pig: pi
// assigns the property directly; capability setter per repo rule).
func (p *CombinedAutocompleteProvider) SetTriggerCharacters(chars []string) {
	p.triggerCharacters = chars
}

// TriggerCharactersSetter is the writable half of the trigger-character
// capability (pig: pi assigns provider.triggerCharacters directly).
type TriggerCharactersSetter interface {
	SetTriggerCharacters(chars []string)
}

// NewCombinedAutocompleteProvider creates the combined provider (pi:
// constructor; commands defaults to an empty slice, fdPath "" = null).
func NewCombinedAutocompleteProvider(commands []SlashCommandOrItem, basePath string, fdPath string) *CombinedAutocompleteProvider {
	if commands == nil {
		commands = []SlashCommandOrItem{}
	}
	return &CombinedAutocompleteProvider{commands: commands, basePath: basePath, fdPath: fdPath}
}

// autocompleteCommandName extracts the command name from a union member
// (pi: `const name = "name" in cmd ? cmd.name : cmd.value`).
func autocompleteCommandName(cmd SlashCommandOrItem) string {
	switch c := cmd.(type) {
	case SlashCommand:
		return c.Name
	case AutocompleteItem:
		return c.Value
	}
	return ""
}

// GetSuggestions implements AutocompleteProvider (pi: getSuggestions).
func (p *CombinedAutocompleteProvider) GetSuggestions(ctx context.Context, lines []string, cursorLine int, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
	currentLine := ""
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	if cursorCol > len(currentLine) {
		cursorCol = len(currentLine)
	}
	textBeforeCursor := currentLine[:cursorCol]

	if atPrefix := p.extractAtPrefix(textBeforeCursor); atPrefix != nil {
		parsed := parsePathPrefix(*atPrefix)
		suggestions := p.getFuzzyFileSuggestions(ctx, parsed.rawPrefix, parsed.isQuotedPrefix)
		if len(suggestions) == 0 {
			return nil
		}

		return &AutocompleteSuggestions{Items: suggestions, Prefix: *atPrefix}
	}

	// pi #10218: complete slash commands after leading whitespace; the
	// prefix is computed from the trimmed text so accepting a completion
	// replaces the command text only, not the whitespace before it.
	commandText := strings.TrimLeftFunc(textBeforeCursor, isJsSpaceRune)
	if !options.Force && strings.HasPrefix(commandText, "/") {
		spaceIndex := strings.Index(commandText, " ")

		if spaceIndex == -1 {
			prefix := commandText[1:]
			commandItems := make([]AutocompleteItem, 0, len(p.commands))
			for _, cmd := range p.commands {
				name := autocompleteCommandName(cmd)
				hint := ""
				desc := ""
				if sc, ok := cmd.(SlashCommand); ok {
					if sc.ArgumentHint != "" {
						hint = sc.ArgumentHint
					}
					desc = sc.Description
				} else if item, ok := cmd.(AutocompleteItem); ok {
					desc = item.Description
				}
				fullDesc := ""
				if hint != "" {
					if desc != "" {
						fullDesc = fmt.Sprintf("%s — %s", hint, desc)
					} else {
						fullDesc = hint
					}
				} else {
					fullDesc = desc
				}
				entry := AutocompleteItem{Value: name, Label: name}
				if fullDesc != "" {
					entry.Description = fullDesc
				}
				commandItems = append(commandItems, entry)
			}

			// pi v0.99.1 (#9944): skill: commands match on the bare skill
			// name unless the query explicitly starts with "skill:"; matches
			// that only match the full "skill:..." name follow the bare-name
			// matches instead of being dropped when the query does not start
			// with "skill:".
			bareNameMatches := FuzzyFilter(commandItems, prefix, func(item AutocompleteItem) string {
				if strings.HasPrefix(item.Value, "skill:") {
					return item.Value[len("skill:"):]
				}
				return item.Value
			})
			bareNameMatchSet := make(map[AutocompleteItem]bool, len(bareNameMatches))
			for _, item := range bareNameMatches {
				bareNameMatchSet[item] = true
			}
			fullNameOnly := make([]AutocompleteItem, 0)
			for _, item := range commandItems {
				if !strings.HasPrefix(item.Value, "skill:") || bareNameMatchSet[item] {
					continue
				}
				fullNameOnly = append(fullNameOnly, item)
			}
			fullNameOnlyMatches := FuzzyFilter(fullNameOnly, prefix, func(item AutocompleteItem) string {
				return item.Value
			})
			filtered := append(append([]AutocompleteItem{}, bareNameMatches...), fullNameOnlyMatches...)
			mapped := make([]AutocompleteItem, 0, len(filtered))
			for _, item := range filtered {
				mapped = append(mapped, AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description})
			}

			if len(mapped) == 0 {
				return nil
			}

			return &AutocompleteSuggestions{Items: mapped, Prefix: commandText}
		}

		commandName := commandText[1:spaceIndex]
		argumentText := commandText[spaceIndex+1:]

		var command *SlashCommand
		for _, cmd := range p.commands {
			if sc, ok := cmd.(SlashCommand); ok && sc.Name == commandName {
				command = &sc
				break
			}
		}
		if command == nil || command.GetArgumentCompletions == nil {
			return nil
		}

		argumentSuggestions := command.GetArgumentCompletions(argumentText)
		if len(argumentSuggestions) == 0 {
			return nil
		}

		return &AutocompleteSuggestions{Items: argumentSuggestions, Prefix: argumentText}
	}

	pathMatch := p.extractPathPrefix(textBeforeCursor, options.Force)
	if pathMatch == nil {
		return nil
	}

	suggestions := p.getFileSuggestions(*pathMatch)
	if len(suggestions) == 0 {
		return nil
	}

	return &AutocompleteSuggestions{Items: suggestions, Prefix: *pathMatch}
}

// ApplyCompletion implements AutocompleteProvider (pi: applyCompletion).
func (p *CombinedAutocompleteProvider) ApplyCompletion(lines []string, cursorLine int, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult {
	currentLine := ""
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	if cursorCol > len(currentLine) {
		cursorCol = len(currentLine)
	}
	beforePrefix := currentLine[:cursorCol-len(prefix)]
	afterCursor := currentLine[cursorCol:]
	isQuotedPrefix := strings.HasPrefix(prefix, "\"") || strings.HasPrefix(prefix, "@\"")
	hasLeadingQuoteAfterCursor := strings.HasPrefix(afterCursor, "\"")
	hasTrailingQuoteInItem := strings.HasSuffix(item.Value, "\"")
	adjustedAfterCursor := afterCursor
	if isQuotedPrefix && hasTrailingQuoteInItem && hasLeadingQuoteAfterCursor {
		adjustedAfterCursor = afterCursor[1:]
	}

	// Check if we're completing a slash command (prefix starts with "/" but
	// NOT a file path). Slash commands are at the start of the line and don't
	// contain path separators after the first /.
	isSlashCommand := strings.HasPrefix(prefix, "/") && strings.TrimSpace(beforePrefix) == "" && !strings.Contains(prefix[1:], "/")
	if isSlashCommand {
		// This is a command name completion
		newLine := fmt.Sprintf("%s/%s %s", beforePrefix, item.Value, adjustedAfterCursor)
		newLines := make([]string, len(lines))
		copy(newLines, lines)
		newLines[cursorLine] = newLine

		return AutocompleteApplyResult{
			Lines:      newLines,
			CursorLine: cursorLine,
			// +2 for "/" and space
			CursorCol: len(beforePrefix) + len(item.Value) + 2,
		}
	}

	// Check if we're completing a file attachment (prefix starts with "@")
	if strings.HasPrefix(prefix, "@") {
		// This is a file attachment completion
		// Don't add space after directories so user can continue autocompleting
		isDirectory := strings.HasSuffix(item.Label, "/")
		suffix := " "
		if isDirectory {
			suffix = ""
		}
		newLine := beforePrefix + item.Value + suffix + adjustedAfterCursor
		newLines := make([]string, len(lines))
		copy(newLines, lines)
		newLines[cursorLine] = newLine

		hasTrailingQuote := strings.HasSuffix(item.Value, "\"")
		cursorOffset := len(item.Value)
		if isDirectory && hasTrailingQuote {
			cursorOffset = len(item.Value) - 1
		}

		return AutocompleteApplyResult{
			Lines:      newLines,
			CursorLine: cursorLine,
			CursorCol:  len(beforePrefix) + cursorOffset + len(suffix),
		}
	}

	// Check if we're in a slash command context (beforePrefix contains
	// "/command ")
	textBeforeCursor := currentLine[:cursorCol]
	if strings.Contains(textBeforeCursor, "/") && strings.Contains(textBeforeCursor, " ") {
		// This is likely a command argument completion
		newLine := beforePrefix + item.Value + adjustedAfterCursor
		newLines := make([]string, len(lines))
		copy(newLines, lines)
		newLines[cursorLine] = newLine

		isDirectory := strings.HasSuffix(item.Label, "/")
		hasTrailingQuote := strings.HasSuffix(item.Value, "\"")
		cursorOffset := len(item.Value)
		if isDirectory && hasTrailingQuote {
			cursorOffset = len(item.Value) - 1
		}

		return AutocompleteApplyResult{
			Lines:      newLines,
			CursorLine: cursorLine,
			CursorCol:  len(beforePrefix) + cursorOffset,
		}
	}

	// For file paths, complete the path
	newLine := beforePrefix + item.Value + adjustedAfterCursor
	newLines := make([]string, len(lines))
	copy(newLines, lines)
	newLines[cursorLine] = newLine

	isDirectory := strings.HasSuffix(item.Label, "/")
	hasTrailingQuote := strings.HasSuffix(item.Value, "\"")
	cursorOffset := len(item.Value)
	if isDirectory && hasTrailingQuote {
		cursorOffset = len(item.Value) - 1
	}

	return AutocompleteApplyResult{
		Lines:      newLines,
		CursorLine: cursorLine,
		CursorCol:  len(beforePrefix) + cursorOffset,
	}
}

// extractAtPrefix extracts the @ prefix for fuzzy file suggestions (pi:
// private extractAtPrefix). Returns nil when absent.
func (p *CombinedAutocompleteProvider) extractAtPrefix(text string) *string {
	if quotedPrefix := extractQuotedPrefix(text); quotedPrefix != nil && strings.HasPrefix(*quotedPrefix, "@\"") {
		return quotedPrefix
	}

	lastDelimiterIndex := findLastDelimiter(text)
	// pi v0.99.1: strip opening wrappers, e.g. "(@src/foo" → "@src/foo".
	token := text
	if lastDelimiterIndex != -1 {
		token = text[delimiterRuneEnd(text, lastDelimiterIndex):]
	}
	token = stripLeadingWrappers(token)

	if strings.HasPrefix(token, "@") {
		return &token
	}

	return nil
}

// extractPathPrefix extracts a path-like prefix from the text before cursor
// (pi: private extractPathPrefix). Returns nil when absent.
func (p *CombinedAutocompleteProvider) extractPathPrefix(text string, forceExtract bool) *string {
	if quotedPrefix := extractQuotedPrefix(text); quotedPrefix != nil {
		return quotedPrefix
	}

	lastDelimiterIndex := findLastDelimiter(text)
	pathPrefix := text
	if lastDelimiterIndex != -1 {
		pathPrefix = text[delimiterRuneEnd(text, lastDelimiterIndex):]
	}
	// pi v0.99.1: opening wrappers before a path do not block it.
	pathPrefix = stripLeadingWrappers(pathPrefix)

	// For forced extraction (Tab key), always return something
	if forceExtract {
		return &pathPrefix
	}

	// For natural triggers, return if it looks like a path, ends with /,
	// starts with ~/, . Only return empty string if the text looks like it's
	// starting a path context.
	if strings.Contains(pathPrefix, "/") || strings.HasPrefix(pathPrefix, ".") || strings.HasPrefix(pathPrefix, "~/") {
		return &pathPrefix
	}

	// Return an empty prefix after whitespace or CJK punctuation, but not
	// for empty text (pi v0.86.0: tokenStartRegex.test(text)). Empty text
	// should not trigger file suggestions - that's for forced Tab completion
	if pathPrefix == "" && text != "" && isAutocompleteSeparatorAtEnd(text) {
		return &pathPrefix
	}

	return nil
}

// expandHomePath expands home directory (~/) to the actual home path (pi:
// private expandHomePath).
func (p *CombinedAutocompleteProvider) expandHomePath(pathArg string) string {
	if strings.HasPrefix(pathArg, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			// pi's os.homedir() practically never fails; keep the raw path on
			// error (declared).
			return pathArg
		}
		expandedPath := path.Join(home, pathArg[2:])
		// Preserve trailing slash if original path had one
		if strings.HasSuffix(pathArg, "/") && !strings.HasSuffix(expandedPath, "/") {
			return expandedPath + "/"
		}
		return expandedPath
	} else if pathArg == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return pathArg
		}
		return home
	}
	return pathArg
}

// scopedFuzzyQuery is the result of resolveScopedFuzzyQuery (pi: inline
// { baseDir, query, displayBase }).
type scopedFuzzyQuery struct {
	baseDir     string
	query       string
	displayBase string
}

// resolveScopedFuzzyQuery resolves a query like "../outside/a" to a scoped
// base directory (pi: private resolveScopedFuzzyQuery). Returns nil when the
// query has no scoped directory or the directory does not exist.
func (p *CombinedAutocompleteProvider) resolveScopedFuzzyQuery(rawQuery string) *scopedFuzzyQuery {
	normalizedQuery := toDisplayPath(rawQuery)
	slashIndex := strings.LastIndex(normalizedQuery, "/")
	if slashIndex == -1 {
		return nil
	}

	displayBase := normalizedQuery[:slashIndex+1]
	query := normalizedQuery[slashIndex+1:]

	var baseDir string
	if strings.HasPrefix(displayBase, "~/") {
		baseDir = p.expandHomePath(displayBase)
	} else if strings.HasPrefix(displayBase, "/") {
		baseDir = displayBase
	} else {
		baseDir = path.Join(p.basePath, displayBase)
	}

	stat, err := os.Stat(baseDir)
	if err != nil || !stat.IsDir() {
		return nil
	}

	return &scopedFuzzyQuery{baseDir: baseDir, query: query, displayBase: displayBase}
}

// scopedPathForDisplay prefixes a scoped relative path for display (pi:
// private scopedPathForDisplay).
func (p *CombinedAutocompleteProvider) scopedPathForDisplay(displayBase string, relativePath string) string {
	normalizedRelativePath := toDisplayPath(relativePath)
	if displayBase == "/" {
		return "/" + normalizedRelativePath
	}
	return toDisplayPath(displayBase) + normalizedRelativePath
}

// getFileSuggestions returns file/directory suggestions for a path prefix
// (pi: private getFileSuggestions).
func (p *CombinedAutocompleteProvider) getFileSuggestions(prefix string) []AutocompleteItem {
	parsed := parsePathPrefix(prefix)
	rawPrefix := parsed.rawPrefix
	isAtPrefix := parsed.isAtPrefix
	isQuotedPrefix := parsed.isQuotedPrefix
	expandedPrefix := rawPrefix

	// Handle home directory expansion
	if strings.HasPrefix(expandedPrefix, "~") {
		expandedPrefix = p.expandHomePath(expandedPrefix)
	}

	isRootPrefix := rawPrefix == "" ||
		rawPrefix == "./" ||
		rawPrefix == "../" ||
		rawPrefix == "~" ||
		rawPrefix == "~/" ||
		rawPrefix == "/" ||
		(isAtPrefix && rawPrefix == "")

	var searchDir string
	var searchPrefix string
	if isRootPrefix {
		// Complete from specified position
		if strings.HasPrefix(rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = expandedPrefix
		} else {
			searchDir = path.Join(p.basePath, expandedPrefix)
		}
		searchPrefix = ""
	} else if strings.HasSuffix(rawPrefix, "/") {
		// If prefix ends with /, show contents of that directory
		if strings.HasPrefix(rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = expandedPrefix
		} else {
			searchDir = path.Join(p.basePath, expandedPrefix)
		}
		searchPrefix = ""
	} else {
		// Split into directory and file prefix
		dir := path.Dir(expandedPrefix)
		file := path.Base(expandedPrefix)
		if strings.HasPrefix(rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = dir
		} else {
			searchDir = path.Join(p.basePath, dir)
		}
		searchPrefix = file
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		// Directory doesn't exist or not accessible
		return []AutocompleteItem{}
	}
	suggestions := []AutocompleteItem{}

	for _, entry := range entries {
		if !strings.HasPrefix(strings.ToLower(entry.Name()), strings.ToLower(searchPrefix)) {
			continue
		}

		// Check if entry is a directory (or a symlink pointing to a directory)
		isDirectory := entry.IsDir()
		if !isDirectory && entry.Type()&os.ModeSymlink != 0 {
			if stat, err := os.Stat(path.Join(searchDir, entry.Name())); err == nil {
				isDirectory = stat.IsDir()
			}
			// Broken symlink or permission error - treat as file
		}

		var relativePath string
		name := entry.Name()
		displayPrefix := rawPrefix

		if strings.HasSuffix(displayPrefix, "/") {
			// If prefix ends with /, append entry to the prefix
			relativePath = displayPrefix + name
		} else if strings.Contains(displayPrefix, "/") || strings.Contains(displayPrefix, "\\") {
			// Preserve ~/ format for home directory paths
			if strings.HasPrefix(displayPrefix, "~/") {
				homeRelativeDir := displayPrefix[2:] // Remove ~/
				dir := path.Dir(homeRelativeDir)
				if dir == "." {
					relativePath = "~/" + name
				} else {
					relativePath = "~/" + path.Join(dir, name)
				}
			} else if strings.HasPrefix(displayPrefix, "/") {
				// Absolute path - construct properly
				dir := path.Dir(displayPrefix)
				if dir == "/" {
					relativePath = "/" + name
				} else {
					relativePath = fmt.Sprintf("%s/%s", dir, name)
				}
			} else {
				relativePath = path.Join(path.Dir(displayPrefix), name)
				// path.join normalizes away ./ prefix, preserve it
				if strings.HasPrefix(displayPrefix, "./") && !strings.HasPrefix(relativePath, "./") {
					relativePath = "./" + relativePath
				}
			}
		} else {
			// For standalone entries, preserve ~/ if original prefix was ~/
			if strings.HasPrefix(displayPrefix, "~") {
				relativePath = "~/" + name
			} else {
				relativePath = name
			}
		}

		relativePath = toDisplayPath(relativePath)
		pathValue := relativePath
		if isDirectory {
			pathValue = relativePath + "/"
		}
		value := buildCompletionValue(pathValue, buildCompletionValueOptions{
			isDirectory:    isDirectory,
			isAtPrefix:     isAtPrefix,
			isQuotedPrefix: isQuotedPrefix,
		})

		label := name
		if isDirectory {
			label += "/"
		}
		suggestions = append(suggestions, AutocompleteItem{Value: value, Label: label})
	}

	// Sort directories first, then alphabetically (pi v0.86.0: directory
	// detection moved from value to label — quoted directory values end with
	// the closing quote, labels end with "/"; localeCompare → byte order,
	// see file header).
	sort.SliceStable(suggestions, func(a, b int) bool {
		aIsDir := strings.HasSuffix(suggestions[a].Label, "/")
		bIsDir := strings.HasSuffix(suggestions[b].Label, "/")
		if aIsDir != bIsDir {
			return aIsDir
		}
		return strings.Compare(suggestions[a].Label, suggestions[b].Label) < 0
	})

	return suggestions
}

// scoreEntry scores an entry against the query; higher = better match.
// isDirectory adds a bonus to prioritize folders (pi: private scoreEntry).
func (p *CombinedAutocompleteProvider) scoreEntry(filePath string, query string, isDirectory bool) int {
	fileName := path.Base(filePath)
	lowerFileName := strings.ToLower(fileName)
	lowerQuery := strings.ToLower(query)

	score := 0

	// Exact filename match (highest)
	if lowerFileName == lowerQuery {
		score = 100
	} else if strings.HasPrefix(lowerFileName, lowerQuery) {
		// Filename starts with query
		score = 80
	} else if strings.Contains(lowerFileName, lowerQuery) {
		// Substring match in filename
		score = 50
	} else if strings.Contains(strings.ToLower(filePath), lowerQuery) {
		// Substring match in full path
		score = 30
	}

	// Directories get a bonus to appear first
	if isDirectory && score > 0 {
		score += 10
	}

	return score
}

// scoredFdEntry pairs an fd entry with its score (pi: inline map result).
type scoredFdEntry struct {
	entry fdEntry
	score int
}

// getBaseDirSuggestions suggests direct children of the base directory (pi:
// private getBaseDirSuggestions — max-depth 1 walk).
func (p *CombinedAutocompleteProvider) getBaseDirSuggestions(ctx context.Context, baseDir string, query string) []fdEntry {
	if p.fdPath == "" || ctx.Err() != nil {
		return []fdEntry{}
	}
	return walkDirectoryWithFd(ctx, baseDir, p.fdPath, query, 100, 1)
}

// getFuzzyFileSuggestions performs fuzzy file search using fd (fast, respects
// .gitignore) (pi: private getFuzzyFileSuggestions).
func (p *CombinedAutocompleteProvider) getFuzzyFileSuggestions(ctx context.Context, query string, isQuotedPrefix bool) []AutocompleteItem {
	if p.fdPath == "" || ctx.Err() != nil {
		return []AutocompleteItem{}
	}

	scopedQuery := p.resolveScopedFuzzyQuery(query)
	fdBaseDir := p.basePath
	fdQuery := query
	if scopedQuery != nil {
		fdBaseDir = scopedQuery.baseDir
		fdQuery = scopedQuery.query
	}
	// v0.85.1: shallow base-dir suggestions first, then recursive results
	// deduplicated by path.
	baseDirEntries := p.getBaseDirSuggestions(ctx, fdBaseDir, fdQuery)
	recursiveEntries := walkDirectoryWithFd(ctx, fdBaseDir, p.fdPath, fdQuery, 100)
	seenPaths := make(map[string]bool, len(baseDirEntries))
	for _, entry := range baseDirEntries {
		seenPaths[entry.path] = true
	}
	entries := make([]fdEntry, 0, len(baseDirEntries)+len(recursiveEntries))
	entries = append(entries, baseDirEntries...)
	for _, entry := range recursiveEntries {
		if seenPaths[entry.path] {
			continue
		}
		seenPaths[entry.path] = true
		entries = append(entries, entry)
	}
	if ctx.Err() != nil {
		return []AutocompleteItem{}
	}

	scoredEntries := make([]scoredFdEntry, 0, len(entries))
	for _, entry := range entries {
		score := 1
		if fdQuery != "" {
			score = p.scoreEntry(entry.path, fdQuery, entry.isDirectory)
		}
		if score > 0 {
			scoredEntries = append(scoredEntries, scoredFdEntry{entry: entry, score: score})
		}
	}

	sort.SliceStable(scoredEntries, func(a, b int) bool {
		scoreDiff := scoredEntries[a].score - scoredEntries[b].score
		if scoreDiff != 0 {
			return scoreDiff > 0
		}
		// v0.85.1 tiebreakers: shallower paths first, then shorter, then
		// lexicographic.
		aDepth := countPathSegments(toDisplayPath(scoredEntries[a].entry.path))
		bDepth := countPathSegments(toDisplayPath(scoredEntries[b].entry.path))
		if aDepth != bDepth {
			return aDepth < bDepth
		}
		aLen := len(scoredEntries[a].entry.path)
		bLen := len(scoredEntries[b].entry.path)
		if aLen != bLen {
			return aLen < bLen
		}
		return scoredEntries[a].entry.path < scoredEntries[b].entry.path
	})
	if len(scoredEntries) > 20 {
		scoredEntries = scoredEntries[:20]
	}

	suggestions := []AutocompleteItem{}
	for _, scored := range scoredEntries {
		entryPath := scored.entry.path
		pathWithoutSlash := entryPath
		if scored.entry.isDirectory {
			pathWithoutSlash = entryPath[:len(entryPath)-1]
		}
		displayPath := pathWithoutSlash
		if scopedQuery != nil {
			displayPath = p.scopedPathForDisplay(scopedQuery.displayBase, pathWithoutSlash)
		}
		entryName := path.Base(pathWithoutSlash)
		completionPath := displayPath
		if scored.entry.isDirectory {
			completionPath = displayPath + "/"
		}
		value := buildCompletionValue(completionPath, buildCompletionValueOptions{
			isDirectory:    scored.entry.isDirectory,
			isAtPrefix:     true,
			isQuotedPrefix: isQuotedPrefix,
		})

		label := entryName
		if scored.entry.isDirectory {
			label += "/"
		}
		suggestions = append(suggestions, AutocompleteItem{
			Value:       value,
			Label:       label,
			Description: displayPath,
		})
	}

	return suggestions
}

// ShouldTriggerFileCompletion implements FileCompletionTriggerer (pi:
// shouldTriggerFileCompletion — called on Tab key).
func (p *CombinedAutocompleteProvider) ShouldTriggerFileCompletion(lines []string, cursorLine int, cursorCol int) bool {
	currentLine := ""
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	if cursorCol > len(currentLine) {
		cursorCol = len(currentLine)
	}
	textBeforeCursor := currentLine[:cursorCol]

	// Don't trigger if we're typing a slash command at the start of the line
	trimmed := strings.TrimSpace(textBeforeCursor)
	if strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " ") {
		return false
	}

	return true
}
