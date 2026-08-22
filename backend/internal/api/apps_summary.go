package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// appSummary is the thin form of an app listing: an address and a state, and
// nothing else.
//
// The full listing hands back every ResourceSnapshot in the environment,
// summary_json and all. That is the shape the console's app grid needs and the
// wrong shape for anything else — on 2026-08-21 the response for one project
// did not fit in an agent's context window, so the id needed for the next call
// had to be recovered by writing the body to a file and parsing it there.
//
// Ref is the same address the resolve endpoint accepts, so a caller that found
// an app here can address it directly next time instead of walking back.
type appSummary struct {
	Ref           string    `json:"ref"`
	Name          string    `json:"name"`
	Project       string    `json:"project"`
	Env           string    `json:"env"`
	ProjectID     uuid.UUID `json:"project_id"`
	EnvironmentID uuid.UUID `json:"environment_id"`
	Phase         string    `json:"phase"`
	Image         string    `json:"image,omitempty"`
	URL           string    `json:"url,omitempty"`
}

// filterAppsByName narrows a listing to one app when the caller knows its name.
// An exact match is preferred over substring matches so that an app whose name
// is a prefix of another's still resolves to itself.
func filterAppsByName(apps []models.ResourceSnapshot, filter string) []models.ResourceSnapshot {
	for _, a := range apps {
		if strings.EqualFold(a.Name, filter) {
			return []models.ResourceSnapshot{a}
		}
	}
	needle := strings.ToLower(filter)
	out := []models.ResourceSnapshot{}
	for _, a := range apps {
		if strings.Contains(strings.ToLower(a.Name), needle) {
			out = append(out, a)
		}
	}
	return out
}

// summarizeApps projects a listing onto appSummary, carrying the project and
// environment NAMES into every row so a caller never has to make a return trip
// to learn what the ids it was handed refer to.
func summarizeApps(apps []models.ResourceSnapshot, projectID, envID uuid.UUID, projectName, envName string) []appSummary {
	out := make([]appSummary, 0, len(apps))
	for _, a := range apps {
		s := appSummary{
			Ref:           joinRef(projectName, envName, a.Name),
			Name:          a.Name,
			Project:       projectName,
			Env:           envName,
			ProjectID:     projectID,
			EnvironmentID: envID,
			Phase:         a.Phase,
		}
		if len(a.SummaryJSON) > 0 {
			var m map[string]any
			if json.Unmarshal(a.SummaryJSON, &m) == nil {
				s.Image, _ = m["image"].(string)
				s.URL, _ = m["url"].(string)
			}
		}
		out = append(out, s)
	}
	return out
}

// projectAndEnvNames looks up the names behind a pair of ids. Failure is not an
// error for the caller: an unnamed listing is still a correct listing, it is
// just one the caller has to resolve by hand.
func (h *Handler) projectAndEnvNames(ctx context.Context, projectID, envID uuid.UUID) (string, string) {
	var projectName, envName string
	_ = h.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&projectName)
	_ = h.pool.QueryRow(ctx, `SELECT name FROM environments WHERE id = $1`, envID).Scan(&envName)
	return projectName, envName
}
