// Package ai/utils: transcript normalization helpers (pi:
// src/utils/transcript.ts, #9548 mid-conversation system messages).
//
// The prompt and tool declarations live in the transcript's system messages.
// These helpers build, replay, collapse, and diff that state.
package utils

import (
	"encoding/json"

	aitypes "github.com/ZoroSpace/pig/ai/types"
)

// SystemMessage and Tool shorthands for readability.
type (
	systemMessage = aitypes.SystemMessage
	tool          = aitypes.Tool
)

// CreateInitialSystemMessage builds the leading system message for a prompt
// and tool set. Returns nil when both are empty, so an empty transcript stays
// empty.
func CreateInitialSystemMessage(systemPrompt string, tools []tool) *systemMessage {
	hasSystemPrompt := systemPrompt != ""
	hasTools := len(tools) > 0
	if !hasSystemPrompt && !hasTools {
		return nil
	}
	msg := &systemMessage{
		Role:      aitypes.RoleSystem,
		Content:   aitypes.SystemContent{String: systemPrompt},
		Timestamp: 0,
	}
	if hasTools {
		msg.ToolsAdded = tools
	}
	return msg
}

// NormalizeContext folds Context.SystemPrompt and Context.Tools into a
// leading system message. This is the only entry point that produces a
// TranscriptContext; every provider-facing function expects the result.
func NormalizeContext(context *aitypes.Context) *aitypes.TranscriptContext {
	var initial *systemMessage
	if context != nil {
		initial = CreateInitialSystemMessage(context.SystemPrompt, context.Tools)
	}
	var messages []aitypes.Message
	if initial != nil {
		// Embed the value form: pig's canonical transcript shape is value
		// messages (JSONL decoding produces values), and adapter switches
		// match value types.
		messages = append(messages, *initial)
	}
	if context != nil {
		messages = append(messages, context.Messages...)
	}
	return &aitypes.TranscriptContext{Messages: messages}
}

// TranscriptMessages is any message list. The replay helpers only read
// entries whose role is "system", so agent transcripts that carry custom
// message roles can be passed without filtering.
type TranscriptMessages = []aitypes.Message

func asSystemMessage(message aitypes.Message) (*systemMessage, bool) {
	if m, ok := message.(systemMessage); ok {
		return &m, true
	}
	if m, ok := message.(*systemMessage); ok {
		return m, true
	}
	return nil, false
}

// GetInitialSystemMessage returns the leading system message, if the
// transcript starts with one.
func GetInitialSystemMessage(messages TranscriptMessages) *systemMessage {
	if len(messages) == 0 {
		return nil
	}
	if m, ok := asSystemMessage(messages[0]); ok {
		return m
	}
	return nil
}

// WithoutInitialSystemMessage drops the leading system message for APIs that
// carry the prompt outside the message list.
func WithoutInitialSystemMessage(messages []aitypes.Message) []aitypes.Message {
	if GetInitialSystemMessage(messages) != nil {
		return messages[1:]
	}
	return messages
}

// GetCurrentTools resolves the tools available after applying every
// transcript delta in order.
//
// pi builds a single TS Map: delete drops the key, set on an existing key
// updates in place, set on a deleted/new key appends at the end of the
// iteration order. The ordered slice below emulates exactly that (a
// two-pass rebuild would leave stale entries for add-then-remove
// histories).
func GetCurrentTools(messages TranscriptMessages) []tool {
	tools := make(map[string]tool)
	var order []string
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		for _, ref := range m.ToolsRemoved {
			if _, exists := tools[ref.Name]; exists {
				delete(tools, ref.Name)
				for i, name := range order {
					if name == ref.Name {
						order = append(order[:i], order[i+1:]...)
						break
					}
				}
			}
		}
		for _, t := range m.ToolsAdded {
			if _, exists := tools[t.Name]; !exists {
				order = append(order, t.Name)
			}
			tools[t.Name] = t
		}
	}
	out := make([]tool, 0, len(order))
	for _, name := range order {
		out = append(out, tools[name])
	}
	return out
}

