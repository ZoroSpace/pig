// pi: src/cli/list-models.ts — list available models with optional fuzzy
// search.
package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

// formatTokenCount formats a number as human-readable (e.g., 200000 → "200K",
// 1000000 → "1M") (pi: formatTokenCount).
func formatTokenCount(count int64) string {
	switch {
	case count >= 1_000_000:
		millions := float64(count) / 1_000_000
		if millions == float64(int64(millions)) {
			return fmt.Sprintf("%dM", int64(millions))
		}
		return fmt.Sprintf("%.1fM", millions)
	case count >= 1_000:
		thousands := float64(count) / 1_000
		if thousands == float64(int64(thousands)) {
			return fmt.Sprintf("%dK", int64(thousands))
		}
		return fmt.Sprintf("%.1fK", thousands)
	default:
		return fmt.Sprintf("%d", count)
	}
}

// modelListRow is one rendered table row (pi: the rows literal).
type modelListRow struct {
	provider string
	model    string
	context  string
	maxOut   string
	thinking string
	images   string
}

// ListModels prints the available models, optionally filtered by a fuzzy
// search pattern (pi: listModels).
func ListModels(ctx context.Context, modelRuntime *core.ModelRuntime, searchPattern string) error {
	loadError := modelRuntime.GetError()
	if loadError != "" {
		fmt.Fprintf(os.Stderr, "Warning: errors loading models.json:\n%s\n", loadError)
	}

	models, err := modelRuntime.GetAvailable(ctx)
	if err != nil {
		return err
	}
	allModels := append([]ai.Model(nil), models...)

	if len(allModels) == 0 {
		fmt.Println(core.FormatNoModelsAvailableMessage())
		return nil
	}

	// Apply fuzzy filter if search pattern provided
	filteredModels := allModels
	if searchPattern != "" {
		filteredModels = tui.FuzzyFilter(allModels, searchPattern, func(m ai.Model) string {
			return string(m.Provider) + " " + m.ID
		})
	}

	if len(filteredModels) == 0 {
		fmt.Printf("No models matching %q\n", searchPattern)
		return nil
	}

	// Sort by provider, then by model id (pi: localeCompare — pig sorts by
	// byte order, same order for the ASCII provider/model ids).
	sort.SliceStable(filteredModels, func(i, j int) bool {
		if c := strings.Compare(string(filteredModels[i].Provider), string(filteredModels[j].Provider)); c != 0 {
			return c < 0
		}
		return strings.Compare(filteredModels[i].ID, filteredModels[j].ID) < 0
	})

	// Calculate column widths
	rows := make([]modelListRow, len(filteredModels))
	for i, m := range filteredModels {
		rows[i] = modelListRow{
			provider: string(m.Provider),
			model:    m.ID,
			context:  formatTokenCount(m.ContextWindow),
			maxOut:   formatTokenCount(m.MaxTokens),
			thinking: boolYesNo(m.Reasoning),
			images:   boolYesNo(modelHasImageInput(m)),
		}
	}

	headers := modelListRow{
		provider: "provider",
		model:    "model",
		context:  "context",
		maxOut:   "max-out",
		thinking: "thinking",
		images:   "images",
	}

	widths := modelListWidths{
		provider: len(headers.provider),
		model:    len(headers.model),
		context:  len(headers.context),
		maxOut:   len(headers.maxOut),
		thinking: len(headers.thinking),
		images:   len(headers.images),
	}
	for _, r := range rows {
		widths.provider = maxInt(widths.provider, len(r.provider))
		widths.model = maxInt(widths.model, len(r.model))
		widths.context = maxInt(widths.context, len(r.context))
		widths.maxOut = maxInt(widths.maxOut, len(r.maxOut))
		widths.thinking = maxInt(widths.thinking, len(r.thinking))
		widths.images = maxInt(widths.images, len(r.images))
	}

	// Print header
	fmt.Println(formatModelRow(headers, widths))

	// Print rows
	for _, row := range rows {
		fmt.Println(formatModelRow(row, widths))
	}
	return nil
}

// modelListWidths carries the computed column widths (pi: the widths
// literal).
type modelListWidths struct {
	provider int
	model    int
	context  int
	maxOut   int
	thinking int
	images   int
}

// formatModelRow renders one padded table line (pi: the two join("  ")
// pipelines).
func formatModelRow(row modelListRow, widths modelListWidths) string {
	return strings.Join([]string{
		padEnd(row.provider, widths.provider),
		padEnd(row.model, widths.model),
		padEnd(row.context, widths.context),
		padEnd(row.maxOut, widths.maxOut),
		padEnd(row.thinking, widths.thinking),
		padEnd(row.images, widths.images),
	}, "  ")
}

// boolYesNo renders a JS truthiness flag (pi: cond ? "yes" : "no").
func boolYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// modelHasImageInput reports whether the model accepts image input (pi:
// m.input.includes("image")).
func modelHasImageInput(m ai.Model) bool {
	for _, input := range m.Input {
		if input == ai.ModelInputImage {
			return true
		}
	}
	return false
}

// padEnd pads a string with trailing spaces to width (pi: padEnd).
func padEnd(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(value))
}

// maxInt is a local max helper for the column widths.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
