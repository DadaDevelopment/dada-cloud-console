package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const scorePath = "/api/public/scores"

const scoreRetries = 4

const (
	ScoreNumeric = "NUMERIC"
	ScoreBoolean = "BOOLEAN"
)

// Score is one evaluation value attached to a trace or, when ObservationID is
// set, to one observation of it.
type Score struct {
	ID            string         `json:"id,omitempty"`
	TraceID       string         `json:"traceId"`
	ObservationID string         `json:"observationId,omitempty"`
	Name          string         `json:"name"`
	Value         float64        `json:"value"`
	DataType      string         `json:"dataType,omitempty"`
	Comment       string         `json:"comment,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type rateLimited struct {
	name       string
	retryAfter time.Duration
	raw        string
}

func (e *rateLimited) Error() string {
	return fmt.Sprintf("langfuse score %s: status 429: %s", e.name, e.raw)
}

// CreateScores stores the scores one by one and returns the first error after
// every score had its chance. There is no batch path: the batch ingestion
// endpoint acknowledges score-create events for organisations created after
// 2026-09-16 and then drops them, and the OTLP path carries no scores.
func (c *Client) CreateScores(ctx context.Context, scores []Score) error {
	var first error
	for _, s := range scores {
		if err := c.CreateScore(ctx, s); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// CreateScore posts one score, waiting out a 429 as long as the context
// allows. A BOOLEAN score carries 1 for true and 0 for false, the only values
// the API accepts for that data type.
func (c *Client) CreateScore(ctx context.Context, score Score) error {
	if !c.Configured() {
		return nil
	}
	if score.TraceID == "" || score.Name == "" {
		return fmt.Errorf("langfuse score: traceId and name are required")
	}
	var err error
	for attempt := 0; attempt < scoreRetries; attempt++ {
		err = c.postScore(ctx, score)
		rl, ok := err.(*rateLimited)
		if !ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rl.retryAfter):
		}
	}
	return err
}

func (c *Client) postScore(ctx context.Context, score Score) error {
	status, raw, err := c.post(ctx, scorePath, score)
	if err != nil {
		return err
	}
	if status == http.StatusTooManyRequests {
		var body struct {
			Details struct {
				RetryAfterSeconds float64 `json:"retryAfterSeconds"`
			} `json:"details"`
		}
		_ = json.Unmarshal(raw, &body)
		wait := time.Duration(body.Details.RetryAfterSeconds*float64(time.Second)) + time.Second
		return &rateLimited{name: score.Name, retryAfter: wait, raw: strings.TrimSpace(string(raw))}
	}
	if status >= 400 {
		return fmt.Errorf("langfuse score %s: status %d: %s", score.Name, status, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, payload any) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("langfuse score: encode: %w", err)
	}
	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("langfuse score: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.PublicKey, c.SecretKey)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("langfuse score: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	return resp.StatusCode, raw, nil
}
