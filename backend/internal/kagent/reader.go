package kagent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// DefaultNamespace is where the agent runtime lives on beget-prod. Every Agent,
// RemoteMCPServer and prompt ConfigMap of the platform sits in it.
const DefaultNamespace = "kagent"

var (
	agentGVR           = schema.GroupVersionResource{Group: "kagent.dev", Version: "v1alpha2", Resource: "agents"}
	remoteMCPServerGVR = schema.GroupVersionResource{Group: "kagent.dev", Version: "v1alpha2", Resource: "remotemcpservers"}
)

// Tool is one MCP server an agent may be pointed at, as the console offers it
// in the agent form.
//
// Ready and DiscoveredTools are what make the list worth showing rather than
// guessing from a config file: a RemoteMCPServer whose endpoint is down is
// still a CR, still listed, and an agent built on it comes up healthy and
// answers every question with "I have no tools". Discovery is the only place
// that difference is visible before a user notices it in a conversation.
type Tool struct {
	Name            string      `json:"name"`
	Description     string      `json:"description"`
	URL             string      `json:"url"`
	Protocol        string      `json:"protocol"`
	Ready           bool        `json:"ready"`
	Reason          string      `json:"reason,omitempty"`
	DiscoveredTools []ToolEntry `json:"discovered_tools"`
}

// ToolEntry is a single callable an MCP server advertises.
type ToolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PodState is one running replica of an agent.
type PodState struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Restarts int32  `json:"restarts"`
}

// State is the live status of one agent, assembled from the three places that
// each hold a different third of the truth.
//
// The Agent CR says whether kagent accepted the declaration. The pods say
// whether it is actually serving. The prompt ConfigMap says which prompt the
// running process loaded -- and that last one is the one a console cannot infer
// from its own database: an agent keeps serving the prompt it started with
// until the deployment rolls, so the version in git and the version answering
// users are routinely different for a few minutes, and permanently different
// whenever a rollout is stuck.
type State struct {
	Name          string     `json:"name"`
	Namespace     string     `json:"namespace"`
	Exists        bool       `json:"exists"`
	Accepted      bool       `json:"accepted"`
	Ready         bool       `json:"ready"`
	Reason        string     `json:"reason,omitempty"`
	Message       string     `json:"message,omitempty"`
	PromptVersion string     `json:"prompt_version,omitempty"`
	Pods          []PodState `json:"pods"`
	URL           string     `json:"url,omitempty"`
	TracesURL     string     `json:"traces_url,omitempty"`
}

// Reader reads the agent runtime out of the cluster the console runs in.
//
// Off-cluster it is disabled rather than broken: local development has no
// service-account mount, and a console that refuses to start there would cost
// more than a state panel that says it cannot see the cluster.
type Reader struct {
	dyn dynamic.Interface
	cs  kubernetes.Interface

	namespace    string
	langfuseHost string
}

// NewReader builds a Reader from the pod's mounted service-account credentials,
// the same in-cluster pattern as newDeleteImpactScanner.
func NewReader(namespace, langfuseHost string) *Reader {
	r := &Reader{namespace: namespaceOrDefault(namespace)}
	r.setTraces(langfuseHost)

	cfg, err := rest.InClusterConfig()
	if err != nil {
		return r
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return r
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return r
	}
	r.dyn, r.cs = dyn, cs
	return r
}

// NewReaderWith builds a Reader over injected clients, for tests.
func NewReaderWith(dyn dynamic.Interface, cs kubernetes.Interface, namespace, langfuseHost string) *Reader {
	r := &Reader{dyn: dyn, cs: cs, namespace: namespaceOrDefault(namespace)}
	r.setTraces(langfuseHost)
	return r
}

func (r *Reader) setTraces(host string) {
	r.langfuseHost = strings.TrimRight(host, "/")
}

func namespaceOrDefault(ns string) string {
	if ns == "" {
		return DefaultNamespace
	}
	return ns
}

// Enabled reports whether this console can see the cluster at all.
func (r *Reader) Enabled() bool { return r != nil && r.dyn != nil && r.cs != nil }

// Namespace is where this reader looks for agents.
func (r *Reader) Namespace() string { return r.namespace }

// ListTools returns every RemoteMCPServer an agent can be pointed at, sorted by
// name so the form does not reshuffle between loads.
func (r *Reader) ListTools(ctx context.Context) ([]Tool, error) {
	if !r.Enabled() {
		return nil, ErrClusterUnavailable
	}
	list, err := r.dyn.Resource(remoteMCPServerGVR).Namespace(r.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing MCP servers: %w", err)
	}

	tools := make([]Tool, 0, len(list.Items))
	for i := range list.Items {
		tools = append(tools, toolFromObject(&list.Items[i]))
	}
	sort.Slice(tools, func(a, b int) bool { return tools[a].Name < tools[b].Name })
	return tools, nil
}

