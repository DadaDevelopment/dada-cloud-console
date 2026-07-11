package cloudtask

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ErrDBCredentialsNotReady signals that the database's Crossplane connection
// secret does not exist yet — the database is still provisioning (or is a VM
// database with no k8s secret). Callers map it to a 404 "not available yet"
// rather than a hard failure.
var ErrDBCredentialsNotReady = errors.New("database credentials not available yet")

// DBCredentials is the reveal-on-demand view of a managed PostgreSQL database's
// connection details, read from the Crossplane connection secret the
// ServiceDatabaseV2 composition writes into the app namespace.
type DBCredentials struct {
	Endpoint string
	Port     string
	Username string
	Password string
	// ExternalHost/ExternalPort are the public connection endpoint, present only
	// when the database has been opted into external access and the composition
	// published it into the connection secret. Empty for private (default) DBs.
	ExternalHost string
	ExternalPort string
}

// DBCredentialsResolver reads a database's live connection credentials from the
// Kubernetes connection secret written by the platform's ServiceDatabaseV2
// composition ("<database>-db-credentials" in the app namespace). Resolve
// returns a clear error when the access path is not configured (no in-cluster
// client) so the reveal handler surfaces it as a failed dependency instead of
// guessing.
type DBCredentialsResolver interface {
	// Resolve reads the connection secret "<resourceName>-db-credentials" in the
	// given app namespace. An empty namespace yields ErrDBCredentialsNotReady
	// (the database has no known k8s secret location — e.g. still provisioning
	// or a VM database).
	Resolve(ctx context.Context, namespace, resourceName string) (DBCredentials, error)
}

// clientsetDBCredentialsResolver reads connection secrets via the in-cluster
// typed clientset.
//
// RBAC: the backend service account needs get on core Secrets in every project
// (app) namespace, since managed-database connection secrets live alongside the
// app that owns them:
//
//	apiGroups: [""]
//	resources: ["secrets"]
//	verbs:     ["get"]
type clientsetDBCredentialsResolver struct {
	clientset kubernetes.Interface
}

// NewDBCredentialsResolver builds a DBCredentialsResolver backed by the pod's
// mounted service-account credentials. When not running inside a cluster (e.g.
// local dev) it returns a resolver whose Resolve always fails with a clear,
// actionable error, so the reveal handler degrades to a failed dependency
// rather than crashing at startup.
func NewDBCredentialsResolver() DBCredentialsResolver {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return unconfiguredDBCredentialsResolver{err: fmt.Errorf("in-cluster config: %w", err)}
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return unconfiguredDBCredentialsResolver{err: fmt.Errorf("build clientset: %w", err)}
	}
	return &clientsetDBCredentialsResolver{clientset: clientset}
}

// Resolve GETs the connection secret named "<resourceName>-db-credentials" in
// the app namespace and maps its keys (endpoint/port/username/password) to the
// console's credential view. A missing secret (database still provisioning)
// returns ErrDBCredentialsNotReady.
func (r *clientsetDBCredentialsResolver) Resolve(ctx context.Context, namespace, resourceName string) (DBCredentials, error) {
	if namespace == "" {
		return DBCredentials{}, ErrDBCredentialsNotReady
	}
	secretName := resourceName + "-db-credentials"
	sec, err := r.clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return DBCredentials{}, ErrDBCredentialsNotReady
		}
		return DBCredentials{}, fmt.Errorf("get secret %q in %q: %w", secretName, namespace, err)
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := sec.Data[k]; ok && len(v) > 0 {
				return string(v)
			}
		}
		return ""
	}
	extHost, extPort := splitEndpoint(get("external_endpoint"))
	if extHost == "" {
		extHost = get("external_host")
	}
	if extPort == "" {
		extPort = get("external_port")
	}
	creds := DBCredentials{
		Endpoint:     get("endpoint", "host"),
		Port:         get("port"),
		Username:     get("username", "user"),
		Password:     get("password"),
		ExternalHost: extHost,
		ExternalPort: extPort,
	}
	if creds.Username == "" && creds.Password == "" {
		return DBCredentials{}, ErrDBCredentialsNotReady
	}
	return creds, nil
}

// unconfiguredDBCredentialsResolver is returned when no in-cluster client could
// be built. Every Resolve fails identically with the wrapped configuration
// error.
type unconfiguredDBCredentialsResolver struct {
	err error
}

func (u unconfiguredDBCredentialsResolver) Resolve(context.Context, string, string) (DBCredentials, error) {
	return DBCredentials{}, fmt.Errorf("database credential access not configured: %w", u.err)
}

// splitEndpoint splits a "host:port" connection endpoint into its parts. A bare
// host (no colon, or an IPv6 literal without a port) yields an empty port.
func splitEndpoint(ep string) (host, port string) {
	if ep == "" {
		return "", ""
	}
	i := strings.LastIndex(ep, ":")
	if i < 0 || strings.Contains(ep[i+1:], "]") {
		return ep, ""
	}
	return ep[:i], ep[i+1:]
}
