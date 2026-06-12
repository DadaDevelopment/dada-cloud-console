// Package mcp provides a reflective MCP server embedded in the backend binary.
// Tools are auto-generated from the backend's own OpenAPI spec (embedded at
// build time), and each tool call proxies to the backend's /api/v1 handlers
// via localhost HTTP — so auth, logging, and all middleware apply unchanged.
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
)

// Spec is the loaded, converted OpenAPI 3 document plus the API base path.
type Spec struct {
	Doc      *openapi3.T
	BasePath string
}

// ParseSpec converts a Swagger 2.0 JSON blob into an OpenAPI 3 document.
// The backend embeds docs/swagger.json (Swagger 2.0 from swaggo); this
// converts it once at server start.
func ParseSpec(raw []byte) (*Spec, error) {
	var doc2 openapi2.T
	if err := json.Unmarshal(raw, &doc2); err != nil {
		return nil, fmt.Errorf("parse swagger 2.0: %w", err)
	}
	doc3, err := openapi2conv.ToV3(&doc2)
	if err != nil {
		return nil, fmt.Errorf("convert swagger 2.0 -> openapi 3: %w", err)
	}
	return &Spec{
		Doc:      doc3,
		BasePath: strings.TrimRight(doc2.BasePath, "/"),
	}, nil
}
