package api

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
)

// agentChatIdentityApp names the app whose ServiceIdentity the console's own
// agent chat authenticates as. It is the console itself: chat is the last AI
// caller on the platform that is not a customer app, and ADR-021 exists to put
// every caller on an identity the console can see and revoke.
const agentChatIdentityApp = "cloud-console"

// agentChatIdentityRefreshInterval bounds how long a rotated token can leave
// chat answering 401. Rotation re-mints and rewrites the delivered Secret, so
// re-reading it is the whole repair.
const agentChatIdentityRefreshInterval = 10 * time.Minute

// currentAgentChatKey returns the console's own identity token, or "" before
// the first successful resolve. It is the llmchat.Client KeyFunc, so it runs
// on every gateway request and must stay allocation-cheap and race-free.
func (h *Handler) currentAgentChatKey() string {
	if p := h.agentChatIdentityKey.Load(); p != nil {
		return *p
	}
	return ""
}

// StartAgentChatIdentityRefresher keeps currentAgentChatKey pointing at a live
// token for the console's own ServiceIdentity.
//
// The console's workload runs in argocd-prod while its identity Secret is
// delivered to its project namespace (platform-prod), and Kubernetes has no
// cross-namespace secretKeyRef -- so the credential cannot be mounted the way
// a customer app mounts its own. Reading it through the API server is the only
// path that does not end in a second hand-pasted copy of the token, which is
// exactly the thing ADR-021 removes.
//
// It deliberately does NOT take the identity-delivery advisory lock. Delivery
// is a write and must happen once per cluster; this is a read and must happen
// on every replica, or the replica that lost the lock keeps serving chat with
// a stale key.
func (h *Handler) StartAgentChatIdentityRefresher(ctx context.Context) {
	clientset := newAppHealthClientset()
	if clientset == nil {
		log.Printf("agent-chat-identity: no in-cluster client, using static key")
		return
	}
	log.Printf("agent-chat-identity: started app=%s interval=%s",
		agentChatIdentityApp, agentChatIdentityRefreshInterval)
	h.refreshAgentChatIdentityKey(ctx, clientset)
	go func() {
		t := time.NewTicker(agentChatIdentityRefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.refreshAgentChatIdentityKey(ctx, clientset)
			}
		}
	}()
}

// refreshAgentChatIdentityKey re-reads the delivered token once. A failure
// leaves the previously resolved key in place: a transient API-server error
// must not take chat down, and the next tick retries.
func (h *Handler) refreshAgentChatIdentityKey(ctx context.Context, clientset kubernetes.Interface) {
	token, err := h.resolveDeliveredIdentityToken(ctx, clientset, agentChatIdentityApp)
	if err != nil {
		log.Printf("agent-chat-identity: resolve: %v", err)
		return
	}
	if token == "" {
		if h.agentChatIdentityKey.Load() == nil {
			log.Printf("agent-chat-identity: no delivered token for app=%s yet", agentChatIdentityApp)
		}
		return
	}
	prev := h.agentChatIdentityKey.Swap(&token)
	if prev == nil || *prev != token {
		log.Printf("agent-chat-identity: token resolved for app=%s", agentChatIdentityApp)
	}
}

// resolveDeliveredIdentityToken finds the live plaintext token of an app's identity by
// locating the Secret the delivery loop wrote for it, and returns "" when the
// app has no live delivered credential.
//
// The Secret is found by the identity label rather than by namespace, so the
// lookup keeps working if the app's project or environment ever changes -- the
// same property that makes an app survive a move.
func (h *Handler) resolveDeliveredIdentityToken(ctx context.Context, clientset kubernetes.Interface, appName string) (string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT id FROM service_identities
		  WHERE app_name = $1 AND revoked_at IS NULL
		  ORDER BY created_at`,
		appName,
	)
	if err != nil {
		return "", err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}

	w := &identityDeliveryWatcher{clientset: clientset, h: h}
	for _, id := range ids {
		token, err := w.adoptToken(ctx, id)
		if err != nil {
			return "", err
		}
		if token != "" {
			return token, nil
		}
	}
	return "", nil
}
