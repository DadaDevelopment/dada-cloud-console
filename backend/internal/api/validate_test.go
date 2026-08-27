package api

import "testing"

func TestValidateKubeName(t *testing.T) {
	valid := []string{"codex-lb-db", "profi-db", "a", "abc123", "a-b-c"}
	for _, name := range valid {
		if err := validateKubeName(name); err != nil {
			t.Errorf("validateKubeName(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []string{"", "UpperCase", "-leading", "trailing-", "has spaces", "has_underscore",
		"toolongname-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, name := range invalid {
		if err := validateKubeName(name); err == nil {
			t.Errorf("validateKubeName(%q) expected error, got nil", name)
		}
	}
}

func TestValidatePgName(t *testing.T) {
	valid := []string{"codexlb", "profi-db", "my-database-1", "a", "mlflow-v2"}
	for _, name := range valid {
		if err := validatePgName(name); err != nil {
			t.Errorf("validatePgName(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []string{"", "1startswithdigit", "has_underscore", "has space", "UpperCase",
		"-leading", "trailing-",
		"toolongname-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, name := range invalid {
		if err := validatePgName(name); err == nil {
			t.Errorf("validatePgName(%q) expected error, got nil", name)
		}
	}
}

// TestValidateKubeName_RejectsFullRefs is a regression test for the [live]
// production bug found 2026-08: creating leadgen's managed database with
// app_ref="leadgen/prod/lead-gen" (a full project/env/app ref -- the exact
// shape this MCP server's OWN addressing convention teaches everywhere else,
// see mcp/addressing.go's ref="project/env/app") was accepted uncritically,
// stored verbatim as ServiceDatabaseV2.spec.appRef, and later broke secret
// resolution: cloudtask.DBCredentialsResolver.Resolve treats app_ref as a
// bare k8s resource name and builds the connection secret name as
// "<appRef>-db-credentials" (dbcreds.go:89) -- a name containing "/" is not
// a valid k8s Secret name, so it can never be found, and queryDatabase /
// GetDatabaseCredentials degrade to a confusing DATABASE_NOT_ACCESSIBLE with
// no indication the real cause was an invalid app_ref supplied at creation
// time, weeks earlier.
//
// createManagedDatabase (databases.go) now runs app_ref through this exact
// validator (already used for req.Name) before accepting it. This test
// pins the validator's behavior on the specific inputs that matter for that
// call site, independent of any database fixture.
func TestValidateKubeName_RejectsFullRefs(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"bare app name", "lead-gen", false},
		{"bare app name with digits", "app2", false},
		{"single char", "a", false},
		{"full project/env/app ref", "leadgen/prod/lead-gen", true},
		{"project/env ref", "leadgen/prod", true},
		{"leading slash", "/lead-gen", true},
		{"trailing slash", "lead-gen/", true},
		{"uppercase", "LeadGen", true},
		{"underscore", "lead_gen", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateKubeName(tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("validateKubeName(%q) = nil, want an error", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateKubeName(%q) = %v, want nil", tc.in, err)
			}
		})
	}
}
