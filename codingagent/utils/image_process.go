// pi: src/utils/image-process.ts — image normalization/resize pipeline for
// inline model attachments.
//
// Segment deviations (photon/WASM boundary, see §1):
//   - pi runs photon (WASM) for decode/resize/re-encode. pig implements the
//     same pipeline with Go-native codecs instead of porting photon/wazero:
//     stdlib image/jpeg|png|gif plus golang.org/x/image bmp|webp|tiff for
//     decode, golang.org/x/image/draw ApproxBiLinear for resampling
//     (2026-09-22, see docs/fix/2026-09-22-image-tool-413-oversize-and-history-bloat.md;
//     the previous declared deviation — oversized images passed through
//     unresized and convertImageBytesToPng always failing — is retired).
//     ApproxBiLinear over CatmullRom is deliberate: CatmullRom's
//     large-downscale distributor allocates ~srcW*dstH temp buffers
//     (measured 168MB of churn on a 10MP photo vs 1MB for ApproxBiLinear),
//     which defeats the pipeline's memory goal; encoded-size difference at
//     inline-use scales is marginal (302KB vs 247KB on the same photo).
//   - Animated sources (multi-frame GIF) are passed through unchanged rather
//     than flattened: providers take the first frame anyway and re-encoding
//     animation is out of scope.
//   - Animated PNG / animated WebP decode as their first frame; resizing one
//     flattens the animation (photon behaves the same on the inline path).
//   - Decode failures pass the original bytes through unchanged instead of
//     returning null→omitted (pi's resizeImage contract): a file the read
//     tool sniffed as an image is more useful to the model untouched than
//     silently dropped. Only the explicit MaxBytes contract path
//     returns null (→ "[Image omitted: could not be resized ...]"), matching
//     pi.
//   - WebP has no Go encoder (x/image/webp is decode-only), so a resized
//     WebP re-encodes as PNG with the mime type updated; pi re-encodes
//     WebP→WebP.
//   - The worker-thread indirection of image-resize.ts is node-runtime
//     plumbing with no pig counterpart (in-process execution).
package utils

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	xbmp "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	xtiff "golang.org/x/image/tiff"
	xwebp "golang.org/x/image/webp"
)

// pngMagic is the PNG signature (pi: PNG_SIGNATURE, sniffed here rather
// than imported from the harness tools layer).
var pngMagic = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// Resize/re-encode defaults (pi: DEFAULT_OPTIONS in image-resize-core.ts —
// inline provider limits).
const (
	defaultResizeMaxWidth  = 2000
	defaultResizeMaxHeight = 2000
	// defaultResizeMaxBytes is 4.5MB of base64 payload (4718592), headroom
	// below Anthropic's 5MB limit (pi: DEFAULT_MAX_BYTES = 4.5 * 1024 * 1024).
	defaultResizeMaxBytes = 4718592
	// defaultJpegQuality is the re-encode quality for JPEG output (pi:
	// DEFAULT_OPTIONS.jpegQuality 80).
	defaultJpegQuality = 80
	// jpegRetryQuality is the single reduced-quality retry applied when the
	// MaxBytes budget is still exceeded.
	jpegRetryQuality = 55
)

