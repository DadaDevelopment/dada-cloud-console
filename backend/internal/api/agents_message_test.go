package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/tggateway"
)

// fakeA2AClient is a stand-in for tggateway.NewA2AClient(): the real one talks
// to a cluster-internal *.kagent.svc.cluster.local address a unit test cannot
// reach, so tests inject this instead through the same tggateway.A2AClient
// interface SendAgentMessage depends on.
type fakeA2AClient struct {
	gotAgent, gotText string
	reply             string
	err               error
}

func (f *fakeA2AClient) Send(ctx context.Context, agentName, text string) (string, error) {
	return f.SendWithContext(ctx, agentName, "", text)
}

func (f *fakeA2AClient) SendWithContext(_ context.Context, agentName, contextID, text string) (string, error) {
	f.gotAgent, f.gotText = agentName, text
	return f.reply, f.err
}

var _ tggateway.A2AClient = (*fakeA2AClient)(nil)

func TestSendAgentMessage_RequireAuth(t *testing.T) {
	h := &Handler{}
	c, w := telegramTestCtx(t, "POST", "/agents/x/message", `{"text":"hi"}`, nil, "x")
	h.SendAgentMessage(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestSendAgentMessage_Unconfigured(t *testing.T) {
	h := &Handler{}
	c, w := telegramTestCtx(t, "POST", "/agents/x/message", `{"text":"hi"}`, testAgentClaims(), "x")
	h.SendAgentMessage(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
}

func TestSendAgentMessage_EmptyText(t *testing.T) {
	h := &Handler{a2a: &fakeA2AClient{}}
	c, w := telegramTestCtx(t, "POST", "/agents/x/message", `{"text":""}`, testAgentClaims(), "x")
	h.SendAgentMessage(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestSendAgentMessage_UnknownAgent refuses to relay to an agent this console
// has never heard of, mirroring TestBindAgentTelegram_UnknownAgent.
func TestSendAgentMessage_UnknownAgent(t *testing.T) {
	pool := testAgentGatePool(t)
	h := &Handler{pool: pool, a2a: &fakeA2AClient{}}
	c, w := telegramTestCtx(t, "POST", "/agents/no-such-agent/message", `{"text":"hi"}`, testAgentClaims(), "no-such-agent")
	h.SendAgentMessage(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestSendAgentMessage_Success round-trips a reply from the A2A client back
// through the endpoint's JSON body.
func TestSendAgentMessage_Success(t *testing.T) {
	pool := testAgentGatePool(t)
	owner := seedUser(t, pool)
	projectID := seedProjectWithOwner(t, pool, owner)
	envID := seedEnvironment(t, pool, projectID)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
		 VALUES ($1, $2, 'ManagedAgent', 'msg-test-agent', 'Pending')`,
		projectID, envID); err != nil {
		t.Fatalf("seed managed agent snapshot: %v", err)
	}

	fake := &fakeA2AClient{reply: "привет, чем помочь?"}
	h := &Handler{pool: pool, a2a: fake}
	c, w := telegramTestCtx(t, "POST", "/agents/msg-test-agent/message", `{"text":"привет"}`, testAgentClaims(), "msg-test-agent")
	h.SendAgentMessage(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Reply != fake.reply {
		t.Errorf("reply = %q, want %q", resp.Reply, fake.reply)
	}
	if fake.gotAgent != "msg-test-agent" || fake.gotText != "привет" {
		t.Errorf("a2a saw agent=%q text=%q", fake.gotAgent, fake.gotText)
	}
}

// TestSendAgentMessage_A2AError surfaces an unreachable/erroring agent as 502,
// not a 500 that hides whose fault it is.
func TestSendAgentMessage_A2AError(t *testing.T) {
	pool := testAgentGatePool(t)
	owner := seedUser(t, pool)
	projectID := seedProjectWithOwner(t, pool, owner)
	envID := seedEnvironment(t, pool, projectID)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
		 VALUES ($1, $2, 'ManagedAgent', 'msg-test-agent-err', 'Pending')`,
		projectID, envID); err != nil {
		t.Fatalf("seed managed agent snapshot: %v", err)
	}

	fake := &fakeA2AClient{err: errors.New("a2a msg-test-agent-err: status 503")}
	h := &Handler{pool: pool, a2a: fake}
	c, w := telegramTestCtx(t, "POST", "/agents/msg-test-agent-err/message", `{"text":"hi"}`, testAgentClaims(), "msg-test-agent-err")
	h.SendAgentMessage(c)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", w.Code, w.Body.String())
	}
}
