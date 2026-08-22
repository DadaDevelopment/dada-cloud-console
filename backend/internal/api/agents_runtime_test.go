package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/kagent"
)

var (
	testAgentGVR = schema.GroupVersionResource{Group: "kagent.dev", Version: "v1alpha2", Resource: "agents"}
	testMCPGVR   = schema.GroupVersionResource{Group: "kagent.dev", Version: "v1alpha2", Resource: "remotemcpservers"}
)

func agentTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(testAgentGVR.GroupVersion().WithKind("AgentList"), &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(testMCPGVR.GroupVersion().WithKind("RemoteMCPServerList"), &unstructured.UnstructuredList{})
	return s
}

func testMCPServer(name, url string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kagent.dev/v1alpha2",
		"kind":       "RemoteMCPServer",
		"metadata":   map[string]any{"name": name, "namespace": kagent.DefaultNamespace},
		"spec":       map[string]any{"url": url, "description": name + " tools", "protocol": "STREAMABLE_HTTP"},
		"status": map[string]any{
			"conditions":      []any{map[string]any{"type": "Accepted", "status": "True"}},
			"discoveredTools": []any{map[string]any{"name": "list_tasks", "description": "list"}},
		},
	}}
}

// agentTestHandler is a Handler with nothing but the agent reader wired, which
// is all these endpoints touch.
func agentTestHandler(objs []runtime.Object, k8s ...runtime.Object) *Handler {
	dyn := dynamicfake.NewSimpleDynamicClient(agentTestScheme(), objs...)
	return &Handler{agents: kagent.NewReaderWith(dyn, k8sfake.NewSimpleClientset(k8s...),
		kagent.DefaultNamespace, "https://langfuse.dada-tuda.ru", "proj-1")}
}

// testAgentClaims is any logged-in tenant.
func testAgentClaims() *auth.Claims {
	return &auth.Claims{UserID: uuid.New(), Username: "tenant"}
}

func agentTestContext(t *testing.T, method, path, body string, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, w
}

// TestListAgentTools_HidesTheClusterAddressFromTenants: the tool list is shared
// platform infrastructure and every authenticated user may see what is on
// offer, but the URL is a cluster-internal address that is of no use to a
// tenant and of some use to anyone probing the cluster.
func TestListAgentTools_HidesTheClusterAddressFromTenants(t *testing.T) {
	h := agentTestHandler([]runtime.Object{testMCPServer("reels-task-tools", "http://reels/mcp")})

	c, w := agentTestContext(t, "GET", "/agents/tools", "", testAgentClaims())
	h.ListAgentTools(c)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var tenant struct {
		Tools []AgentToolResponse `json:"tools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tenant); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tenant.Tools) != 1 || tenant.Tools[0].Name != "reels-task-tools" {
		t.Fatalf("tools read wrong: %+v", tenant.Tools)
	}
	if tenant.Tools[0].URL != "" {
		t.Errorf("a tenant must not be handed the cluster address, got %q", tenant.Tools[0].URL)
	}
	if !tenant.Tools[0].Ready || len(tenant.Tools[0].DiscoveredTools) != 1 {
		t.Errorf("discovery must reach the form: %+v", tenant.Tools[0])
	}

	c, w = agentTestContext(t, "GET", "/agents/tools", "", &auth.Claims{Groups: []string{"/platform-admins"}})
	h.ListAgentTools(c)
	var admin struct {
		Tools []AgentToolResponse `json:"tools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &admin); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if admin.Tools[0].URL != "http://reels/mcp" {
		t.Errorf("a platform admin debugging the runtime needs the URL, got %q", admin.Tools[0].URL)
	}
}

