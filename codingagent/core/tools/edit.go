// pi: src/core/tools/edit.ts — the coding-agent edit tool.
//
// Segment deviations:
//   - The render functions live in renderers_edit.go (S5a: pi moved them to
//     core/tools/renderers/edit.ts; pig keeps package tools, see the file
//     header there).
//   - signal wiring → context.Context (the throwIfAborted probes observe the
//     same aborts while keeping the mutation queue locked, per the TS
//     comment).
//   - editAccessError formats node errno codes; pig surfaces the os error's
//     wrapped errno.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// replaceEditSchemaJSON is one targeted replacement (pi: replaceEditSchema).
const replaceEditSchemaJSON = `{
  "type": "object",
  "properties": {
    "oldText": {
      "type": "string",
      "description": "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."
    },
    "newText": {
      "type": "string",
      "description": "Replacement text for this targeted edit."
    }
  },
  "required": ["oldText", "newText"]
}`

// editSchemaJSON is the edit tool's parameter schema (pi: editSchema).
const editSchemaJSON = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Path to the file to edit (relative or absolute)"
    },
    "edits": {
      "type": "array",
      "items": ` + replaceEditSchemaJSON + `,
      "description": "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."
    }
  },
  "required": ["path", "edits"]
}`

// EditToolSystemPromptContribution is the edit tool's system prompt metadata
// (pi: editToolSystemPromptContribution).
var EditToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
	Guidelines: []string{
		"Use edit for precise changes (edits[].oldText must match exactly)",
		"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
		"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
		"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
	},
}

// EditToolInput is the edit tool's parameter shape (pi: EditToolInput).
type EditToolInput struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

// EditToolDetails carries the diff artifacts (pi: EditToolDetails). Wire
// keys are camelCase for session-JSONL parity with pi (edit.ts
// EditToolDetails { diff, patch, firstChangedLine? }); the tags were
// missing until 2026-10-10, so pig wrote capitalized keys
// (docs/fix/2026-10-10-edit-details-map-form.md).
type EditToolDetails struct {
	// Diff is the display-oriented diff of the changes made.
	Diff string `json:"diff"`
	// Patch is the standard unified patch of the changes made.
	Patch string `json:"patch"`
	// FirstChangedLine is the line number of the first change in the new
	// file (for editor navigation); nil = pi's undefined.
	FirstChangedLine *int `json:"firstChangedLine,omitempty"`
}

// EditOperations are pluggable operations for the edit tool; override these
// to delegate file editing to remote systems (pi: EditOperations).
type EditOperations struct {
	// ReadFile reads file contents (pi: readFile; Buffer → []byte).
	ReadFile func(ctx context.Context, absolutePath string) ([]byte, error)
	// WriteFile writes content to a file.
	WriteFile func(ctx context.Context, absolutePath string, content string) error
	// Access checks the file is readable and writable (pi: access).
	Access func(ctx context.Context, absolutePath string) error
}

func defaultEditOperations() EditOperations {
	return EditOperations{
		ReadFile: func(ctx context.Context, path string) ([]byte, error) { return os.ReadFile(path) },
		WriteFile: func(ctx context.Context, path string, content string) error {
			return os.WriteFile(path, []byte(content), 0o644)
		},
		Access: func(ctx context.Context, path string) error {
			if _, err := os.Stat(path); err != nil {
				return codingagentutils.FSAccessMessage(err, path)
			}
			// pi: fsAccess(R_OK | W_OK) — probe the write permission the
			// way access(2) would.
			file, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				if code := codingagentutils.NodeErrCode(err); code != "" {
					return &codingagentutils.NodeErr{Code: code, Message: fmt.Sprintf("%s: %s, open '%s'", code, underlyingErrText(err), path), Err: err}
				}
				return err
			}
			file.Close()
			return nil
		},
	}
}

// EditToolOptions parameterize the edit tool (pi: EditToolOptions).
type EditToolOptions struct {
	// Operations customizes file editing. Default: local filesystem.
	Operations *EditOperations
}

// isSingleEditInput reports whether the value is one {oldText, newText}
// object (pi: isSingleEditInput).
func isSingleEditInput(value map[string]any) bool {
	if value == nil {
		return false
	}
	_, oldOK := value["oldText"].(string)
	_, newOK := value["newText"].(string)
	return oldOK && newOK
}

// prepareEditArguments coerces legacy/JSON-string argument shapes (pi:
// prepareEditArguments).
func prepareEditArguments(input any) any {
	args, ok := input.(map[string]any)
	if !ok {
		return input
	}
	// Some models (Opus 4.6, GLM-5.1) send edits as a JSON string instead of
	// an array. Others send a single edit object instead of a one-element
	// edits array.
	if editsRaw, present := args["edits"]; present {
		switch v := editsRaw.(type) {
		case string:
			var parsed any
			if err := json.Unmarshal([]byte(v), &parsed); err == nil {
				if arr, ok := parsed.([]any); ok {
					args["edits"] = arr
				} else if single, ok := parsed.(map[string]any); ok && isSingleEditInput(single) {
					args["edits"] = []any{single}
				}
			}
		case map[string]any:
			if isSingleEditInput(v) {
				args["edits"] = []any{v}
			}
		}
	}

	oldText, oldOK := args["oldText"].(string)
	newText, newOK := args["newText"].(string)
	if !oldOK || !newOK {
		return args
	}
	edits := []any{}
	if arr, ok := args["edits"].([]any); ok {
		edits = append(edits, arr...)
	}
	edits = append(edits, map[string]any{"oldText": oldText, "newText": newText})
	rest := map[string]any{}
	for key, value := range args {
		if key == "oldText" || key == "newText" {
			continue
		}
		rest[key] = value
	}
	rest["edits"] = edits
	return rest
}

// validateEditInput checks the edits array (pi: validateEditInput).
func validateEditInput(input EditToolInput) (string, []Edit, error) {
	if len(input.Edits) == 0 {
		return "", nil, errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	return input.Path, input.Edits, nil
}

// editAccessError wraps file access failures with node-style error codes
// (pi: `error instanceof Error && "code" in error ? "Error code: " +
// error.code : String(error)`).
func editAccessError(path string, err error) error {
	if code := codingagentutils.NodeErrCodeOf(err); code != "" {
		return fmt.Errorf("Could not edit file: %s. Error code: %s.", path, code)
	}
	return fmt.Errorf("Could not edit file: %s. Error: %v.", path, err)
}

