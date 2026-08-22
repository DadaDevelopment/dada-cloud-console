package api

import (
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

func TestParseRef_ReadsBothAddressForms(t *testing.T) {
	cases := []struct {
		name                   string
		ref, project, env, app string
		wantP, wantE, wantA    string
	}{
		{name: "full ref", ref: "internal/prod/telemost-bot", wantP: "internal", wantE: "prod", wantA: "telemost-bot"},
		{name: "project only", ref: "internal", wantP: "internal"},
		{name: "project and env", ref: "internal/prod", wantP: "internal", wantE: "prod"},
		{name: "separate params", project: "internal", env: "prod", app: "bot", wantP: "internal", wantE: "prod", wantA: "bot"},
		{name: "ref wins over params", ref: "internal/prod", project: "other", env: "staging", app: "bot",
			wantP: "internal", wantE: "prod", wantA: "bot"},
		{name: "slashes and spaces are noise", ref: " /internal/prod/ ", wantP: "internal", wantE: "prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, e, a := parseRef(tc.ref, tc.project, tc.env, tc.app)
			if p != tc.wantP || e != tc.wantE || a != tc.wantA {
				t.Errorf("parseRef = %q/%q/%q, want %q/%q/%q", p, e, a, tc.wantP, tc.wantE, tc.wantA)
			}
		})
	}
}

func snapshot(name string, summary string) models.ResourceSnapshot {
	return models.ResourceSnapshot{Name: name, Phase: "Healthy", SummaryJSON: json.RawMessage(summary)}
}

func TestFilterAppsByName_PrefersTheExactMatch(t *testing.T) {
	apps := []models.ResourceSnapshot{
		snapshot("bot", `{}`),
		snapshot("bot-worker", `{}`),
	}

	got := filterAppsByName(apps, "bot")
	if len(got) != 1 || got[0].Name != "bot" {
		t.Fatalf("exact name resolved to %v, want just bot — otherwise naming an app still returns its neighbours", appNames(got))
	}

	got = filterAppsByName(apps, "work")
	if len(got) != 1 || got[0].Name != "bot-worker" {
		t.Fatalf("substring filter returned %v, want bot-worker", appNames(got))
	}

	if got = filterAppsByName(apps, "nothing"); len(got) != 0 {
		t.Fatalf("filter matched %v for a name that does not exist", appNames(got))
	}
}

func TestSummarizeApps_CarriesTheAddressBackToTheCaller(t *testing.T) {
	projectID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	envID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	got := summarizeApps(
		[]models.ResourceSnapshot{snapshot("telemost-bot", `{"image":"ghcr.io/x:1","url":"https://bot.example","port":8000}`)},
		projectID, envID, "internal", "prod")

	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	s := got[0]
	if s.Ref != "internal/prod/telemost-bot" {
		t.Errorf("ref = %q, want internal/prod/telemost-bot", s.Ref)
	}
	if s.Project != "internal" || s.Env != "prod" {
		t.Errorf("row carries ids without names (%q/%q), so the caller has to go back and ask", s.Project, s.Env)
	}
	if s.ProjectID != projectID || s.EnvironmentID != envID {
		t.Error("row lost the ids it is supposed to hand forward")
	}
	if s.Image != "ghcr.io/x:1" || s.URL != "https://bot.example" || s.Phase != "Healthy" {
		t.Errorf("row lost state: %+v", s)
	}

	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatal(err)
	}
	if _, leaked := fields["summary_json"]; leaked {
		t.Error("the summary view still carries summary_json — the thin listing is not thin")
	}
	if len(fields) > 9 {
		t.Errorf("summary row has %d fields (%v); it is meant to be an address plus a state", len(fields), fields)
	}
}

func appNames(apps []models.ResourceSnapshot) []string {
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, a.Name)
	}
	return out
}
