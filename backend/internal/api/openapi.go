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
//	    -g cmd/server/main.go --parseInternal --parseDependency \
//	    --outputTypes json -o internal/api/docs
//
//go:embed docs/swagger.json
var openapiSpec []byte

// ServeOpenAPISpec serves the embedded OpenAPI 2.0 (swagger) spec as JSON.
// Public — no authentication required.
func ServeOpenAPISpec(c *gin.Context) {
	c.Data(http.StatusOK, "application/json; charset=utf-8", openapiSpec)
}
