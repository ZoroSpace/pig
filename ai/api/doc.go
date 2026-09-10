// Package api will host the wire-protocol adapters (pi: src/api/*.ts,
// one file per API). They are a leaf above package ai: adapters implement
// ai.ProviderStreams against the exported ai API, so the subpackage can
// import ai without a cycle. api/lazy.ts is the one exception: models.go
// consumes it, so it lives in package ai as lazy.go.
package api
