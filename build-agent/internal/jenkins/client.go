// Package jenkins is a thin REST client for the dada-cloud control plane. It
// drives a parameterized job through trigger → queue-resolve → poll-result and
// streams the console via progressiveText (incremental offset). No Jenkins-side
// plugins are required: everything here is the stock Remote API + CSRF crumb.
package jenkins

import (
	"context"
	"encoding/json"
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
	baseURL string // e.g. https://jenkins.dada-tuda.ru (no trailing slash)
	user    string
	token   string
	http    *http.Client
}

// New returns a Client. baseURL may carry a trailing slash; it is trimmed.
func New(baseURL, user, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
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
	resp, err := c.do(ctx, http.MethodGet, c.baseURL+"/crumbIssuer/api/json", nil, nil)
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
	resp, err := c.do(ctx, http.MethodPost, u, strings.NewReader(form.Encode()), hdr)
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
	resp, err := c.do(ctx, http.MethodGet, u, nil, nil)
	if err != nil {
		return 0, false, fmt.Errorf("queue item: %w", err)
	}
	defer resp.Body.Close()
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
	resp, err := c.do(ctx, http.MethodGet, u, nil, nil)
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
	resp, err := c.do(ctx, http.MethodGet, u, nil, nil)
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

func readErr(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return fmt.Sprintf("%d %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
