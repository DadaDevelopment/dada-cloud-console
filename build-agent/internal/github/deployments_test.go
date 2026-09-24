package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type rewriteTransport struct{ target *url.URL }

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = t.target.Scheme
	r.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c := New("1", "")
	c.http = &http.Client{Transport: rewriteTransport{u}}
	c.tokens[7] = cachedToken{token: "inst-token", expires: time.Now().Add(time.Hour)}
	return c
}

func TestCreateDeploymentRequest(t *testing.T) {
	var path, auth string
	var body map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 991}`))
	})
	id, err := c.CreateDeployment(context.Background(), 7, "keksmd/site", DeploymentRequest{
		Ref: "abc123", Environment: "Production", Production: true, Description: "dada cloud: site",
	})
	if err != nil || id != 991 {
		t.Fatalf("CreateDeployment = %d, %v; want 991", id, err)
	}
	if path != "/repos/keksmd/site/deployments" || auth != "token inst-token" {
		t.Errorf("request = %s auth %q", path, auth)
	}
	rc, ok := body["required_contexts"].([]any)
	if !ok || len(rc) != 0 {
		t.Errorf("required_contexts = %#v; must be an explicit empty list or GitHub 409s on our own pending status", body["required_contexts"])
	}
	if body["ref"] != "abc123" || body["environment"] != "Production" || body["production_environment"] != true || body["auto_merge"] != false {
		t.Errorf("body = %#v", body)
	}
}

func TestPostDeploymentStatusRequest(t *testing.T) {
	var path string
	var body map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
	})
	err := c.PostDeploymentStatus(context.Background(), 7, "keksmd/site", 991, DeploymentStatus{
		State: "in_progress", LogURL: "https://console/b/1", Description: "Building",
	})
	if err != nil {
		t.Fatalf("PostDeploymentStatus: %v", err)
	}
	if path != "/repos/keksmd/site/deployments/991/statuses" {
		t.Errorf("path = %s", path)
	}
	if _, has := body["environment_url"]; has {
		t.Error("empty environment_url was sent; GitHub would render a blank link")
	}
	if body["state"] != "in_progress" || body["log_url"] != "https://console/b/1" {
		t.Errorf("body = %#v", body)
	}
}

func TestPostDeploymentStatusSurfacesRefusal(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	})
	if err := c.PostDeploymentStatus(context.Background(), 7, "keksmd/site", 1, DeploymentStatus{State: "success"}); err == nil {
		t.Fatal("a 403 from GitHub read as success")
	}
}
