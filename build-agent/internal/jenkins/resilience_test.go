package jenkins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fast returns a client whose retry backoff is short enough for a test.
func fast(baseURL string) *Client {
	c := New(baseURL, "u", "t")
	c.backoff = time.Millisecond
	return c
}

func TestReadErrFlattensUpstreamHTML(t *testing.T) {
	body := "<html>\n<head><title>503 Service Temporarily Unavailable</title></head>\n<body>\n" +
		"<center><h1>503 Service Temporarily Unavailable</h1></center>\n<hr><center>nginx</center>\n</body>\n</html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	got := readErr(resp)
	if strings.Contains(got, "<") || strings.Contains(got, "\n") {
		t.Fatalf("readErr kept markup or newlines: %q", got)
	}
	if !strings.HasPrefix(got, "503 ") || !strings.Contains(got, "Service Temporarily Unavailable") {
		t.Fatalf("readErr = %q, want it to lead with the status and the reason", got)
	}
	if len([]rune(got)) > upstreamErrMaxLen+8 {
		t.Fatalf("readErr len = %d, want it capped near %d: %q", len([]rune(got)), upstreamErrMaxLen, got)
	}
}

func TestCrumbRetriesGatewayOutage(t *testing.T) {
	var crumbHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			crumbHits++
			if crumbHits < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("<html><body>503 Service Temporarily Unavailable</body></html>"))
				return
			}
			w.Write([]byte(`{"crumbRequestField":"Jenkins-Crumb","crumb":"abc"}`))
		case "/job/web/buildWithParameters":
			if r.Header.Get("Jenkins-Crumb") != "abc" {
				t.Errorf("crumb header = %q", r.Header.Get("Jenkins-Crumb"))
			}
			w.Header().Set("Location", "http://x/queue/item/42/")
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	qid, err := fast(srv.URL).TriggerBuild(context.Background(), "web", nil)
	if err != nil || qid != 42 {
		t.Fatalf("trigger through a crumb outage: qid=%d err=%v, want 42 nil", qid, err)
	}
	if crumbHits != 3 {
		t.Fatalf("crumb attempts = %d, want 3", crumbHits)
	}
}

func TestTriggerRetriesTransientStatusOnce(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			w.WriteHeader(http.StatusNotFound)
		case "/job/web/buildWithParameters":
			posts++
			if posts == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Header().Set("Location", "http://x/queue/item/8/")
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	qid, err := fast(srv.URL).TriggerBuild(context.Background(), "web", map[string]string{"branch": "main"})
	if err != nil || qid != 8 {
		t.Fatalf("trigger: qid=%d err=%v, want 8 nil", qid, err)
	}
	if posts != 2 {
		t.Fatalf("posts = %d, want 2 (one shed by the gateway, one accepted)", posts)
	}
}

func TestTriggerDoesNotRepeatAfterTransportFailure(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			w.WriteHeader(http.StatusNotFound)
		case "/job/web/buildWithParameters":
			posts++
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("no hijacker")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			conn.Close() // request landed, answer never arrives
		}
	}))
	defer srv.Close()

	if _, err := fast(srv.URL).TriggerBuild(context.Background(), "web", nil); err == nil {
		t.Fatal("want an error when the trigger response is lost")
	}
	if posts != 1 {
		t.Fatalf("posts = %d, want 1: repeating a landed trigger starts a duplicate build", posts)
	}
}

func TestResolveBuildNumberEvictedItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("<html><head><title>Error 404 Not Found</title></head><body>HTTP ERROR 404</body></html>"))
	}))
	defer srv.Close()

	_, _, err := fast(srv.URL).ResolveBuildNumber(context.Background(), 67584)
	if !errors.Is(err, ErrQueueItemGone) {
		t.Fatalf("err = %v, want ErrQueueItemGone", err)
	}
	if strings.Contains(err.Error(), "<") {
		t.Fatalf("error carries markup: %q", err)
	}
}

func TestResolveBuildNumberRetriesGateway(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"cancelled":false,"executable":{"number":11}}`))
	}))
	defer srv.Close()

	n, started, err := fast(srv.URL).ResolveBuildNumber(context.Background(), 7)
	if err != nil || !started || n != 11 {
		t.Fatalf("resolve = (%d,%v,%v), want (11,true,nil)", n, started, err)
	}
}

func TestFindBuildByQueueID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/web/api/json" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"builds":[{"number":312,"queueId":67590},{"number":311,"queueId":67584}]}`))
	}))
	defer srv.Close()

	c := fast(srv.URL)
	n, ok, err := c.FindBuildByQueueID(context.Background(), "web", 67584)
	if err != nil || !ok || n != 311 {
		t.Fatalf("find = (%d,%v,%v), want (311,true,nil)", n, ok, err)
	}
	if _, ok, err := c.FindBuildByQueueID(context.Background(), "web", 999); err != nil || ok {
		t.Fatalf("find of an unknown queue id = (%v,%v), want (false,nil)", ok, err)
	}
}
