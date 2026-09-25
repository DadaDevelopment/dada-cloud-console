package agentruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestSyntheticActor(t *testing.T) {
	for name, want := range map[string]bool{
		"eval_t12_fxpro_existing_2875a0": true,
		"@qa_target1_x":                  true,
		"dada_roll_probe":                true,
		"alexkekiy":                      false,
		"ViktoriaIzgagina":               false,
		"":                               false,
		"medieval_fan":                   false,
	} {
		if got := syntheticActor(name); got != want {
			t.Errorf("syntheticActor(%q) = %v, want %v", name, got, want)
		}
	}
	t.Setenv(syntheticUsernamesEnv, "")
	if syntheticActor("eval_t01") {
		t.Fatal("an empty list must switch the filter off")
	}
}

func TestCRMSyncSkipsSyntheticActors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	conv := Conversation{ID: uuid.New(), AgentName: "tg-exchange-support", Channel: "telegram", ExternalID: "-90012345678", ActorUsername: "eval_t12_fxpro_existing_2875a0"}
	contacts := &ContactSync{endpoint: srv.URL, token: "t", agent: "tg-exchange-support", client: srv.Client()}
	if err := contacts.Ensure(context.Background(), conv); err != nil {
		t.Fatalf("contact sync of an eval user: %v", err)
	}
	state := &StateSync{endpoint: srv.URL, token: "t", agent: "tg-exchange-support", client: srv.Client()}
	if err := state.Push(context.Background(), conv, RuntimeState{}, ""); err != nil {
		t.Fatalf("state sync of an eval user: %v", err)
	}
	if calls != 0 {
		t.Fatalf("eval traffic reached the CRM %d times", calls)
	}
}
