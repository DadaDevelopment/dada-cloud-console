package tggateway

import (
	"context"
	"errors"
	"testing"
)

type userSessionFake struct{ fakeTelegram }

func (userSessionFake) GetMe(context.Context, string) (string, error) { return "real_person", nil }

func TestReconcile_UserTransportWaitsForItsClient(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, fakeA2A{}, nil)
	ctx := context.Background()

	if err := store.Upsert(ctx, Binding{AgentName: "agent-u", BotToken: "session-1", Transport: TransportUser, Status: StatusActive}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if mgr.liveCount() != 0 {
		t.Fatalf("user binding must not run on the bot client, got %d pollers", mgr.liveCount())
	}

	mgr.RegisterTransport(TransportUser, userSessionFake{})
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, func() bool { return mgr.liveCount() == 1 })
	mgr.mu.Lock()
	transport := mgr.pollers["agent-u"].transport
	mgr.mu.Unlock()
	if transport != TransportUser {
		t.Fatalf("expected user poller, got %q", transport)
	}
}

func TestReconcile_RestartsPollerOnTransportChange(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, fakeA2A{}, nil)
	mgr.RegisterTransport(TransportUser, userSessionFake{})
	ctx := context.Background()

	if err := store.Upsert(ctx, Binding{AgentName: "agent-s", BotToken: "tok", Status: StatusActive}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, func() bool { return mgr.liveCount() == 1 })

	if err := store.Upsert(ctx, Binding{AgentName: "agent-s", BotToken: "tok", Transport: TransportUser, Status: StatusActive}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	waitFor(t, func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		p, ok := mgr.pollers["agent-s"]
		return ok && p.transport == TransportUser
	})
}

func TestBindTransport_RejectsUnwiredTransportAndValidatesViaItsClient(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, fakeA2A{}, nil)
	ctx := context.Background()

	if _, err := mgr.BindTransport(ctx, "agent-x", "proj-1", "session", TransportUser); !errors.Is(err, ErrTransportUnavailable) {
		t.Fatalf("expected ErrTransportUnavailable, got %v", err)
	}
	if _, err := store.Get(ctx, "agent-x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row must not be stored for an unwired transport, got %v", err)
	}

	mgr.RegisterTransport(TransportUser, userSessionFake{})
	b, err := mgr.BindTransport(ctx, "agent-x", "proj-1", "session", TransportUser)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if b.BotUsername != "real_person" || b.Transport != TransportUser {
		t.Fatalf("expected user transport validated by its own client, got %+v", b)
	}
	stored, err := store.Get(ctx, "agent-x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Transport != TransportUser {
		t.Fatalf("expected transport persisted, got %q", stored.Transport)
	}
	waitFor(t, func() bool { return mgr.liveCount() == 1 })
}

func TestParseTransport(t *testing.T) {
	for raw, want := range map[string]Transport{"": TransportBot, "bot": TransportBot, "user": TransportUser} {
		got, ok := ParseTransport(raw)
		if !ok || got != want {
			t.Fatalf("ParseTransport(%q) = %q, %v", raw, got, ok)
		}
	}
	if _, ok := ParseTransport("mtproto"); ok {
		t.Fatalf("unknown transport must be rejected")
	}
}
