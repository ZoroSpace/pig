// Package assets embeds the coding-agent binary assets (pi: files shipped
// next to the Bun-compiled binary — theme/ etc.; pig embeds them so the
// single static binary is self-contained).
package assets

import "embed"

// Theme holds the built-in theme JSON files (pi: theme/dark.json,
// theme/light.json next to the executable).
//
//go:embed theme
var Theme embed.FS
