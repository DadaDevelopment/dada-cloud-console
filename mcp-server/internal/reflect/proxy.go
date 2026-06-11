package reflect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dada-tuda/console/mcp-server/internal/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandler matches the SDK's non-generic AddTool handler signature.
type ToolHandler func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error)

var httpClient = &http.Client{Timeout: 60 * time.Second}

// MakeHandler builds the proxy handler for a generated tool. On call it:
//   - parses tool args (a flat JSON object),
//   - substitutes path params into the path template,
//   - appends query params,
//   - JSON-encodes the remaining (body) props as the request body,
//   - sets Authorization from the bearer carried in ctx (auth passthrough),
//   - executes against backendURL+basePath+path,
//   - maps the response to a CallToolResult.
//
// The bearer is read from ctx (see auth.BearerFromContext). The SDK's streamable
// HTTP transport does NOT propagate the per-request http.Request context into
// tool-call handler ctx (messages are dispatched on the session's run loop), but
// it DOES expose the per-request headers via req.GetExtra().Header. main.go wraps
// this handler to read that header and inject it into ctx, so the proxy itself
// stays transport-agnostic and unit-testable by injecting ctx directly.
func MakeHandler(g GeneratedTool, backendURL, basePath string) ToolHandler {
	backendURL = strings.TrimRight(backendURL, "/")
	basePath = strings.TrimRight(basePath, "/")

	pathSet := toSet(g.PathParams)
	querySet := toSet(g.QueryParams)

	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if raw := req.Params.Arguments; len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return errResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
		}

		// Fill path params.
		filledPath := g.PathTemplate
		for _, name := range g.PathParams {
			val, ok := args[name]
			if !ok {
				return errResult(fmt.Sprintf("missing required path parameter %q", name)), nil
			}
			filledPath = strings.ReplaceAll(filledPath, "{"+name+"}", url.PathEscape(fmt.Sprint(val)))
		}

		// Query params.
		q := url.Values{}
		for _, name := range g.QueryParams {
			if val, ok := args[name]; ok {
				q.Set(name, fmt.Sprint(val))
			}
		}

		// Body = everything that isn't a path or query param.
		var bodyReader io.Reader
		if g.Method != http.MethodGet {
			body := map[string]any{}
			for k, v := range args {
				if pathSet[k] || querySet[k] {
					continue
				}
				body[k] = v
			}
			if len(body) > 0 {
				b, err := json.Marshal(body)
				if err != nil {
					return errResult(fmt.Sprintf("encode body: %v", err)), nil
				}
				bodyReader = bytes.NewReader(b)
			}
		}

		fullURL := backendURL + basePath + filledPath
		if enc := q.Encode(); enc != "" {
			fullURL += "?" + enc
		}

		httpReq, err := http.NewRequestWithContext(ctx, g.Method, fullURL, bodyReader)
		if err != nil {
			return errResult(fmt.Sprintf("build request: %v", err)), nil
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if bearer := auth.BearerFromContext(ctx); bearer != "" {
			httpReq.Header.Set("Authorization", bearer)
		}

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return errResult(fmt.Sprintf("backend error (transient), retry: %v", err)), nil
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		return mapResponse(resp.StatusCode, respBody), nil
	}
}

func mapResponse(status int, body []byte) *mcp.CallToolResult {
	switch {
	case status >= 200 && status < 300:
		text := string(body)
		if status == http.StatusAccepted {
			text = "Operation queued — poll the getOperation tool with the returned operation id to track it to a terminal status.\n\n" + text
		}
		return textResult(text, false)
	case status >= 400 && status < 500:
		// Tool-level error: pass the backend's JSON message through verbatim so
		// the agent can correct and retry. NOT a Go transport error.
		return textResult(string(body), true)
	default: // 5xx
		return textResult(fmt.Sprintf("backend error (transient), retry: status %d\n%s", status, string(body)), true)
	}
}

func textResult(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: isError,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func errResult(msg string) *mcp.CallToolResult {
	return textResult(msg, true)
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}