// underlyingErrText extracts the errno text from a path error.
func underlyingErrText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// CreateEditToolDefinition builds the edit tool definition (pi:
// createEditToolDefinition; renderCall/renderResult arrive with the theme
// batch).
func CreateEditToolDefinition(cwd string, options *EditToolOptions) *ToolDefinition {
	ops := defaultEditOperations()
	if options != nil && options.Operations != nil {
		ops = *options.Operations
	}
	return &ToolDefinition{
		Name:             "edit",
		Label:            "edit",
		Description:      "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		PromptSnippet:    EditToolSystemPromptContribution.Snippet,
		PromptGuidelines: append([]string{}, EditToolSystemPromptContribution.Guidelines...),
		Parameters:       json.RawMessage(editSchemaJSON),
		// pi v0.86.0: strict-mode tool sampling is hardcoded (was
		// getExperimentalToolSampling()).
		ConstrainedSampling: types.ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
		PrepareArguments:    prepareEditArguments,
		// pig-only deviation (2026-10-10): pi sets renderShell: "self" here; pig
		// drops it so edit renders in the default shell and gets the state-colored
		// corner frame like every other framed tool (user request, see
		// docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS). The renderers' own
		// padding is adjusted to match (renderers_edit.go).
		NewRenderState: func() any { return &EditRenderState{} },
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeToolParams[EditToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			path, edits, err := validateEditInput(input)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			absolutePath := ResolveToCwd(path, executeCwd(toolCtx, cwd))

			// Do not reject from an abort event listener here: that would
			// release the mutation queue while an in-flight filesystem
			// operation may still finish. Checking signal.aborted after each
			// await observes the same aborts while keeping the queue locked
			// until the current operation has settled.
			throwIfAborted := func() error {
				if ctx.Err() != nil {
					return errors.New("Operation aborted")
				}
				return nil
			}

			return WithFileMutationQueue(ctx, absolutePath, func() (agent.AgentToolResult, error) {
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}
				// Check if file exists.
				if err := ops.Access(ctx, absolutePath); err != nil {
					if abortErr := throwIfAborted(); abortErr != nil {
						return agent.AgentToolResult{}, abortErr
					}
					return agent.AgentToolResult{}, editAccessError(path, err)
				}
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				// Read the file.
				buffer, err := ops.ReadFile(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				rawContent := string(buffer)
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				// Strip BOM before matching. The model will not include an
				// invisible BOM in oldText.
				bom, content := codingagentutils.SplitBom(rawContent)
				originalEnding := DetectLineEnding(content)
				normalizedContent := NormalizeToLF(content)
				applied, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				finalContent := bom + RestoreLineEndings(applied.NewContent, originalEnding)
				if err := ops.WriteFile(ctx, absolutePath, finalContent); err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				diffResult := generateDiffStringResult(applied.BaseContent, applied.NewContent)
				patch := GenerateUnifiedPatch(path, applied.BaseContent, applied.NewContent)
				var firstChangedLine *int
				if diffResult.HasFirstChangedLine {
					line := diffResult.FirstChangedLine
					firstChangedLine = &line
				}
				return agent.AgentToolResult{
					Content: []types.Content{asTextContent(fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path))},
					Details: &EditToolDetails{
						Diff:             diffResult.Diff,
						Patch:            patch,
						FirstChangedLine: firstChangedLine,
					},
				}, nil
			})
		},
		// pi: ...editRenderers.
		RenderCall:   EditRenderers.RenderCall,
		RenderResult: EditRenderers.RenderResult,
	}
}

// CreateEditTool wraps the edit definition into an AgentTool (pi:
// createEditTool).
func CreateEditTool(cwd string, options *EditToolOptions) Tool {
	return WrapToolDefinition(CreateEditToolDefinition(cwd, options), nil)
}