func toolFromObject(obj *unstructured.Unstructured) Tool {
	t := Tool{Name: obj.GetName(), DiscoveredTools: []ToolEntry{}}
	t.Description, _, _ = unstructured.NestedString(obj.Object, "spec", "description")
	t.URL, _, _ = unstructured.NestedString(obj.Object, "spec", "url")
	t.Protocol, _, _ = unstructured.NestedString(obj.Object, "spec", "protocol")

	t.Ready, t.Reason, _ = conditionState(obj, "Accepted")

	discovered, _, _ := unstructured.NestedSlice(obj.Object, "status", "discoveredTools")
	for _, raw := range discovered {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := entry["description"].(string)
		t.DiscoveredTools = append(t.DiscoveredTools, ToolEntry{Name: name, Description: desc})
	}
	return t
}

// AgentState assembles the live state of one agent.
//
// A missing Agent CR is not an error: between the git commit and Argo's sync
// the claim legitimately does not exist yet, and reporting that as a 500 would
// turn the normal case of "just created" into an incident.
func (r *Reader) AgentState(ctx context.Context, name string) (State, error) {
	if err := ValidateName(name); err != nil {
		return State{}, err
	}
	if !r.Enabled() {
		return State{}, ErrClusterUnavailable
	}

	st := State{Name: name, Namespace: r.namespace, Pods: []PodState{}}

	obj, err := r.dyn.Resource(agentGVR).Namespace(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if isNotFound(err) {
			return st, nil
		}
		return State{}, fmt.Errorf("reading agent: %w", err)
	}
	st.Exists = true
	st.TracesURL = r.tracesURL(obj.GetAnnotations()[LangfuseProjectAnnotation])
	st.Accepted, _, _ = conditionState(obj, "Accepted")
	st.Ready, st.Reason, st.Message = conditionState(obj, "Ready")
	st.URL = fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/", name, r.namespace)

	st.Pods = r.pods(ctx, name)
	st.PromptVersion = r.promptVersion(ctx, name)
	return st, nil
}

// pods lists the agent's replicas by the label kagent stamps on every pod it
// creates for an Agent. Failures are swallowed: pod state is a detail of the
// panel, and losing it must not lose the CR state next to it.
func (r *Reader) pods(ctx context.Context, name string) []PodState {
	list, err := r.cs.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kagent=" + name,
	})
	if err != nil {
		return []PodState{}
	}
	pods := make([]PodState, 0, len(list.Items))
	for i := range list.Items {
		pods = append(pods, podState(&list.Items[i]))
	}
	sort.Slice(pods, func(a, b int) bool { return pods[a].Name < pods[b].Name })
	return pods
}

func podState(pod *corev1.Pod) PodState {
	s := PodState{Name: pod.Name, Phase: string(pod.Status.Phase)}
	ready := len(pod.Status.ContainerStatuses) > 0
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			ready = false
		}
		s.Restarts += cs.RestartCount
	}
	s.Ready = ready
	return s
}

// promptVersion reads the version key of the prompt ConfigMap -- the same value
// that reaches the pod as PROMPT_VERSION, because the composition wires the env
// var to this key.
func (r *Reader) promptVersion(ctx context.Context, name string) string {
	cm, err := r.cs.CoreV1().ConfigMaps(r.namespace).Get(ctx, name+"-prompt", metav1.GetOptions{})
	if err != nil {
		return ""
	}
	return cm.Data["version"]
}

// LangfuseProjectAnnotation names the Langfuse project one agent reports into.
//
// Per agent rather than per platform: agents carry their own Langfuse
// credentials, so two agents of the same cluster legitimately write into two
// different projects, and the console cannot know which from its own config.
const LangfuseProjectAnnotation = "platform.dada-tuda.ru/langfuse-project"

// tracesURL points at the Langfuse project this one agent reports into, or ""
// when the agent does not say which project that is. An empty string is the
// honest answer: a link built from the platform's own project id opens somebody
// else's traces, which reads as "this agent never ran" rather than "the link is
// pointed at the wrong project".
func (r *Reader) tracesURL(project string) string {
	if r.langfuseHost == "" || project == "" {
		return ""
	}
	return fmt.Sprintf("%s/project/%s/traces", r.langfuseHost, project)
}

// conditionState reads one status condition, returning whether it is True plus
// its reason and message.
func conditionState(obj *unstructured.Unstructured, condType string) (bool, string, string) {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		cond, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t != condType {
			continue
		}
		status, _ := cond["status"].(string)
		reason, _ := cond["reason"].(string)
		message, _ := cond["message"].(string)
		return status == "True", reason, message
	}
	return false, "", ""
}
