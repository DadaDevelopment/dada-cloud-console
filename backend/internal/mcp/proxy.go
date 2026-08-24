package mcp

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

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandler matches the SDK's AddTool handler signature.
type ToolHandler func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error)

// proxyClient carries no Timeout of its own: a client-wide deadline would apply
// the same bound to a listing and to a promotion, and those differ by two orders
// of magnitude. The bound is set per call instead, from proxyDeadline.
var proxyClient = &http.Client{}

// proxyTimeout is the bound for an ordinary tool call. Every verb this API
// exposes either answers inside the latency budget or hands back a 202 with an
// operation to poll, so a minute is already generous.
const proxyTimeout = 60 * time.Second

// proxySlowTimeout is the bound for the verbs that do their work inline instead
// of behind an operation. Crystallization manifests a box's whole delta, streams
// it into a fresh workload, waits for that workload to come up and then verifies
// it file by file — minutes of honest work in one request. Under the ordinary
// bound the agent got "context deadline exceeded" every time and the feature was
// unreachable through MCP no matter how well it worked.
const proxySlowTimeout = 15 * time.Minute

// slowTools names the verbs that get proxySlowTimeout. It is a list rather than a
// heuristic so that making a tool slow is a decision someone writes down.
var slowTools = map[string]bool{"crystallizeBox": true}

// proxyDeadline picks the bound for one tool call.
func proxyDeadline(toolName string) time.Duration {
	if slowTools[toolName] {
		return proxySlowTimeout
	}
	return proxyTimeout
}

// bearerKey is a context key for the inbound bearer token.
type bearerKey struct{}

// WithBearer stashes the raw Authorization header value in ctx.
func WithBearer(ctx context.Context, bearer string) context.Context {
	return context.WithValue(ctx, bearerKey{}, bearer)
}

// BearerFromContext retrieves the bearer stored by WithBearer.
func BearerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(bearerKey{}).(string)
	return v
}

// MakeHandler builds the proxy handler for a generated tool. Each tool call:
//   - parses args (flat JSON object)
//   - substitutes path params
//   - appends query params
//   - JSON-encodes remaining props as body
//   - forwards Authorization from ctx to the backend request
//   - maps the response to a CallToolResult
//
// backendURL is the base URL for self-proxy (e.g. "http://127.0.0.1:8080").
func MakeHandler(g GeneratedTool, backendURL, basePath string) ToolHandler {
	backendURL = strings.TrimRight(backendURL, "/")
	basePath = strings.TrimRight(basePath, "/")

	pathSet := toSet(g.PathParams)
	querySet := toSet(g.QueryParams)

	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		args := map[string]any{}
		if raw := req.Params.Arguments; len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return errResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
		}

		applyToolDefaults(g.Name, args)
		applySubjectAliases(g, args)
		if msg := resolveAddressArgs(ctx, g, args, backendURL, basePath); msg != "" {
			return errResult(msg), nil
		}

		filledPath := g.PathTemplate
		for _, name := range g.PathParams {
			val, ok := args[name]
			if !ok {
				return errResult(missingParamMessage(g, name, args)), nil
			}
			filledPath = strings.ReplaceAll(filledPath, "{"+name+"}", url.PathEscape(fmt.Sprint(val)))
		}

		q := url.Values{}
		for _, name := range g.QueryParams {
			if val, ok := args[name]; ok {
				q.Set(name, fmt.Sprint(val))
			}
		}

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

		callCtx, cancel := context.WithTimeout(ctx, proxyDeadline(g.Name))
		defer cancel()

		httpReq, err := http.NewRequestWithContext(callCtx, g.Method, fullURL, bodyReader)
		if err != nil {
			return errResult(fmt.Sprintf("build request: %v", err)), nil
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if bearer := BearerFromContext(ctx); bearer != "" {
			httpReq.Header.Set("Authorization", bearer)
		}

		resp, err := proxyClient.Do(httpReq)
		if err != nil {
			return errResult(fmt.Sprintf("backend error (transient), retry: %v", err)), nil
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			respBody = slimResponse(g.Name, respBody)
		}
		return mapResponse(resp.StatusCode, respBody), nil
	}
}

// toolFailurePreamble frames a failed tool call for the model that reads it.
// A bare backend error string is indistinguishable from a finding about the
// user's application, and the model relays it as one: on 2026-08-11 the 409
// "this environment has no AppServer attached" was reported to a live user as
// the cause of their outage, twice, while their app ran in Kubernetes where an
// AppServer plays no part at all. The preamble says what the text actually is,
// so the model has to treat it as a dead end for this tool rather than as a
// fact about the user.
const toolFailurePreamble = "TOOL CALL FAILED. The text below describes this tool call itself — it is not a diagnosis of the user's application, not a cause of any outage, and must never be relayed to the user as a finding about their app or infrastructure. Read it as: this tool cannot answer here. Use a different tool, or tell the user you could not determine the answer.\n\n"

func mapResponse(status int, body []byte) *sdkmcp.CallToolResult {
	switch {
	case status >= 200 && status < 300:
		text := string(body)
		if status == http.StatusAccepted {
			text = "Operation queued — poll the getOperation tool with the returned operation id to track it to a terminal status.\n\n" + text
		}
		return textResult(text, false)
	case status >= 400 && status < 500:
		return textResult(toolFailurePreamble+string(body), true)
	default:
		return textResult(fmt.Sprintf("%sbackend error (transient), retry: status %d\n%s", toolFailurePreamble, status, string(body)), true)
	}
}

func textResult(text string, isError bool) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: isError,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}},
	}
}

func errResult(msg string) *sdkmcp.CallToolResult {
	return textResult(toolFailurePreamble+msg, true)
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}
