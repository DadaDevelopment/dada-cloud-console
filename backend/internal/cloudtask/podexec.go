package cloudtask

import (
	"bytes"
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

const podExecStderrLimit = 4096

// PodExecError wraps a failed pod-exec with the captured stderr tail, so the
// caller can surface a useful message (e.g. "tar: <path>: No such file or
// directory") instead of just the transport error.
type PodExecError struct {
	Stderr string
	Err    error
}

func (e *PodExecError) Error() string {
	if e.Stderr == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%v (stderr: %s)", e.Err, e.Stderr)
}

func (e *PodExecError) Unwrap() error { return e.Err }

// PodTarExporter finds a running pod for an app and streams a tar.gz of a
// directory inside one of its containers, without touching the pod's
// filesystem or restarting it. FindRunningPod returns the name of a Running
// pod in namespace labeled dada.io/app=appName plus its first container's
// name. StreamTarball execs "tar czf - -C srcPath ." inside that
// pod/container and writes the resulting tar.gz stream to out as it is
// produced.
type PodTarExporter interface {
	Enabled() bool
	FindRunningPod(ctx context.Context, namespace, appName string) (podName, containerName string, err error)
	StreamTarball(ctx context.Context, namespace, podName, containerName, srcPath string, out io.Writer) error
}

// clientsetPodTarExporter execs against the in-cluster API server using the
// pod's mounted service-account credentials (RBAC: pods/exec create, same
// service account as the other in-cluster resolvers in this package).
type clientsetPodTarExporter struct {
	clientset kubernetes.Interface
	restCfg   *rest.Config
}

// NewPodTarExporter builds a PodTarExporter backed by the pod's mounted
// service-account credentials. Off-cluster (local dev, no service-account
// mount) it returns an exporter whose every method fails with a clear "not
// configured" error, mirroring NewS3CredentialsResolver / NewCounterResolver.
func NewPodTarExporter() PodTarExporter {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return unconfiguredPodTarExporter{err: fmt.Errorf("in-cluster config: %w", err)}
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return unconfiguredPodTarExporter{err: fmt.Errorf("build clientset: %w", err)}
	}
	return &clientsetPodTarExporter{clientset: clientset, restCfg: cfg}
}

func (p *clientsetPodTarExporter) Enabled() bool { return true }

func (p *clientsetPodTarExporter) FindRunningPod(ctx context.Context, namespace, appName string) (string, string, error) {
	pods, err := p.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "dada.io/app=" + appName,
	})
	if err != nil {
		return "", "", fmt.Errorf("list pods: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil {
			continue
		}
		if len(pod.Spec.Containers) == 0 {
			continue
		}
		return pod.Name, pod.Spec.Containers[0].Name, nil
	}
	return "", "", fmt.Errorf("no running pod found for app %q in namespace %q", appName, namespace)
}

func (p *clientsetPodTarExporter) StreamTarball(ctx context.Context, namespace, podName, containerName, srcPath string, out io.Writer) error {
	req := p.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   []string{"tar", "czf", "-", "-C", srcPath, "."},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(p.restCfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("build pod exec: %w", err)
	}

	var stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: out,
		Stderr: &stderr,
	})
	if err != nil {
		tail := stderr.String()
		if len(tail) > podExecStderrLimit {
			tail = tail[len(tail)-podExecStderrLimit:]
		}
		return &PodExecError{Stderr: tail, Err: err}
	}
	return nil
}

// unconfiguredPodTarExporter is returned when no in-cluster client could be
// built. Every method fails identically with the wrapped configuration error.
type unconfiguredPodTarExporter struct{ err error }

func (u unconfiguredPodTarExporter) Enabled() bool { return false }

func (u unconfiguredPodTarExporter) FindRunningPod(context.Context, string, string) (string, string, error) {
	return "", "", fmt.Errorf("volume export not configured: %w", u.err)
}

func (u unconfiguredPodTarExporter) StreamTarball(context.Context, string, string, string, string, io.Writer) error {
	return fmt.Errorf("volume export not configured: %w", u.err)
}
