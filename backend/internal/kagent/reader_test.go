package kagent

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// scheme registers the two kagent list kinds the fake dynamic client needs to
// answer a List call for an unstructured GVR.
func scheme() *runtime.Scheme {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(agentGVR.GroupVersion().WithKind("AgentList"), &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(remoteMCPServerGVR.GroupVersion().WithKind("RemoteMCPServerList"), &unstructured.UnstructuredList{})
	return s
}

func remoteMCPServer(name, url string, accepted bool, discovered ...string) *unstructured.Unstructured {
	tools := []any{}
	for _, d := range discovered {
		tools = append(tools, map[string]any{"name": d, "description": d + " does a thing"})
	}
	status := "False"
	if accepted {
		status = "True"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kagent.dev/v1alpha2",
		"kind":       "RemoteMCPServer",
		"metadata":   map[string]any{"name": name, "namespace": DefaultNamespace},
		"spec": map[string]any{
			"description": name + " tools",
			"url":         url,
			"protocol":    "STREAMABLE_HTTP",
		},
		"status": map[string]any{
			"conditions": []any{map[string]any{
				"type": "Accepted", "status": status, "reason": "Reconciled",
			}},
			"discoveredTools": tools,
		},
	}}
}

func agentCR(name string, ready bool) *unstructured.Unstructured {
	status := "False"
	reason, message := "DeploymentNotReady", "Deployment is not ready"
	if ready {
		status, reason, message = "True", "DeploymentReady", "Deployment is ready"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kagent.dev/v1alpha2",
		"kind":       "Agent",
		"metadata": map[string]any{
			"name":        name,
			"namespace":   DefaultNamespace,
			"annotations": map[string]any{LangfuseProjectAnnotation: "proj-1"},
		},
		"spec": map[string]any{},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Accepted", "status": "True", "reason": "Reconciled"},
			map[string]any{"type": "Ready", "status": status, "reason": reason, "message": message},
		}},
	}}
}

func readerFor(t *testing.T, objs []runtime.Object, k8s ...runtime.Object) *Reader {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme(), objs...)
	return NewReaderWith(dyn, fake.NewSimpleClientset(k8s...), DefaultNamespace,
		"https://langfuse.dada-tuda.ru/")
}

// TestListTools_ReportsWhatEachServerActuallyDiscovered is the reason this
// endpoint exists rather than the console reading a static list: a
// RemoteMCPServer whose endpoint is down still exists as a CR, and an agent
// built on it comes up healthy and toolless.
func TestListTools_ReportsWhatEachServerActuallyDiscovered(t *testing.T) {
	r := readerFor(t, []runtime.Object{
		remoteMCPServer("reels-task-tools", "http://reels/mcp", true, "list_tasks", "close_task"),
		remoteMCPServer("dead-tools", "http://dead/mcp", false),
	})

	tools, err := r.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2: %+v", len(tools), tools)
	}
	if tools[0].Name != "dead-tools" || tools[1].Name != "reels-task-tools" {
		t.Fatalf("tools must be sorted by name, got %q, %q", tools[0].Name, tools[1].Name)
	}
	if tools[0].Ready {
		t.Error("a server whose Accepted condition is False must not be offered as ready")
	}
	if len(tools[0].DiscoveredTools) != 0 {
		t.Errorf("DiscoveredTools must be empty, got %+v", tools[0].DiscoveredTools)
	}
	live := tools[1]
	if !live.Ready || live.URL != "http://reels/mcp" || live.Protocol != "STREAMABLE_HTTP" {
		t.Errorf("live server read wrong: %+v", live)
	}
	if len(live.DiscoveredTools) != 2 || live.DiscoveredTools[0].Name != "list_tasks" {
		t.Errorf("discovered tools read wrong: %+v", live.DiscoveredTools)
	}
}

// TestAgentState_ReadsCRPodsAndPromptVersion covers the whole point of the
// reader: the CR, the pods and the prompt version each live somewhere else, and
// only together do they answer "is this agent serving, and serving what".
func TestAgentState_ReadsCRPodsAndPromptVersion(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "reels-poc-77d9",
			Namespace: DefaultNamespace,
			Labels:    map[string]string{"kagent": "reels-poc"},
		},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Ready: true, RestartCount: 2}},
		},
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "reels-poc-prompt", Namespace: DefaultNamespace},
		Data:       map[string]string{"version": "7", "system.md": "you are a bot"},
	}
	r := readerFor(t, []runtime.Object{agentCR("reels-poc", true)}, pod, cm)

	st, err := r.AgentState(context.Background(), "reels-poc")
	if err != nil {
		t.Fatalf("AgentState: %v", err)
	}
	if !st.Exists || !st.Accepted || !st.Ready {
		t.Fatalf("agent must read as live: %+v", st)
	}
	if st.Reason != "DeploymentReady" {
		t.Errorf("Reason = %q, want DeploymentReady", st.Reason)
	}
	if st.PromptVersion != "7" {
		t.Errorf("PromptVersion = %q, want the ConfigMap version the pod loads as PROMPT_VERSION", st.PromptVersion)
	}
	if len(st.Pods) != 1 || !st.Pods[0].Ready || st.Pods[0].Restarts != 2 {
		t.Errorf("pods read wrong: %+v", st.Pods)
	}
	if st.URL != "http://reels-poc.kagent.svc.cluster.local:8080/" {
		t.Errorf("URL = %q; headers survive only on the agent's own Service, not the controller proxy", st.URL)
	}
	if st.TracesURL != "https://langfuse.dada-tuda.ru/project/proj-1/traces" {
		t.Errorf("TracesURL = %q", st.TracesURL)
	}
}