// GetCurrentSystemMessage replays every system message into one leading
// system message holding the current prompt and tools. Later Content is
// appended to the base prompt, Sections are patched by name, and tools are
// resolved with GetCurrentTools.
func GetCurrentSystemMessage(messages TranscriptMessages) *systemMessage {
	var content []string
	var sectionOrder []string
	sections := make(map[string]*string)
	var timestamp *int64
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		if timestamp == nil {
			ts := m.Timestamp
			timestamp = &ts
		}
		if text := systemMessageContentText(m.Content); text != "" {
			content = append(content, text)
		}
		if m.Sections != nil {
			for _, name := range m.Sections.Keys {
				// Emulate the TS Map in pi: delete removes the key from the
				// iteration order, set on an existing key updates in place,
				// set on a deleted/new key appends at the end.
				if m.Sections.Values[name] == nil {
					if _, existed := sections[name]; existed {
						delete(sections, name)
						for i, ordered := range sectionOrder {
							if ordered == name {
								sectionOrder = append(sectionOrder[:i], sectionOrder[i+1:]...)
								break
							}
						}
					}
				} else {
					if _, existed := sections[name]; !existed {
						sectionOrder = append(sectionOrder, name)
					}
					sections[name] = m.Sections.Values[name]
				}
			}
		}
	}
	tools := GetCurrentTools(messages)
	if timestamp == nil && len(tools) == 0 {
		return nil
	}
	ts := int64(0)
	if timestamp != nil {
		ts = *timestamp
	}
	msg := &systemMessage{
		Role:      aitypes.RoleSystem,
		Content:   aitypes.SystemContent{String: joinDouble(content)},
		Timestamp: ts,
	}
	if len(sections) > 0 {
		msg.Sections = &aitypes.SystemSections{Keys: sectionOrder, Values: sections}
	}
	if len(tools) > 0 {
		msg.ToolsAdded = tools
	}
	return msg
}

func joinDouble(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n\n"
		}
		out += p
	}
	return out
}

// GetCurrentSystemPrompt renders the current system prompt text after
// replaying every system message.
func GetCurrentSystemPrompt(messages TranscriptMessages) string {
	message := GetCurrentSystemMessage(messages)
	if message == nil {
		return ""
	}
	return GetSystemMessageText(message)
}

// CollapseSystemMessages rebuilds the transcript for APIs without
// mid-conversation system messages: the replayed system message leads, and
// every later system message is dropped.
func CollapseSystemMessages(context *aitypes.TranscriptContext) *aitypes.TranscriptContext {
	head := GetCurrentSystemMessage(context.Messages)
	messages := make([]aitypes.Message, 0, len(context.Messages))
	for _, message := range context.Messages {
		if _, ok := asSystemMessage(message); ok {
			continue
		}
		messages = append(messages, message)
	}
	if head != nil {
		// Value form for the same reason as NormalizeContext.
		messages = append([]aitypes.Message{*head}, messages...)
	}
	return &aitypes.TranscriptContext{Messages: messages}
}

// ResolveTranscript keeps later system messages in place when the model
// accepts them; otherwise collapses them.
func ResolveTranscript(context *aitypes.TranscriptContext, supportsMidConvoSystemMessages bool) *aitypes.TranscriptContext {
	if supportsMidConvoSystemMessages {
		return context
	}
	return CollapseSystemMessages(context)
}

// ToToolDeclaration strips executable and display-only fields from a tool
// before transcript comparison or persistence.
func ToToolDeclaration(t tool) tool {
	var parameters json.RawMessage
	if len(t.Parameters) > 0 {
		// JSON round-trip drops Go-side formatting; pi relies on the same
		// normalization to make structural comparisons exact.
		parameters, _ = json.Marshal(json.RawMessage(t.Parameters))
	}
	out := tool{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  parameters,
	}
	if t.ConstrainedSampling != nil {
		out.ConstrainedSampling = t.ConstrainedSampling
	}
	return out
}

