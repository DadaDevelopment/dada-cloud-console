package portainer

// Endpoint represents a Portainer environment (endpoint).
type Endpoint struct {
	ID                  int    `json:"Id"`
	Name                string `json:"Name"`
	Type                int    `json:"Type"`
	EdgeKey             string `json:"EdgeKey"`
	EdgeID              string `json:"EdgeID"`
	Status              int    `json:"Status"`
	LastCheckInDate     int64  `json:"LastCheckInDate"`
	Heartbeat           bool   `json:"Heartbeat"`
	EdgeCheckinInterval int    `json:"EdgeCheckinInterval"`
	TagIDs              []int  `json:"TagIds"`
}

// Stack represents a Portainer stack.
//
// UpdateDate and GitConfig.ConfigHash are the only evidence a caller has that a
// redeploy actually landed: a redeploy that pulls images and recreates
// containers can outlive our HTTP client, and a client-side timeout says
// nothing about what the server did. Re-reading the stack and seeing the
// timestamp advance turns "unknown" into "delivered".
type Stack struct {
	ID         int             `json:"Id"`
	Name       string          `json:"Name"`
	EndpointID int             `json:"EndpointId"`
	Status     int             `json:"Status"`
	UpdateDate int64           `json:"UpdateDate"`
	GitConfig  *StackGitConfig `json:"GitConfig"`
}

// StackGitConfig is the git source a stack was last deployed from.
type StackGitConfig struct {
	URL           string `json:"URL"`
	ReferenceName string `json:"ReferenceName"`
	ConfigHash    string `json:"ConfigHash"`
}

// CreateStackRequest is the body for POST /api/stacks/create/standalone/repository.
type CreateStackRequest struct {
	Name                     string `json:"Name"`
	RepositoryURL            string `json:"RepositoryURL"`
	RepositoryReferenceName  string `json:"RepositoryReferenceName"`
	ComposeFile              string `json:"ComposeFile"`
	RepositoryAuthentication bool   `json:"RepositoryAuthentication"`
	RepositoryUsername       string `json:"RepositoryUsername"`
	RepositoryPassword       string `json:"RepositoryPassword"`
	TLSSkipVerify            bool   `json:"TLSSkipVerify"`
	Env                      []any  `json:"Env"`
	AutoUpdate               *any   `json:"AutoUpdate"`
}

// RedeployStackRequest is the body for PUT /api/stacks/{id}/git/redeploy.
type RedeployStackRequest struct {
	PullImage                bool   `json:"pullImage"`
	Prune                    bool   `json:"prune"`
	RepositoryReferenceName  string `json:"RepositoryReferenceName"`
	RepositoryAuthentication bool   `json:"RepositoryAuthentication"`
	RepositoryUsername       string `json:"RepositoryUsername"`
	RepositoryPassword       string `json:"RepositoryPassword"`
}

// EdgeGroup is a Portainer edge group: the fan-out target for an edge stack. A
// dynamic group auto-includes every edge endpoint carrying one of its tags, so a
// VM joins simply by being tagged at provision time.
type EdgeGroup struct {
	ID        int    `json:"Id"`
	Name      string `json:"Name"`
	Dynamic   bool   `json:"Dynamic"`
	TagIDs    []int  `json:"TagIds"`
	Endpoints []int  `json:"Endpoints"`
}

// createEdgeGroupRequest is the body for POST /api/edge_groups. Edge-compute APIs
// use camelCase keys (unlike the PascalCase legacy stack API).
type createEdgeGroupRequest struct {
	Name         string `json:"name"`
	Dynamic      bool   `json:"dynamic"`
	TagIDs       []int  `json:"tagIDs"`
	PartialMatch bool   `json:"partialMatch"`
	Endpoints    []int  `json:"endpoints"`
}

// Tag is a Portainer tag (used to drive dynamic edge-group membership).
type Tag struct {
	ID   int    `json:"ID"`
	Name string `json:"Name"`
}

// EdgeStack is a compose stack delivered by Portainer to every endpoint in its
// edge groups. Editing the git source + redeploying fans the new config to the
// whole fleet (existing VMs included) — the core of git-driven VM config delivery.
type EdgeStack struct {
	ID         int    `json:"Id"`
	Name       string `json:"Name"`
	EdgeGroups []int  `json:"EdgeGroups"`
}

// CreateEdgeStackGitRequest is the body for POST /api/edge_stacks/create/repository.
// DeploymentType 0 = compose. EdgeGroups targets the fan-out.
type CreateEdgeStackGitRequest struct {
	Name                     string `json:"name"`
	RepositoryURL            string `json:"repositoryURL"`
	RepositoryReferenceName  string `json:"repositoryReferenceName"`
	FilePathInRepository     string `json:"filePathInRepository"`
	EdgeGroups               []int  `json:"edgeGroups"`
	DeploymentType           int    `json:"deploymentType"`
	RepositoryAuthentication bool   `json:"repositoryAuthentication"`
	RepositoryUsername       string `json:"repositoryUsername"`
	RepositoryPassword       string `json:"repositoryPassword"`
}

// Container is a Docker container as returned by the Portainer proxy
// (GET /containers/json). Image/Ports/Mounts are what a read-only workload
// discovery needs — especially Mounts, which carry the live named-volume names
// used to pin the gitops compose `external: true` (the PG data-safety artifact).
type Container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	Ports   []Port            `json:"Ports"`
	Mounts  []Mount           `json:"Mounts"`
}

// Port is a published/exposed port entry from /containers/json.
type Port struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

// Mount is a container mount from /containers/json. For Type=="volume", Name is
// the live Docker volume name (what the external-volume pin must reference). For
// Type=="bind", Source is the host path to mirror verbatim.
type Mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}
