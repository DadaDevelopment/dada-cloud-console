package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Box handler tests.
//
// Split deliberately in two: the authorization ladder is exercised WITHOUT a
// database (the 401 and malformed-id arms return before any query), and the
// lifecycle is exercised against a real one behind the TEST_DATABASE_URL skip
// idiom. Fixtures follow storage_cap_test.go: a throwaway project registered for
// cleanup, so a failed test cannot leave rows that make the next run pass or fail
// for the wrong reason.

// boxParams builds the path params a box handler reads.
func boxParams(projectID uuid.UUID, boxName string) gin.Params {
	p := gin.Params{{Key: "projectId", Value: projectID.String()}}
	if boxName != "" {
		p = append(p, gin.Param{Key: "boxName", Value: boxName})
	}
	return p
}

// newBoxCtx is newCreateCtx with a settable method, so DELETE and GET handlers
// are exercised through the verb they are actually routed under.
func newBoxCtx(t *testing.T, method, body string, params gin.Params, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, rec
}

// seedBoxFixture creates a throwaway project and returns its id.
func seedBoxFixture(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var projectID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"box-test-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// projects cascades to environments, and environments cascades to boxes, so
	// one cleanup removes the whole fixture including anything the handler created.
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })
	return projectID
}

// --- authorization ladder (no database needed) ---

// TestBoxHandlersRejectAnonymous asserts every box handler answers 401 before it
// touches anything. The pool is a zero value on purpose: if a handler queried
// before checking claims, this test would panic instead of passing, which is
// exactly the signal we want.
func TestBoxHandlersRejectAnonymous(t *testing.T) {
	h := &Handler{pool: &pgxpool.Pool{}, cfg: &config.Config{}}
	projectID := uuid.New()
	cases := []struct {
		name    string
		method  string
		handler func(*gin.Context)
	}{
		{"ListBoxes", http.MethodGet, h.ListBoxes},
		{"CreateBox", http.MethodPost, h.CreateBox},
		{"GetBox", http.MethodGet, h.GetBox},
		{"GetBoxState", http.MethodGet, h.GetBoxState},
		{"DeleteBox", http.MethodDelete, h.DeleteBox},
		{"SuspendBox", http.MethodPost, h.SuspendBox},
		{"ResumeBox", http.MethodPost, h.ResumeBox},
		{"ExtendBox", http.MethodPost, h.ExtendBox},
	}
	for _, tc := range cases {
		c, rec := newBoxCtx(t, tc.method, `{}`, boxParams(projectID, "box-x"), nil)
		tc.handler(c)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with no claims: status = %d, want 401; body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// TestBoxHandlersMalformedProjectIDIs404 pins 404 rather than 400 for a
// malformed project id. A 400 would confirm the id format is wrong while a 404
// says nothing, and the whole point of the ladder is that an outsider learns
// nothing from status codes.
func TestBoxHandlersMalformedProjectIDIs404(t *testing.T) {
	h := &Handler{pool: &pgxpool.Pool{}, cfg: &config.Config{}}
	claims := &auth.Claims{UserID: uuid.New()}
	for name, handler := range map[string]func(*gin.Context){
		"ListBoxes":  h.ListBoxes,
		"CreateBox":  h.CreateBox,
		"GetBox":     h.GetBox,
		"DeleteBox":  h.DeleteBox,
		"SuspendBox": h.SuspendBox,
	} {
		c, rec := newBoxCtx(t, http.MethodPost, `{}`,
			gin.Params{{Key: "projectId", Value: "not-a-uuid"}, {Key: "boxName", Value: "box-x"}}, claims)
		handler(c)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s with a malformed project id: status = %d, want 404; body=%s", name, rec.Code, rec.Body.String())
		}
	}
}

// --- lifecycle (needs a database) ---

// TestCreateBox_CreatesEnvironmentBoxAndOperation is the slice's Verify line:
// POST /boxes leaves a boxes row, an environments row with runtime='box' and
// type='dev', and a Created BoxUp operation bound to that environment.
func TestCreateBox_CreatesEnvironmentBoxAndOperation(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	c, rec := newBoxCtx(t, http.MethodPost, `{"name":"box-alpha"}`, boxParams(projectID, ""), claims)
	h.CreateBox(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Box       models.Box       `json:"box"`
		Operation models.Operation `json:"operation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Box.Status != models.BoxStatusRequested {
		t.Errorf("box status = %q, want Requested", body.Box.Status)
	}
	if body.Box.Image == "" || body.Box.Profile == "" {
		t.Errorf("catalog defaults not applied: image=%q profile=%q", body.Box.Image, body.Box.Profile)
	}

	// The environment row is the identity carrier crystallization will promote.
	var envRuntime, envType string
	if err := pool.QueryRow(context.Background(),
		`SELECT runtime, type FROM environments WHERE id = $1`, body.Box.EnvironmentID,
	).Scan(&envRuntime, &envType); err != nil {
		t.Fatalf("box's environment row missing: %v", err)
	}
	if envRuntime != "box" || envType != "dev" {
		t.Errorf("environment = runtime %q / type %q, want box / dev", envRuntime, envType)
	}

	if body.Operation.Action != models.ActionBoxUp {
		t.Errorf("operation action = %q, want BoxUp", body.Operation.Action)
	}
	if body.Operation.Status != models.OperationStatusCreated {
		t.Errorf("operation status = %q, want Created", body.Operation.Status)
	}
	if body.Operation.ResourceKind != models.ResourceKindBox {
		t.Errorf("operation resource_kind = %q, want Box", body.Operation.ResourceKind)
	}
	// environment_id is stamped so everything the box later accumulates
	// correlates with the operation that created it.
	if body.Operation.EnvironmentID == nil || *body.Operation.EnvironmentID != body.Box.EnvironmentID {
		t.Errorf("operation environment_id = %v, want the box's %v", body.Operation.EnvironmentID, body.Box.EnvironmentID)
	}
}

// TestCreateBox_GeneratesNameWhenOmitted: every request field is optional,
// because an entrance that takes more than one step is a VPS with extra steps.
func TestCreateBox_GeneratesNameWhenOmitted(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	c, rec := newBoxCtx(t, http.MethodPost, `{}`, boxParams(projectID, ""), claims)
	h.CreateBox(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("empty body: status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Box models.Box `json:"box"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Box.Name) != len("box-")+8 || body.Box.Name[:4] != "box-" {
		t.Errorf("generated name = %q, want box-<8 hex>", body.Box.Name)
	}
}

func TestCreateBox_RejectsUnknownCatalogEntries(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	for name, body := range map[string]string{
		"unknown image":   `{"name":"box-bad-img","image":"warm-v99"}`,
		"unknown profile": `{"name":"box-bad-prof","profile":"box-enormous"}`,
		"ttl too large":   `{"name":"box-bad-ttl","ttl_seconds":999999}`,
		"negative cap":    `{"name":"box-bad-cap","spend_cap_rub":-1}`,
	} {
		c, rec := newBoxCtx(t, http.MethodPost, body, boxParams(projectID, ""), claims)
		h.CreateBox(c)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body=%s", name, rec.Code, rec.Body.String())
		}
	}
}

