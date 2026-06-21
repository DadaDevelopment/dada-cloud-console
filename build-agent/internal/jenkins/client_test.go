package jenkins

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJobPath(t *testing.T) {
	cases := map[string]string{
		"job":            "/job/job",
		"folder/job":     "/job/folder/job/job",
		"/folder/job/":   "/job/folder/job/job",
		"a/b/c":          "/job/a/job/b/job/c",
		"with space/job": "/job/with%20space/job/job",
	}
	for in, want := range cases {
		if got := jobPath(in); got != want {
			t.Errorf("jobPath(%q)=%q want %q", in, got, want)
		}
	}
}

func TestQueueIDFromLocation(t *testing.T) {
	id, err := queueIDFromLocation("https://j/queue/item/4242/")
	if err != nil || id != 4242 {
		t.Fatalf("got id=%d err=%v want 4242", id, err)
	}
	if _, err := queueIDFromLocation(""); err == nil {
		t.Fatal("want error on empty location")
	}
}

func TestTriggerResolveAndLogs(t *testing.T) {
	var triggered bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/crumbIssuer/api/json":
			w.WriteHeader(http.StatusNotFound) // crumbs disabled
		case r.URL.Path == "/job/web/buildWithParameters":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			if r.Form.Get("framework") != "web" {
				t.Errorf("framework param=%q", r.Form.Get("framework"))
			}
			triggered = true
			w.Header().Set("Location", "http://x/queue/item/7/")
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/queue/item/7/api/json":
			w.Write([]byte(`{"cancelled":false,"executable":{"number":11}}`))
		case r.URL.Path == "/job/web/11/api/json":
			w.Write([]byte(`{"number":11,"result":"SUCCESS","building":false,"duration":4200}`))
		case r.URL.Path == "/job/web/11/logText/progressiveText":
			if r.URL.Query().Get("start") == "0" {
				w.Header().Set("X-Text-Size", "10")
				w.Header().Set("X-More-Data", "true")
				w.Write([]byte("first line\n"[:10]))
			} else {
				w.Header().Set("X-Text-Size", "20")
				w.Write([]byte("done\n"))
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "t")
	ctx := context.Background()

	qid, err := c.TriggerBuild(ctx, "web", map[string]string{"framework": "web"})
	if err != nil || qid != 7 {
		t.Fatalf("trigger: qid=%d err=%v", qid, err)
	}
	if !triggered {
		t.Fatal("job not triggered")
	}

	num, ok, err := c.ResolveBuildNumber(ctx, 7)
	if err != nil || !ok || num != 11 {
		t.Fatalf("resolve: num=%d ok=%v err=%v", num, ok, err)
	}

	bi, err := c.GetBuild(ctx, "web", 11)
	if err != nil || bi.Result != "SUCCESS" || bi.Building {
		t.Fatalf("getbuild: %+v err=%v", bi, err)
	}

	text, next, more, err := c.ProgressiveText(ctx, "web", 11, 0)
	if err != nil || next != 10 || !more || text == "" {
		t.Fatalf("progressive#1: text=%q next=%d more=%v err=%v", text, next, more, err)
	}
	text2, next2, more2, err := c.ProgressiveText(ctx, "web", 11, next)
	if err != nil || next2 != 20 || more2 || text2 != "done\n" {
		t.Fatalf("progressive#2: text=%q next=%d more=%v err=%v", text2, next2, more2, err)
	}
}
