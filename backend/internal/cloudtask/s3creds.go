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

// ErrS3CredentialsNotReady signals that the bucket's Crossplane connection
// secret does not exist yet — the bucket is still provisioning. Callers map it
// to a 404/409 "not available yet" rather than a hard failure.
var ErrS3CredentialsNotReady = errors.New("s3 credentials not available yet")

// S3Credentials is the reveal-on-demand view of an S3Bucket's access details,
// read from the Crossplane connection secret. Only the fields the console shows
// are surfaced; FTP/SFTP hosts are best-effort (empty when the bucket has them
// disabled).
type S3Credentials struct {
	Endpoint   string
	AccessKey  string
	SecretKey  string
	BucketName string
	FtpHost    string
	SftpHost   string
}

// S3CredentialsResolver reads an S3Bucket's live access credentials from the
// Kubernetes connection secret the platform's S3Bucket composition writes
// (writeConnectionSecretToRef → "<bucket>-s3-credentials"). Resolve returns a
// clear error when the access path is not configured (no in-cluster client) so
// the reveal handler surfaces it as a failed dependency instead of guessing.
type S3CredentialsResolver interface {
	// Resolve reads the connection secret at the composition-default location
	// (<resourceName>-s3-credentials in the configured namespace).
	Resolve(ctx context.Context, resourceName string) (S3Credentials, error)
	// ResolveRef reads an explicitly-declared connection secret. An empty
	// namespace falls back to the configured default. Used when an adopted
	// bucket declares its own spec.connectionSecret instead of relying on the
	// composition default.
	ResolveRef(ctx context.Context, namespace, secretName string) (S3Credentials, error)
}

// clientsetS3CredentialsResolver reads connection secrets via the in-cluster
// typed clientset.
//
// RBAC: the backend service account needs get on core Secrets in the
// crossplane connection-secret namespace:
//
//	apiGroups: [""]
//	resources: ["secrets"]
//	verbs:     ["get"]
type clientsetS3CredentialsResolver struct {
	clientset kubernetes.Interface
	namespace string
}

// NewS3CredentialsResolver builds an S3CredentialsResolver backed by the pod's
// mounted service-account credentials. When not running inside a cluster (e.g.
// local dev) it returns a resolver whose Resolve always fails with a clear,
// actionable error, so the reveal handler degrades to a failed dependency
// rather than crashing at startup. namespace is where the S3Bucket composition
// writes connection secrets (CROSSPLANE_SECRET_NAMESPACE, default
// crossplane-system).
func NewS3CredentialsResolver(namespace string) S3CredentialsResolver {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return unconfiguredS3CredentialsResolver{err: fmt.Errorf("in-cluster config: %w", err)}
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return unconfiguredS3CredentialsResolver{err: fmt.Errorf("build clientset: %w", err)}
	}
	if namespace == "" {
		namespace = "crossplane-system"
	}
	return &clientsetS3CredentialsResolver{clientset: clientset, namespace: namespace}
}

// Resolve GETs the connection secret named "<resourceName>-s3-credentials" in
// the configured namespace — the composition default when the bucket does not
// declare its own spec.connectionSecret.
func (r *clientsetS3CredentialsResolver) Resolve(ctx context.Context, resourceName string) (S3Credentials, error) {
	return r.ResolveRef(ctx, r.namespace, resourceName+"-s3-credentials")
}

// ResolveRef GETs the named connection secret (namespace defaults to the
// resolver's configured namespace when empty) and maps its Terraform-output
// keys to the console's credential view. A missing secret (bucket still
// provisioning) returns ErrS3CredentialsNotReady.
func (r *clientsetS3CredentialsResolver) ResolveRef(ctx context.Context, namespace, secretName string) (S3Credentials, error) {
	if namespace == "" {
		namespace = r.namespace
	}
	sec, err := r.clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return S3Credentials{}, ErrS3CredentialsNotReady
		}
		return S3Credentials{}, fmt.Errorf("get secret %q: %w", secretName, err)
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := sec.Data[k]; ok && len(v) > 0 {
				return string(v)
			}
		}
		return ""
	}
	creds := S3Credentials{
		Endpoint:   get("s3_url", "path_style_url", "virtual_hosted_style_url"),
		AccessKey:  get("access_key"),
		SecretKey:  get("secret_key"),
		BucketName: get("bucket_name"),
		FtpHost:    get("ftp_host"),
		SftpHost:   get("sftp_host"),
	}
	if creds.AccessKey == "" && creds.SecretKey == "" {
		return S3Credentials{}, ErrS3CredentialsNotReady
	}
	return creds, nil
}

// unconfiguredS3CredentialsResolver is returned when no in-cluster client could
// be built. Every Resolve fails identically with the wrapped configuration
// error.
type unconfiguredS3CredentialsResolver struct {
	err error
}

func (u unconfiguredS3CredentialsResolver) Resolve(context.Context, string) (S3Credentials, error) {
	return S3Credentials{}, fmt.Errorf("S3 credential access not configured: %w", u.err)
}

func (u unconfiguredS3CredentialsResolver) ResolveRef(context.Context, string, string) (S3Credentials, error) {
	return S3Credentials{}, fmt.Errorf("S3 credential access not configured: %w", u.err)
}
