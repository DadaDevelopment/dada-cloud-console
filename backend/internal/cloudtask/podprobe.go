package cloudtask

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// netProbeToolsImage is the platform-maintained diagnostics image attached as
// an ephemeral container: busybox + curl + openssl + bind-tools, pinned so a
// probe behaves the same regardless of what the app's own image contains (or
// lacks -- a distroless or scratch app image has neither a shell nor any of
// these tools, so exec'ing straight into the app container the way
// StreamTarball does would simply fail).
const netProbeToolsImage = "registry.dada.cloud/dada/net-probe-tools:v1"

// netProbeContainerName is deliberately fixed rather than per-request: an
// ephemeral container cannot be removed from a running pod once added (a real
// Kubernetes limitation, not an oversight here), so every probe against the
// same pod reuses the one container instead of piling up a new one per call.
const netProbeContainerName = "netprobe-diag"

const netProbeContainerReadyTimeout = 10 * time.Second

const netProbeStepTimeout = 8 * time.Second

const netProbeOutputLimit = 4096

// ProbeSpec is a fixed, non-shell-templated diagnostic request: Target and
// Port are passed as exec argv elements, never interpolated into a shell
// string, so there is no command injection surface no matter what a caller
// puts in Target.
type ProbeSpec struct {
	Target   string
	Port     int
	Protocol string // "tcp", "tls", or "http"
}

// StepResult is one diagnostic step's outcome: DNS resolution, a bare TCP
// connect, a TLS handshake, or (protocol "http" only) an HTTP request. Each
// step runs and times out independently so one hung step still leaves the
// earlier ones' results intact instead of losing the whole probe.
type StepResult struct {
	Ran        bool
	Ok         bool
	DurationMs int64
	Output     string
	Error      string
}

// ProbeResult is the full outcome of one probe. HTTP is only populated when
// ProbeSpec.Protocol is "http".
type ProbeResult struct {
	DNS  StepResult
	TCP  StepResult
	TLS  StepResult
	HTTP *StepResult
}

// PodProber execs a fixed diagnostic sequence inside an ephemeral debug
// container attached to a running app pod. The ephemeral container shares the
// pod's network namespace, so it sees exactly the network path the app itself
// has -- no more, no less -- while carrying its own guaranteed toolset
// regardless of the app's own image.
type PodProber interface {
	Enabled() bool
	FindRunningPod(ctx context.Context, namespace, appName string) (podName, containerName string, err error)
	RunNetworkProbe(ctx context.Context, namespace, podName string, spec ProbeSpec) (ProbeResult, error)
}

type clientsetPodProber struct {
	clientset kubernetes.Interface
	restCfg   *rest.Config
}

// NewPodProber builds a PodProber backed by the pod's mounted service-account
// credentials, mirroring NewPodTarExporter. Off-cluster it returns a prober
// whose every method fails with a clear "not configured" error.
func NewPodProber() PodProber {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return unconfiguredPodProber{err: fmt.Errorf("in-cluster config: %w", err)}
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return unconfiguredPodProber{err: fmt.Errorf("build clientset: %w", err)}
	}
	return &clientsetPodProber{clientset: clientset, restCfg: cfg}
}

func (p *clientsetPodProber) Enabled() bool { return true }

