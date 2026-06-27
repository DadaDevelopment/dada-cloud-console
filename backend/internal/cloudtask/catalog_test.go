package cloudtask

import "testing"

func TestCatalog_MetrikaEntry(t *testing.T) {
	e, ok := Lookup("yandex-metrika-goals")
	if !ok {
		t.Fatal("metrika entry missing")
	}
	if !e.AppliesTo("web") || e.AppliesTo("database") {
		t.Fatal("AppliesTo should be web-only")
	}
	params, err := e.ResolveParams(ResolverCfg{MetrikaOAuthToken: "tok"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if params["metrika_oauth_token"] != "tok" {
		t.Fatalf("token not propagated: %v", params)
	}
	goals, _ := params["goals"].([]map[string]string)
	if len(goals) != 4 {
		t.Fatalf("want 4 default goals, got %d", len(goals))
	}
	if _, present := params["site_url"]; present {
		t.Fatalf("site_url should be omitted when not provided")
	}
}

func TestCatalog_MetrikaResolve_RequiresToken(t *testing.T) {
	e, _ := Lookup("yandex-metrika-goals")
	if _, err := e.ResolveParams(ResolverCfg{}); err == nil {
		t.Fatal("expected error when token missing")
	}
}

func TestCatalog_MetrikaResolve_AddsSiteURLWhenKnown(t *testing.T) {
	e, _ := Lookup("yandex-metrika-goals")
	params, err := e.ResolveParams(ResolverCfg{MetrikaOAuthToken: "tok", SiteURL: "https://app.example.com"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if params["site_url"] != "https://app.example.com" {
		t.Fatalf("site_url not propagated: %v", params["site_url"])
	}
}
