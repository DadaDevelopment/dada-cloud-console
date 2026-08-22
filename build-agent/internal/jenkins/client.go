// Package jenkins is a thin REST client for the dada-cloud control plane. It
// drives a parameterized job through trigger → queue-resolve → poll-result and
// streams the console via progressiveText (incremental offset). No Jenkins-side
// plugins are required: everything here is the stock Remote API + CSRF crumb.
package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var queueItemBodyRe = regexp.MustCompile(`/queue/item/(\d+)/`)

// Client talks to a Jenkins controller with API-token basic auth.
type Client struct {
	baseURL  string // e.g. https://jenkins.dada-tuda.ru (no trailing slash)
	user     string
	token    string
	http     *http.Client
	attempts int           // total tries per call, including the first
	backoff  time.Duration // base delay, doubled per retry
}

// ErrQueueItemGone reports that Jenkins no longer holds the queue item we are
// polling. Jenkins evicts a left queue item after five minutes, so a 404 is
// ambiguous by itself: the item was either cancelled long ago or -- far more
// often -- it started and its build has been running past the eviction
// window. Treating it as a plain error failed builds whose Jenkins job was
// alive and, worse, re-queued them into a duplicate job. Callers catch this
// and go looking for the started build by queue id instead.
var ErrQueueItemGone = errors.New("queue item no longer known to jenkins")

// New returns a Client. baseURL may carry a trailing slash; it is trimmed.
func New(baseURL, user, token string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		user:     user,
		token:    token,
		http:     &http.Client{Timeout: 30 * time.Second},
		attempts: defaultAttempts,
		backoff:  defaultBackoff,
	}
}

// defaultAttempts and defaultBackoff bound the in-call retry of a transient
// upstream answer. A gateway 502/503/504 in front of Jenkins is the single
// most common way a build died without the user's repo being touched: the
// controller restarts, the ingress sheds load for a few seconds, and the one
// request we happened to make in that window turned into a red build the user
// had to restart by hand. Four tries over ~7s of backoff outlive that window
// without holding a build hostage to a real outage.
const (
	defaultAttempts = 4
	defaultBackoff  = time.Second
)

// transientStatus reports whether an upstream status means "not now" rather
// than "no". These are gateway answers: the request never reached Jenkins (or
// Jenkins refused to take it yet), so nothing was queued, started, or lost by
// asking again.
func transientStatus(code int) bool {
	switch code {
	case http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout, http.StatusTooManyRequests:
		return true
	}
	return false
}