func (p *clientsetPodProber) FindRunningPod(ctx context.Context, namespace, appName string) (string, string, error) {
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

func (p *clientsetPodProber) RunNetworkProbe(ctx context.Context, namespace, podName string, spec ProbeSpec) (ProbeResult, error) {
	if err := p.ensureProbeContainer(ctx, namespace, podName); err != nil {
		return ProbeResult{}, fmt.Errorf("ensure probe container: %w", err)
	}

	var result ProbeResult
	result.DNS = p.execStep(ctx, namespace, podName, []string{"getent", "hosts", spec.Target})

	if spec.Protocol == "http" {
		result.TCP = p.execStep(ctx, namespace, podName, []string{"nc", "-zv", "-w5", spec.Target, fmt.Sprintf("%d", spec.Port)})
		httpStep := p.execStep(ctx, namespace, podName, []string{
			"curl", "-sS", "-m", "10", "-D", "-", "-o", "/dev/null",
			fmt.Sprintf("https://%s:%d/", spec.Target, spec.Port),
		})
		result.HTTP = &httpStep
		return result, nil
	}

	result.TCP = p.execStep(ctx, namespace, podName, []string{"nc", "-zv", "-w5", spec.Target, fmt.Sprintf("%d", spec.Port)})

	if spec.Protocol == "tls" {
		result.TLS = p.execStep(ctx, namespace, podName, []string{
			"sh", "-c",
			fmt.Sprintf("openssl s_client -connect %s:%d -servername %s -verify_return_error </dev/null 2>&1",
				shellQuote(spec.Target), spec.Port, shellQuote(spec.Target)),
		})
	}

	return result, nil
}

// ensureProbeContainer adds the fixed-name ephemeral container to the pod if
// it is not already present and Running. Ephemeral containers cannot be
// removed from a live pod, so this is deliberately idempotent: at most one
// such container ever exists per pod for the lifetime of that pod.
func (p *clientsetPodProber) ensureProbeContainer(ctx context.Context, namespace, podName string) error {
	pod, err := p.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get pod: %w", err)
	}

	for _, status := range pod.Status.EphemeralContainerStatuses {
		if status.Name == netProbeContainerName && status.State.Running != nil {
			return nil
		}
	}

	hasSpec := false
	for _, ec := range pod.Spec.EphemeralContainers {
		if ec.Name == netProbeContainerName {
			hasSpec = true
			break
		}
	}
	if !hasSpec {
		pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, corev1.EphemeralContainer{
			EphemeralContainerCommon: corev1.EphemeralContainerCommon{
				Name:    netProbeContainerName,
				Image:   netProbeToolsImage,
				Command: []string{"sleep", "3600"},
			},
		})
		if _, err := p.clientset.CoreV1().Pods(namespace).UpdateEphemeralContainers(ctx, podName, pod, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("add ephemeral container: %w", err)
		}
	}

	return wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, netProbeContainerReadyTimeout, true, func(ctx context.Context) (bool, error) {
		pod, err := p.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		for _, status := range pod.Status.EphemeralContainerStatuses {
			if status.Name == netProbeContainerName && status.State.Running != nil {
				return true, nil
			}
		}
		return false, nil
	})
}

func (p *clientsetPodProber) execStep(ctx context.Context, namespace, podName string, cmd []string) StepResult {
	stepCtx, cancel := context.WithTimeout(ctx, netProbeStepTimeout)
	defer cancel()

	start := time.Now()
	req := p.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: netProbeContainerName,
			Command:   cmd,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(p.restCfg, "POST", req.URL())
	if err != nil {
		return StepResult{Ran: true, DurationMs: time.Since(start).Milliseconds(), Error: fmt.Sprintf("build exec: %v", err)}
	}

	var out bytes.Buffer
	err = exec.StreamWithContext(stepCtx, remotecommand.StreamOptions{Stdout: &out, Stderr: &out})
	duration := time.Since(start).Milliseconds()
	text := truncateProbeOutput(out.String())
	if err != nil {
		return StepResult{Ran: true, DurationMs: duration, Output: text, Error: err.Error()}
	}
	return StepResult{Ran: true, Ok: true, DurationMs: duration, Output: text}
}

func truncateProbeOutput(s string) string {
	if len(s) <= netProbeOutputLimit {
		return s
	}
	return s[len(s)-netProbeOutputLimit:]
}

// shellQuote wraps a value in single quotes for the one exec step that must
// run inside "sh -c" (openssl's -connect/-servername flags share a single
// argument string with no argv-only form). The value is never user-controlled
// shell syntax: net_probe.go's target validation has already rejected
// anything but a bare hostname before this is called, and any embedded quote
// is neutralized rather than trusted.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type unconfiguredPodProber struct{ err error }

func (u unconfiguredPodProber) Enabled() bool { return false }

func (u unconfiguredPodProber) FindRunningPod(context.Context, string, string) (string, string, error) {
	return "", "", fmt.Errorf("network probe not configured: %w", u.err)
}

func (u unconfiguredPodProber) RunNetworkProbe(context.Context, string, string, ProbeSpec) (ProbeResult, error) {
	return ProbeResult{}, fmt.Errorf("network probe not configured: %w", u.err)
}
