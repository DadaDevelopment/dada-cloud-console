package cloudtask

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// actionSetGVR is the Kanister ActionSet the console creates imperatively to run
// the per-database backup/restore blueprint (K10 policies cannot pass a
// per-database option, so we drive Kanister directly).
var actionSetGVR = schema.GroupVersionResource{
	Group:    "cr.kanister.io",
	Version:  "v1alpha1",
	Resource: "actionsets",
}

// KanisterActionSpec parameterises one backup/restore/delete ActionSet. Namespace
// is where the ActionSet, the target StatefulSet and the Kanister Profile all
// live (the managed-Postgres namespace). Kopia is the input artifact snapshot,
// required for restore and delete.
type KanisterActionSpec struct {
	Namespace   string
	StatefulSet string
	Profile     string
	Blueprint   string
	Database    string
	DumpPath    string
	Kopia       string
	Labels      map[string]string
}

// ActionSetStatus is the distilled live status of a Kanister ActionSet. State is
// the Kanister phase (pending|running|complete|failed); KopiaSnapshot is the
// output artifact of a completed backup.
type ActionSetStatus struct {
	State         string
	KopiaSnapshot string
	Error         string
}

// Kanister phase constants.
const (
	KanisterComplete = "complete"
	KanisterFailed   = "failed"
	KanisterRunning  = "running"
	KanisterPending  = "pending"
)

// KanisterClient creates and inspects the ActionSets that execute per-database
// backup/restore. Off-cluster it is disabled: Enabled() is false and every
// create fails with a clear "not configured" error so the API degrades to a
// failed dependency instead of crashing at startup.
type KanisterClient interface {
	Enabled() bool
	CreateBackup(ctx context.Context, spec KanisterActionSpec) (string, error)
	CreateRestore(ctx context.Context, spec KanisterActionSpec) (string, error)
	CreateDelete(ctx context.Context, spec KanisterActionSpec) (string, error)
	Status(ctx context.Context, namespace, name string) (ActionSetStatus, error)
	StatusByLabel(ctx context.Context, namespace, labelKey, labelValue string) (ActionSetStatus, bool, error)
}

type dynamicKanisterClient struct {
	dyn dynamic.Interface
}

// NewKanisterClient builds a KanisterClient backed by the pod's in-cluster
// service-account credentials. Off-cluster it returns a disabled client.
func NewKanisterClient() KanisterClient {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return disabledKanisterClient{err: fmt.Errorf("in-cluster config: %w", err)}
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return disabledKanisterClient{err: fmt.Errorf("build dynamic client: %w", err)}
	}
	return &dynamicKanisterClient{dyn: dyn}
}

func (c *dynamicKanisterClient) Enabled() bool { return true }

func (c *dynamicKanisterClient) CreateBackup(ctx context.Context, spec KanisterActionSpec) (string, error) {
	return c.create(ctx, actionSetObject("backup", spec, false))
}

func (c *dynamicKanisterClient) CreateRestore(ctx context.Context, spec KanisterActionSpec) (string, error) {
	return c.create(ctx, actionSetObject("restore", spec, true))
}

func (c *dynamicKanisterClient) CreateDelete(ctx context.Context, spec KanisterActionSpec) (string, error) {
	return c.create(ctx, actionSetObject("delete", spec, true))
}

func (c *dynamicKanisterClient) create(ctx context.Context, obj *unstructured.Unstructured) (string, error) {
	ns := obj.GetNamespace()
	created, err := c.dyn.Resource(actionSetGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create actionset: %w", err)
	}
	return created.GetName(), nil
}

func (c *dynamicKanisterClient) Status(ctx context.Context, namespace, name string) (ActionSetStatus, error) {
	obj, err := c.dyn.Resource(actionSetGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return ActionSetStatus{}, fmt.Errorf("get actionset %q: %w", name, err)
	}
	return parseActionSetStatus(obj), nil
}