// TestCreateBox_DuplicateLiveNameIsConflict, and the name becomes reusable once
// the box is Deleted — the partial unique index is what enforces both, so two
// racing replicas cannot both win.
func TestCreateBox_DuplicateLiveNameIsConflictThenReusable(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	c1, rec1 := newBoxCtx(t, http.MethodPost, `{"name":"box-dup"}`, boxParams(projectID, ""), claims)
	h.CreateBox(c1)
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("first create: status = %d, want 202; body=%s", rec1.Code, rec1.Body.String())
	}

	c2, rec2 := newBoxCtx(t, http.MethodPost, `{"name":"box-dup"}`, boxParams(projectID, ""), claims)
	h.CreateBox(c2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("duplicate live name: status = %d, want 409; body=%s", rec2.Code, rec2.Body.String())
	}

	// Tombstone the first box AND free its environment name (the environment row
	// is retained for the box's whole life; a real delete of the box's identity
	// carrier is out of scope for slice 1, so the fixture renames it here to prove
	// the BOX index is what allowed the reuse).
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET status = 'Deleted', deleted_at = now() WHERE project_id = $1 AND name = 'box-dup'`,
		projectID); err != nil {
		t.Fatalf("tombstone box: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE environments SET name = 'box-dup-old' WHERE project_id = $1 AND name = 'box-dup'`,
		projectID); err != nil {
		t.Fatalf("rename old environment: %v", err)
	}

	c3, rec3 := newBoxCtx(t, http.MethodPost, `{"name":"box-dup"}`, boxParams(projectID, ""), claims)
	h.CreateBox(c3)
	if rec3.Code != http.StatusAccepted {
		t.Fatalf("reuse of a deleted box's name: status = %d, want 202; body=%s", rec3.Code, rec3.Body.String())
	}
}

