// Package logsearch is a lean, read-only Elasticsearch client used by the
// console backend to read back the container logs that the portainer-agent's
// filebeat sidecars ship from VMs.
//
// It mirrors internal/portainer / internal/prometheus: New returns nil when
// unconfigured so callers treat aggregated log search as disabled. Only a
// single bounded _search is implemented.
//
// NOTE: the _source field names (message / host.name / container label) and the
// default index pattern are assumptions about the filebeat mapping. They are
// intentionally isolated in this file (sourceDoc + buildQuery) so adjusting them
// to a live cluster is a one-place change.
package logsearch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a read-only Elasticsearch search client.
type Client struct {
	baseURL    string
	index      string
	apiKey     string
	httpClient *http.Client
}

// New creates a read-only ES client. Returns nil when baseURL is empty so
// callers can treat log search as disabled. index defaults to "dada-vm-logs-*"
// (the index the VM filebeat bootstrap writes to:
// dada-vm-logs-<app|unknown>-<date>).
func New(baseURL, apiKey, index string) *Client {
	if baseURL == "" {
		return nil
	}
	if index == "" {
		index = "dada-vm-logs-*"
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		index:      index,
		apiKey:     encodeAPIKey(apiKey),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// encodeAPIKey normalizes an Elasticsearch API key for the
// `Authorization: ApiKey <value>` header. ES expects base64(id:api_key); the
// raw "id:api_key" form (what filebeat config and our Secret store, since
// filebeat base64-encodes it itself) must be encoded here. A value without a
// colon is assumed already base64-encoded and passed through. Verified live:
// raw key → 401, base64(raw) → 200.
func encodeAPIKey(k string) string {
	if k == "" || !strings.Contains(k, ":") {
		return k
	}
	return base64.StdEncoding.EncodeToString([]byte(k))
}

// SearchOpts bounds an aggregated log search. At least one of VMName/App should
// be set by callers for project-scoping (enforced at the handler layer).
type SearchOpts struct {
	VMName string
	App    string
	Query  string // free text → ES query_string over the message field
	Since  time.Time
	Until  time.Time
	Size   int // default 200, cap 1000

	// Monitoring app-log scoping (dada-app-logs-* index). These match the labels
	// the ingest path tags app logs with; set together with a monitoring-scoped
	// index (see Handler.appLogsearch). Level filters by log level (e.g. ERROR).
	ProjectID     string
	MonitoringApp string
	Source        string
	Level         string

	// Native (k8s) app scoping for the infra stream (filebeat-*, in-cluster kube
	// pod logs shipped by the elastic-stack filebeat). KubeApp matches the
	// dada.io/app pod label the app-resources chart sets; KubeNamespaces is the
	// tenancy boundary — the environments' namespaces the app belongs to. Search
	// refuses KubeApp without KubeNamespaces so an app-name collision in another
	// tenant's namespace can never leak logs.
	KubeApp        string
	KubeNamespaces []string
}

// termOneOf builds a bool/should over keyword + text variants of a field so a
// single value matches regardless of how ES dynamic-mapped it.
func termOneOf(field, value string) map[string]any {
	return map[string]any{
		"bool": map[string]any{
			"should": []map[string]any{
				{"term": map[string]any{field + ".keyword": value}},
				{"term": map[string]any{field: value}},
			},
			"minimum_should_match": 1,
		},
	}
}

// LogEntry is one normalized log line.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
	VMName    string `json:"vm_name,omitempty"`
	App       string `json:"app,omitempty"`
	Stream    string `json:"stream,omitempty"`
}

// SearchResult is the normalized search response.
type SearchResult struct {
	Total   int        `json:"total"`
	Entries []LogEntry `json:"entries"`
}

type sourceDoc struct {
	Timestamp string          `json:"@timestamp"`
	Message   string          `json:"message"`
	Log       string          `json:"log"`
	VMName    string          `json:"vm_name"`
	App       json.RawMessage `json:"app"`
	Host      struct {
		Name string `json:"name"`
	} `json:"host"`
	Stream     string `json:"stream"`
	Kubernetes struct {
		NamespaceName string         `json:"namespace_name"`
		PodName       string         `json:"pod_name"`
		Labels        map[string]any `json:"labels"`
	} `json:"kubernetes"`
}

func decodeAppField(raw json.RawMessage) (name, message string) {
	if len(raw) == 0 {
		return "", ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, ""
	}
	var obj struct {
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Msg != "" {
			return "", obj.Msg
		}
		return "", obj.Message
	}
	return "", ""
}

func (c *Client) buildQuery(opts SearchOpts) map[string]any {
	filters := []map[string]any{}
	if opts.VMName != "" {
		// vm_name is the field the filebeat bootstrap sets (= AppServer name);
		// host.name is filebeat's default (a container id, not the server name).
		// filebeat dynamic-maps strings as text + a .keyword subfield, so exact
		// match needs .keyword (a `term` on the analyzed text field matches
		// nothing — verified live: term vm_name=0, term vm_name.keyword=68).
		filters = append(filters, map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"term": map[string]any{"vm_name.keyword": opts.VMName}},
					{"term": map[string]any{"vm_name": opts.VMName}},
					{"term": map[string]any{"host.name.keyword": opts.VMName}},
				},
				"minimum_should_match": 1,
			},
		})
	}
	if opts.App != "" {
		// A first-class VM Application == one docker-compose SERVICE in the shared
		// per-VM stack, so the service label is what isolates one app (same as the
		// container metrics query, which keys by com_docker_compose_service). The
		// fleet fluent-bit lua enrichment stamps flat com_docker_compose_service /
		// _project on every record from the container's config.v2.json. Keep the
		// older container.labels.* / project variants for back-compat with the
		// single-app model + any pre-enrichment logs. Keyword + text tried defensively.
		filters = append(filters, map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"term": map[string]any{"com_docker_compose_service.keyword": opts.App}},
					{"term": map[string]any{"com_docker_compose_service": opts.App}},
					{"match": map[string]any{"com_docker_compose_service": opts.App}},
					{"term": map[string]any{"container.labels.com_docker_compose_project": opts.App}},
					{"term": map[string]any{"container.labels.com_docker_compose_project.keyword": opts.App}},
					{"match": map[string]any{"container.labels.com_docker_compose_project": opts.App}},
					{"term": map[string]any{"app": opts.App}},
					{"match": map[string]any{"container.labels.dada_io_app": opts.App}},
				},
				"minimum_should_match": 1,
			},
		})
	}
	// Native app scoping over the infra stream. kubernetes.namespace is the hard
	// tenancy filter; the caller (SearchLogs) resolves it from the environments
	// table, never from user input.
	if len(opts.KubeNamespaces) > 0 {
		filters = append(filters, map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"terms": map[string]any{"kubernetes.namespace_name": opts.KubeNamespaces}},
					{"terms": map[string]any{"kubernetes.namespace_name.keyword": opts.KubeNamespaces}},
				},
				"minimum_should_match": 1,
			},
		})
	}
	if opts.KubeApp != "" {
		// filebeat dedots label keys ("." → "_") by default, so dada.io/app is
		// indexed as kubernetes.labels.dada_io/app; match the un-dedotted form and
		// the "<app>-deploy" Deployment-name chart convention too (same fallback
		// chain the gitops-agent status reconciler uses). Keyword + text variants
		// are tried defensively, mirroring the VM filters above.
		should := []map[string]any{
			{"term": map[string]any{"kubernetes.labels.dada_io/app": opts.KubeApp}},
			{"term": map[string]any{"kubernetes.labels.dada_io/app.keyword": opts.KubeApp}},
			{"term": map[string]any{"kubernetes.labels.dada.io/app": opts.KubeApp}},
		}
		filters = append(filters, map[string]any{
			"bool": map[string]any{"should": should, "minimum_should_match": 1},
		})
	}

	// Monitoring app-log label scoping (dada-app-logs-* index).
	if opts.ProjectID != "" {
		filters = append(filters, termOneOf("project_id", opts.ProjectID))
	}
	if opts.MonitoringApp != "" {
		filters = append(filters, termOneOf("monitoring_app", opts.MonitoringApp))
	}
	if opts.Source != "" {
		filters = append(filters, termOneOf("source", opts.Source))
	}
	if opts.Level != "" {
		filters = append(filters, termOneOf("level", opts.Level))
	}

	rng := map[string]any{}
	if !opts.Since.IsZero() {
		rng["gte"] = opts.Since.UTC().Format(time.RFC3339)
	}
	if !opts.Until.IsZero() {
		rng["lte"] = opts.Until.UTC().Format(time.RFC3339)
	}
	if len(rng) > 0 {
		filters = append(filters, map[string]any{"range": map[string]any{"@timestamp": rng}})
	}

	must := []map[string]any{}
	if opts.Query != "" {
		must = append(must, map[string]any{
			"query_string": map[string]any{
				"query":   opts.Query,
				"fields":  []string{"message", "log", "app.msg"},
				"lenient": true,
			},
		})
	}

	size := opts.Size
	if size <= 0 {
		size = 200
	}
	if size > 1000 {
		size = 1000
	}

	return map[string]any{
		"size": size,
		"sort": []map[string]any{{"@timestamp": map[string]any{"order": "desc"}}},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": filters,
				"must":   must,
			},
		},
	}
}