func (c *dynamicKanisterClient) StatusByLabel(ctx context.Context, namespace, labelKey, labelValue string) (ActionSetStatus, bool, error) {
	list, err := c.dyn.Resource(actionSetGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelKey + "=" + labelValue,
	})
	if err != nil {
		return ActionSetStatus{}, false, fmt.Errorf("list actionsets: %w", err)
	}
	if len(list.Items) == 0 {
		return ActionSetStatus{}, false, nil
	}
	return parseActionSetStatus(&list.Items[0]), true, nil
}

// actionSetObject builds a single-action ActionSet. withArtifact injects the
// input kopia snapshot (restore/delete consume the backup produced earlier).
func actionSetObject(action string, spec KanisterActionSpec, withArtifact bool) *unstructured.Unstructured {
	act := map[string]any{
		"name":      action,
		"blueprint": spec.Blueprint,
		"object": map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "StatefulSet",
			"name":       spec.StatefulSet,
			"namespace":  spec.Namespace,
		},
		"profile": map[string]any{
			"name":      spec.Profile,
			"namespace": spec.Namespace,
		},
		"options": map[string]any{
			"database": spec.Database,
			"dumpPath": spec.DumpPath,
		},
	}
	if withArtifact {
		act["artifacts"] = map[string]any{
			"pgBackup": map[string]any{
				"keyValue": map[string]any{
					"kopiaSnapshot": spec.Kopia,
				},
			},
		}
	}
	labels := map[string]any{}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cr.kanister.io/v1alpha1",
		"kind":       "ActionSet",
		"metadata": map[string]any{
			"generateName": "db-" + action + "-",
			"namespace":    spec.Namespace,
			"labels":       labels,
		},
		"spec": map[string]any{
			"actions": []any{act},
		},
	}}
}

// parseActionSetStatus distils status.state, the pgBackup output artifact's
// kopia snapshot, and any error message from a Kanister ActionSet.
func parseActionSetStatus(obj *unstructured.Unstructured) ActionSetStatus {
	st := ActionSetStatus{}
	st.State, _, _ = unstructured.NestedString(obj.Object, "status", "state")
	if msg, found, _ := unstructured.NestedString(obj.Object, "status", "error", "message"); found {
		st.Error = msg
	}
	actions, _, _ := unstructured.NestedSlice(obj.Object, "status", "actions")
	if len(actions) > 0 {
		if a, ok := actions[0].(map[string]any); ok {
			st.KopiaSnapshot = firstNonEmpty(
				digString(a, "artifacts", "pgBackup", "keyValue", "kopiaSnapshot"),
				digString(a, "artifacts", "pgBackup", "kopiaSnapshot"),
			)
		}
	}
	return st
}

// digString walks a nested map[string]any by keys, returning the string leaf or
// "" when any hop is missing or not the expected type.
func digString(m map[string]any, keys ...string) string {
	cur := any(m)
	for _, k := range keys {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = asMap[k]
	}
	s, _ := cur.(string)
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// disabledKanisterClient is returned off-cluster; every create fails with the
// wrapped configuration error and Status is never reached (callers check Enabled).
type disabledKanisterClient struct {
	err error
}

func (d disabledKanisterClient) Enabled() bool { return false }
func (d disabledKanisterClient) CreateBackup(context.Context, KanisterActionSpec) (string, error) {
	return "", d.notConfigured()
}
func (d disabledKanisterClient) CreateRestore(context.Context, KanisterActionSpec) (string, error) {
	return "", d.notConfigured()
}
func (d disabledKanisterClient) CreateDelete(context.Context, KanisterActionSpec) (string, error) {
	return "", d.notConfigured()
}
func (d disabledKanisterClient) Status(context.Context, string, string) (ActionSetStatus, error) {
	return ActionSetStatus{}, d.notConfigured()
}
func (d disabledKanisterClient) StatusByLabel(context.Context, string, string, string) (ActionSetStatus, bool, error) {
	return ActionSetStatus{}, false, d.notConfigured()
}
func (d disabledKanisterClient) notConfigured() error {
	return fmt.Errorf("Kanister backup access not configured: %w", d.err)
}