// TestBoxReadsHideDeletedAndUnknown: a deleted box is not addressable, because its
// name is reusable and "the deleted one" therefore has no stable meaning.
func TestBoxReadsHideDeletedAndUnknown(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	c, rec := newBoxCtx(t, http.MethodPost, `{"name":"box-gone"}`, boxParams(projectID, ""), claims)
	h.CreateBox(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET status = 'Deleted' WHERE project_id = $1 AND name = 'box-gone'`, projectID); err != nil {
		t.Fatalf("tombstone: %v", err)
	}

	for name, handler := range map[string]func(*gin.Context){
		"GetBox":      h.GetBox,
		"GetBoxState": h.GetBoxState,
	} {
		c, rec := newBoxCtx(t, http.MethodGet, "", boxParams(projectID, "box-gone"), claims)
		handler(c)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s on a deleted box: status = %d, want 404; body=%s", name, rec.Code, rec.Body.String())
		}
	}

	cl, recl := newBoxCtx(t, http.MethodGet, "", boxParams(projectID, ""), claims)
	h.ListBoxes(cl)
	if recl.Code != http.StatusOK {
		t.Fatalf("ListBoxes: status = %d; body=%s", recl.Code, recl.Body.String())
	}
	var listed struct {
		Boxes []models.Box `json:"boxes"`
	}
	if err := json.Unmarshal(recl.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, b := range listed.Boxes {
		if b.Name == "box-gone" {
			t.Error("ListBoxes returned a Deleted box")
		}
	}
}

// TestDeleteBox_MarksDeletingBeforeEnqueue: the status has to move BEFORE the
// operation exists, or a concurrent read hands out a box that is being torn down.
func TestDeleteBox_MarksDeletingBeforeEnqueue(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	c, rec := newBoxCtx(t, http.MethodPost, `{"name":"box-del"}`, boxParams(projectID, ""), claims)
	h.CreateBox(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	cd, recd := newBoxCtx(t, http.MethodDelete, "", boxParams(projectID, "box-del"), claims)
	h.DeleteBox(cd)
	if recd.Code != http.StatusAccepted {
		t.Fatalf("delete: status = %d, want 202; body=%s", recd.Code, recd.Body.String())
	}

	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM boxes WHERE project_id = $1 AND name = 'box-del'`, projectID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != string(models.BoxStatusDeleting) {
		t.Errorf("status after delete = %q, want Deleting", status)
	}

	var action string
	if err := pool.QueryRow(context.Background(),
		`SELECT action FROM operations WHERE project_id = $1 AND resource_name = 'box-del' AND action = 'DeleteBox'`,
		projectID).Scan(&action); err != nil {
		t.Fatalf("DeleteBox operation missing: %v", err)
	}

	// Idempotent: the second delete is the same intent, not an error.
	cd2, recd2 := newBoxCtx(t, http.MethodDelete, "", boxParams(projectID, "box-del"), claims)
	h.DeleteBox(cd2)
	if recd2.Code != http.StatusAccepted {
		t.Errorf("repeated delete: status = %d, want 202; body=%s", recd2.Code, recd2.Body.String())
	}
}

// TestSuspendResumeRespectThePhaseMachine: a Requested box has no body to freeze,
// and a Ready box has nothing to wake.
func TestSuspendResumeRespectThePhaseMachine(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	c, rec := newBoxCtx(t, http.MethodPost, `{"name":"box-phase"}`, boxParams(projectID, ""), claims)
	h.CreateBox(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Requested -> Sleeping is not a legal transition.
	cs, recs := newBoxCtx(t, http.MethodPost, `{}`, boxParams(projectID, "box-phase"), claims)
	h.SuspendBox(cs)
	if recs.Code != http.StatusConflict {
		t.Errorf("suspend a Requested box: status = %d, want 409; body=%s", recs.Code, recs.Body.String())
	}

	// Resume of a not-sleeping box is refused too.
	cr, recr := newBoxCtx(t, http.MethodPost, `{}`, boxParams(projectID, "box-phase"), claims)
	h.ResumeBox(cr)
	if recr.Code != http.StatusConflict {
		t.Errorf("resume a Requested box: status = %d, want 409; body=%s", recr.Code, recr.Body.String())
	}

	// Drive it to Ready and suspend for real.
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET status = 'Ready' WHERE project_id = $1 AND name = 'box-phase'`, projectID); err != nil {
		t.Fatalf("set Ready: %v", err)
	}
	cs2, recs2 := newBoxCtx(t, http.MethodPost, `{}`, boxParams(projectID, "box-phase"), claims)
	h.SuspendBox(cs2)
	if recs2.Code != http.StatusAccepted {
		t.Fatalf("suspend a Ready box: status = %d, want 202; body=%s", recs2.Code, recs2.Body.String())
	}

	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET status = 'Sleeping' WHERE project_id = $1 AND name = 'box-phase'`, projectID); err != nil {
		t.Fatalf("set Sleeping: %v", err)
	}
	cr2, recr2 := newBoxCtx(t, http.MethodPost, `{}`, boxParams(projectID, "box-phase"), claims)
	h.ResumeBox(cr2)
	if recr2.Code != http.StatusAccepted {
		t.Fatalf("resume a Sleeping box: status = %d, want 202; body=%s", recr2.Code, recr2.Body.String())
	}
}

// TestExtendBox_MovesExpiryAndRefusesGarbage. Extend is the one synchronous box
// mutation: it moves a timestamp in our own row and touches no runtime.
func TestExtendBox_MovesExpiryAndRefusesGarbage(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	c, rec := newBoxCtx(t, http.MethodPost, `{"name":"box-ext"}`, boxParams(projectID, ""), claims)
	h.CreateBox(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: status = %d; body=%s", rec.Code, rec.Body.String())
	}

	for name, body := range map[string]string{
		"zero":     `{"ttl_seconds":0}`,
		"negative": `{"ttl_seconds":-5}`,
		"over cap": `{"ttl_seconds":999999}`,
	} {
		ce, rece := newBoxCtx(t, http.MethodPost, body, boxParams(projectID, "box-ext"), claims)
		h.ExtendBox(ce)
		if rece.Code != http.StatusBadRequest {
			t.Errorf("extend with %s ttl: status = %d, want 400; body=%s", name, rece.Code, rece.Body.String())
		}
	}

	ce, rece := newBoxCtx(t, http.MethodPost, `{"ttl_seconds":3600}`, boxParams(projectID, "box-ext"), claims)
	h.ExtendBox(ce)
	if rece.Code != http.StatusOK {
		t.Fatalf("extend: status = %d, want 200; body=%s", rece.Code, rece.Body.String())
	}
	var body struct {
		Box models.Box `json:"box"`
	}
	if err := json.Unmarshal(rece.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Box.TTLSeconds != 3600 {
		t.Errorf("ttl_seconds = %d, want 3600", body.Box.TTLSeconds)
	}
	if body.Box.ExpiresAt == nil {
		t.Fatal("expires_at not set by extend")
	}

	// A box being torn down cannot be extended.
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET status = 'Deleting' WHERE project_id = $1 AND name = 'box-ext'`, projectID); err != nil {
		t.Fatalf("set Deleting: %v", err)
	}
	ce2, rece2 := newBoxCtx(t, http.MethodPost, `{"ttl_seconds":3600}`, boxParams(projectID, "box-ext"), claims)
	h.ExtendBox(ce2)
	if rece2.Code != http.StatusConflict {
		t.Errorf("extend a Deleting box: status = %d, want 409; body=%s", rece2.Code, rece2.Body.String())
	}
}

// TestBoxHandlersUnknownProjectIs404NotForbidden is the anti-enumeration
// assertion, and it needs a real database because it is effectiveRole's
// pgx.ErrNoRows arm that has to become a 404. A non-god claim against a project
// that exists but the caller is not a member of must be indistinguishable from a
// project that does not exist.
func TestBoxHandlersUnknownProjectIs404NotForbidden(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID := seedBoxFixture(t, pool)
	outsider := &auth.Claims{UserID: seedUser(t, pool)}

	for name, tc := range map[string]struct {
		method  string
		handler func(*gin.Context)
	}{
		"ListBoxes":  {http.MethodGet, h.ListBoxes},
		"CreateBox":  {http.MethodPost, h.CreateBox},
		"GetBox":     {http.MethodGet, h.GetBox},
		"DeleteBox":  {http.MethodDelete, h.DeleteBox},
		"SuspendBox": {http.MethodPost, h.SuspendBox},
		"ExtendBox":  {http.MethodPost, h.ExtendBox},
	} {
		// A project that exists but the caller cannot see.
		c, rec := newBoxCtx(t, tc.method, `{"ttl_seconds":60}`, boxParams(projectID, "box-x"), outsider)
		tc.handler(c)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s as a non-member: status = %d, want 404 (403 would confirm the project exists); body=%s",
				name, rec.Code, rec.Body.String())
		}
		// A project that does not exist: the SAME status, or the pair is an oracle.
		c2, rec2 := newBoxCtx(t, tc.method, `{"ttl_seconds":60}`, boxParams(uuid.New(), "box-x"), outsider)
		tc.handler(c2)
		if rec2.Code != rec.Code {
			t.Errorf("%s: unknown project answered %d but invisible project answered %d — "+
				"the difference lets an outsider enumerate project ids", name, rec2.Code, rec.Code)
		}
	}
}