// DeclarationsEqual reports whether two tools declare the same interface to
// the model. Both sides go through ToToolDeclaration first: its JSON
// round-trip drops the typebox symbol keys and undefined fields that a
// structural comparison would see, and builds both objects with the same key
// order, so comparing the serialized declarations is exact. This avoids a
// deep-equal dependency in a browser-safe package.
func DeclarationsEqual(left, right tool) bool {
	l, err := json.Marshal(ToToolDeclaration(left))
	if err != nil {
		return false
	}
	r, err := json.Marshal(ToToolDeclaration(right))
	if err != nil {
		return false
	}
	// pig: ConstrainedSampling is typed any — a JSONL-decoded value
	// re-marshals as a map (keys sorted) while a registry-side struct keeps
	// field order, so raw byte comparison flags semantically identical tools
	// on session resume (spurious toolsRemoved+toolsAdded declaration).
	// pi's JS object key order is stable across this comparison in every real
	// flow, so canonicalize both sides through decode-to-any (map keys sort
	// deterministically) before the byte comparison.
	var lc, rc any
	if json.Unmarshal(l, &lc) != nil || json.Unmarshal(r, &rc) != nil {
		return false
	}
	lb, err := json.Marshal(lc)
	if err != nil {
		return false
	}
	rb, err := json.Marshal(rc)
	if err != nil {
		return false
	}
	return string(lb) == string(rb)
}

// ToolStateChanges is the delta between two complete tool states.
type ToolStateChanges struct {
	ToolsAdded   []tool
	ToolsRemoved []aitypes.ToolReference
}

// GetToolStateChanges compares two complete tool states. A changed
// definition is a removal followed by an addition.
func GetToolStateChanges(previous []tool, current []tool) ToolStateChanges {
	previousTools := make(map[string]tool, len(previous))
	for _, t := range previous {
		previousTools[t.Name] = t
	}
	currentTools := make(map[string]tool, len(current))
	for _, t := range current {
		currentTools[t.Name] = t
	}
	changes := ToolStateChanges{
		ToolsAdded:   []tool{},
		ToolsRemoved: []aitypes.ToolReference{},
	}
	for _, t := range current {
		previousTool, ok := previousTools[t.Name]
		if !ok || !DeclarationsEqual(previousTool, t) {
			changes.ToolsAdded = append(changes.ToolsAdded, ToToolDeclaration(t))
		}
	}
	for _, t := range previous {
		currentTool, ok := currentTools[t.Name]
		if !ok || !DeclarationsEqual(t, currentTool) {
			changes.ToolsRemoved = append(changes.ToolsRemoved, aitypes.ToolReference{Name: t.Name})
		}
	}
	return changes
}

// GetDeclaredTools returns every definition referenced by transcript tool
// state, in first-declaration order.
func GetDeclaredTools(messages TranscriptMessages) []tool {
	definitions := make(map[string]tool)
	var out []tool
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		for _, t := range m.ToolsAdded {
			if _, seen := definitions[t.Name]; !seen {
				out = append(out, t)
			}
			definitions[t.Name] = t
		}
	}
	return out
}

// HasToolRedefinitions reports whether a tool name was declared twice with
// different definitions. A transport that can only reference previously
// declared tools by name cannot replay such a history.
//
// Deprecated: no built-in transport needs this anymore — Anthropic expresses
// redefinitions with inline tool_definition blocks. Kept for API
// compatibility and will be removed in a future release.
func HasToolRedefinitions(messages TranscriptMessages) bool {
	declared := make(map[string]tool)
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		for _, t := range m.ToolsAdded {
			if previous, ok := declared[t.Name]; ok && !DeclarationsEqual(previous, t) {
				return true
			}
			declared[t.Name] = t
		}
	}
	return false
}

