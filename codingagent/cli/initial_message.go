// pi: src/cli/initial-message.ts
package cli

import (
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/samber/lo"
)

// InitialMessageInput carries the sources for the non-interactive initial
// prompt (pi: InitialMessageInput).
type InitialMessageInput struct {
	Parsed *Args
	// FileText is the @file text; "" = not provided (pi: fileText?: string,
	// consumed behind a truthy check so "" is indistinguishable from absent).
	FileText string
	// FileImages are the @file image attachments (pi: fileImages?:
	// ImageContent[]).
	FileImages []ai.ImageContent
	// StdinContent is the piped stdin; nil = not provided (pi: stdinContent?:
	// string, consumed behind an `!== undefined` check, so "" from an empty
	// pipe is meaningful and stays a pointer).
	StdinContent *string
}

// InitialMessageResult is the assembled initial prompt (pi:
// InitialMessageResult; optional members nil = undefined).
type InitialMessageResult struct {
	InitialMessage *string
	InitialImages  []ai.ImageContent
}

// BuildInitialMessage combines stdin content, @file text, and the first CLI
// message into a single initial prompt for non-interactive mode.
func BuildInitialMessage(input InitialMessageInput) InitialMessageResult {
	var parts []string
	if input.StdinContent != nil {
		parts = append(parts, *input.StdinContent)
	}
	if input.FileText != "" {
		parts = append(parts, input.FileText)
	}

	if len(input.Parsed.Messages) > 0 {
		parts = append(parts, input.Parsed.Messages[0])
		input.Parsed.Messages = input.Parsed.Messages[1:]
	}

	result := InitialMessageResult{}
	if len(parts) > 0 {
		result.InitialMessage = lo.ToPtr(strings.Join(parts, ""))
	}
	if len(input.FileImages) > 0 {
		result.InitialImages = input.FileImages
	}
	return result
}