// doRetry issues a request, retrying transient upstream answers with an
// exponential backoff.
//
// retryTransport separates the two kinds of failure a POST cannot treat
// alike. A transient STATUS is always safe to repeat: the gateway answered
// for Jenkins, so no build was queued. A TRANSPORT error is not: the request
// may well have landed and started a build whose response we never read, and
// repeating it would trigger a second build for one push. Reads pass true;
// buildWithParameters passes false.
func (c *Client) doRetry(ctx context.Context, method, fullURL string, body func() io.Reader, hdr map[string]string, retryTransport bool) (*http.Response, error) {
	attempts := c.attempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			delay := c.backoff << (i - 1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		var rdr io.Reader
		if body != nil {
			rdr = body()
		}
		resp, err := c.do(ctx, method, fullURL, rdr, hdr)
		if err != nil {
			if !retryTransport || ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			continue
		}
		if transientStatus(resp.StatusCode) && i < attempts-1 {
			lastErr = fmt.Errorf("%s", readErr(resp))
			resp.Body.Close()
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

// BuildInfo is the subset of a build's api/json we act on.
type BuildInfo struct {
	Number   int    `json:"number"`
	Result   string `json:"result"`   // SUCCESS|FAILURE|ABORTED|null(building)
	Building bool   `json:"building"` // true while running
	Duration int64  `json:"duration"` // ms, 0 while building
}

// jobPath turns a folder/job full name into Jenkins' nested URL form:
// "folder/job" → "/job/folder/job/job/job". Each segment is path-escaped.
func jobPath(fullName string) string {
	parts := strings.Split(strings.Trim(fullName, "/"), "/")
	var b strings.Builder
	for _, p := range parts {
		b.WriteString("/job/")
		b.WriteString(url.PathEscape(p))
	}
	return b.String()
}

// do issues an authenticated request and returns the raw response.
func (c *Client) do(ctx context.Context, method, fullURL string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.token)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.http.Do(req)
}

// crumb fetches the CSRF crumb. Controllers with crumbs disabled return 404; we
// treat that as "no crumb needed" rather than an error.
func (c *Client) crumb(ctx context.Context) (field, value string, err error) {
	resp, err := c.doRetry(ctx, http.MethodGet, c.baseURL+"/crumbIssuer/api/json", nil, nil, true)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", "", nil // CSRF protection disabled
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("crumb: %s", readErr(resp))
	}
	var cr struct {
		Field string `json:"crumbRequestField"`
		Crumb string `json:"crumb"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", "", fmt.Errorf("decode crumb: %w", err)
	}
	return cr.Field, cr.Crumb, nil
}

// TriggerBuild starts a parameterized build and returns the queue item id. The
// id is parsed from the Location header (.../queue/item/<id>/).
func (c *Client) TriggerBuild(ctx context.Context, jobFullName string, params map[string]string) (int64, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	hdr := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if field, value, err := c.crumb(ctx); err != nil {
		return 0, fmt.Errorf("get crumb: %w", err)
	} else if field != "" {
		hdr[field] = value
	}

	u := c.baseURL + jobPath(jobFullName) + "/buildWithParameters"
	encoded := form.Encode()
	resp, err := c.doRetry(ctx, http.MethodPost, u, func() io.Reader { return strings.NewReader(encoded) }, hdr, false)
	if err != nil {
		return 0, fmt.Errorf("trigger build: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		loc := resp.Header.Get("Location")
		id, err := queueIDFromLocation(loc)
		if err != nil {
			return 0, fmt.Errorf("parse queue location %q: %w", loc, err)
		}
		return id, nil
	}

	if resp.StatusCode == http.StatusOK {
		if loc := resp.Header.Get("Location"); loc != "" {
			if id, err := queueIDFromLocation(loc); err == nil {
				return id, nil
			}
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if m := queueItemBodyRe.FindSubmatch(body); m != nil {
			if id, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil {
				return id, nil
			}
		}
		return 0, fmt.Errorf("trigger build %q: 200 response with no queue item id: %s", jobFullName, strings.TrimSpace(string(body)))
	}

	return 0, fmt.Errorf("trigger build %q: %s", jobFullName, readErr(resp))
}

// queueIDFromLocation extracts <id> from a .../queue/item/<id>/ Location URL.
func queueIDFromLocation(loc string) (int64, error) {
	loc = strings.TrimRight(loc, "/")
	idx := strings.LastIndex(loc, "/")
	if idx < 0 || idx+1 >= len(loc) {
		return 0, fmt.Errorf("no id segment")
	}
	return strconv.ParseInt(loc[idx+1:], 10, 64)
}

// ResolveBuildNumber polls one queue item. It returns (number, true, nil) once
// the item has been assigned an executor, (0, false, nil) while still queued,
// and an error if the item was cancelled.
func (c *Client) ResolveBuildNumber(ctx context.Context, queueID int64) (int, bool, error) {
	u := fmt.Sprintf("%s/queue/item/%d/api/json?tree=cancelled,executable[number]", c.baseURL, queueID)
	resp, err := c.doRetry(ctx, http.MethodGet, u, nil, nil, true)
	if err != nil {
		return 0, false, fmt.Errorf("queue item: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, false, fmt.Errorf("queue item %d: %w", queueID, ErrQueueItemGone)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("queue item %d: %s", queueID, readErr(resp))
	}
	var qi struct {
		Cancelled  bool `json:"cancelled"`
		Executable *struct {
			Number int `json:"number"`
		} `json:"executable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&qi); err != nil {
		return 0, false, fmt.Errorf("decode queue item: %w", err)
	}
	if qi.Cancelled {
		return 0, false, fmt.Errorf("queue item %d cancelled before start", queueID)
	}
	if qi.Executable == nil || qi.Executable.Number == 0 {
		return 0, false, nil // still queued
	}
	return qi.Executable.Number, true, nil
}

// GetBuild returns the build's result/building/duration.
func (c *Client) GetBuild(ctx context.Context, jobFullName string, number int) (BuildInfo, error) {
	u := fmt.Sprintf("%s%s/%d/api/json?tree=number,result,building,duration", c.baseURL, jobPath(jobFullName), number)
	resp, err := c.doRetry(ctx, http.MethodGet, u, nil, nil, true)
	if err != nil {
		return BuildInfo{}, fmt.Errorf("get build: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return BuildInfo{}, fmt.Errorf("get build %s#%d: %s", jobFullName, number, readErr(resp))
	}
	var bi BuildInfo
	if err := json.NewDecoder(resp.Body).Decode(&bi); err != nil {
		return BuildInfo{}, fmt.Errorf("decode build: %w", err)
	}
	return bi, nil
}

// ProgressiveText fetches console text appended since `start`. It returns the
// new text, the next offset to pass, and whether more data is still coming
// (X-More-Data). This is the incremental log bridge primitive.
func (c *Client) ProgressiveText(ctx context.Context, jobFullName string, number int, start int64) (text string, next int64, more bool, err error) {
	u := fmt.Sprintf("%s%s/%d/logText/progressiveText?start=%d", c.baseURL, jobPath(jobFullName), number, start)
	resp, err := c.doRetry(ctx, http.MethodGet, u, nil, nil, true)
	if err != nil {
		return "", start, false, fmt.Errorf("progressive text: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", start, false, fmt.Errorf("progressive text %s#%d: %s", jobFullName, number, readErr(resp))
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", start, false, fmt.Errorf("read console: %w", err)
	}
	more = strings.EqualFold(resp.Header.Get("X-More-Data"), "true")
	next = start
	if sz := resp.Header.Get("X-Text-Size"); sz != "" {
		if n, perr := strconv.ParseInt(sz, 10, 64); perr == nil {
			next = n
		}
	}
	return string(buf), next, more, nil
}

// htmlTagRe and wsRe flatten an upstream error page into one line.
var (
	htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe      = regexp.MustCompile(`\s+`)
)

// upstreamErrMaxLen caps the body excerpt carried in an error.
const upstreamErrMaxLen = 160

// readErr renders a failed response as one short human line.
//
// Both nginx and Jetty answer with a full HTML document, and every byte of it
// used to travel into builds.error_message and out to the build page and the
// admin panel: the owner of a broken app was shown three lines of markup and
// a Jetty version banner to say "the build server was briefly unavailable".
// The tags go, the whitespace collapses, and the excerpt is capped, so what
// survives is the sentence a reader actually needs ("503 Service Temporarily
// Unavailable") rather than the page it arrived in.
func readErr(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return strings.TrimSpace(fmt.Sprintf("%d %s", resp.StatusCode, flattenUpstreamBody(string(b))))
}

// flattenUpstreamBody strips markup, collapses whitespace and truncates.
func flattenUpstreamBody(body string) string {
	s := htmlTagRe.ReplaceAllString(body, " ")
	s = strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
	if runes := []rune(s); len(runes) > upstreamErrMaxLen {
		s = strings.TrimSpace(string(runes[:upstreamErrMaxLen])) + "…"
	}
	return s
}

// FindBuildByQueueID returns the number of the build Jenkins started for
// queueID, and false when no recent build carries it.
//
// Every build's api/json exposes the queueId it was started from, which makes
// this an exact correlation rather than a guess by app name and timestamp:
// when a queue item is evicted out from under a poll (ErrQueueItemGone), the
// build it became can be adopted instead of failing the row and triggering a
// duplicate job.
func (c *Client) FindBuildByQueueID(ctx context.Context, jobFullName string, queueID int64) (int, bool, error) {
	u := fmt.Sprintf("%s%s/api/json?tree=builds[number,queueId]{0,%d}", c.baseURL, jobPath(jobFullName), buildScanDepth)
	resp, err := c.doRetry(ctx, http.MethodGet, u, nil, nil, true)
	if err != nil {
		return 0, false, fmt.Errorf("list builds: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("list builds %s: %s", jobFullName, readErr(resp))
	}
	var jr struct {
		Builds []struct {
			Number  int   `json:"number"`
			QueueID int64 `json:"queueId"`
		} `json:"builds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		return 0, false, fmt.Errorf("decode builds: %w", err)
	}
	for _, b := range jr.Builds {
		if b.QueueID == queueID && b.Number > 0 {
			return b.Number, true, nil
		}
	}
	return 0, false, nil
}

// buildScanDepth bounds how far back FindBuildByQueueID looks. A queue item
// is evicted five minutes after it leaves the queue, so the build that took
// it is always among the most recent ones on a job of this throughput.
const buildScanDepth = 60

// StopBuild asks Jenkins to stop a running build. It is the counterpart to a
// canceled or superseded build row: without this call, the row moves to a
// terminal status in our own database while the Jenkins job it started keeps
// running, finishes, and pushes an image nobody will ever deploy -- burning
// CI capacity a real build is queued behind.
//
// A 404/409 (build already gone, already finished, or never existed) is not
// an error at build level: by the time this is called, the outcome we asked
// for -- the job no longer running unattended -- may already hold, so the
// caller should not fail the build over it. Only a genuine transport/gateway
// failure is returned.
func (c *Client) StopBuild(ctx context.Context, jobFullName string, number int) error {
	hdr := map[string]string{}
	if field, value, err := c.crumb(ctx); err != nil {
		return fmt.Errorf("get crumb: %w", err)
	} else if field != "" {
		hdr[field] = value
	}
	u := fmt.Sprintf("%s%s/%d/stop", c.baseURL, jobPath(jobFullName), number)
	resp, err := c.doRetry(ctx, http.MethodPost, u, nil, hdr, false)
	if err != nil {
		return fmt.Errorf("stop build: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict {
		return nil
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusFound {
		return fmt.Errorf("stop build %s#%d: %s", jobFullName, number, readErr(resp))
	}
	return nil
}

// CancelQueueItem asks Jenkins to cancel a queued item that has not yet been
// assigned an executor. Used for the narrower window between TriggerBuild
// and the queue item resolving to a build number: canceling the row before
// Jenkins even started it should not let that item start a build at all.
//
// Like StopBuild, a 404 (the item already left the queue -- started, was
// canceled already, or was evicted) is not an error: the queue no longer
// holding it is exactly the state being asked for.
func (c *Client) CancelQueueItem(ctx context.Context, queueID int64) error {
	hdr := map[string]string{}
	if field, value, err := c.crumb(ctx); err != nil {
		return fmt.Errorf("get crumb: %w", err)
	} else if field != "" {
		hdr[field] = value
	}
	u := fmt.Sprintf("%s/queue/cancelItem?id=%d", c.baseURL, queueID)
	resp, err := c.doRetry(ctx, http.MethodPost, u, nil, hdr, false)
	if err != nil {
		return fmt.Errorf("cancel queue item: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusFound {
		return fmt.Errorf("cancel queue item %d: %s", queueID, readErr(resp))
	}
	return nil
}
