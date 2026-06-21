package logsearch

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

// WriteClient indexes application log documents into Elasticsearch. It writes to
// a dated index (<baseIndex>-YYYY.MM.DD) so the existing read pattern
// "dada-app-logs-*" catches them and the LogsViewer keeps working unchanged.
// New returns nil when unconfigured so callers treat ingestion as disabled.
type WriteClient struct {
	baseURL    string
	baseIndex  string
	apiKey     string
	httpClient *http.Client
}

// NewWriteClient builds an ES write client. baseIndex defaults to
// "dada-app-logs" (read pattern: dada-app-logs-*). apiKey is normalized the same
// way as the read client (base64(id:key)). Returns nil when baseURL is empty.
func NewWriteClient(baseURL, apiKey, baseIndex string) *WriteClient {
	if baseURL == "" {
		return nil
	}
	if baseIndex == "" {
		baseIndex = "dada-app-logs"
	}
	return &WriteClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		baseIndex:  strings.TrimRight(baseIndex, "-*"),
		apiKey:     encodeAPIKey(apiKey),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// AppLog is one application log document to index. Org/Project/Environment/
// MonitoringApp are tenancy labels; the handler fills them from authoritative DB
// values, never from the client body.
type AppLog struct {
	Timestamp     time.Time
	Source        string
	Level         string
	Message       string
	OrgID         string
	ProjectID     string
	Environment   string
	MonitoringApp string
}

// Index writes a single app-log document. The doc mirrors the fields the read
// path expects (message, @timestamp, app, vm_name) plus the monitoring tenancy
// labels, so existing log search filters (app=) resolve these entries.
func (c *WriteClient) Index(ctx context.Context, doc AppLog) error {
	if c == nil {
		return fmt.Errorf("log ingestion not configured")
	}
	ts := doc.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	ts = ts.UTC()

	body := map[string]any{
		"@timestamp":     ts.Format(time.RFC3339Nano),
		"message":        doc.Message,
		"level":          doc.Level,
		"source":         doc.Source,
		"org_id":         doc.OrgID,
		"project_id":     doc.ProjectID,
		"environment":    doc.Environment,
		"monitoring_app": doc.MonitoringApp,
		// reuse-compat with the read path / LogsViewer:
		"app":     doc.MonitoringApp,
		"vm_name": doc.Source,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}

	index := fmt.Sprintf("%s-%s", c.baseIndex, ts.Format("2006.01.02"))
	u := fmt.Sprintf("%s/%s/_doc", c.baseURL, index)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "ApiKey "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("elasticsearch index: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("elasticsearch index: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