// ProcessImageOptions parameterize ProcessImage (pi: ProcessImageOptions).
type ProcessImageOptions struct {
	// AutoResizeImages resizes images to inline provider limits. Defaults
	// to true; nil = default.
	AutoResizeImages *bool
	// ResizeOptions are optional resize overrides; nil = resizeImage
	// defaults.
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

// ImageResizeOptions carries the resize overrides (pi: ImageResizeOptions in
// image-resize-core.ts).
type ImageResizeOptions struct {
	MaxWidth  *int
	MaxHeight *int
	// MaxBytes is the maximum base64-encoded payload size in bytes (pi:
	// maxBytes; 0/unset = the defaultResizeMaxBytes budget).
	MaxBytes *int
	// JpegQuality is the re-encode quality for JPEG output. 0/unset =
	// defaultJpegQuality.
	JpegQuality *int
}

// ResizedImage is the resize outcome (pi: ResizedImage, exported for the
// test seam).
type ResizedImage struct {
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

// convertImageBytesToPng transcodes unsupported formats to PNG (pi:
// convertImageBytesToPng). The source format is sniffed from the magic
// bytes: BMP, WebP, TIFF, and static GIF decode via Go codecs; animated GIF
// cannot be flattened here and fails like an undecodable format.
func convertImageBytesToPng(data []byte) ([]byte, error) {
	decoded, _, err := decodeImageBytes(data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, decoded); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeImageBytes decodes image bytes by magic-byte sniffing (not the
// claimed mime). ok=false for animated GIF (multi-frame), which callers
// treat as pass-through.
func decodeImageBytes(data []byte) (image.Image, string, error) {
	switch {
	case len(data) >= 3 && data[0] == 'B' && data[1] == 'M':
		img, err := xbmp.Decode(bytes.NewReader(data))
		return img, "image/bmp", err
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		img, err := xwebp.Decode(bytes.NewReader(data))
		return img, "image/webp", err
	case len(data) >= 4 && (string(data[0:4]) == "II*\x00" || string(data[0:4]) == "MM\x00*"):
		img, err := xtiff.Decode(bytes.NewReader(data))
		return img, "image/tiff", err
	case len(data) >= 3 && string(data[0:3]) == "GIF":
		return decodeGif(data)
	case len(data) >= 8 && bytes.Equal(data[0:8], pngMagic):
		img, err := png.Decode(bytes.NewReader(data))
		return img, "image/png", err
	default:
		img, err := jpeg.Decode(bytes.NewReader(data))
		return img, "image/jpeg", err
	}
}

// decodeGif decodes single-frame GIFs; animated GIFs return ok=false so the
// pass-through path preserves the animation.
func decodeGif(data []byte) (image.Image, string, error) {
	frames, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, "image/gif", err
	}
	if len(frames.Image) != 1 {
		return nil, "image/gif", errAnimatedGif
	}
	return frames.Image[0], "image/gif", nil
}

// errAnimatedGif marks animation that pig passes through instead of
// transcoding (see header deviation notes).
var errAnimatedGif = fmt.Errorf("animated image passed through unresized")

// decodeNormalizedImage decodes the normalized inline image for resizing.
// ok=false keeps the caller's pass-through behavior (decode failure or
// animation).
func decodeNormalizedImage(data []byte, mimeType string) (image.Image, bool) {
	var img image.Image
	var err error
	switch mimeType {
	case "image/jpeg":
		img, err = jpeg.Decode(bytes.NewReader(data))
	case "image/png":
		img, err = png.Decode(bytes.NewReader(data))
	case "image/gif":
		img, _, err = decodeGif(data)
	case "image/webp":
		img, err = xwebp.Decode(bytes.NewReader(data))
	default:
		return nil, false
	}
	if err != nil || img == nil {
		return nil, false
	}
	return img, true
}

// normalizeImage normalizes the mime type and converts unsupported formats
// (pi: normalizeImage).
func normalizeImage(data []byte, mimeType string) *normalizedImage {
	normalizedMimeType := normalizeSupportedImageMimeType(mimeType)
	if normalizedMimeType != "" {
		return &normalizedImage{bytes: data, mimeType: normalizedMimeType}
	}
	pngBytes, err := convertImageBytesToPng(data)
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

// passThroughImage mirrors the previous no-op path: original bytes,
// WasResized=false, no hints.
func passThroughImage(inputBytes []byte, mimeType string) *ResizedImage {
	return &ResizedImage{
		Data:     base64.StdEncoding.EncodeToString(inputBytes),
		MimeType: mimeType,
	}
}

// ResizeOptionsFromModel maps a model's catalog resize profile onto the
// processing options (pi: ModelImageResizeOptions — pi passes it straight
// into processImage thanks to TS structural typing; Go needs the explicit
// mapping). nil profile → nil options (resizeImage defaults apply).
func ResizeOptionsFromModel(profile *types.ModelImageResizeOptions) *ImageResizeOptions {
	if profile == nil {
		return nil
	}
	out := &ImageResizeOptions{}
	if profile.MaxWidth != 0 {
		out.MaxWidth = lo.ToPtr(int(profile.MaxWidth))
	}
	if profile.MaxHeight != 0 {
		out.MaxHeight = lo.ToPtr(int(profile.MaxHeight))
	}
	if profile.MaxBytes != 0 {
		out.MaxBytes = lo.ToPtr(int(profile.MaxBytes))
	}
	if profile.JpegQuality != 0 {
		out.JpegQuality = lo.ToPtr(int(profile.JpegQuality))
	}
	return out
}

// resizeImage resizes an image to fit within the inline limits (pi:
// resizeImage / resizeImageInProcess). Downscale-only: images already within
// the limits (dimensions AND base64 payload size) are passed through
// byte-identical (no re-encode, matching pi's early return). Decode failures
// pass through unchanged (see header deviations); an explicit MaxBytes
// budget that cannot be met returns nil → the caller emits pi's "[Image
// omitted: could not be resized below the inline image size limit.]".
func resizeImage(inputBytes []byte, mimeType string, options *ImageResizeOptions) *ResizedImage {
	maxWidth, maxHeight := defaultResizeMaxWidth, defaultResizeMaxHeight
	maxBytes := defaultResizeMaxBytes
	jpegQuality := defaultJpegQuality
	if options != nil {
		if options.MaxWidth != nil && *options.MaxWidth > 0 {
			maxWidth = *options.MaxWidth
		}
		if options.MaxHeight != nil && *options.MaxHeight > 0 {
			maxHeight = *options.MaxHeight
		}
		if options.MaxBytes != nil && *options.MaxBytes > 0 {
			maxBytes = *options.MaxBytes
		}
		if options.JpegQuality != nil && *options.JpegQuality > 0 {
			jpegQuality = *options.JpegQuality
		}
	}

	decoded, ok := decodeNormalizedImage(inputBytes, mimeType)
	if !ok {
		return passThroughImage(inputBytes, mimeType)
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return passThroughImage(inputBytes, mimeType)
	}
	// pi: already within all limits — dimensions AND encoded (base64) size —
	// pass through unchanged.
	if width <= maxWidth && height <= maxHeight && base64.StdEncoding.EncodedLen(len(inputBytes)) < maxBytes {
		return passThroughImage(inputBytes, mimeType)
	}

	scale := math.Min(float64(maxWidth)/float64(width), float64(maxHeight)/float64(height))
	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)
	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), decoded, bounds, xdraw.Src, nil)

	outMimeType := mimeType
	encode := func(quality int) ([]byte, bool) {
		var buf bytes.Buffer
		var err error
		switch mimeType {
		case "image/jpeg":
			err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality})
		case "image/gif":
			err = gif.Encode(&buf, dst, nil)
		default:
			// png, and webp (no Go WebP encoder — header deviation:
			// re-encodes as PNG).
			outMimeType = "image/png"
			err = png.Encode(&buf, dst)
		}
		if err != nil {
			return nil, false
		}
		return buf.Bytes(), true
	}

	encoded, ok := encode(jpegQuality)
	if !ok {
		return passThroughImage(inputBytes, mimeType)
	}
	if base64.StdEncoding.EncodedLen(len(encoded)) > maxBytes {
		if mimeType == "image/jpeg" {
			// One reduced-quality retry within the explicit budget.
			encoded, ok = encode(jpegRetryQuality)
		} else {
			ok = false
		}
		if !ok || base64.StdEncoding.EncodedLen(len(encoded)) > maxBytes {
			return nil
		}
	}

	return &ResizedImage{
		Data:           base64.StdEncoding.EncodeToString(encoded),
		MimeType:       outMimeType,
		OriginalWidth:  width,
		OriginalHeight: height,
		Width:          newWidth,
		Height:         newHeight,
		WasResized:     true,
	}
}

