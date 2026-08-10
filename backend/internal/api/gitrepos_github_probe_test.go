package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestGithubRepoPubliclyClonable pins the status-code classification behind
// linkGitRepo's connect-time probe. It exists because user tech@... connected
// app a2ahub-landing (repo keksmd/a2ahub-landing) with no installation and no
// token; the repo was private, and the connect succeeded anyway -- the build
// only failed 6+ days later with a bare "could not read Username for
// 'https://github.com'". A decisive 401/403/404 must fail the connect up
// front; anything inconclusive (network error, timeout, 5xx) must not, since
// this probe can never be allowed to block a connect on a flaky github.com.
func TestGithubRepoPubliclyClonable(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		wantClonable bool
		wantDecisive bool
	}{
		{name: "200 is a public clonable repo", status: http.StatusOK, wantClonable: true, wantDecisive: true},
		{name: "401 is a private repo, decisive", status: http.StatusUnauthorized, wantClonable: false, wantDecisive: true},
		{name: "403 is a private repo, decisive", status: http.StatusForbidden, wantClonable: false, wantDecisive: true},
		{name: "404 is a gone repo, decisive", status: http.StatusNotFound, wantClonable: false, wantDecisive: true},
		{name: "500 is not decisive", status: http.StatusInternalServerError, wantClonable: false, wantDecisive: false},
		{name: "unexpected status is not decisive", status: http.StatusTeapot, wantClonable: false, wantDecisive: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			clonable, decisive := probeAt(t, srv.URL)
			if clonable != tc.wantClonable || decisive != tc.wantDecisive {
				t.Errorf("probeAt(%d) = (%v, %v), want (%v, %v)", tc.status, clonable, decisive, tc.wantClonable, tc.wantDecisive)
			}
		})
	}

	t.Run("network error is not decisive", func(t *testing.T) {
		origClient := githubCloneProbeClient
		githubCloneProbeClient = &http.Client{
			Timeout: time.Second,
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			}),
		}
		defer func() { githubCloneProbeClient = origClient }()

		clonable, decisive := githubRepoPubliclyClonable(context.Background(), "keksmd/a2ahub-landing")
		if clonable != false || decisive != false {
			t.Errorf("githubRepoPubliclyClonable on network error = (%v, %v), want (false, false)", clonable, decisive)
		}
	})
}

// probeAt runs githubRepoPubliclyClonable against srvURL instead of the real
// github.com, by swapping in a transport that rewrites the request's
// destination while leaving the path/query the probe built untouched.
func probeAt(t *testing.T, srvURL string) (clonable bool, decisive bool) {
	t.Helper()
	target, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}

	origClient := githubCloneProbeClient
	githubCloneProbeClient = &http.Client{
		Timeout: 2 * time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			redirected := r.Clone(r.Context())
			redirected.URL.Scheme = target.Scheme
			redirected.URL.Host = target.Host
			redirected.Host = target.Host
			return http.DefaultTransport.RoundTrip(redirected)
		}),
	}
	defer func() { githubCloneProbeClient = origClient }()

	return githubRepoPubliclyClonable(context.Background(), "keksmd/a2ahub-landing")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
