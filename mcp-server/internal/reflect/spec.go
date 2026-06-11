// Package reflect loads the backend OpenAPI spec and reflects it into MCP tools.
package reflect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
)

// Spec is the loaded, converted OpenAPI 3 document plus the API base path.
type Spec struct {
	Doc      *openapi3.T
	BasePath string // e.g. "/api/v1"
}

// LoadSpec loads the spec from a file path (preferred) or, if path is empty,
// over HTTP from backendURL+"/openapi.json".
//
// The backend emits Swagger 2.0 (via swaggo). kin-openapi only parses OpenAPI
// 3, so we unmarshal as openapi2.T and convert with openapi2conv.ToV3.
func LoadSpec(ctx context.Context, path, backendURL string) (*Spec, error) {
	raw, basePath, err := loadRaw(ctx, path, backendURL)
	if err != nil {
		return nil, err
	}

	var doc2 openapi2.T
	if err := json.Unmarshal(raw, &doc2); err != nil {
		return nil, fmt.Errorf("parse swagger 2.0: %w", err)
	}

	doc3, err := openapi2conv.ToV3(&doc2)
	if err != nil {
		return nil, fmt.Errorf("convert swagger 2.0 -> openapi 3: %w", err)
	}

	if basePath == "" {
		basePath = doc2.BasePath
	}

	return &Spec{Doc: doc3, BasePath: strings.TrimRight(basePath, "/")}, nil
}

func loadRaw(ctx context.Context, path, backendURL string) (raw []byte, basePath string, err error) {
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("read spec file %q: %w", path, err)
		}
		return b, "", nil
	}

	if backendURL == "" {
		return nil, "", fmt.Errorf("no spec source: set MCP_OPENAPI_PATH or BACKEND_URL")
	}

	url := strings.TrimRight(backendURL, "/") + "/openapi.json"
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch spec from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetch spec from %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read spec body: %w", err)
	}
	return b, "", nil
}
