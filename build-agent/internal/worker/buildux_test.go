package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/build-agent/internal/config"
	"github.com/dada-tuda/console/build-agent/internal/jenkins"
)

func TestClassifyFailureDigsPastScriptExitCode(t *testing.T) {
	console := strings.Join([]string{
		"[Pipeline] sh",
		"+ npm run build",
		"> tvk-assistant@1.0.0 build",
		"src/bot.ts(12,5): error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.",
		"",
		"[Pipeline] }",
		"ERROR: script returned exit code 1",
		"Finished: FAILURE",
	}, "\n")

	code, detail := classifyFailure(console)
	if code != buildFailGeneric {
		t.Fatalf("code = %q, want %q", code, buildFailGeneric)
	}
	if strings.Contains(detail, "script returned exit code") {
		t.Fatalf("detail = %q: the wrapper tells the owner only that the build failed", detail)
	}
	if !strings.Contains(detail, "TS2345") {
		t.Fatalf("detail = %q, want the compiler line that actually broke the build", detail)
	}
}

func TestClassifyFailureKeepsWrapperWhenNothingAbove(t *testing.T) {
	code, detail := classifyFailure("ERROR: script returned exit code 1\nFinished: FAILURE")
	if code != buildFailGeneric || detail != "script returned exit code 1" {
		t.Fatalf("classify = (%q,%q), want the wrapper as a last resort, not an empty detail", code, detail)
	}
}

// TestWaitForBuildNumberAdoptsEvictedQueueItem proves the affiliate-site case:
// Jenkins forgot the queue item (404) while the build it became is running.
func TestWaitForBuildNumberAdoptsEvictedQueueItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/queue/item/67584/api/json":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("<html><body>HTTP ERROR 404 Not Found</body></html>"))
		case "/job/web/api/json":
			w.Write([]byte(`{"builds":[{"number":311,"queueId":67584}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	r := &Runner{jenkins: jenkins.New(srv.URL, "u", "t"), cfg: &config.Config{JenkinsJob: "web"}}
	n, err := r.waitForBuildNumber(context.Background(), 67584)
	if err != nil || n != 311 {
		t.Fatalf("waitForBuildNumber = (%d,%v), want (311,nil)", n, err)
	}
}

// TestWaitForBuildNumberFailsWhenEvictedItemStartedNothing keeps the adoption
// honest: no build carries the queue id, so the build really is lost and the
// caller must be told.
func TestWaitForBuildNumberFailsWhenEvictedItemStartedNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/queue/item/5/api/json":
			w.WriteHeader(http.StatusNotFound)
		case "/job/web/api/json":
			w.Write([]byte(`{"builds":[{"number":311,"queueId":900}]}`))
		}
	}))
	defer srv.Close()

	r := &Runner{jenkins: jenkins.New(srv.URL, "u", "t"), cfg: &config.Config{JenkinsJob: "web"}}
	if _, err := r.waitForBuildNumber(context.Background(), 5); err == nil {
		t.Fatal("want an error when nothing was started from the evicted item")
	}
}

// TestWaitForBuildNumberSurvivesTransientQueueError proves the fanvk-shaped
// case one layer up: a gateway blip during the queue poll must not end a build
// that is still queued.
// The gateway sheds more answers than one client-level retry budget absorbs,
// so this exercises the runner's own patience, not the client's.
func TestWaitForBuildNumberSurvivesTransientQueueError(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits <= 6 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"cancelled":false,"executable":{"number":12}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := &Runner{jenkins: jenkins.New(srv.URL, "u", "t"), cfg: &config.Config{JenkinsJob: "web"}}
	n, err := r.waitForBuildNumber(ctx, 7)
	if err != nil || n != 12 {
		t.Fatalf("waitForBuildNumber = (%d,%v), want (12,nil)", n, err)
	}
}
