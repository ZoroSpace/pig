// pi: src/cli/file-processor.ts — Process @file CLI arguments into text
// content and image attachments
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/samber/lo"
)

// ProcessedFiles is the combined text content and image attachments (pi:
// ProcessedFiles).
type ProcessedFiles struct {
	Text   string
	Images []ai.ImageContent
}

// ProcessFileOptions carries the @file processing switches (pi:
// ProcessFileOptions).
type ProcessFileOptions struct {
	// AutoResizeImages resizes images to the inline provider limits.
	// Default: true.
	AutoResizeImages *bool
}

// processFilesFatal terminates the process for unreadable @file arguments
// (pi: console.error(chalk.red(...)) + process.exit(1); chalk colors are
// dropped per the model-resolver precedent); var for tests.
var processFilesFatal = func(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

// ProcessFileArguments processes @file arguments into text content and image
// attachments.
func ProcessFileArguments(fileArgs []string, options *ProcessFileOptions) ProcessedFiles {
	autoResizeImages := options == nil || options.AutoResizeImages == nil || *options.AutoResizeImages
	text := ""
	var images []ai.ImageContent

	// pi resolves against process.cwd() per argument; os.Getwd failure has no
	// pi counterpart (process.cwd() cannot fail there), so the resolved path
	// is used as-is in that case.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

	for _, fileArg := range fileArgs {
		// Expand and resolve path (handles ~ expansion and macOS screenshot Unicode spaces)
		// pi: resolve(resolveReadPath(fileArg, process.cwd()))
		resolved := tools.ResolveReadPath(fileArg, cwd)
		absolutePath := resolved
		if abs, err := filepath.Abs(resolved); err == nil {
			absolutePath = abs
		}

		// Check if file exists (pi: access() — any failure, including
		// permission errors, reports "File not found")
		if _, err := os.Stat(absolutePath); err != nil {
			processFilesFatal(fmt.Sprintf("Error: File not found: %s", absolutePath))
		}

		// Check if file is empty
		stats, err := os.Stat(absolutePath)
		// pi: stat() failure would surface as an unhandled rejection; pig
		// routes it through the same fatal channel (declared deviation).
		if err != nil {
			processFilesFatal(fmt.Sprintf("Error: Could not read file %s: %s", absolutePath, err.Error()))
		}
		if stats.Size() == 0 {
			// Skip empty files
			continue
		}

		mimeType := utils.DetectSupportedImageMimeTypeFromFile(absolutePath)

		if mimeType != "" {
			// Handle image file
			// pi: readFile without a try/catch — a read failure here would
			// crash the process; pig reports it through the fatal channel
			// with the text-file message shape (declared deviation).
			content, err := os.ReadFile(absolutePath)
			if err != nil {
				processFilesFatal(fmt.Sprintf("Error: Could not read file %s: %s", absolutePath, err.Error()))
			}
			processed := utils.ProcessImage(content, mimeType, &utils.ProcessImageOptions{
				AutoResizeImages: lo.ToPtr(autoResizeImages),
			})

			if !processed.OK {
				text += fmt.Sprintf("<file name=\"%s\">%s</file>\n", absolutePath, processed.Message)
				continue
			}

			attachment := ai.NewImageContent(processed.Data, processed.MimeType)
			images = append(images, attachment)

			// Add text reference to image with optional processing hints
			if len(processed.Hints) > 0 {
				text += fmt.Sprintf("<file name=\"%s\">%s</file>\n", absolutePath, strings.Join(processed.Hints, "\n"))
			} else {
				text += fmt.Sprintf("<file name=\"%s\"></file>\n", absolutePath)
			}
		} else {
			// Handle text file
			content, err := os.ReadFile(absolutePath)
			if err != nil {
				message := err.Error() // pi: error instanceof Error ? error.message : String(error)
				processFilesFatal(fmt.Sprintf("Error: Could not read file %s: %s", absolutePath, message))
			}
			text += fmt.Sprintf("<file name=\"%s\">\n%s\n</file>\n", absolutePath, utils.StripBom(string(content)))
		}
	}

	return ProcessedFiles{Text: text, Images: images}
}
