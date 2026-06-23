// Package k8s wraps an in-cluster Kubernetes clientset used by the status
// reconciler to read live workload state. It is intentionally read-only: the
// agent only ever lists/gets Deployments to mirror their status into the DB.
package k8s

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// NewInClusterClient builds a clientset from the pod's mounted service-account
// credentials. Returns an error when not running inside a cluster (e.g. local
// dev), so callers can disable the reconciler gracefully instead of crashing.
func NewInClusterClient() (*kubernetes.Clientset, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return clientset, nil
}
