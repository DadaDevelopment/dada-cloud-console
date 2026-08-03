package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedStorageCapFixture creates a throwaway org-owned project + prod
// environment, mirroring seedOptimisticFixture but stamping org_id so
// storageCapBytes has a real org to resolve a plan for.
func seedStorageCapFixture(t *testing.T, pool *pgxpool.Pool, orgID string) (projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"storage-cap-"+suffix, orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "storage-cap-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return projectID, envID
}

// seedFreePlanOrg gives an org a billing_accounts row on the free plan, whose
// testPlans() quota is StorageGB: 2.
func seedFreePlanOrg(t *testing.T, pool *pgxpool.Pool, orgID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, updated_at)
		VALUES ($1, 'free', now(), now())
	`, orgID); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})
}

func assertQuotaExceeded(t *testing.T, rec interface{ Result() *http.Response }, body []byte, code int, wantLimit float64) {
	t.Helper()
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", code, body)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if parsed["error"] != "quota_exceeded" || parsed["resource"] != "storage_gb" {
		t.Fatalf("body = %v, want error=quota_exceeded resource=storage_gb", parsed)
	}
	if limit, _ := parsed["limit"].(float64); limit != wantLimit {
		t.Fatalf("limit = %v, want %v", parsed["limit"], wantLimit)
	}
}

func TestUpdateAppStorage_BillingDisabled_LegacyTenGiCap(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{BillingEnabled: false}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	appName := "app-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, appName)
	epParams := append(params(projectID, envID), gin.Param{Key: "appName", Value: appName})

	c, rec := newCreateCtx(t, `{"path":"/data","size":"11Gi"}`, epParams, claims)
	h.UpdateAppStorage(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("11Gi with billing disabled: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "quota_exceeded" || body["resource"] != "storage_gb" {
		t.Fatalf("body = %v, want quota_exceeded/storage_gb", body)
	}
	if limit, _ := body["limit"].(float64); limit != 10 {
		t.Fatalf("limit = %v, want 10", body["limit"])
	}

	c2, rec2 := newCreateCtx(t, `{"path":"/data","size":"10Gi"}`, epParams, claims)
	h.UpdateAppStorage(c2)
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("10Gi with billing disabled: status = %d, want 202; body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestCreateApp_FreePlan_StorageCapEnforced(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{BillingEnabled: true}, billingPlans: testPlans()}
	orgID := "org-storagecap-" + uuid.NewString()[:8]
	seedFreePlanOrg(t, pool, orgID)
	projectID, envID := seedStorageCapFixture(t, pool, orgID)
	claims := godClaims(seedUser(t, pool))

	tooBig := "app-" + uuid.NewString()[:8]
	c, rec := newCreateCtx(t,
		`{"name":"`+tooBig+`","image":"nginx:latest","port":8080,"volume":{"path":"/data","size":"3Gi"}}`,
		params(projectID, envID), claims)
	h.CreateApp(c)
	assertQuotaExceeded(t, rec, rec.Body.Bytes(), rec.Code, 2)

	ok := "app-" + uuid.NewString()[:8]
	c2, rec2 := newCreateCtx(t,
		`{"name":"`+ok+`","image":"nginx:latest","port":8080,"volume":{"path":"/data","size":"2Gi"}}`,
		params(projectID, envID), claims)
	h.CreateApp(c2)
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("2Gi on free plan: status = %d, want 202; body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestUpdateAppStorage_FreePlan_ExistingVolumeStaysOperable(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{BillingEnabled: true}, billingPlans: testPlans()}
	orgID := "org-storagecap-" + uuid.NewString()[:8]
	seedFreePlanOrg(t, pool, orgID)
	projectID, envID := seedStorageCapFixture(t, pool, orgID)
	claims := godClaims(seedUser(t, pool))
	appName := "app-" + uuid.NewString()[:8]

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		VALUES ($1, $2, 'App', $3, 'Ready', $4)
	`, projectID, envID, appName, `{"volume":{"path":"/data","size":"12Gi","storage_class":"longhorn-dev"}}`); err != nil {
		t.Fatalf("seed app with existing 12Gi volume: %v", err)
	}
	epParams := append(params(projectID, envID), gin.Param{Key: "appName", Value: appName})

	c, rec := newCreateCtx(t, `{"path":"/data","size":"12Gi","storage_class":"longhorn-dev"}`, epParams, claims)
	h.UpdateAppStorage(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("same-size 12Gi re-apply on free plan (existing over cap): status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	c2, rec2 := newCreateCtx(t, `{"path":"/data","size":"13Gi","storage_class":"longhorn-dev"}`, epParams, claims)
	h.UpdateAppStorage(c2)
	assertQuotaExceeded(t, rec2, rec2.Body.Bytes(), rec2.Code, 2)
}

func TestCreateApp_ExemptOrg_Unlimited(t *testing.T) {
	pool := testOptimisticPool(t)
	orgID := "org-storagecap-" + uuid.NewString()[:8]
	h := &Handler{pool: pool, cfg: &config.Config{BillingEnabled: true, BillingExemptOrgs: []string{orgID}}, billingPlans: testPlans()}
	projectID, envID := seedStorageCapFixture(t, pool, orgID)
	claims := godClaims(seedUser(t, pool))

	name := "app-" + uuid.NewString()[:8]
	c, rec := newCreateCtx(t,
		`{"name":"`+name+`","image":"nginx:latest","port":8080,"volume":{"path":"/data","size":"50Gi"}}`,
		params(projectID, envID), claims)
	h.CreateApp(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("50Gi for an exempt org: status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidateAppVolume_AbsoluteCeiling(t *testing.T) {
	if _, err := validateAppVolume(&appVolumeReq{Path: "/data", Size: "200Gi"}); err == nil {
		t.Fatal("200Gi passed validateAppVolume; the 100Gi absolute ceiling should reject it regardless of plan")
	}
	v, err := validateAppVolume(&appVolumeReq{Path: "/data", Size: "100Gi"})
	if err != nil {
		t.Fatalf("100Gi at the absolute ceiling was rejected: %v", err)
	}
	if v.Size != "100Gi" {
		t.Fatalf("Size = %q, want 100Gi", v.Size)
	}
}
