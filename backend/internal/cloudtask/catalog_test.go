package cloudtask

import (
	"context"
	"errors"
	"testing"
)

// stubCounterResolver is a CounterResolver test double: it returns id when err
// is nil, else the error.
type stubCounterResolver struct {
	id  string
	err error
}

func (s stubCounterResolver) Resolve(context.Context, string) (string, error) {
	return s.id, s.err
}

func TestCatalog_MetrikaEntry(t *testing.T) {
	e, ok := Lookup("yandex-metrika-goals")
	if !ok {
		t.Fatal("metrika entry missing")
	}
	if !e.AppliesTo("web") || e.AppliesTo("database") {
		t.Fatal("AppliesTo should be web-only")
	}

	res := stubCounterResolver{id: "98765432"}
	counterID, err := res.Resolve(context.Background(), "my-app")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	params, err := e.ResolveParams(ResolverCfg{CounterID: counterID, ProjectType: "front"})
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	if params["counterId"] != "98765432" {
		t.Fatalf("counterId not propagated: %v", params)
	}
	if params["projectType"] != "front" {
		t.Fatalf("projectType not propagated: %v", params)
	}
	if _, present := params["metrika_oauth_token"]; present {
		t.Fatalf("metrika_oauth_token must never be in params: %v", params)
	}
	if _, present := params["goals"]; present {
		t.Fatalf("goals must not be in params: %v", params)
	}
	if _, present := params["archetype"]; present {
		t.Fatalf("archetype should be omitted when not provided")
	}
}

func TestCatalog_MetrikaProjectTypeDefaultsToFront(t *testing.T) {
	e, _ := Lookup("yandex-metrika-goals")
	params, err := e.ResolveParams(ResolverCfg{CounterID: "1"})
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	if params["projectType"] != "front" {
		t.Fatalf("projectType should default to front: %v", params)
	}
}

func TestCatalog_MetrikaArchetypeIncludedWhenSet(t *testing.T) {
	e, _ := Lookup("yandex-metrika-goals")
	params, err := e.ResolveParams(ResolverCfg{CounterID: "1", Archetype: "landing"})
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	if params["archetype"] != "landing" {
		t.Fatalf("archetype not propagated: %v", params)
	}
}

func TestCatalog_MetrikaResolve_RequiresCounterID(t *testing.T) {
	e, _ := Lookup("yandex-metrika-goals")
	if _, err := e.ResolveParams(ResolverCfg{}); err == nil {
		t.Fatal("expected error when counterId missing")
	}
}

func TestCatalog_CounterResolverErrorSurfaces(t *testing.T) {
	res := stubCounterResolver{err: errors.New("YandexMetrikaCounter access not configured")}
	if _, err := res.Resolve(context.Background(), "my-app"); err == nil {
		t.Fatal("expected resolver error to surface")
	}
}

func TestCatalog_DBAdvisoryEntry(t *testing.T) {
	e, ok := Lookup("db-advisory-fix")
	if !ok {
		t.Fatal("db-advisory-fix entry missing")
	}
	if e.NeedsCounter {
		t.Fatal("db-advisory-fix must not require a Metrika counter")
	}
	resolved, err := e.ResolveParams(ResolverCfg{})
	if err != nil {
		t.Fatalf("ResolveParams: %v", err)
	}
	merged := MergeClientParams(e, resolved, map[string]string{
		"finding":  "Index public.big_pkey takes 563 MB and was never used.",
		"sql":      "DROP INDEX CONCURRENTLY public.big_pkey;",
		"database": "odds-research",
		"evil":     "ignored",
	})
	if merged["finding"] == "" || merged["sql"] == "" || merged["database"] != "odds-research" {
		t.Fatalf("client params not merged: %v", merged)
	}
	if _, ok := merged["evil"]; ok {
		t.Fatal("non-whitelisted client param must be dropped")
	}
}

func TestMergeClientParams_ServerValueWinsAndLongValuesAreCapped(t *testing.T) {
	e := Entry{ClientParamKeys: []string{"finding", "counterId"}}
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'x'
	}
	merged := MergeClientParams(e, map[string]any{"counterId": "server"}, map[string]string{
		"counterId": "client-spoof",
		"finding":   string(long),
	})
	if merged["counterId"] != "server" {
		t.Fatal("server-resolved value must win over a client key collision")
	}
	if len(merged["finding"].(string)) != 4000 {
		t.Fatalf("client value must be capped at 4000, got %d", len(merged["finding"].(string)))
	}
}
