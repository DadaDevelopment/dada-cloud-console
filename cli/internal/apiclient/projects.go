package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"time"
)

// Project mirrors the subset of models.Project the CLI needs to let a user
// pick a target.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// Environment mirrors the subset of models.Environment the CLI needs.
type Environment struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Runtime   string `json:"runtime"`
}

type listProjectsResponse struct {
	Projects []Project `json:"projects"`
}

// ListProjects returns every project the caller can access, per
// GET /projects (backend/internal/api/projects.go ListProjects).
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out listProjectsResponse
	if err := c.doJSON(ctx, "GET", "/projects", nil, "", &out); err != nil {
		return nil, err
	}
	return out.Projects, nil
}

// createProjectRequest mirrors backend/internal/api/projects.go:113. Only
// slug is required; an empty default_environment makes the server name the
// first environment "prod".
type createProjectRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name,omitempty"`
}

// createProjectResponse mirrors the 201 body of POST /projects
// (backend/internal/api/projects.go:244).
type createProjectResponse struct {
	ProjectID            string `json:"project_id"`
	DefaultEnvironmentID string `json:"default_environment_id"`
	OrgID                string `json:"org_id"`
	Role                 string `json:"role"`
}

// CreateProject creates a project owned by the caller in their personal org.
// The slug is unique platform-wide, so a name another tenant already took
// comes back as HTTP 409 - callers are expected to retry with a variant.
func (c *Client) CreateProject(ctx context.Context, slug string) (Project, error) {
	body, err := json.Marshal(createProjectRequest{Slug: slug, DisplayName: slug})
	if err != nil {
		return Project{}, err
	}
	var out createProjectResponse
	if err := c.doJSON(ctx, "POST", "/projects", bytes.NewReader(body), "application/json", &out); err != nil {
		return Project{}, err
	}
	return Project{ID: out.ProjectID, Name: slug, DisplayName: slug, Role: out.Role}, nil
}

type getProjectResponse struct {
	Project      Project       `json:"project"`
	Role         string        `json:"role"`
	Environments []Environment `json:"environments"`
}

// GetProjectEnvironments returns the environments of one project, per
// GET /projects/:projectId (backend/internal/api/projects.go GetProject).
func (c *Client) GetProjectEnvironments(ctx context.Context, projectID string) ([]Environment, error) {
	var out getProjectResponse
	if err := c.doJSON(ctx, "GET", "/projects/"+projectID, nil, "", &out); err != nil {
		return nil, err
	}
	return out.Environments, nil
}

// resourceSnapshot is the subset of models.ResourceSnapshot the CLI reads to
// find an app's live URL after a build lands.
type resourceSnapshot struct {
	Name         string         `json:"name"`
	Phase        string         `json:"phase"`
	Summary      map[string]any `json:"summary_json"`
	LastSyncedAt time.Time      `json:"last_synced_at"`
}

type listAppsResponse struct {
	Apps []resourceSnapshot `json:"apps"`
}

// appHostname is the subset of models.DomainHostname the CLI reads. The
// managed one is the surrogate *.dada-tuda.ru name the platform mints for the
// app itself; anything else is a domain the user attached.
type appHostname struct {
	Hostname string `json:"hostname"`
	Managed  bool   `json:"managed"`
	Status   string `json:"status"`
}

type listHostnamesResponse struct {
	Hostnames []appHostname `json:"hostnames"`
}

// FindAppHostname returns the address the app will answer on, which the
// platform knows from the moment the app is created - the domain_hostnames row
// is written in the same statement as the CreateApp operation, long before any
// pod exists. An attached custom domain wins over the surrogate one, because
// that is the address the user thinks of as theirs.
func (c *Client) FindAppHostname(ctx context.Context, projectID, envID, appName string) (host string, ok bool, err error) {
	var out listHostnamesResponse
	path := "/projects/" + projectID + "/environments/" + envID + "/apps/" + appName + "/hostnames"
	if err := c.doJSON(ctx, "GET", path, nil, "", &out); err != nil {
		return "", false, err
	}
	surrogate := ""
	for _, h := range out.Hostnames {
		if h.Hostname == "" {
			continue
		}
		if !h.Managed {
			return h.Hostname, true, nil
		}
		if surrogate == "" {
			surrogate = h.Hostname
		}
	}
	return surrogate, surrogate != "", nil
}

// FindAppURL looks up appName in the environment's app list and returns its
// live URL from the resource snapshot summary, if the platform has assigned
// one yet. ok is false when the app or its URL isn't visible yet - normal
// immediately after a build, before the deploy watcher has synced.
func (c *Client) FindAppURL(ctx context.Context, projectID, envID, appName string) (url string, ok bool, err error) {
	var out listAppsResponse
	if err := c.doJSON(ctx, "GET", "/projects/"+projectID+"/environments/"+envID+"/apps", nil, "", &out); err != nil {
		return "", false, err
	}
	for _, a := range out.Apps {
		if a.Name != appName {
			continue
		}
		if u, exists := a.Summary["url"]; exists {
			if s, isStr := u.(string); isStr && s != "" {
				return s, true, nil
			}
		}
		return "", false, nil
	}
	return "", false, nil
}
