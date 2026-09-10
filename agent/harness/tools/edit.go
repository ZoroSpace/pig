// pi: src/harness/tools/edit.ts — the edit tool (exact text replacement).
//
// Segment deviations:
//   - prepareArguments mutates a copy of the raw argument map (TS mutates
//     the input object); the legacy oldText/newText shim and the JSON-string
//     edits coercion follow the TS control flow exactly.
//   - editAccessError chains the FileError as cause (pi: { cause: error }).
//   - signal?.aborted checks → ctx.Err() probes.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
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

// EditToolInput is the edit tool's parameter shape (pi: EditToolInput).
type EditToolInput struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

// EditToolDetails carries the diff artifacts (pi: EditToolDetails).
type EditToolDetails struct {
	Diff             string
	Patch            string
	FirstChangedLine *int
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

// editAccessError wraps file access failures (pi: editAccessError).
func editAccessError(path string, fileErr error) error {
	code := "unknown"
	if fe, ok := harness.AsFileError(fileErr); ok {
		code = string(fe.Code)
	}
	return fmt.Errorf("Could not edit file: %s. Error code: %s.", path, code)
}

// CreateEditTool builds the edit tool (pi: createEditTool).
func CreateEditTool() *harness.AgentHarnessTool {
	return &harness.AgentHarnessTool{
		Name:  "edit",
		Label: "edit",
		Description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. " +
			"If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. " +
			"Do not include large unchanged regions just to connect distant changes.",
		Parameters:       json.RawMessage(editSchemaJSON),
		PrepareArguments: prepareEditArguments,
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, context any) (agent.AgentToolResult, error) {
			input, err := decodeParams[EditToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			path, edits, err := validateEditInput(input)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			executionContext, err := executionContextOf(context)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			env := executionContext.Env

			absolutePath, err := ResolveToolPath(ctx, env, path)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			return WithFileMutationQueue(ctx, env, absolutePath, func() (agent.AgentToolResult, error) {
				if ctx.Err() != nil {
					return agent.AgentToolResult{}, errors.New("Operation aborted")
				}
				info, err := env.FileInfo(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, editAccessError(path, err)
				}
				if info.Kind != harness.FileKindFile && info.Kind != harness.FileKindSymlink {
					return agent.AgentToolResult{}, fmt.Errorf("Could not edit file: %s. Path is not a file.", path)
				}

				content, err := env.ReadTextFile(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, editAccessError(path, err)
				}
				if ctx.Err() != nil {
					return agent.AgentToolResult{}, errors.New("Operation aborted")
				}

				bom, text := StripBom(content)
				originalEnding := DetectLineEnding(text)
				normalizedContent := NormalizeToLF(text)
				applied, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if ctx.Err() != nil {
					return agent.AgentToolResult{}, errors.New("Operation aborted")
				}

				finalContent := bom + RestoreLineEndings(applied.NewContent, originalEnding)
				if err := env.WriteFile(ctx, absolutePath, []byte(finalContent)); err != nil {
					return agent.AgentToolResult{}, editAccessError(path, err)
				}
				if ctx.Err() != nil {
					return agent.AgentToolResult{}, errors.New("Operation aborted")
				}

				diffResult := GenerateDiffString(applied.BaseContent, applied.NewContent)
				var firstChangedLine *int
				if diffResult.HasFirstChangedLine {
					line := diffResult.FirstChangedLine
					firstChangedLine = &line
				}
				return agent.AgentToolResult{
					Content: []types.Content{
						&types.TextContent{Kind: types.KindText, Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path)},
					},
					Details: &EditToolDetails{
						Diff:             diffResult.Diff,
						Patch:            GenerateUnifiedPatch(path, applied.BaseContent, applied.NewContent),
						FirstChangedLine: firstChangedLine,
					},
				}, nil
			})
		},
	}
}
