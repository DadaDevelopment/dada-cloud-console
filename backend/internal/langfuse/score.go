package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const scorePath = "/api/public/scores"

const (
	ScoreNumeric = "NUMERIC"
	ScoreBoolean = "BOOLEAN"
)

// Score is one evaluation value attached to a trace or, when ObservationID is
// set, to one observation of it.
type Score struct {
	ID            string  `json:"id,omitempty"`
	TraceID       string  `json:"traceId"`
	ObservationID string  `json:"observationId,omitempty"`
	Name          string  `json:"name"`
	Value         float64 `json:"value"`
	DataType      string  `json:"dataType,omitempty"`
	Comment       string  `json:"comment,omitempty"`
}

// CreateScore posts one score. A BOOLEAN score carries 1 for true and 0 for
// false, the only values the API accepts for that data type.
func (c *Client) CreateScore(ctx context.Context, score Score) error {
	if !c.Configured() {
		return nil
	}
	if score.TraceID == "" || score.Name == "" {
		return fmt.Errorf("langfuse score: traceId and name are required")
	}
	body, err := json.Marshal(score)
	if err != nil {
		return fmt.Errorf("langfuse score: encode: %w", err)
	}
	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+scorePath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("langfuse score: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.PublicKey, c.SecretKey)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("langfuse score: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("langfuse score %s: status %d: %s", score.Name, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
