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
}

// Stack represents a Portainer stack.
type Stack struct {
	ID         int    `json:"Id"`
	Name       string `json:"Name"`
	EndpointID int    `json:"EndpointId"`
	Status     int    `json:"Status"`
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

// Container is a Docker container as returned by the Portainer proxy.
type Container struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}
