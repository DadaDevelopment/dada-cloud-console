package api

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/dada-tuda/console/backend/internal/config"
)

// TestIdentityDeliveryLockKeyIsDistinct guards the one invariant a new
// background loop can silently break: two loops sharing an advisory lock key
// means whichever pod grabs it first mutes the other forever.
func TestIdentityDeliveryLockKeyIsDistinct(t *testing.T) {
	keys := map[int64]string{
		lockKeyDomainReconcile:   "domain-reconcile",
		lockKeyBackupReconcile:   "backup-reconcile",
		lockKeyAppHealthWatch:    "app-health",
		lockKeyAppVolumeWatch:    "app-volume",
		lockKeyCostWarmFast:      "cost-warm-fast",
		lockKeyCostWarmSlow:      "cost-warm-slow",
		lockKeyAppAutoscaleWatch: "app-autoscale",
	}
	if name, dup := keys[lockKeyIdentityDelivery]; dup {
		t.Fatalf("lockKeyIdentityDelivery 0x%x already taken by %s", lockKeyIdentityDelivery, name)
	}
}

// TestIdentitySecretKeysAreStable pins the names an app compiles against. A
// rename here silently stops every already-deployed app from finding its
// credential, and nothing in the console would report an error.
func TestIdentitySecretKeysAreStable(t *testing.T) {
	if identitySecretSuffix != "-identity-credentials" {
		t.Fatalf("identitySecretSuffix=%q changed; app namespaces already hold the old name", identitySecretSuffix)
	}
	if identityTokenSecretKey != "DADA_SERVICE_TOKEN" {
		t.Fatalf("identityTokenSecretKey=%q changed; deployed apps read the old key", identityTokenSecretKey)
	}
}

func newDeliveryWatcher(t *testing.T, pool *pgxpool.Pool, objs ...k8sruntime.Object) (*identityDeliveryWatcher, kubernetes.Interface) {
	t.Helper()
	cs := k8sfake.NewSimpleClientset(objs...)
	h := &Handler{pool: pool, cfg: &config.Config{AIGatewayPublicURL: "https://ai.example.test/v1"}}
	return &identityDeliveryWatcher{clientset: cs, h: h}, cs
}

// seedDeliveryApp creates a project, a Kubernetes environment and an App
// snapshot on it, i.e. exactly what the reconciler's target query looks for.
func seedDeliveryApp(t *testing.T, pool *pgxpool.Pool, namespace string) (appName string, projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	appName = "delivery-" + suffix

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"delivery-test-"+suffix, "org-delivery-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, 'prod', $2, 'prod', 'k8s') RETURNING id`,
		projectID, namespace,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
		 VALUES ($1, $2, 'App', $3, 'Ready')`,
		projectID, envID, appName,
	); err != nil {
		t.Fatalf("seed app snapshot: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM service_identity_tokens WHERE identity_id IN
			(SELECT id FROM service_identities WHERE environment_id = $1)`, envID)
		_, _ = pool.Exec(ctx, `DELETE FROM service_identities WHERE environment_id = $1`, envID)
		_, _ = pool.Exec(ctx, `DELETE FROM resource_snapshots WHERE environment_id = $1`, envID)
		_, _ = pool.Exec(ctx, `DELETE FROM environments WHERE id = $1`, envID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
	})
	return appName, projectID, envID
}

func deliveredToken(t *testing.T, cs kubernetes.Interface, namespace, appName string) string {
	t.Helper()
	s, err := cs.CoreV1().Secrets(namespace).Get(context.Background(), appName+identitySecretSuffix, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get delivered secret in %s: %v", namespace, err)
	}
	return string(s.Data[identityTokenSecretKey])
}

