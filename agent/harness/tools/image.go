// pi: src/harness/tools/image.ts — image signature detection and base64
// encoding.
//
// Segment deviations:
//   - encodeBase64 is byte-exact standard base64; pig delegates to
//     encoding/base64 (StdEncoding) instead of hand-rolling the alphabet.
//   - The typed byte/index helpers use binary.ByteOrder and direct slicing
//     instead of DataView; out-of-range reads read zero in TS (bytes[i] ??
//     0) and are guarded by length checks here, preserving the same results.
package tools

import "encoding/base64"

// pngSignature is the PNG magic (pi: PNG_SIGNATURE).
var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// DetectSupportedImageMimeType sniffs the buffer for a supported image
// format (pi: detectSupportedImageMimeType); "" = unsupported (pi:
// undefined).
func DetectSupportedImageMimeType(buffer []byte) string {
	if startsWithBytes(buffer, []byte{0xff, 0xd8, 0xff}) {
		// pi reads buffer[3] with `?? undefined` semantics: out of range ≠
		// 0xf7 → jpeg.
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if startsWithBytes(buffer, pngSignature) {
		if isPng(buffer) && !isAnimatedPng(buffer) {
			return "image/png"
		}
		return ""
	}
	// pi: GIF87a/GIF89a — a bare "GIF" prefix misclassifies text files
	// beginning with GIF as images (#9755).
	if startsWithAscii(buffer, 0, "GIF87a") || startsWithAscii(buffer, 0, "GIF89a") {
		return "image/gif"
	}
	if startsWithAscii(buffer, 0, "RIFF") && startsWithAscii(buffer, 8, "WEBP") {
		return "image/webp"
	}
	if startsWithAscii(buffer, 0, "BM") && isBmp(buffer) {
		return "image/bmp"
	}
	return ""
}

// EncodeBase64 encodes bytes as standard base64 (pi: encodeBase64).
func EncodeBase64(bytes []byte) string {
	return base64.StdEncoding.EncodeToString(bytes)
}

// isPng checks the IHDR chunk header (pi: isPng).
func isPng(buffer []byte) bool {
	return len(buffer) >= 16 && readUint32BE(buffer, len(pngSignature)) == 13 && startsWithAscii(buffer, 12, "IHDR")
}

// isAnimatedPng detects an acTL chunk before IDAT (pi: isAnimatedPng).
func isAnimatedPng(buffer []byte) bool {
	offset := len(pngSignature)
	for offset+8 <= len(buffer) {
		chunkLength := readUint32BE(buffer, offset)
		chunkTypeOffset := offset + 4
		if startsWithAscii(buffer, chunkTypeOffset, "acTL") {
			return true
		}
		if startsWithAscii(buffer, chunkTypeOffset, "IDAT") {
			return false
		}
		nextOffset := offset + 8 + int(chunkLength) + 4
		if nextOffset <= offset || nextOffset > len(buffer) {
			return false
		}
		offset = nextOffset
	}
	return false
}

// isBmp validates the BMP header fields (pi: isBmp).
func isBmp(buffer []byte) bool {
	if len(buffer) < 26 {
		return false
	}
	declaredFileSize := readUint32LE(buffer, 2)
	pixelDataOffset := readUint32LE(buffer, 10)
	dibHeaderSize := readUint32LE(buffer, 14)
	if declaredFileSize != 0 && declaredFileSize < 26 {
		return false
	}
	if pixelDataOffset < 14+dibHeaderSize {
		return false
	}
	if declaredFileSize != 0 && pixelDataOffset >= declaredFileSize {
		return false
	}

	var colorPlanes uint32
	var bitsPerPixel uint32
	if dibHeaderSize == 12 {
		colorPlanes = readUint16LE(buffer, 22)
		bitsPerPixel = readUint16LE(buffer, 24)
	} else if dibHeaderSize >= 40 && dibHeaderSize <= 124 {
		if len(buffer) < 30 {
			return false
		}
		colorPlanes = readUint16LE(buffer, 26)
		bitsPerPixel = readUint16LE(buffer, 28)
	} else {
		return false
	}
	if colorPlanes != 1 {
		return false
	}
	for _, bits := range []uint32{1, 4, 8, 16, 24, 32} {
		if bitsPerPixel == bits {
			return true
		}
	}
	return false
}

func readUint16LE(buffer []byte, offset int) uint32 {
	if offset+2 > len(buffer) {
		return 0
	}
	return uint32(buffer[offset]) | uint32(buffer[offset+1])<<8
}

func readUint32BE(buffer []byte, offset int) uint32 {
	if offset+4 > len(buffer) {
		return 0
	}
	return uint32(buffer[offset])<<24 | uint32(buffer[offset+1])<<16 | uint32(buffer[offset+2])<<8 | uint32(buffer[offset+3])
}

func readUint32LE(buffer []byte, offset int) uint32 {
	if offset+4 > len(buffer) {
		return 0
	}
	return uint32(buffer[offset]) | uint32(buffer[offset+1])<<8 | uint32(buffer[offset+2])<<16 | uint32(buffer[offset+3])<<24
}

func startsWithBytes(buffer []byte, bytes []byte) bool {
	if len(buffer) < len(bytes) {
		return false
	}
	for i, b := range bytes {
		if buffer[i] != b {
			return false
		}
	}
	return true
}

func startsWithAscii(buffer []byte, offset int, text string) bool {
	if len(buffer) < offset+len(text) {
		return false
	}
	for i := 0; i < len(text); i++ {
		if buffer[offset+i] != text[i] {
			return false
		}
	}
	return true
}