// TestAgentState_MissingAgentIsNotAnError covers the window every freshly
// created agent passes through: the git commit has landed and Argo has not
// synced yet. Reporting that as a failure turns the normal case into an alarm.
func TestAgentState_MissingAgentIsNotAnError(t *testing.T) {
	r := readerFor(t, nil)

	st, err := r.AgentState(context.Background(), "not-yet")
	if err != nil {
		t.Fatalf("a not-yet-synced agent must not error: %v", err)
	}
	if st.Exists || st.Ready {
		t.Fatalf("missing agent must read as absent: %+v", st)
	}
	if st.Pods == nil {
		t.Error("Pods must encode as [] and not null; the console iterates it during render")
	}
}

// TestAgentState_RejectsANameTheClusterWouldReject keeps the validation in
// front of the cluster call, so a bad name comes back as a field error instead
// of a 404 that reads as "your agent is gone".
func TestAgentState_RejectsANameTheClusterWouldReject(t *testing.T) {
	r := readerFor(t, nil)
	if _, err := r.AgentState(context.Background(), "Reels_POC"); err == nil {
		t.Fatal("an invalid agent name must be refused before the cluster is asked")
	}
}

// TestReader_OffClusterIsDisabledNotBroken: local development has no
// service-account mount, and a console that will not start there costs more
// than a panel that says it cannot see the cluster.
func TestReader_OffClusterIsDisabledNotBroken(t *testing.T) {
	r := NewReaderWith(nil, nil, "", "")
	if r.Enabled() {
		t.Fatal("a reader with no clients must not claim to be enabled")
	}
	if r.Namespace() != DefaultNamespace {
		t.Errorf("Namespace() = %q, want the default", r.Namespace())
	}
	if _, err := r.ListTools(context.Background()); err != ErrClusterUnavailable {
		t.Errorf("ListTools error = %v, want ErrClusterUnavailable", err)
	}
	if _, err := r.AgentState(context.Background(), "reels-poc"); err != ErrClusterUnavailable {
		t.Errorf("AgentState error = %v, want ErrClusterUnavailable", err)
	}
}

// TestTracesURL_EmptyWhenTheProjectIsUnknown: a link built from a guessed
// project id opens somebody else's traces, which reads as "this agent never
// ran" rather than "the link is pointed at the wrong project".
func TestTracesURL_EmptyWhenTheProjectIsUnknown(t *testing.T) {
	r := NewReaderWith(nil, nil, "", "https://langfuse.dada-tuda.ru")
	if got := r.tracesURL(""); got != "" {
		t.Fatalf("tracesURL() = %q, want empty", got)
	}
}

// TestAgentState_TracesLinkFollowsTheAgentNotThePlatform is the defect this
// annotation exists for: agents carry their own Langfuse credentials, so two
// agents of one cluster write into two different projects, and a link built
// from the platform's own project id sent every agent to the console's traces.
func TestAgentState_TracesLinkFollowsTheAgentNotThePlatform(t *testing.T) {
	own := agentCR("telemost-poc", true)
	own.SetAnnotations(map[string]string{LangfuseProjectAnnotation: "proj-telemost"})

	st, err := readerFor(t, []runtime.Object{own}).AgentState(context.Background(), "telemost-poc")
	if err != nil {
		t.Fatalf("AgentState: %v", err)
	}
	if st.TracesURL != "https://langfuse.dada-tuda.ru/project/proj-telemost/traces" {
		t.Fatalf("TracesURL = %q, want the agent's own project", st.TracesURL)
	}
}

// TestAgentState_NoTracesLinkWhenTheAgentDoesNotSayWhere: silence beats a link
// into a project this agent never wrote to.
func TestAgentState_NoTracesLinkWhenTheAgentDoesNotSayWhere(t *testing.T) {
	bare := agentCR("reels-poc", true)
	bare.SetAnnotations(nil)

	st, err := readerFor(t, []runtime.Object{bare}).AgentState(context.Background(), "reels-poc")
	if err != nil {
		t.Fatalf("AgentState: %v", err)
	}
	if st.TracesURL != "" {
		t.Fatalf("TracesURL = %q, want empty for an agent that names no project", st.TracesURL)
	}
}
