package theme

import (
	"strings"
	"testing"
)

func validThemeDoc() map[string]any {
	colors := map[string]any{}
	for _, c := range requiredThemeColors {
		colors[string(c)] = "#000000"
	}
	for _, b := range requiredThemeBgs {
		colors[string(b)] = "#000000"
	}
	return map[string]any{"name": "test", "colors": colors}
}

// TestValidateThemeJsonStrictUnknownKeys ports pi #9880's strict
// additionalProperties: false behavior (top level, colors, export).
func TestValidateThemeJsonStrictUnknownKeys(t *testing.T) {
	if _, err := ValidateThemeJson("test", validThemeDoc()); err != nil {
		t.Fatalf("valid theme rejected: %v", err)
	}

	t.Run("unknown top-level key rejected", func(t *testing.T) {
		doc := validThemeDoc()
		doc["unsupported"] = true
		_, err := ValidateThemeJson("test", doc)
		if err == nil || !strings.Contains(err.Error(), "/unsupported") {
			t.Fatalf("want /unsupported error, got %v", err)
		}
	})

	t.Run("unknown color token rejected", func(t *testing.T) {
		doc := validThemeDoc()
		doc["colors"].(map[string]any)["bogus"] = "#ffffff"
		_, err := ValidateThemeJson("test", doc)
		if err == nil || !strings.Contains(err.Error(), "/colors/bogus") {
			t.Fatalf("want /colors/bogus error, got %v", err)
		}
	})

	t.Run("unknown export key rejected", func(t *testing.T) {
		doc := validThemeDoc()
		doc["export"] = map[string]any{"nope": "#ffffff"}
		_, err := ValidateThemeJson("test", doc)
		if err == nil || !strings.Contains(err.Error(), "/export/nope") {
			t.Fatalf("want /export/nope error, got %v", err)
		}
	})

	t.Run("pig-only imagePlaceholder token is accepted", func(t *testing.T) {
		doc := validThemeDoc()
		doc["colors"].(map[string]any)[ColorImagePlaceholder] = "#123456"
		if _, err := ValidateThemeJson("test", doc); err != nil {
			t.Fatalf("pig-only token rejected: %v", err)
		}
	})

	t.Run("name with slash rejected", func(t *testing.T) {
		doc := validThemeDoc()
		doc["name"] = "a/b"
		_, err := ValidateThemeJson("test", doc)
		if err == nil || !strings.Contains(err.Error(), "cannot contain") {
			t.Fatalf("want slash-name error, got %v", err)
		}
	})
}
