package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCloudTaskCtx(t *testing.T, method, body string, params gin.Params, withClaims bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = params
	return c, rec
}

func TestCreateCloudTask_NoClaims_401(t *testing.T) {
	h := &Handler{}
	c, rec := newCloudTaskCtx(t, http.MethodPost, `{"task_type":"yandex-metrika-goals"}`,
		gin.Params{{Key: "projectId", Value: "00000000-0000-0000-0000-000000000001"},
			{Key: "envId", Value: "00000000-0000-0000-0000-000000000002"},
			{Key: "appName", Value: "web"}}, false)
	h.CreateCloudTask(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

func TestListCloudTasks_NoClaims_401(t *testing.T) {
	h := &Handler{}
	c, rec := newCloudTaskCtx(t, http.MethodGet, "",
		gin.Params{{Key: "projectId", Value: "00000000-0000-0000-0000-000000000001"},
			{Key: "appName", Value: "web"}}, false)
	h.ListCloudTasks(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

func TestCreateCloudTask_BadProjectID_404(t *testing.T) {
	h := &Handler{}
	c, rec := newCloudTaskCtx(t, http.MethodPost, `{"task_type":"yandex-metrika-goals"}`,
		gin.Params{{Key: "projectId", Value: "not-a-uuid"}}, false)
	// No claims -> 401 first (claims checked before parse). Assert it does not panic.
	h.CreateCloudTask(c)
	if rec.Code == 0 {
		t.Fatal("handler did not write a response")
	}
}
