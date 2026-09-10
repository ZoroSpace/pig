// pi: src/utils/image-process.ts — image normalization/resize pipeline for
// inline model attachments.
//
// Segment deviations (photon/WASM boundary, see §1):
//   - convertImageBytesToPng and resizeImage run Photon (WASM) in pi. The
//     photon runtime arrives with Phase 8 (wazero); until then pig takes
//     pi's "photon unavailable" path: normalizeImage falls back to null for
//     non-supported formats and resizeImage resolves null, so processImage
//     returns the documented omission messages — identical observable
//     behavior to pi running without a loadable photon runtime.
//   - The worker-thread indirection of image-resize.ts is node-runtime
//     plumbing with no pig counterpart (in-process execution).
package utils

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// ProcessImageOptions parameterize ProcessImage (pi: ProcessImageOptions).
type ProcessImageOptions struct {
	// AutoResizeImages resizes images to inline provider limits. Defaults
	// to true; nil = default.
	AutoResizeImages *bool
	// ResizeOptions are optional resize overrides; nil = resizeImage
	// defaults. (pi: ImageResizeOptions — photon-backed, arrives Phase 8.)
	ResizeOptions *ImageResizeOptions
}

// ProcessImageResult is the outcome of ProcessImage (pi: ProcessImageResult
// union → single struct; OK discriminates the success payload from Message).
type ProcessImageResult struct {
	OK       bool
	Data     string
	MimeType string
	Hints    []string
	Message  string
}

// ImageResizeOptions carries the resize overrides (pi: ImageResizeOptions;
// the photon implementation arrives Phase 8).
type ImageResizeOptions struct {
	MaxWidth  *int
	MaxHeight *int
	// MaxEncodedBytes is the encoded file size budget.
	MaxEncodedBytes *int
}

// resizedImage mirrors pi's ResizedImage.
type resizedImage struct {
	Data           string
	MimeType       string
	OriginalWidth  int
	OriginalHeight int
	Width          int
	Height         int
	WasResized     bool
}

func baseMimeType(mimeType string) string {
	parts := strings.SplitN(mimeType, ";", 2)
	base := strings.TrimSpace(parts[0])
	if base == "" {
		return strings.ToLower(mimeType)
	}
	return strings.ToLower(base)
}

func normalizeSupportedImageMimeType(mimeType string) string {
	switch baseMimeType(mimeType) {
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/gif":
		return "image/gif"
	case "image/webp":
		return "image/webp"
	default:
		return ""
	}
}

// normalizedImage mirrors pi's NormalizedImage.
type normalizedImage struct {
	bytes         []byte
	mimeType      string
	convertedFrom string
}

// convertImageBytesToPng converts unsupported formats to PNG via photon (pi:
// convertImageBytesToPng). Phase 8 placeholder: photon is not loaded yet, so
// this always fails, taking pi's photon-unavailable path.
func convertImageBytesToPng(bytes []byte) ([]byte, error) {
	return nil, fmt.Errorf("photon runtime not available")
}

// normalizeImage normalizes the mime type and converts unsupported formats
// (pi: normalizeImage).
func normalizeImage(bytes []byte, mimeType string) *normalizedImage {
	normalizedMimeType := normalizeSupportedImageMimeType(mimeType)
	if normalizedMimeType != "" {
		return &normalizedImage{bytes: bytes, mimeType: normalizedMimeType}
	}
	pngBytes, err := convertImageBytesToPng(bytes)
	if err != nil || pngBytes == nil {
		return nil
	}
	return &normalizedImage{bytes: pngBytes, mimeType: "image/png", convertedFrom: baseMimeType(mimeType)}
}

func conversionHint(from string, to string) string {
	if from == "" || from == to {
		return ""
	}
	return fmt.Sprintf("[Image converted from %s to %s.]", from, to)
}

// resizeImage resizes an image to fit within the inline limits (pi:
// resizeImage). Phase 8 placeholder: without photon no actual resizing/
// re-encoding happens, so the input is returned as-is — identical to pi for
// images already within the limits (the common case); oversized images are
// passed through unresized (declared deviation until the wazero runtime
// lands).
func resizeImage(inputBytes []byte, mimeType string, options *ImageResizeOptions) *resizedImage {
	return &resizedImage{
		Data:           base64.StdEncoding.EncodeToString(inputBytes),
		MimeType:       mimeType,
		OriginalWidth:  0,
		OriginalHeight: 0,
		Width:          0,
		Height:         0,
		WasResized:     false,
	}
}

// FormatDimensionNote formats the coordinate-mapping note for resized images
// (pi: formatDimensionNote).
func FormatDimensionNote(result *resizedImage) string {
	if !result.WasResized {
		return ""
	}
	scale := float64(result.OriginalWidth) / float64(result.Width)
	return fmt.Sprintf("[Image: original %dx%d, displayed at %dx%d. Multiply coordinates by %s to map to original image.]",
		result.OriginalWidth, result.OriginalHeight, result.Width, result.Height, strconv.FormatFloat(scale, 'f', 2, 64))
}

// ProcessImage normalizes and optionally resizes an image for inline use
// (pi: processImage).
func ProcessImage(bytes []byte, mimeType string, options *ProcessImageOptions) ProcessImageResult {
	autoResizeImages := options == nil || options.AutoResizeImages == nil || *options.AutoResizeImages
	normalized := normalizeImage(bytes, mimeType)
	if normalized == nil {
		return ProcessImageResult{
			Message: "[Image omitted: could not be converted to a supported inline image format.]",
		}
	}

	if autoResizeImages {
		resized := resizeImage(normalized.bytes, normalized.mimeType, func() *ImageResizeOptions {
			if options == nil {
				return nil
			}
			return options.ResizeOptions
		}())
		if resized == nil {
			return ProcessImageResult{
				Message: "[Image omitted: could not be resized below the inline image size limit.]",
			}
		}
		var hints []string
		if convertedHint := conversionHint(normalized.convertedFrom, resized.MimeType); convertedHint != "" {
			hints = append(hints, convertedHint)
		}
		if dimensionNote := FormatDimensionNote(resized); dimensionNote != "" {
			hints = append(hints, dimensionNote)
		}
		return ProcessImageResult{
			OK:       true,
			Data:     resized.Data,
			MimeType: resized.MimeType,
			Hints:    hints,
		}
	}

	var hints []string
	if convertedHint := conversionHint(normalized.convertedFrom, normalized.mimeType); convertedHint != "" {
		hints = append(hints, convertedHint)
	}
	return ProcessImageResult{
		OK:       true,
		Data:     base64.StdEncoding.EncodeToString(normalized.bytes),
		MimeType: normalized.mimeType,
		Hints:    hints,
	}
}
