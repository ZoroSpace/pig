// pi: test/initial-message.test.ts
package cli

import (
	"testing"

	"github.com/samber/lo"
)

func createArgs(messages ...string) *Args {
	return &Args{
		Messages:        append([]string{}, messages...),
		FileArgs:        []string{},
		UnknownFlags:    map[string]any{},
		UnknownFlagKeys: []string{},
		Diagnostics:     []ArgDiagnostic{},
	}
}

func TestBuildInitialMessage(t *testing.T) {
	t.Run("merges piped stdin with the first CLI message into one prompt", func(t *testing.T) {
		parsed := createArgs("Summarize the text given")
		result := BuildInitialMessage(InitialMessageInput{
			Parsed:       parsed,
			StdinContent: lo.ToPtr("README contents\n"),
		})

		if result.InitialMessage == nil || *result.InitialMessage != "README contents\nSummarize the text given" {
			t.Fatalf("InitialMessage = %v, want %q", result.InitialMessage, "README contents\nSummarize the text given")
		}
		if len(parsed.Messages) != 0 {
			t.Fatalf("parsed.Messages = %v, want empty", parsed.Messages)
		}
	})

	t.Run("uses stdin as the initial prompt when no CLI message is present", func(t *testing.T) {
		parsed := createArgs()
		result := BuildInitialMessage(InitialMessageInput{
			Parsed:       parsed,
			StdinContent: lo.ToPtr("README contents"),
		})

		if result.InitialMessage == nil || *result.InitialMessage != "README contents" {
			t.Fatalf("InitialMessage = %v, want %q", result.InitialMessage, "README contents")
		}
		if len(parsed.Messages) != 0 {
			t.Fatalf("parsed.Messages = %v, want empty", parsed.Messages)
		}
	})

	t.Run("combines stdin, file text, and first CLI message in one prompt", func(t *testing.T) {
		parsed := createArgs("Explain it", "Second message")
		result := BuildInitialMessage(InitialMessageInput{
			Parsed:       parsed,
			StdinContent: lo.ToPtr("stdin\n"),
			FileText:     "file\n",
		})

		if result.InitialMessage == nil || *result.InitialMessage != "stdin\nfile\nExplain it" {
			t.Fatalf("InitialMessage = %v, want %q", result.InitialMessage, "stdin\nfile\nExplain it")
		}
		if len(parsed.Messages) != 1 || parsed.Messages[0] != "Second message" {
			t.Fatalf("parsed.Messages = %v, want [Second message]", parsed.Messages)
		}
	})
}