// TestValidateAgent_AnswersBeforeTheCommit is the whole reason this endpoint
// exists: every one of these refusals otherwise arrives from Argo minutes after
// the git write, attached to no user action.
func TestValidateAgent_AnswersBeforeTheCommit(t *testing.T) {
	h := agentTestHandler([]runtime.Object{testMCPServer("reels-task-tools", "http://reels/mcp")})

	c, w := agentTestContext(t, "POST", "/agents/validate",
		`{"name":"reels-poc","prompt":"You are a helpful agent.","tools":["reels-task-tools"],"allowed_headers":["x-dada-user"]}`,
		testAgentClaims())
	h.ValidateAgent(c)
	if w.Code != 200 {
		t.Fatalf("a valid draft must pass, got %d: %s", w.Code, w.Body.String())
	}

	cases := map[string]string{
		"Reels_POC":    `{"name":"Reels_POC","prompt":"hi"}`,
		"empty prompt": `{"name":"reels-poc","prompt":"   "}`,
		"unknown tool": `{"name":"reels-poc","prompt":"hi","tools":["nope"]}`,
		"bad header":   `{"name":"reels-poc","prompt":"hi","allowed_headers":["X-Dada-User"]}`,
	}
	for why, body := range cases {
		c, w := agentTestContext(t, "POST", "/agents/validate", body, testAgentClaims())
		h.ValidateAgent(c)
		if w.Code != 400 {
			t.Errorf("%s: status = %d, want 400 (%s)", why, w.Code, w.Body.String())
		}
	}
}

// TestGetAgentState_ReportsAPodServingAnOlderPrompt is the state the console
// cannot infer from its own database: git carries version 8, the pod still
// serves the prompt it started with. Between a commit and a finished rollout
// that is normal; when a rollout is stuck it is permanent and invisible.
func TestGetAgentState_ReportsAPodServingAnOlderPrompt(t *testing.T) {
	agent := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kagent.dev/v1alpha2",
		"kind":       "Agent",
		"metadata":   map[string]any{"name": "reels-poc", "namespace": kagent.DefaultNamespace},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Accepted", "status": "True"},
			map[string]any{"type": "Ready", "status": "True", "reason": "DeploymentReady"},
		}},
	}}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "reels-poc-prompt", Namespace: kagent.DefaultNamespace},
		Data:       map[string]string{"version": "7"},
	}
	h := agentTestHandler([]runtime.Object{agent}, cm)

	c, w := agentTestContext(t, "GET", "/agents/reels-poc/state", "", testAgentClaims())
	c.Params = gin.Params{{Key: "agentName", Value: "reels-poc"}}
	h.GetAgentState(c)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var st kagent.State
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !st.Exists || !st.Ready || st.PromptVersion != "7" {
		t.Fatalf("state read wrong: %+v", st)
	}
	if st.TracesURL == "" {
		t.Error("the traces link is the only way from the console to what the agent actually said")
	}
}

// TestAgentEndpoints_OffClusterAnswer503: local development has no cluster, and
// a 500 there reads as a broken console rather than as an absent runtime.
func TestAgentEndpoints_OffClusterAnswer503(t *testing.T) {
	h := &Handler{}

	c, w := agentTestContext(t, "GET", "/agents/tools", "", testAgentClaims())
	h.ListAgentTools(c)
	if w.Code != 503 {
		t.Errorf("ListAgentTools status = %d, want 503", w.Code)
	}

	c, w = agentTestContext(t, "GET", "/agents/reels-poc/state", "", testAgentClaims())
	c.Params = gin.Params{{Key: "agentName", Value: "reels-poc"}}
	h.GetAgentState(c)
	if w.Code != 503 {
		t.Errorf("GetAgentState status = %d, want 503", w.Code)
	}

	// Validation of name and prompt needs no cluster, so it must still answer.
	c, w = agentTestContext(t, "POST", "/agents/validate",
		`{"name":"reels-poc","prompt":"You are a helpful agent.","tools":["reels-task-tools"]}`,
		testAgentClaims())
	h.ValidateAgent(c)
	if w.Code != 200 {
		t.Errorf("ValidateAgent status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// TestAgentEndpoints_RequireAuth keeps the runtime behind a login: the tool
// list names internal systems even without its URLs.
func TestAgentEndpoints_RequireAuth(t *testing.T) {
	h := agentTestHandler(nil)
	for name, call := range map[string]func(*gin.Context){
		"tools":    h.ListAgentTools,
		"validate": h.ValidateAgent,
		"state":    h.GetAgentState,
	} {
		c, w := agentTestContext(t, "GET", "/agents/tools", "{}", nil)
		call(c)
		if w.Code != 401 {
			t.Errorf("%s status = %d, want 401", name, w.Code)
		}
	}
}
