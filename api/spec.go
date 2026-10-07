// Package api holds the Threavia client API contract.
//
// The OpenAPI document is the contract, not a description written after the
// fact: Core serves it, the TypeScript client is generated from it, and a route
// the server registers without describing it here fails the test that holds the
// two together.
package api

import _ "embed"

// OpenAPI is the client API document, in YAML.
//
//go:embed openapi.yaml
var OpenAPI []byte
