// Package types is the canonical home of pi's message model, stream event
// protocol, provider/model/compat types, and image-generation types (pi:
// src/types.ts). It is a leaf: it imports nothing from this module except
// telemetry. Runtime code lives in package ai; package ai re-exports this
// package's surface via index.go (pi: index.ts re-exports types.ts).
package types
