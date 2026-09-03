package tggateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// linkFetchTimeout caps one title lookup. This is a best-effort enrichment:
// a slow or dead link site must never delay a user's message, so the fetch
// is abandoned (title left empty) rather than awaited.
const linkFetchTimeout = 3 * time.Second

// linkTitleMaxBytes bounds how much of the response is read -- titles live
// in the first bytes of the HTML head; nobody needs a 50MB download to
// learn a page's title.
const linkTitleMaxBytes = 64 * 1024

// LinkTitleFetcher resolves a URL's page title, best-effort. A nil fetcher
// or an error means "no title": the URL alone still flows to the agent.
type LinkTitleFetcher interface {
	FetchTitle(ctx context.Context, url string) string
}

type httpLinkTitleFetcher struct {
	client *http.Client
}

func NewLinkTitleFetcher() LinkTitleFetcher {
	return &httpLinkTitleFetcher{client: &http.Client{Timeout: linkFetchTimeout}}
}

// FetchTitle GETs the URL (HTML only) and returns the contents of
// <title>...</title>, whitespace-collapsed. Any failure -- network, status,
// encoding, missing title -- yields "" and is deliberately silent: this is
// enrichment, not a pipeline stage.
func (f *httpLinkTitleFetcher) FetchTitle(ctx context.Context, url string) string {
	if url == "" {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, linkFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dada-agent-harness/1.0)")

	resp, err := f.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "html") {
		return ""
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, linkTitleMaxBytes))
	if err != nil {
		return ""
	}
	return extractHTMLTitle(string(raw))
}

// extractHTMLTitle pulls <title> text case-insensitively and collapses
// whitespace, without pulling in a full HTML parser for one tag.
func extractHTMLTitle(html string) string {
	lower := strings.ToLower(html)
	open := strings.Index(lower, "<title")
	if open < 0 {
		return ""
	}
	gt := strings.Index(lower[open:], ">")
	if gt < 0 {
		return ""
	}
	open += gt + 1
	close := strings.Index(lower[open:], "</title>")
	if close < 0 {
		return ""
	}
	title := strings.TrimSpace(html[open : open+close])
	fields := strings.Fields(title)
	return strings.Join(fields, " ")
}

// enrichEntities fills each entity's Title (best-effort, bounded). It runs
// sequentially per message: batches carry few links, and the 3s per-link
// cap bounds the worst case.
func enrichEntities(ctx context.Context, fetcher LinkTitleFetcher, entities []TelegramEntity) []RuntimeLinkMeta {
	if len(entities) == 0 {
		return nil
	}
	out := make([]RuntimeLinkMeta, 0, len(entities))
	seen := map[string]bool{}
	for _, e := range entities {
		if e.URL == "" || seen[e.URL] {
			continue
		}
		seen[e.URL] = true
		out = append(out, RuntimeLinkMeta{URL: e.URL, Title: fetcher.FetchTitle(ctx, e.URL)})
	}
	return out
}