// TestIdentityDelivery_MintsAndIsIdempotent is the core claim of phase 2: an
// app that never had a credential ends the tick holding a live one, and a
// second tick leaves it alone. A loop that re-minted every tick would rotate
// the token out from under a running pod every ten minutes.
func TestIdentityDelivery_MintsAndIsIdempotent(t *testing.T) {
	pool := testPaymentsPool(t)
	ns := "delivery-ns-" + uuid.NewString()[:8]
	appName, _, envID := seedDeliveryApp(t, pool, ns)
	w, cs := newDeliveryWatcher(t, pool)
	ctx := context.Background()

	w.tick(ctx)

	token := deliveredToken(t, cs, ns, appName)
	if !strings.HasPrefix(token, identityTokenPrefix) {
		t.Fatalf("delivered token %q does not carry the routable prefix %q", token, identityTokenPrefix)
	}

	var identityID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM service_identities WHERE app_name = $1 AND environment_id = $2 AND revoked_at IS NULL`,
		appName, envID).Scan(&identityID); err != nil {
		t.Fatalf("identity was not declared for the app: %v", err)
	}
	live, err := w.tokenIsLive(ctx, identityID, token)
	if err != nil {
		t.Fatalf("tokenIsLive: %v", err)
	}
	if !live {
		t.Fatal("delivered token does not resolve to a live row: the app holds a credential that 401s")
	}

	w.tick(ctx)
	if again := deliveredToken(t, cs, ns, appName); again != token {
		t.Fatal("second tick rotated the token; a converged app must be left untouched")
	}

	var liveTokens int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM service_identity_tokens WHERE identity_id = $1 AND revoked_at IS NULL`,
		identityID).Scan(&liveTokens); err != nil {
		t.Fatalf("count live tokens: %v", err)
	}
	if liveTokens != 1 {
		t.Fatalf("live tokens=%d want 1", liveTokens)
	}
}

// TestIdentityDelivery_RemintsRevokedToken covers the failure the loop exists
// to repair: the token in the namespace stopped resolving, so the app is
// holding a dead credential and only re-delivery fixes it.
func TestIdentityDelivery_RemintsRevokedToken(t *testing.T) {
	pool := testPaymentsPool(t)
	ns := "delivery-ns-" + uuid.NewString()[:8]
	appName, _, envID := seedDeliveryApp(t, pool, ns)
	w, cs := newDeliveryWatcher(t, pool)
	ctx := context.Background()

	w.tick(ctx)
	first := deliveredToken(t, cs, ns, appName)

	var identityID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM service_identities WHERE app_name = $1 AND environment_id = $2 AND revoked_at IS NULL`,
		appName, envID).Scan(&identityID); err != nil {
		t.Fatalf("select identity: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE service_identity_tokens SET revoked_at = now() WHERE identity_id = $1`, identityID); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	w.tick(ctx)

	second := deliveredToken(t, cs, ns, appName)
	if second == first {
		t.Fatal("revoked token was left in the namespace; the app keeps 401ing")
	}
	live, err := w.tokenIsLive(ctx, identityID, second)
	if err != nil {
		t.Fatalf("tokenIsLive: %v", err)
	}
	if !live {
		t.Fatal("re-delivered token is not live")
	}
}

// TestIdentityDelivery_LeavesUnmanagedSecretAlone keeps the loop from being a
// cluster-wide secret shredder: a Secret it did not write is never overwritten,
// whatever its name.
func TestIdentityDelivery_LeavesUnmanagedSecretAlone(t *testing.T) {
	pool := testPaymentsPool(t)
	ns := "delivery-ns-" + uuid.NewString()[:8]
	appName, _, _ := seedDeliveryApp(t, pool, ns)

	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: appName + identitySecretSuffix, Namespace: ns},
		Data:       map[string][]byte{identityTokenSecretKey: []byte("hand-written-value")},
	}
	w, cs := newDeliveryWatcher(t, pool, foreign)

	w.tick(context.Background())

	if got := deliveredToken(t, cs, ns, appName); got != "hand-written-value" {
		t.Fatalf("unmanaged secret was overwritten (value=%q)", got)
	}
}

// TestIdentityDelivery_MoveKeepsTokenAndClearsOldNamespace is the regression
// test for 2026-08-02: an app that changes project keeps the exact credential
// it had, and no live copy is left readable in the namespace it left.
func TestIdentityDelivery_MoveKeepsTokenAndClearsOldNamespace(t *testing.T) {
	pool := testPaymentsPool(t)
	srcNS := "delivery-src-" + uuid.NewString()[:8]
	appName, _, envID := seedDeliveryApp(t, pool, srcNS)
	w, cs := newDeliveryWatcher(t, pool)
	ctx := context.Background()

	w.tick(ctx)
	before := deliveredToken(t, cs, srcNS, appName)

	dstNS := "delivery-dst-" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `UPDATE environments SET namespace = $1 WHERE id = $2`, dstNS, envID); err != nil {
		t.Fatalf("simulate move: %v", err)
	}

	w.tick(ctx)

	if after := deliveredToken(t, cs, dstNS, appName); after != before {
		t.Fatal("move re-minted the credential; the identity row is the principal and a move must re-point, not rotate")
	}
	if _, err := cs.CoreV1().Secrets(srcNS).Get(ctx, appName+identitySecretSuffix, metav1.GetOptions{}); err == nil {
		t.Fatal("a live token was left behind in the source namespace")
	}
}