// FormatDimensionNote formats the coordinate-mapping note for resized images
// (pi: formatDimensionNote).
func FormatDimensionNote(result *ResizedImage) string {
	if !result.WasResized {
		return ""
	}
	scale := float64(result.OriginalWidth) / float64(result.Width)
	return fmt.Sprintf("[Image: original %dx%d, displayed at %dx%d. Multiply coordinates by %s to map to original image.]",
		result.OriginalWidth, result.OriginalHeight, result.Width, result.Height, strconv.FormatFloat(scale, 'f', 2, 64))
}

// ResizeImageFn is the resizeImage entry used by ProcessImage; package-level
// var for tests (pig-only seam; pi mocks the image-resize module via
// vi.mock).
var ResizeImageFn = resizeImage

// ProcessImage normalizes and optionally resizes an image for inline use
// (pi: processImage).
func ProcessImage(data []byte, mimeType string, options *ProcessImageOptions) ProcessImageResult {
	autoResizeImages := options == nil || options.AutoResizeImages == nil || *options.AutoResizeImages
	normalized := normalizeImage(data, mimeType)
	if normalized == nil {
		return ProcessImageResult{
			Message: "[Image omitted: could not be converted to a supported inline image format.]",
		}
	}

	if autoResizeImages {
		resized := ResizeImageFn(normalized.bytes, normalized.mimeType, func() *ImageResizeOptions {
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
