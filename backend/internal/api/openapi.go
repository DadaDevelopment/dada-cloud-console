package api

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// openapiSpec is the swagger.json generated from the handler annotations by
// `swag init` (see the Makefile / docs in the repo). It is embedded so the
// running server can serve its own spec — the reflective MCP server consumes
// this to expose each operation as an agent-facing tool.
//
// Regenerate with:
//
//	go run github.com/swaggo/swag/cmd/swag@v1.16.4 init \
//	    -g cmd/server/main.go --parseInternal \
//	    --outputTypes go,json,yaml -o internal/api/docs
//
// NOTE: --parseDependency is deliberately absent, and this comment used to carry
// it. With swag 1.16.4 that flag renames every definition from `api.foo` to
// `internal_api.foo`, so following the old recipe rewrote ~600 lines of spec that
// had nothing to do with the change being made. All three outputs are listed
// because all three files are committed: swagger.json is embedded above,
// TestOpenAPICoverage reads it, and internal/mcp reflects tools from it.
//
//go:embed docs/swagger.json
var openapiSpec []byte

// ServeOpenAPISpec serves the embedded OpenAPI 2.0 (swagger) spec as JSON.
// Public — no authentication required.
func ServeOpenAPISpec(c *gin.Context) {
	c.Data(http.StatusOK, "application/json; charset=utf-8", openapiSpec)
}

// EmbeddedSpec returns the raw embedded swagger.json bytes. Used by the
// embedded MCP server to reflect tools at startup without an HTTP round-trip.
func EmbeddedSpec() []byte { return openapiSpec }
