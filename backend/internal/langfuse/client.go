// Package langfuse is a minimal client for the Langfuse batch ingestion API.
//
// It exists to ship console-agent turn traces to Langfuse without pulling in an
// OpenTelemetry SDK for what is one HTTP POST per turn. The only entry point
// the request path uses is IngestAsync, which is strictly fire-and-forget: it
// runs on its own goroutine with its own background context, recovers from
// panics, and never reports back. Tracing must not be able to slow down or
// break the SSE response the user is waiting on.
package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const ingestPath = "/api/public/ingestion"

const ingestTimeout = 5 * time.Second

const maxResponseBody = 64 << 10

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

var defaultHTTPClient = &http.Client{Timeout: ingestTimeout}

// Event is one envelope in an ingestion batch.
type Event struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Body      any    `json:"body"`
}

const (
	EventTypeTraceCreate       = "trace-create"
	EventTypeObservationCreate = "observation-create"
)

const (
	ObservationTypeSpan       = "SPAN"
	ObservationTypeGeneration = "GENERATION"
	ObservationTypeTool       = "TOOL"
)

const (
	LevelDefault = "DEFAULT"
	LevelWarning = "WARNING"
	LevelError   = "ERROR"
)

// TraceBody is the body of a trace-create event.
type TraceBody struct {
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp,omitempty"`
	Name      string         `json:"name,omitempty"`
	UserID    string         `json:"userId,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
	Input     any            `json:"input,omitempty"`
	Output    any            `json:"output,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
}

// Usage is the token accounting attached to a generation observation.
type Usage struct {
	PromptTokens     int64 `json:"promptTokens,omitempty"`
	CompletionTokens int64 `json:"completionTokens,omitempty"`
	TotalTokens      int64 `json:"totalTokens,omitempty"`
}

// ObservationBody is the body of an observation-create event.
type ObservationBody struct {
	ID                  string         `json:"id"`
	TraceID             string         `json:"traceId"`
	Type                string         `json:"type"`
	Name                string         `json:"name,omitempty"`
	StartTime           string         `json:"startTime,omitempty"`
	EndTime             string         `json:"endTime,omitempty"`
	Input               any            `json:"input,omitempty"`
	Output              any            `json:"output,omitempty"`
	Metadata            map[string]any `json:"metadata,omitempty"`
	Model               string         `json:"model,omitempty"`
	Usage               *Usage         `json:"usage,omitempty"`
	Level               string         `json:"level,omitempty"`
	StatusMessage       string         `json:"statusMessage,omitempty"`
	ParentObservationID string         `json:"parentObservationId,omitempty"`
}

type ingestRequest struct {
	Batch []Event `json:"batch"`
}

type ingestError struct {
	ID      string `json:"id"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}

type ingestResponse struct {
	Successes []struct {
		ID string `json:"id"`
	} `json:"successes"`
	Errors []ingestError `json:"errors"`
}

// Client talks to one Langfuse project. A zero-value or nil Client is a safe
// no-op, which is how the feature stays off when no keys are configured.
type Client struct {
	Host       string
	PublicKey  string
	SecretKey  string
	Enabled    bool
	HTTPClient *http.Client
}

// New builds a client. It is cheap enough to call per turn: the underlying HTTP
// client is shared package-wide so connections are reused.
func New(host, publicKey, secretKey string, enabled bool) *Client {
	return &Client{
		Host:       strings.TrimRight(host, "/"),
		PublicKey:  publicKey,
		SecretKey:  secretKey,
		Enabled:    enabled,
		HTTPClient: defaultHTTPClient,
	}
}

// Configured reports whether this client can actually send anything. Missing
// keys are the normal state on environments where tracing is not wired up.
func (c *Client) Configured() bool {
	return c != nil && c.Enabled && c.Host != "" && c.PublicKey != "" && c.SecretKey != ""
}

// FormatTime renders t the way Langfuse expects: UTC with millisecond
// resolution. Second-resolution timestamps collapse fast tool spans onto each
// other in the UI.
func FormatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

// Ingest posts a batch synchronously and reports the outcome. The request path
// does not call this directly, IngestAsync does; it is exported so the batch
// contract stays testable.
func (c *Client) Ingest(ctx context.Context, batch []Event) error {
	if !c.Configured() || len(batch) == 0 {
		return nil
	}

	body, err := json.Marshal(ingestRequest{Batch: batch})
	if err != nil {
		return fmt.Errorf("langfuse ingestion: encode batch: %w", err)
	}

	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+ingestPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("langfuse ingestion: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.PublicKey, c.SecretKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("langfuse ingestion: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("langfuse ingestion: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed ingestResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	if len(parsed.Errors) > 0 {
		return fmt.Errorf("langfuse ingestion: %d event(s) rejected: %s", len(parsed.Errors), parsed.Errors[0].Message)
	}
	return nil
}

// IngestAsync ships a batch without blocking the caller and without ever
// failing it. It uses its own background context on purpose: the SSE request
// context is cancelled the moment the browser closes the stream, which is
// exactly when the most interesting turns would otherwise lose their trace.
func (c *Client) IngestAsync(batch []Event) {
	if !c.Configured() || len(batch) == 0 {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("langfuse: ingest panicked: %v", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), ingestTimeout)
		defer cancel()
		if err := c.Ingest(ctx, batch); err != nil {
			log.Printf("langfuse: ingest failed: %v", err)
		}
	}()
}
