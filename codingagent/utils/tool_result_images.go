// pi: utils/tool-result-images.ts — normalize image blocks returned by tool
// results.
//
// The `read` tool and `@file` CLI attachments run their images through
// processImage, but tools that produce images themselves (extensions, MCP
// bridges, screenshot tools) hand back arbitrary base64 payloads that go
// straight into session history and every subsequent provider request.
// Oversized images make the provider reject the whole conversation, not
// just the offending turn, so normalize them once as they enter history.
package utils

import (
	"encoding/base64"

	"github.com/ZoroSpace/pig/ai/types"
)

// NormalizeToolResultImagesOptions are the options for
// NormalizeToolResultImages (pi: NormalizeToolResultImagesOptions).
type NormalizeToolResultImagesOptions struct {
	// AutoResizeImages resizes oversized images to inline provider limits.
	// Defaults to true; nil = default (pi: autoResizeImages?).
	AutoResizeImages *bool
}

// NormalizeToolResultImages normalizes image blocks returned by tool
// results. Returns the original slice when nothing changed so callers can
// skip rewriting the result (pi: normalizeToolResultImages).
func NormalizeToolResultImages(content []types.Content, options *NormalizeToolResultImagesOptions) ([]types.Content, error) {
	hasImage := false
	for _, block := range content {
		if block.ContentKind() == types.KindImage {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return content, nil
	}

	autoResizeImages := true
	if options != nil && options.AutoResizeImages != nil {
		autoResizeImages = *options.AutoResizeImages
	}
	normalized := make([]types.Content, 0, len(content))
	changed := false

	for _, block := range content {
		image, ok := block.(types.ImageContent)
		if !ok {
			normalized = append(normalized, block)
			continue
		}

		// Node's Buffer.from(data, "base64") is lenient about padding; try
		// the raw (unpadded) alphabet as a fallback and pass the original
		// block through when both fail (declared deviation).
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(image.Data)
		}
		if err != nil {
			// Unlike `read`, keep the original block. The tool already
			// produced this image and the failure may just be an unavailable
			// image backend, so passing it through preserves the behavior
			// tools have today instead of silently deleting their output.
			normalized = append(normalized, block)
			continue
		}
		processed := ProcessImage(data, image.MimeType, &ProcessImageOptions{AutoResizeImages: &autoResizeImages})
		if !processed.OK {
			// Unlike `read`, keep the original block (see above; pi's
			// comment applies to the not-ok path too).
			normalized = append(normalized, block)
			continue
		}

		if processed.Data == image.Data && processed.MimeType == image.MimeType && len(processed.Hints) == 0 {
			normalized = append(normalized, block)
			continue
		}

		normalized = append(normalized, types.ImageContent{
			Kind:     types.KindImage,
			Data:     processed.Data,
			MimeType: processed.MimeType,
		})
		if len(processed.Hints) > 0 {
			text := ""
			for i, hint := range processed.Hints {
				if i > 0 {
					text += "\n"
				}
				text += hint
			}
			normalized = append(normalized, types.NewTextContent(text))
		}
		changed = true
	}

	if !changed {
		return content, nil
	}
	return normalized, nil
}
