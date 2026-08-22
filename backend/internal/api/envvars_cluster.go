package api

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// clusterEnvVar is one environment variable as the RUNNING workload carries it,
// which is a different question from what the console's env_vars table holds.
//
// Only the name and where the value comes from travel. Values never do: a plain
// literal in a pod spec is as likely to be a token as anything in env_vars, and
// this endpoint is a listing, not a reveal.
type clusterEnvVar struct {
	Key string `json:"key"`
	// From names the provenance: "value" for a literal, "secretKeyRef",
	// "configMapKeyRef", "fieldRef" or "resourceFieldRef" for the corresponding
	// valueFrom source.
	From string `json:"from"`
	// Ref is the object the value is read out of, as "secret/name" or
	// "configmap/name", empty for a literal.
	Ref string `json:"ref,omitempty"`
	// InConsole reports whether the console's env_vars table also holds this
	// key. False means editing it through the console is a NEW row, and the
	// value the pod runs on today lives only in git or in a hand-made Secret.
	InConsole bool `json:"in_console"`
}

// clusterEnvSource is a bulk source (envFrom) the workload pulls a whole object
// into its environment from -- the shape `useDotEnv: true` produces.
//
// Its keys are deliberately NOT enumerated: doing so means reading Secret
// contents for a listing, and the point here is answering "is anything else
// feeding this container", which the source alone answers.
type clusterEnvSource struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Prefix string `json:"prefix,omitempty"`
}

// clusterEnvSnapshot is what the live workload says about its environment,
// alongside how confident the read was.
type clusterEnvSnapshot struct {
	Vars    []clusterEnvVar    `json:"vars"`
	Sources []clusterEnvSource `json:"sources"`
	// Observed is false when the cluster could not be read at all (off-cluster
	// console, RBAC, no workload found, more than one candidate). It is the
	// difference between "this app has no other variables" and "we could not
	// tell", and callers must not collapse the two: reporting an empty list as
	// fact is exactly the failure this field exists to prevent.
	Observed bool   `json:"observed"`
	Reason   string `json:"reason,omitempty"`
}

// readClusterEnv reports the environment of the app's running workload.
//
// It exists because ListEnvVars answered from env_vars alone, and env_vars is
// empty for every app whose manifests were written by hand. On 2026-08-21 an
// agent read {"env_vars":[]} for internal/prod/telemost-bot while its Deployment
// carried twelve variables, concluded the app had none, and wrote one -- the
// re-render that followed deleted the other eleven. An empty list was true about
// the table and false about the app.
//
// The workload is identified by the dada.io/app label, falling back to a
// Deployment named after the app for the hand-maintained ones that predate the
// label. Anything less certain than exactly one match reports Observed=false
// rather than an empty environment.
func (h *Handler) readClusterEnv(ctx context.Context, namespace, appName string, consoleKeys map[string]bool) clusterEnvSnapshot {
	if namespace == "" {
		return clusterEnvSnapshot{Reason: "environment has no namespace"}
	}
	cs := h.platformHealthClientset()
	if cs == nil {
		return clusterEnvSnapshot{Reason: "console is not running in-cluster"}
	}

	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var pod *corev1.PodSpec
	deps, err := cs.AppsV1().Deployments(namespace).List(readCtx, metav1.ListOptions{
		LabelSelector: "dada.io/app=" + appName,
	})
	switch {
	case err != nil:
		return clusterEnvSnapshot{Reason: "reading workloads failed: " + err.Error()}
	case len(deps.Items) == 1:
		pod = &deps.Items[0].Spec.Template.Spec
	case len(deps.Items) > 1:
		return clusterEnvSnapshot{Reason: "more than one workload carries the app label"}
	}

	if pod == nil {
		dep, err := cs.AppsV1().Deployments(namespace).Get(readCtx, appName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return clusterEnvSnapshot{Reason: "no workload found for this app"}
		}
		if err != nil {
			return clusterEnvSnapshot{Reason: "reading workload failed: " + err.Error()}
		}
		pod = &dep.Spec.Template.Spec
	}

	snap := clusterEnvSnapshot{Observed: true}
	seen := map[string]bool{}
	for _, ctr := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
		for _, e := range ctr.Env {
			if seen[e.Name] {
				continue
			}
			seen[e.Name] = true
			snap.Vars = append(snap.Vars, clusterEnvVar{
				Key:       e.Name,
				From:      envValueOrigin(e),
				Ref:       envValueRef(e),
				InConsole: consoleKeys[e.Name],
			})
		}
		for _, from := range ctr.EnvFrom {
			if s := envFromSource(from); s.Name != "" {
				snap.Sources = append(snap.Sources, s)
			}
		}
	}
	sort.Slice(snap.Vars, func(i, j int) bool { return snap.Vars[i].Key < snap.Vars[j].Key })
	return snap
}

// envValueOrigin names where a container env entry gets its value.
func envValueOrigin(e corev1.EnvVar) string {
	switch {
	case e.ValueFrom == nil:
		return "value"
	case e.ValueFrom.SecretKeyRef != nil:
		return "secretKeyRef"
	case e.ValueFrom.ConfigMapKeyRef != nil:
		return "configMapKeyRef"
	case e.ValueFrom.FieldRef != nil:
		return "fieldRef"
	case e.ValueFrom.ResourceFieldRef != nil:
		return "resourceFieldRef"
	default:
		return "unknown"
	}
}

// envValueRef names the object a valueFrom entry reads out of.
func envValueRef(e corev1.EnvVar) string {
	if e.ValueFrom == nil {
		return ""
	}
	if r := e.ValueFrom.SecretKeyRef; r != nil {
		return "secret/" + r.Name
	}
	if r := e.ValueFrom.ConfigMapKeyRef; r != nil {
		return "configmap/" + r.Name
	}
	return ""
}

// envFromSource describes one envFrom entry.
func envFromSource(f corev1.EnvFromSource) clusterEnvSource {
	if r := f.SecretRef; r != nil {
		return clusterEnvSource{Kind: "secret", Name: r.Name, Prefix: f.Prefix}
	}
	if r := f.ConfigMapRef; r != nil {
		return clusterEnvSource{Kind: "configmap", Name: r.Name, Prefix: f.Prefix}
	}
	return clusterEnvSource{}
}
