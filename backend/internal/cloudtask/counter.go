package cloudtask

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// CounterResolver resolves an app's authoritative Yandex Metrika counter id from
// the project's live YandexMetrikaCounter custom resource. It is the single
// source of truth for counterId; callers never guess.
//
// Resolve returns a clear error when the access path is not configured (no
// in-cluster client) so the create handler can surface it as a failed
// dependency instead of firing a task with a missing counter.
type CounterResolver interface {
	Resolve(ctx context.Context, appName string) (string, error)
}

// yandexMetrikaCounterGVR is the cluster-scoped Crossplane CR holding the
// resolved counter id. Group/version/kind mirror the platform XRD; the counter
// id lives at status.counterId. See gitops-agent discovery for the same GVR.
var yandexMetrikaCounterGVR = schema.GroupVersionResource{
	Group:    "platform.dada-tuda.ru",
	Version:  "v1alpha1",
	Resource: "yandexmetrikacounters",
}

// dynamicCounterResolver reads the YandexMetrikaCounter CR via the in-cluster
// dynamic client.
//
// RBAC: the backend service account needs get on
// platform.dada-tuda.ru/yandexmetrikacounters (cluster-scoped):
//
//	apiGroups: ["platform.dada-tuda.ru"]
//	resources: ["yandexmetrikacounters"]
//	verbs:     ["get", "list"]
type dynamicCounterResolver struct {
	dyn dynamic.Interface
}

// NewCounterResolver builds a CounterResolver backed by the pod's mounted
// service-account credentials. When not running inside a cluster (e.g. local
// dev) it returns a resolver whose Resolve always fails with a clear,
// actionable error, so the create handler degrades to a failed dependency
// rather than crashing at startup.
func NewCounterResolver() CounterResolver {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return unconfiguredCounterResolver{err: fmt.Errorf("in-cluster config: %w", err)}
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return unconfiguredCounterResolver{err: fmt.Errorf("build dynamic client: %w", err)}
	}
	return &dynamicCounterResolver{dyn: dyn}
}

// Resolve GETs the cluster-scoped YandexMetrikaCounter named after the app and
// extracts status.counterId. The CR name equals the app name by platform
// convention (one counter per app). Missing CR, not-yet-provisioned status, or
// empty id all surface as distinct errors.
func (r *dynamicCounterResolver) Resolve(ctx context.Context, appName string) (string, error) {
	obj, err := r.dyn.Resource(yandexMetrikaCounterGVR).Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("YandexMetrikaCounter %q not found", appName)
		}
		return "", fmt.Errorf("get YandexMetrikaCounter %q: %w", appName, err)
	}
	id, found, err := unstructured.NestedString(obj.Object, "status", "counterId")
	if err != nil {
		return "", fmt.Errorf("read status.counterId for %q: %w", appName, err)
	}
	if !found || id == "" {
		return "", fmt.Errorf("YandexMetrikaCounter %q has no resolved counterId yet", appName)
	}
	return id, nil
}

// unconfiguredCounterResolver is returned when no in-cluster client could be
// built. Every Resolve fails identically with the wrapped configuration error.
type unconfiguredCounterResolver struct {
	err error
}

func (u unconfiguredCounterResolver) Resolve(context.Context, string) (string, error) {
	return "", fmt.Errorf("YandexMetrikaCounter access not configured: %w", u.err)
}