// HasNonAdditiveToolChanges reports whether tool history contains a removal
// or same-name redeclaration that an addition-only transport cannot replay.
func HasNonAdditiveToolChanges(messages TranscriptMessages) bool {
	return findLastNonAdditiveToolChange(systemMessagesOnly(messages)) >= 0
}

func systemMessagesOnly(messages TranscriptMessages) []*systemMessage {
	out := make([]*systemMessage, 0, len(messages))
	for _, message := range messages {
		if m, ok := asSystemMessage(message); ok {
			out = append(out, m)
		}
	}
	return out
}

// findLastNonAdditiveToolChange returns the index of the last system message
// with a removal or same-name redeclaration, or -1.
func findLastNonAdditiveToolChange(systemMessages []*systemMessage) int {
	// A name declared before counts as a redeclaration even after its removal:
	// Kimi K3 ignores a removed tool that a later tool message declares again
	// (verified October 2026).
	declared := make(map[string]bool)
	last := -1
	for index, message := range systemMessages {
		if len(message.ToolsRemoved) > 0 {
			last = index
		}
		for _, t := range message.ToolsAdded {
			if declared[t.Name] {
				last = index
			}
			declared[t.Name] = true
		}
	}
	return last
}

// TranscriptToolSupport describes how a transport can express tool changes
// after the first request.
type TranscriptToolSupport string

const (
	// TranscriptToolSupportNone means only the top-level tool list exists; it
	// always holds the current tools.
	TranscriptToolSupportNone TranscriptToolSupport = "none"
	// TranscriptToolSupportAdditions means tools can be added at a later
	// message, but not removed or redefined.
	TranscriptToolSupportAdditions TranscriptToolSupport = "additions"
	// TranscriptToolSupportChanges means every tool, including the initial
	// ones, is declared in place, and a later declaration under the same name
	// replaces the earlier one. Removals are not expressible on the wire, so
	// the transport announces them as text and the removed tools stay
	// declared.
	TranscriptToolSupportChanges TranscriptToolSupport = "changes"
)

// TranscriptTools splits tool declarations between the top-level request
// field and in-place changes.
type TranscriptTools struct {
	// RequestTools are the tools sent in the top-level request field.
	RequestTools []tool
	// FoldedSystemMessages is the number of leading system messages whose
	// tool changes RequestTools already holds. Every later system message
	// carries its own tool changes in place.
	FoldedSystemMessages int
}

// ResolveTranscriptTools splits tool declarations between the top-level
// request field and in-place changes.
//
// With "changes", the top-level list stays empty and every system message,
// including the leading one, declares its tools in place: an in-place
// declaration can replace an earlier in-place one, but not a top-level one.
// With "additions", a removal or redefinition cannot be expressed in place,
// so the top-level list becomes the tool state after the last such change;
// later additions stay anchored, and the cached prefix is invalidated once
// per removal instead of on every later addition. With "none", the top-level
// list is the current tool set.
func ResolveTranscriptTools(messages TranscriptMessages, support TranscriptToolSupport) TranscriptTools {
	systemMessages := systemMessagesOnly(messages)
	if support == TranscriptToolSupportNone {
		return TranscriptTools{RequestTools: GetCurrentTools(messages), FoldedSystemMessages: len(systemMessages)}
	}
	if support == TranscriptToolSupportChanges {
		return TranscriptTools{RequestTools: []tool{}, FoldedSystemMessages: 0}
	}
	folded := 0
	if GetInitialSystemMessage(messages) != nil {
		folded = 1
	}
	if last := findLastNonAdditiveToolChange(systemMessages) + 1; last > folded {
		folded = last
	}
	return TranscriptTools{RequestTools: GetCurrentTools(leadingSystemMessages(systemMessages[:folded])), FoldedSystemMessages: folded}
}

func leadingSystemMessages(messages []*systemMessage) TranscriptMessages {
	out := make(TranscriptMessages, 0, len(messages))
	for _, m := range messages {
		out = append(out, *m)
	}
	return out
}
