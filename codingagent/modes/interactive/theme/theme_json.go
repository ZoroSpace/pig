// theme_json.go (pi: theme/theme-json.ts, v0.85.1) — theme JSON validation,
// kept out of `theme.go` on purpose.
//
// pi moves full validation out of theme.ts so that presentations which only
// use built-in themes never pay the typebox import (~17 MB of module graph).
// `interactive-mode` installs this validator; anything that does not simply
// skips validation, as built-in themes already do. In Go the cost is a
// hand-written validator instead of typebox, but the seam is kept 1:1 so the
// consumption shape matches: theme.go's parseThemeJson consults the
// installed validator and otherwise performs a minimal structural check.
package theme

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ThemeJsonValidator validates one theme document (pi: ThemeJsonValidator).
type ThemeJsonValidator = func(label string, json any) (*ThemeJson, error)

// themeJsonValidator is the installed validator (pi: module-level
// themeJsonValidator; nil = no full validation).
var themeJsonValidator ThemeJsonValidator

// SetThemeJsonValidator installs full theme validation (pi:
// setThemeJsonValidator). Without it, documents are accepted as-is, which is
// what built-in themes already do.
func SetThemeJsonValidator(validator ThemeJsonValidator) {
	themeJsonValidator = validator
}

// validateColorValue checks the ColorValue union (string, or integer
// 0-255).
func validateColorValue(path string, value ColorValue) (string, bool) {
	switch v := value.(type) {
	case string:
		return path, true
	case int:
		if v < 0 || v > 255 {
			return path + ": color index must be between 0 and 255", false
		}
		return path, true
	case float64:
		if v != math.Trunc(v) || v < 0 || v > 255 {
			return path + ": color index must be an integer between 0 and 255", false
		}
		return path, true
	}
	return path + ": must be a string or a 256-color index", false
}

// ValidateThemeJson hand-validates the theme schema (pi: validateThemeJson in
// theme-json.ts; pig hand-writes the TypeBox shape). Missing required colors
// keep pi's diagnostic formatting; the theme name may not contain "/".
func ValidateThemeJson(label string, parsed any) (*ThemeJson, error) {
	top, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Invalid theme %q:\n  - /: must be object", label)
	}

	var missingColors []string
	var otherErrors []string

	name, hasName := top["name"]
	if !hasName {
		missingColors = append(missingColors, "name")
	} else if _, ok := name.(string); !ok {
		otherErrors = append(otherErrors, "  - /name: must be string")
	}
	if v, ok := top["$schema"]; ok {
		if _, ok := v.(string); !ok {
			otherErrors = append(otherErrors, "  - /$schema: must be string")
		}
	}
	if v, ok := top["appearance"]; ok && v != nil {
		appearance, ok := v.(string)
		if !ok || (appearance != "dark" && appearance != "light") {
			otherErrors = append(otherErrors, "  - /appearance: must be \"dark\" or \"light\"")
		}
	}
	if v, ok := top["vars"]; ok && v != nil {
		vars, ok := v.(map[string]any)
		if !ok {
			otherErrors = append(otherErrors, "  - /vars: must be object")
		} else {
			keys := make([]string, 0, len(vars))
			for k := range vars {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if msg, ok := validateColorValue("/vars/"+k, vars[k]); !ok {
					otherErrors = append(otherErrors, "  - "+msg)
				}
			}
		}
	}

	colorsRaw, hasColors := top["colors"]
	if !hasColors || colorsRaw == nil {
		// Entire colors object missing: report every required color.
		for _, c := range requiredThemeColors {
			missingColors = append(missingColors, string(c))
		}
		for _, c := range requiredThemeBgs {
			missingColors = append(missingColors, string(c))
		}
	} else {
		colors, ok := colorsRaw.(map[string]any)
		if !ok {
			otherErrors = append(otherErrors, "  - /colors: must be object")
		} else {
			for _, c := range requiredThemeColors {
				if _, ok := colors[string(c)]; !ok {
					missingColors = append(missingColors, string(c))
				}
			}
			for _, c := range requiredThemeBgs {
				if _, ok := colors[string(c)]; !ok {
					missingColors = append(missingColors, string(c))
				}
			}
			keys := make([]string, 0, len(colors))
			for k := range colors {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if msg, ok := validateColorValue("/colors/"+k, colors[k]); !ok {
					otherErrors = append(otherErrors, "  - "+msg)
				}
			}
		}
	}

	if v, ok := top["export"]; ok && v != nil {
		exportSection, ok := v.(map[string]any)
		if !ok {
			otherErrors = append(otherErrors, "  - /export: must be object")
		} else {
			for _, k := range []string{"pageBg", "cardBg", "infoBg"} {
				if val, ok := exportSection[k]; ok {
					if msg, ok := validateColorValue("/export/"+k, val); !ok {
						otherErrors = append(otherErrors, "  - "+msg)
					}
				}
			}
		}
	}

	if len(missingColors) > 0 || len(otherErrors) > 0 {
		errorMessage := fmt.Sprintf("Invalid theme %q:\n", label)
		if len(missingColors) > 0 {
			errorMessage += "\nMissing required color tokens:\n"
			sort.Strings(missingColors)
			for _, c := range missingColors {
				errorMessage += "  - " + c + "\n"
			}
			// pi joins with "\n" so the last entry has no trailing newline
			// before the fixed text; rebuild without the trailing newline.
			errorMessage = strings.TrimSuffix(errorMessage, "\n")
			errorMessage += "\n\nPlease add these colors to your theme's \"colors\" object."
			errorMessage += "\nSee the built-in themes (dark.json, light.json) for reference values."
		}
		if len(otherErrors) > 0 {
			errorMessage += "\n\nOther errors:\n" + strings.Join(otherErrors, "\n")
		}
		return nil, fmt.Errorf("%s", errorMessage)
	}

	// Re-marshal into the typed shape via JSON round-trip.
	raw, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	var themeJson ThemeJson
	if err := json.Unmarshal(raw, &themeJson); err != nil {
		return nil, fmt.Errorf("Failed to parse theme %s: %v", label, err)
	}
	// pi: theme names cannot contain "/" (reserved for automatic light/dark
	// theme settings).
	if err := assertThemeNameIsValid(themeJson.Name); err != nil {
		return nil, err
	}
	return &themeJson, nil
}
