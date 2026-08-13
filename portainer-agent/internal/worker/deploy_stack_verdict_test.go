package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
)

func TestStackAdvancedDetectsARedeployWeLostTheAnswerTo(t *testing.T) {
	cases := []struct {
		name   string
		before portainer.Stack
		after  portainer.Stack
		want   bool
	}{
		{
			name:   "timestamp advanced",
			before: portainer.Stack{ID: 3, UpdateDate: 100},
			after:  portainer.Stack{ID: 3, UpdateDate: 160},
			want:   true,
		},
		{
			name:   "nothing moved",
			before: portainer.Stack{ID: 3, UpdateDate: 100},
			after:  portainer.Stack{ID: 3, UpdateDate: 100},
			want:   false,
		},
		{
			name:   "commit moved while the clock did not",
			before: portainer.Stack{ID: 3, UpdateDate: 100, GitConfig: &portainer.StackGitConfig{ConfigHash: "aaa"}},
			after:  portainer.Stack{ID: 3, UpdateDate: 100, GitConfig: &portainer.StackGitConfig{ConfigHash: "bbb"}},
			want:   true,
		},
		{
			name:   "same commit redeployed and the clock did not move",
			before: portainer.Stack{ID: 3, UpdateDate: 100, GitConfig: &portainer.StackGitConfig{ConfigHash: "aaa"}},
			after:  portainer.Stack{ID: 3, UpdateDate: 100, GitConfig: &portainer.StackGitConfig{ConfigHash: "aaa"}},
			want:   false,
		},
		{
			name:   "empty hash is not evidence",
			before: portainer.Stack{ID: 3, GitConfig: &portainer.StackGitConfig{ConfigHash: "aaa"}},
			after:  portainer.Stack{ID: 3, GitConfig: &portainer.StackGitConfig{ConfigHash: ""}},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stackAdvanced(tc.before, tc.after); got != tc.want {
				t.Fatalf("stackAdvanced = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRedeployLandedNeverSecondGuessesAnHTTPStatus pins the boundary that keeps
// this recovery honest: a status code is Portainer's verdict and must stand.
// Only a transport failure — no response at all — is worth confirming, and the
// non-transport path must return without touching the API.
func TestRedeployLandedNeverSecondGuessesAnHTTPStatus(t *testing.T) {
	w := &VMWatcher{}
	statusErr := fmt.Errorf("redeploy stack: %w", errors.New("portainer PUT /api/stacks/3/git/redeploy: status 409: conflict"))
	if w.redeployLanded(context.Background(), portainer.Stack{ID: 3}, statusErr) {
		t.Fatal("an HTTP status verdict was overridden")
	}
}

func TestRedeployLandedRecognisesAWrappedTransportError(t *testing.T) {
	transport := &portainer.TransportError{
		Method: "PUT",
		Path:   "/api/stacks/3/git/redeploy?endpointId=3",
		Err:    context.DeadlineExceeded,
	}
	wrapped := fmt.Errorf("redeploy stack: %w", transport)
	var got *portainer.TransportError
	if !errors.As(wrapped, &got) {
		t.Fatal("a wrapped TransportError is invisible to the confirm path")
	}
	if got.Path != transport.Path {
		t.Fatalf("unwrapped the wrong error: %v", got)
	}
}
