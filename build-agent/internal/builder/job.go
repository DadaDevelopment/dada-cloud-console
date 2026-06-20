// Package builder creates and watches the per-build k8s Job that runs BuildKit.
package builder

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
)

// Result is the outcome of a finished build Job.
type Result struct {
	Digest string // sha256:... pushed image digest (parsed from build logs)
}

// Builder runs one build as a k8s Job and streams its logs.
type Builder interface {
	// Build renders + applies the Job into p.Namespace (already provisioned with
	// quotas + netpol + secrets by isolation), watches the builder pod to
	// completion, streams its logs to logSink, and returns the pushed digest.
	Build(ctx context.Context, p JobParams, logSink func(line string)) (Result, error)
}

// K8sBuilder is the production Builder backed by the in-cluster API.
type K8sBuilder struct {
	cs  kubernetes.Interface
	tpl *template.Template
}

// NewK8sBuilder returns a K8sBuilder. It panics if the embedded template fails
// to parse (a programming error, surfaced at startup).
func NewK8sBuilder(cs kubernetes.Interface) *K8sBuilder {
	tpl := template.Must(template.New("job").Parse(jobTemplate))
	return &K8sBuilder{cs: cs, tpl: tpl}
}

// render interpolates JobParams into a batchv1.Job.
func (b *K8sBuilder) render(p JobParams) (*batchv1.Job, error) {
	var buf bytes.Buffer
	if err := b.tpl.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("render job template: %w", err)
	}
	var job batchv1.Job
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(buf.Bytes()), 4096).Decode(&job); err != nil {
		return nil, fmt.Errorf("decode job yaml: %w", err)
	}
	return &job, nil
}

func (b *K8sBuilder) Build(ctx context.Context, p JobParams, logSink func(line string)) (Result, error) {
	job, err := b.render(p)
	if err != nil {
		return Result{}, err
	}

	if _, err := b.cs.BatchV1().Jobs(p.Namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return Result{}, fmt.Errorf("create job: %w", err)
		}
	}

	podName, err := b.waitForPod(ctx, p.Namespace, p.BuildID)
	if err != nil {
		return Result{}, err
	}

	digest := b.streamLogs(ctx, p.Namespace, podName, logSink)

	// Wait for the Job to reach a terminal condition.
	if err := b.waitForJob(ctx, p.Namespace, job.Name); err != nil {
		return Result{}, err
	}
	if digest == "" {
		return Result{}, fmt.Errorf("build finished without a pushed digest")
	}
	return Result{Digest: digest}, nil
}

// waitForPod blocks until the build pod exists and is past Pending (so logs are
// available), returning its name.
func (b *K8sBuilder) waitForPod(ctx context.Context, ns, buildID string) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		pods, err := b.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: "dada.io/build=" + buildID,
		})
		if err != nil {
			return "", fmt.Errorf("list build pods: %w", err)
		}
		if len(pods.Items) > 0 {
			pod := pods.Items[0]
			if pod.Status.Phase != corev1.PodPending {
				return pod.Name, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// streamLogs follows the builder container logs, forwarding each line to logSink
// and scanning for the digest marker emitted by the entrypoint
// ("==> digest:sha256:..."). Returns the parsed digest ("" if none).
func (b *K8sBuilder) streamLogs(ctx context.Context, ns, podName string, logSink func(line string)) string {
	req := b.cs.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
		Container: "builder",
		Follow:    true,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		logSink(fmt.Sprintf("log stream error: %v", err))
		return ""
	}
	defer stream.Close()

	var digest string
	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if logSink != nil {
			logSink(line)
		}
		if d := parseDigest(line); d != "" {
			digest = d
		}
	}
	return digest
}

// waitForJob blocks until the Job completes or fails.
func (b *K8sBuilder) waitForJob(ctx context.Context, ns, name string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		job, err := b.cs.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get job: %w", err)
		}
		for _, c := range job.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				return nil
			}
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				return fmt.Errorf("build job failed: %s", c.Message)
			}
		}
	}
}

const digestMarker = "==> digest:"

func parseDigest(line string) string {
	i := strings.Index(line, digestMarker)
	if i < 0 {
		return ""
	}
	d := strings.TrimSpace(line[i+len(digestMarker):])
	if strings.HasPrefix(d, "sha256:") {
		return d
	}
	return ""
}

var _ Builder = (*K8sBuilder)(nil)
