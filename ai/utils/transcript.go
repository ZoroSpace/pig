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
		messages = append(messages, initial)
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
func GetCurrentTools(messages TranscriptMessages) []tool {
	tools := make(map[string]tool)
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		for _, ref := range m.ToolsRemoved {
			delete(tools, ref.Name)
		}
		for _, t := range m.ToolsAdded {
			tools[t.Name] = t
		}
	}
	out := make([]tool, 0, len(tools))
	// Preserve first-declaration order like the TS Map insertion order.
	seen := make(map[string]bool)
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		for _, ref := range m.ToolsRemoved {
			delete(seen, ref.Name)
		}
		for _, t := range m.ToolsAdded {
			if !seen[t.Name] {
				seen[t.Name] = true
				out = append(out, tools[t.Name])
			}
		}
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
				if _, existed := sections[name]; !existed && m.Sections.Values[name] != nil {
					sectionOrder = append(sectionOrder, name)
				}
				if m.Sections.Values[name] == nil {
					delete(sections, name)
				} else {
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
		messages = append([]aitypes.Message{head}, messages...)
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
	return string(l) == string(r)
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
// different definitions. Transports that reference tools by name (Anthropic
// tool_addition/tool_removal) cannot express that.
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
	declared := make(map[string]bool)
	for _, message := range messages {
		m, ok := asSystemMessage(message)
		if !ok {
			continue
		}
		if len(m.ToolsRemoved) > 0 {
			return true
		}
		for _, t := range m.ToolsAdded {
			if declared[t.Name] {
				return true
			}
			declared[t.Name] = true
		}
	}
	return false
}

// TranscriptTools splits tool declarations between the top-level request
// field and in-place additions.
type TranscriptTools struct {
	// RequestTools are the tools sent in the top-level request field.
	RequestTools []tool
	// AnchorsAdditions reports whether later system messages carry their own
	// ToolsAdded as in-place additions. When false, RequestTools already
	// holds the complete current tool set.
	AnchorsAdditions bool
}

// ResolveTranscriptTools splits tool declarations between the top-level
// request field and in-place additions. Transports that can anchor additions
// at a system message keep the initial tools at the top and load later ones
// where they appear; that only works when no tool was removed or
// redeclared, so everything else sends the current tool list.
func ResolveTranscriptTools(messages TranscriptMessages, supportsToolAdditions bool) TranscriptTools {
	anchorsAdditions := supportsToolAdditions && !HasNonAdditiveToolChanges(messages)
	var requestTools []tool
	if anchorsAdditions {
		if head := GetInitialSystemMessage(messages); head != nil {
			requestTools = head.ToolsAdded
		}
	} else {
		requestTools = GetCurrentTools(messages)
	}
	return TranscriptTools{RequestTools: requestTools, AnchorsAdditions: anchorsAdditions}
}
