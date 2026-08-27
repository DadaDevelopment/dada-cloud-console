package cloudtask

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ErrRedisCredentialsNotReady signals that the cache user's Crossplane
// connection secret does not exist yet -- the ServiceCacheV2 is still
// provisioning. Callers map it to a 404 "not available yet" rather than a
// hard failure, mirroring ErrDBCredentialsNotReady.
var ErrRedisCredentialsNotReady = errors.New("redis credentials not available yet")

// RedisCredentials is the reveal-on-demand view of a managed Redis ACL
// user's connection details, read from the Crossplane connection secret the
// ServiceCacheV2 composition writes (provider-redis's User controller
// publishes username/password/endpoint/port -- see
// provider-redis/internal/controller/user/user.go's connectionDetail*
// constants).
type RedisCredentials struct {
	Endpoint string
	Port     string
	Username string
	Password string
}

// RedisCredentialsResolver reads a ServiceCacheV2 user's live connection
// credentials from the Kubernetes connection secret written by the
// platform's ServiceCacheV2 composition
// ("<appRef>-<resourceName>-redis-credentials" in the app namespace).
// Resolve returns a clear error when the access path is not configured (no
// in-cluster client) so the reveal handler surfaces it as a failed
// dependency instead of guessing. Mirrors DBCredentialsResolver exactly.
type RedisCredentialsResolver interface {
	// Resolve reads the connection secret named secretName in the given app
	// namespace. An empty namespace or secretName yields
	// ErrRedisCredentialsNotReady.
	Resolve(ctx context.Context, namespace, secretName string) (RedisCredentials, error)
}

// clientsetRedisCredentialsResolver reads connection secrets via the
// in-cluster typed clientset.
//
// RBAC: the backend service account needs get on core Secrets in every
// project (app) namespace -- the same permission dbcreds.go already
// requires, since managed-cache connection secrets live alongside the app
// that owns them exactly like managed-database ones do:
//
//	apiGroups: [""]
//	resources: ["secrets"]
//	verbs:     ["get"]
type clientsetRedisCredentialsResolver struct {
	clientset kubernetes.Interface
}

// NewRedisCredentialsResolver builds a RedisCredentialsResolver backed by
// the pod's mounted service-account credentials. When not running inside a
// cluster (e.g. local dev) it returns a resolver whose Resolve always fails
// with a clear, actionable error, so the reveal handler degrades to a
// failed dependency rather than crashing at startup. Mirrors
// NewDBCredentialsResolver exactly.
func NewRedisCredentialsResolver() RedisCredentialsResolver {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return unconfiguredRedisCredentialsResolver{err: fmt.Errorf("in-cluster config: %w", err)}
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return unconfiguredRedisCredentialsResolver{err: fmt.Errorf("build clientset: %w", err)}
	}
	return &clientsetRedisCredentialsResolver{clientset: clientset}
}

// Resolve GETs the named connection secret in the app namespace and maps
// its keys (username/password/endpoint/port) to the console's credential
// view. A missing secret (cache user still provisioning) returns
// ErrRedisCredentialsNotReady.
func (r *clientsetRedisCredentialsResolver) Resolve(ctx context.Context, namespace, secretName string) (RedisCredentials, error) {
	if namespace == "" || secretName == "" {
		return RedisCredentials{}, ErrRedisCredentialsNotReady
	}
	sec, err := r.clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return RedisCredentials{}, ErrRedisCredentialsNotReady
		}
		return RedisCredentials{}, fmt.Errorf("get secret %q in %q: %w", secretName, namespace, err)
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := sec.Data[k]; ok && len(v) > 0 {
				return string(v)
			}
		}
		return ""
	}
	creds := RedisCredentials{
		Endpoint: get("endpoint", "host"),
		Port:     get("port"),
		Username: get("username", "user"),
		Password: get("password"),
	}
	if creds.Username == "" && creds.Password == "" {
		return RedisCredentials{}, ErrRedisCredentialsNotReady
	}
	return creds, nil
}

// unconfiguredRedisCredentialsResolver is returned when no in-cluster
// client could be built. Every Resolve fails identically with the wrapped
// configuration error.
type unconfiguredRedisCredentialsResolver struct {
	err error
}

func (u unconfiguredRedisCredentialsResolver) Resolve(context.Context, string, string) (RedisCredentials, error) {
	return RedisCredentials{}, fmt.Errorf("redis credential access not configured: %w", u.err)
}
