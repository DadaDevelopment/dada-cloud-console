package api

import (
	"context"
	"testing"

	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// TestResolveDeliveredIdentityTokenSurvivesNilPool pins the guard that builds
// #897-899 were missing.
//
// NewHandler starts the agent-chat identity refresher, and a caller that only
// wants the route table (TestOpenAPICoverage) hands NewHandler a nil pool. On a
// developer machine the refresher returns early because there is no in-cluster
// client, so the crash is invisible locally; on the Jenkins agent, which runs
// inside the cluster, the client exists and the very first resolve dereferenced
// the nil pool and took the whole api package down with a SIGSEGV.
//
// The test therefore skips the refresher's client check and calls the resolve
// directly with a fake clientset, which is the only way to reproduce the CI
// environment from a laptop.
func TestResolveDeliveredIdentityTokenSurvivesNilPool(t *testing.T) {
	h := &Handler{}

	token, err := h.resolveDeliveredIdentityToken(context.Background(), k8sfake.NewSimpleClientset(), agentChatIdentityApp)
	if err != nil {
		t.Fatalf("resolveDeliveredIdentityToken with no database: err=%v, want nil", err)
	}
	if token != "" {
		t.Fatalf("token=%q, want empty: a Handler without a database knows no identity", token)
	}
}
