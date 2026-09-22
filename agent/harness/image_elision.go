// pig-specific: tool-result image elision at the LLM context boundary.
//
// pi sends every tool-result image back on every request for the whole
// session, which pig kept 1:1. With phone-photo-sized JPEGs that means each
// read adds ~4-5MB of base64 to history and every subsequent request body
// grows unboundedly — providers reject the whole conversation with 413 once
// the accumulated images exceed the gateway body limit, and in-memory
// request serialization churns hundreds of MB per turn (see
// docs/fix/2026-09-22-image-tool-413-oversize-and-history-bloat.md).
//
// Policy: an image block in a tool result is elided from the LLM context as
// soon as the model has produced an assistant response after it. In other
// words each image survives exactly the request(s) the model has not yet
// responded to; afterwards the model can re-read the file (it is on disk)
// if it needs to view it again. Session entries keep the full data — the
// transcript, /export, and branch summaries are unaffected; only provider
// request payloads shrink. User-message images (e.g. clipboard pastes, not
// re-readable from disk) are never elided; the blockImages setting remains
// the nuclear option for those.
package harness

import (
	"github.com/ZoroSpace/pig/ai/types"
)

// elidedImageNote is the text block replacing an elided image (model-facing,
// actionable).
const elidedImageNote = "[Image elided from context to keep the request small. The image was already shown to you earlier; use the read tool on the same file if you need to view it again.]"

// ElideRespondedToolResultImages replaces image blocks in tool-result
// messages that precede the last assistant message with the elision note.
// Messages are copied on write; the input slice is never mutated. Returns
// the original slice when nothing changed.
func ElideRespondedToolResultImages(messages []types.Message) []types.Message {
	lastAssistant := -1
	for i, message := range messages {
		switch message.(type) {
		case types.AssistantMessage, *types.AssistantMessage:
			lastAssistant = i
		}
	}
	if lastAssistant <= 0 {
		return messages
	}

	// Fast path: no images to elide.
	hasElidable := false
	for i := 0; i < lastAssistant; i++ {
		if result, ok := messages[i].(types.ToolResultMessage); ok {
			if contentHasImage(result.Content) {
				hasElidable = true
				break
			}
		} else if result, ok := messages[i].(*types.ToolResultMessage); ok {
			if contentHasImage(result.Content) {
				hasElidable = true
				break
			}
		}
	}
	if !hasElidable {
		return messages
	}

	out := make([]types.Message, len(messages))
	copy(out, messages)
	for i := 0; i < lastAssistant; i++ {
		switch m := messages[i].(type) {
		case types.ToolResultMessage:
			if content, replaced := elideImagesInContent(m.Content); replaced {
				copied := m
				copied.Content = content
				out[i] = copied
			}
		case *types.ToolResultMessage:
			if content, replaced := elideImagesInContent(m.Content); replaced {
				copied := *m
				copied.Content = content
				out[i] = &copied
			}
		}
	}
	return out
}

func contentHasImage(content []types.Content) bool {
	for _, block := range content {
		if block.ContentKind() == types.KindImage {
			return true
		}
	}
	return false
}

// elideImagesInContent replaces image blocks with the elision note,
// deduping consecutive notes. replaced=false when the content had no images.
func elideImagesInContent(content []types.Content) ([]types.Content, bool) {
	replaced := false
	var out []types.Content
	for _, block := range content {
		if block.ContentKind() == types.KindImage {
			replaced = true
			if len(out) > 0 {
				if text, ok := out[len(out)-1].(types.TextContent); ok && text.Text == elidedImageNote {
					continue
				}
			}
			out = append(out, types.NewTextContent(elidedImageNote))
			continue
		}
		out = append(out, block)
	}
	return out, replaced
}
