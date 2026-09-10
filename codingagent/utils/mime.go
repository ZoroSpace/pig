// pi: src/utils/mime.ts — image signature sniffing (coding-agent copy; the
// agent package has its own near-identical copy in agent/harness/tools/
// image.go, and pig mirrors the duplication).
package utils

import (
	"os"

	"github.com/ZoroSpace/pig/agent/harness/tools"
)

// ImageTypeSniffBytes is the number of leading bytes read for sniffing (pi:
// IMAGE_TYPE_SNIFF_BYTES).
const ImageTypeSniffBytes = 4100

// DetectSupportedImageMimeType sniffs the buffer for a supported image
// format (pi: detectSupportedImageMimeType); "" = null.
func DetectSupportedImageMimeType(buffer []byte) string {
	return tools.DetectSupportedImageMimeType(buffer)
}

// DetectSupportedImageMimeTypeFromFile sniffs the first bytes of a file (pi:
// detectSupportedImageMimeTypeFromFile).
func DetectSupportedImageMimeTypeFromFile(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer file.Close()
	buffer := make([]byte, ImageTypeSniffBytes)
	n, _ := file.Read(buffer)
	return tools.DetectSupportedImageMimeType(buffer[:n])
}
