package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/kagent"
	"github.com/dada-tuda/console/backend/internal/models"
)

var testModelConfigGVR = schema.GroupVersionResource{Group: "kagent.dev", Version: "v1alpha2", Resource: "modelconfigs"}

func testModelConfig(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kagent.dev/v1alpha2",
		"kind":       "ModelConfig",
		"metadata":   map[string]any{"name": name, "namespace": kagent.DefaultNamespace},
		"spec":       map[string]any{"model": "glm-4.5-flash", "provider": "OpenAI"},
	}}
}

func saveCheckRuntime(objs ...runtime.Object) *kagent.Reader {
	s := agentTestScheme()
	s.AddKnownTypeWithName(testModelConfigGVR.GroupVersion().WithKind("ModelConfigList"), &unstructured.UnstructuredList{})
	dyn := dynamicfake.NewSimpleDynamicClient(s, objs...)
	return kagent.NewReaderWith(dyn, k8sfake.NewSimpleClientset(), kagent.DefaultNamespace, "")
}

// TestValidateAgent_RefusesAModelConfigTheRuntimeDoesNotHave: a typo in
// model_config is accepted by git and by Argo and then leaves the agent Unready
// forever. The save refuses it now, so the validator must too.
func TestValidateAgent_RefusesAModelConfigTheRuntimeDoesNotHave(t *testing.T) {
	h := &Handler{agents: saveCheckRuntime(testModelConfig("tg-referral-glm-53-flash"))}

	c, w := agentTestContext(t, "POST", "/agents/validate",
		`{"name":"native","prompt":"hi","model_config":"tg-referral-glm-53-flash"}`, testAgentClaims())
	h.ValidateAgent(c)
	if w.Code != http.StatusOK {
		t.Fatalf("an existing model config must pass, got %d: %s", w.Code, w.Body.String())
	}

	c, w = agentTestContext(t, "POST", "/agents/validate",
		`{"name":"native","prompt":"hi","model_config":"tg-referral-glm-53-flsh"}`, testAgentClaims())
	h.ValidateAgent(c)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"field":"model_config"`) {
		t.Fatalf("a missing model config must be a field error, got %d: %s", w.Code, w.Body.String())
	}
}

// TestSaveAndValidate_AgreeOnEveryDraft runs the same drafts through both
// endpoints: whatever the validator clears, the save must accept, and whatever
// the save refuses, the validator must refuse with the same status.
func TestSaveAndValidate_AgreeOnEveryDraft(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}, agents: saveCheckRuntime(
		testModelConfig("tg-referral-glm-53-flash"),
		testMCPServer("reels-task-tools", "http://reels/mcp"),
	)}
	projectID, envID := seedOptimisticFixture(t, pool)
	userID := seedUser(t, pool)
	existing := "native-" + uuid.NewString()[:8]
	seedManagedAgentSnapshot(t, pool, projectID, envID, existing)
	fresh := "fresh-" + uuid.NewString()[:8]
	t.Cleanup(func() { dropSeededAudit(pool, managedAgentKind, fresh) })

	tools := []models.AgentToolRef{{Name: "reels-task-tools"}}
	cases := []struct {
		why  string
		req  saveAgentRequest
		want int
	}{
		{"tools-only save of an existing agent", saveAgentRequest{Name: existing, Tools: tools}, http.StatusAccepted},
		{"tools-only save of a new agent", saveAgentRequest{Name: fresh, Tools: tools}, http.StatusBadRequest},
		{"unknown model config", saveAgentRequest{Name: existing, ModelConfig: "nope"}, http.StatusBadRequest},
		{"known model config", saveAgentRequest{Name: existing, ModelConfig: "tg-referral-glm-53-flash"}, http.StatusAccepted},
	}
	for _, tc := range cases {
		body, _ := json.Marshal(map[string]any{
			"name":         tc.req.Name,
			"prompt":       tc.req.Prompt,
			"model_config": tc.req.ModelConfig,
			"tools":        tc.req.Tools,
		})
		path := "/agents/validate?project=" + projectID.String() + "&environment=" + envID.String()
		c, w := agentTestContext(t, "POST", path, string(body), godClaims(userID))
		h.ValidateAgent(c)
		wantValidate := tc.want
		if wantValidate == http.StatusAccepted {
			wantValidate = http.StatusOK
		}
		if w.Code != wantValidate {
			t.Errorf("%s: validate = %d, want %d (%s)", tc.why, w.Code, wantValidate, w.Body.String())
		}

		c, rec := saveAgentCtx(t, projectID, envID, userID, tc.req)
		h.SaveAgent(c)
		if rec.Code != tc.want {
			t.Errorf("%s: save = %d, want %d (%s)", tc.why, rec.Code, tc.want, rec.Body.String())
		}
		if tc.why != "tools-only save of an existing agent" || rec.Code != http.StatusAccepted {
			continue
		}
		var out struct {
			Operation models.Operation `json:"operation"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		var payload models.SaveAgentPayload
		_ = json.Unmarshal(out.Operation.Payload, &payload)
		if payload.Prompt != "" || payload.ModelConfig != "" || len(payload.Env) != 0 {
			t.Errorf("a tools-only save must leave the rest unsaid for the git writer to keep, got %+v", payload)
		}
	}

	var ops int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM operations WHERE project_id = $1 AND resource_name = $2`, projectID, fresh).Scan(&ops); err != nil {
		t.Fatal(err)
	}
	if ops != 0 {
		t.Fatalf("a refused save must not queue an operation, got %d", ops)
	}
}
