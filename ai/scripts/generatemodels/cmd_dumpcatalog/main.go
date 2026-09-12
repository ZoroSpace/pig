// Dump the builtin google/google-vertex catalogs as JSON for comparison
// against pi's data/*.json (C4 verification probe).
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ZoroSpace/pig/ai"
)

func main() {
	out := map[string]map[string]ai.Model{}
	for _, provider := range []ai.ProviderID{"google", "google-vertex"} {
		m := map[string]ai.Model{}
		for id, model := range ai.MODELS[provider] {
			m[id] = model
		}
		out[string(provider)] = m
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
