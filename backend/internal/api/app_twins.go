package api

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// TwinRef points from one app to its twin: the same repo deployed again under
// another project the same owner controls. A user who connects the same
// GitHub repo twice ends up with two apps of the same name in two different
// projects, one of them often a crashlooping leftover, with nothing in the
// console showing the two are related.
type TwinRef struct {
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	AppName      string `json:"app_name"`
	RepoFullName string `json:"repo_full_name"`
}

// FillTwins writes a "twin_of" entry into each app's summary when twins holds
// a TwinRef for that app's name. Pure: it only touches SummaryJSON, mirroring
// FillRepoFullNameAndSource, and never clobbers other summary keys.
func FillTwins(apps []models.ResourceSnapshot, twins map[string]TwinRef) {
	for i := range apps {
		twin, ok := twins[apps[i].Name]
		if !ok {
			continue
		}
		var m map[string]any
		if len(apps[i].SummaryJSON) > 0 {
			_ = json.Unmarshal(apps[i].SummaryJSON, &m)
		}
		if m == nil {
			m = map[string]any{}
		}
		m["twin_of"] = twin
		if b, err := json.Marshal(m); err == nil {
			apps[i].SummaryJSON = b
		}
	}
}

// RepoByAppFromSummaries pulls repo_full_name back out of each app's summary,
// keyed by app name. Archive uploads carry a synthetic repo_full_name of
// "upload/"+appName (see FillRepoFullNameAndSource) and are skipped: an
// uploaded archive is never a twin of anything, it has no repo to share.
func RepoByAppFromSummaries(apps []models.ResourceSnapshot) map[string]string {
	repoByApp := make(map[string]string, len(apps))
	for _, a := range apps {
		if len(a.SummaryJSON) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(a.SummaryJSON, &m); err != nil {
			continue
		}
		repo, _ := m["repo_full_name"].(string)
		if repo == "" || strings.HasPrefix(repo, "upload/") {
			continue
		}
		repoByApp[a.Name] = repo
	}
	return repoByApp
}

// loadAppTwins looks up, for each repo in repoByApp, whether the same owner
// has that repo connected in another project. Query failure returns nil
// rather than breaking ListApps: a missing twin hint is not worth failing the
// whole app list over.
func (h *Handler) loadAppTwins(ctx context.Context, projectID uuid.UUID, repoByApp map[string]string) map[string]TwinRef {
	if len(repoByApp) == 0 {
		return nil
	}
	repos := make([]string, 0, len(repoByApp))
	seen := make(map[string]struct{}, len(repoByApp))
	for _, repo := range repoByApp {
		if _, ok := seen[repo]; ok {
			continue
		}
		seen[repo] = struct{}{}
		repos = append(repos, repo)
	}

	rows, err := h.pool.Query(ctx,
		`SELECT gr.repo_full_name, gr.app_name, gr.project_id,
		        COALESCE(NULLIF(p.display_name, ''), p.name)
		 FROM git_repos gr
		 JOIN projects p ON p.id = gr.project_id
		 WHERE gr.project_id <> $1
		   AND gr.repo_full_name = ANY($2)
		   AND p.owner_id = (SELECT owner_id FROM projects WHERE id = $1)
		 ORDER BY p.name`,
		projectID, repos,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type match struct {
		twin        TwinRef
		projectName string
	}
	byRepo := make(map[string]match, len(repos))
	for rows.Next() {
		var repo, appName, projectIDStr, projectName string
		if scanErr := rows.Scan(&repo, &appName, &projectIDStr, &projectName); scanErr != nil {
			continue
		}
		if _, exists := byRepo[repo]; exists {
			continue
		}
		byRepo[repo] = match{
			twin: TwinRef{
				ProjectID:    projectIDStr,
				ProjectName:  projectName,
				AppName:      appName,
				RepoFullName: repo,
			},
			projectName: projectName,
		}
	}
	if err := rows.Err(); err != nil {
		return nil
	}

	twins := make(map[string]TwinRef, len(byRepo))
	localApps := make([]string, 0, len(repoByApp))
	for appName := range repoByApp {
		localApps = append(localApps, appName)
	}
	sort.Strings(localApps)
	for _, appName := range localApps {
		repo := repoByApp[appName]
		if m, ok := byRepo[repo]; ok {
			twins[appName] = m.twin
		}
	}
	if len(twins) == 0 {
		return nil
	}
	return twins
}
