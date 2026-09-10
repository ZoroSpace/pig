// pi: harness/session/jsonl/codec.ts — header parsing/validation for both
// the current format-4 header and the legacy v3 session header.
package jsonl

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// maxSafeInteger mirrors Number.MAX_SAFE_INTEGER.
const maxSafeInteger = float64(1<<53 - 1)

// LegacyV3SessionHeader is the v1→v3 legacy header (pi:
// LegacyV3SessionHeader).
type LegacyV3SessionHeader struct {
	Type      string `json:"type"` // "session"
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"` // ISO string
	Cwd       string `json:"cwd"`
	// ParentSession is the legacy parent session file path.
	ParentSession *string `json:"parentSession,omitempty"`
}

// isRecord reports whether the decoded value is a JSON object.
func isRecord(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

// isSafeIntegerAtLeast mirrors Number.isSafeInteger.
func isSafeIntegerAtLeast(value any, minimum float64) bool {
	number, ok := value.(float64)
	if !ok {
		return false
	}
	return number >= minimum && number <= maxSafeInteger && number == math.Trunc(number)
}

// IsLegacyV3SessionHeader validates the legacy header shape (pi:
// isLegacyV3SessionHeader).
func IsLegacyV3SessionHeader(value any) bool {
	record, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if record["type"] != "session" {
		return false
	}
	version, ok := record["version"].(float64)
	if !ok || version != 3 {
		return false
	}
	id, ok := record["id"].(string)
	if !ok {
		return false
	}
	cwd, ok := record["cwd"].(string)
	if !ok {
		return false
	}
	timestamp, ok := record["timestamp"].(string)
	if !ok {
		return false
	}
	if _, err := time.Parse(time.RFC3339, timestamp); err != nil {
		return false
	}
	_ = id
	_ = cwd
	if parent, exists := record["parentSession"]; exists && parent != nil {
		if _, ok := parent.(string); !ok {
			return false
		}
	}
	return true
}

// IsJsonlStorageHeader validates the v4 header shape (pi:
// isJsonlStorageHeader).
func IsJsonlStorageHeader(value any) bool {
	record, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if record["kind"] != "header" {
		return false
	}
	if v, ok := record["v"].(float64); !ok || v != JSONLFormatVersion {
		return false
	}
	if _, ok := record["id"].(string); !ok {
		return false
	}
	if _, ok := record["cwd"].(string); !ok {
		return false
	}
	if !isSafeIntegerAtLeast(record["storageVersion"], 1) {
		return false
	}
	if !isSafeIntegerAtLeast(record["createdAt"], 0) {
		return false
	}
	if nextSeq, exists := record["nextSeq"]; exists && nextSeq != nil {
		if !isSafeIntegerAtLeast(nextSeq, 1) {
			return false
		}
	}
	if parentSessionID, exists := record["parentSessionId"]; exists && parentSessionID != nil {
		if _, ok := parentSessionID.(string); !ok {
			return false
		}
	}
	if legacyPath, exists := record["legacyParentSessionPath"]; exists && legacyPath != nil {
		if _, ok := legacyPath.(string); !ok {
			return false
		}
	}
	return true
}

// JsonlParsedSessionHeader is one parsed header line (pi:
// JsonlParsedSessionHeader). Format: "v4" | "v3-legacy".
type JsonlParsedSessionHeader struct {
	Format string
	// V4 is set when Format is "v4".
	V4 *JsonlStorageHeader
	// V3Legacy is set when Format is "v3-legacy".
	V3Legacy *LegacyV3SessionHeader
}

// ParseJsonlSessionHeader parses one header line (pi:
// parseJsonlSessionHeader).
func ParseJsonlSessionHeader(line string) (JsonlParsedSessionHeader, error) {
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return JsonlParsedSessionHeader{}, fmt.Errorf("Invalid JSONL session header: not valid JSON")
	}
	if IsJsonlStorageHeader(value) {
		var header JsonlStorageHeader
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			return JsonlParsedSessionHeader{}, err
		}
		return JsonlParsedSessionHeader{Format: "v4", V4: &header}, nil
	}
	if IsLegacyV3SessionHeader(value) {
		var header LegacyV3SessionHeader
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			return JsonlParsedSessionHeader{}, err
		}
		return JsonlParsedSessionHeader{Format: "v3-legacy", V3Legacy: &header}, nil
	}
	return JsonlParsedSessionHeader{}, fmt.Errorf("Unsupported JSONL session header")
}

// unusedStrconv keeps strconv available for header number formatting tests.
var _ = strconv.Itoa
var _ = strings.TrimSpace