// Search runs a bounded _search against the configured index and returns
// normalized, newest-first log entries.
func (c *Client) Search(ctx context.Context, opts SearchOpts) (*SearchResult, error) {
	if opts.KubeApp != "" && len(opts.KubeNamespaces) == 0 {
		// Tenancy guard: an app-name filter over the shared infra stream without
		// a namespace scope would match same-named apps of other tenants.
		return nil, fmt.Errorf("logsearch: KubeApp requires KubeNamespaces")
	}
	body, err := json.Marshal(c.buildQuery(opts))
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/%s/_search", c.baseURL, c.index)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "ApiKey "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // cap 8 MiB
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("elasticsearch search: status %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source sourceDoc `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("elasticsearch decode: %w", err)
	}

	out := &SearchResult{Total: parsed.Hits.Total.Value}
	for _, h := range parsed.Hits.Hits {
		vm := h.Source.VMName
		app, appMsg := decodeAppField(h.Source.App)
		if k := h.Source.Kubernetes; k.PodName != "" {
			vm = k.PodName
			if app == "" {
				for _, key := range []string{"dada_io/app", "dada.io/app"} {
					if v, ok := k.Labels[key].(string); ok && v != "" {
						app = v
						break
					}
				}
			}
		}
		if vm == "" {
			vm = h.Source.Host.Name
		}
		msg := h.Source.Message
		if msg == "" {
			msg = h.Source.Log
		}
		if msg == "" {
			msg = appMsg
		}
		out.Entries = append(out.Entries, LogEntry{
			Timestamp: h.Source.Timestamp,
			Message:   msg,
			VMName:    vm,
			App:       app,
			Stream:    h.Source.Stream,
		})
	}
	return out, nil
}
