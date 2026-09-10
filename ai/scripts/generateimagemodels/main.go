// ai/scripts/generateimagemodels (pi: packages/ai/scripts/generate-image-models.ts) —
// fetches the OpenRouter image-model catalog (output_modalities=image) and
// regenerates ai/image_models_generated.go.
//
// Run: cd ai/scripts/generateimagemodels && go run .
// Output: ai/image_models_generated.go (overwritten in place).
//
// model-data.ts / check-model-data.ts are NOT ported here (or in
// ai/scripts/generatemodels): their validation targets pi's per-provider
// providers/*.models.ts shards plus providers/data/*.json and .manifest.json,
// a layout pig deliberately does not use — the generators emit one file each
// (see the ai/models_generated.go header comment). If the data/manifest layout
// is ever adopted, port model-data.ts alongside it.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	strict := false
	for _, arg := range os.Args[1:] {
		if arg != "--strict" {
			fmt.Fprintf(os.Stderr, "Unknown argument: %s\n", arg)
			os.Exit(1)
		}
		strict = true
	}
	if err := generateImageModels(context.Background(), strict); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// generateImageModels is the pi generateImageModels() port: fetch, emit,
// write.
func generateImageModels(ctx context.Context, strict bool) error {
	models, err := fetchOpenRouterImageModels(ctx, strict)
	if err != nil {
		return err
	}
	content, err := generateImageModelsFile(models)
	if err != nil {
		return err
	}
	outPath := filepath.Join(sourceDir(), "..", "..", "image_models_generated.go")
	if err := os.WriteFile(outPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("Generated %s (%d image models)\n", outPath, len(models))
	return nil
}

// sourceDir resolves the generator's source directory so the output path is
// stable regardless of the working directory.
func sourceDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filename)
}
