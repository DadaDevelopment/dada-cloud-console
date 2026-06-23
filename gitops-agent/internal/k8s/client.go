// Package k8s wraps an in-cluster Kubernetes clientset used by the status
// reconciler to read live workload state. It is intentionally read-only: the
// agent only ever lists/gets Deployments to mirror their status into the DB.
package k8s

import (
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Clients bundles the typed (core/apps) and dynamic (CRDs like KServe
// InferenceService) clients the status reconciler needs.
type Clients struct {
	Typed   kubernetes.Interface
	Dynamic dynamic.Interface
}

// NewInClusterClients builds typed + dynamic clients from the pod's mounted
// service-account credentials. Returns an error when not running inside a
// cluster (e.g. local dev), so callers can disable the reconciler gracefully
// instead of crashing.
func NewInClusterClients() (*Clients, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return &Clients{Typed: typed, Dynamic: dyn}, nil
}
