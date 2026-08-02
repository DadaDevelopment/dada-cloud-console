package api

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// identityDeliveryInterval is the reconcile period for identity delivery. The
// loop is convergent, not event-driven: an app created between two ticks, an
// app whose namespace was recreated, and an app moved to another project all
// reach the same steady state on the next tick, so no caller has to remember
// to deliver.
const identityDeliveryInterval = 10 * time.Minute

// identitySecretSuffix names the per-app Secret holding the app's identity
// token, mirroring "<database>-db-credentials" so an app's platform-issued
// credentials all look alike in the namespace.
const identitySecretSuffix = "-identity-credentials"

// identityTokenSecretKey is the key inside that Secret, and so the env var the
// app reads. One name across every app is what lets a runtime library pick the
// credential up with no per-app configuration.
const identityTokenSecretKey = "DADA_SERVICE_TOKEN"

// identityAIBaseURLSecretKey ships the AI gateway base URL next to the token,
// so an app needs nothing but this Secret to make its first authenticated
// call.
const identityAIBaseURLSecretKey = "DADA_AI_BASE_URL"

// identitySecretAppLabel carries the identity id on every delivered Secret, so
// the reconciler can find copies left in a namespace the app has since moved
// out of without having to guess names.
const identitySecretIdentityLabel = "dada.io/identity"

// identitySecretManagedLabel marks a Secret as written by this loop. Only
// labelled Secrets are ever overwritten or deleted, so a hand-made Secret that
// happens to share the name is left alone.
const identitySecretManagedLabel = "dada.io/managed-by"

// identitySecretManagedValue is the value of identitySecretManagedLabel.
const identitySecretManagedValue = "dada-cloud-console"

// identityDeliveryWatcher gives every Kubernetes app a service identity and
// keeps a live token for it in the app's own namespace (ADR-021 phase 2).
//
// The token is minted at delivery time and written straight to the cluster: it
// never lands in git and is never stored in plaintext by the console, which is
// what separates this from the pasted key it replaces. The Secret is the only
// copy, so "the app has no live token" and "the Secret is missing or stale"
// are the same condition, and re-delivery is the only repair.
type identityDeliveryWatcher struct {
	clientset kubernetes.Interface
	h         *Handler
}

// StartIdentityDeliveryWatcher launches the delivery loop. No-op off-cluster,
// so local dev and tests never spawn it.
func (h *Handler) StartIdentityDeliveryWatcher(ctx context.Context) {
	clientset := newAppHealthClientset()
	if clientset == nil {
		log.Printf("identity-delivery: no in-cluster client, delivery disabled")
		return
	}
	w := &identityDeliveryWatcher{clientset: clientset, h: h}
	log.Printf("identity-delivery: started interval=%s secret=<app>%s key=%s",
		identityDeliveryInterval, identitySecretSuffix, identityTokenSecretKey)
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyIdentityDelivery, "identity-delivery", w.tick)
		t := time.NewTicker(identityDeliveryInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyIdentityDelivery, "identity-delivery", w.tick)
			}
		}
	}()
}

// deliverableApp is one Kubernetes app that should own an identity, with the
// namespace its token belongs in.
type deliverableApp struct {
	Name      string
	ProjectID uuid.UUID
	EnvID     uuid.UUID
	Namespace string
}

// identityDeliveryTargetsSQL lists every app on a Kubernetes environment that
// has a namespace to deliver into. VM/box environments are excluded: their
// workloads are Compose stacks with no Secret to mount.
const identityDeliveryTargetsSQL = `
	SELECT rs.name, rs.project_id, rs.environment_id, e.namespace
	  FROM resource_snapshots rs
	  JOIN environments e ON e.id = rs.environment_id
	 WHERE rs.kind = 'App'
	   AND e.runtime = 'k8s'
	   AND coalesce(e.namespace, '') <> ''
	 ORDER BY rs.project_id, rs.name`

// tick reconciles every app once. One failing app never blocks the rest: each
// is independent, and the next tick retries whatever did not converge.
func (w *identityDeliveryWatcher) tick(ctx context.Context) {
	rows, err := w.h.pool.Query(ctx, identityDeliveryTargetsSQL)
	if err != nil {
		log.Printf("identity-delivery: list apps: %v", err)
		return
	}
	var targets []deliverableApp
	for rows.Next() {
		var t deliverableApp
		if err := rows.Scan(&t.Name, &t.ProjectID, &t.EnvID, &t.Namespace); err != nil {
			log.Printf("identity-delivery: scan app: %v", err)
			continue
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("identity-delivery: iterate apps: %v", err)
		return
	}

	var delivered, pruned int
	for _, t := range targets {
		identityID, err := w.ensureIdentity(ctx, t)
		if err != nil {
			log.Printf("identity-delivery: %s/%s ensure identity: %v", t.Namespace, t.Name, err)
			continue
		}
		did, err := w.ensureSecret(ctx, t, identityID)
		if err != nil {
			log.Printf("identity-delivery: %s/%s deliver: %v", t.Namespace, t.Name, err)
			continue
		}
		if did {
			delivered++
		}
		n, err := w.pruneStaleSecrets(ctx, t, identityID)
		if err != nil {
			log.Printf("identity-delivery: %s/%s prune: %v", t.Namespace, t.Name, err)
			continue
		}
		pruned += n
	}
	log.Printf("identity-delivery: tick apps=%d delivered=%d pruned=%d", len(targets), delivered, pruned)
}

// ensureIdentity returns the app's live identity id, declaring one on first
// sight. The row is the principal: it is created once and then only ever
// re-pointed, so an app that moves keeps the identity it was born with.
func (w *identityDeliveryWatcher) ensureIdentity(ctx context.Context, t deliverableApp) (uuid.UUID, error) {
	var id uuid.UUID
	err := w.h.pool.QueryRow(ctx,
		`SELECT id FROM service_identities
		  WHERE app_name = $1 AND environment_id = $2 AND revoked_at IS NULL`,
		t.Name, t.EnvID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, err
	}
	if err := w.h.pool.QueryRow(ctx,
		`INSERT INTO service_identities (app_name, project_id, environment_id, display_name, scopes)
		 VALUES ($1, $2, $3, $1, $4)
		 RETURNING id`,
		t.Name, t.ProjectID, t.EnvID, identityDefaultScopes,
	).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// ensureSecret guarantees the app's namespace holds a Secret whose token is
// live for this identity, and reports whether it had to (re-)deliver.
//
// A Secret carrying a token that no longer resolves is treated exactly like a
// missing one. That is the whole failure mode this loop exists to close: a
// revoked or rotated token leaves the app holding a credential that 401s, and
// nothing else in the system would notice.
//
// Minting is the last resort and only happens when the identity holds no live
// token at all. An identity whose token exists but is invisible to this loop
// is the pre-delivery world -- reels-tracker's plaintext still sits as a
// literal in argo-infra -- and minting there would revoke the credential a
// running app is using this second. That app is repaired by delivering to it,
// not by silently rotating it out from under itself.
//
// The payload goes in Data, not StringData: StringData is a write-only
// convenience the API server folds into Data, so a Secret written that way
// reads back with an empty Data on the very next tick and the loop would
// re-mint forever.
func (w *identityDeliveryWatcher) ensureSecret(ctx context.Context, t deliverableApp, identityID uuid.UUID) (bool, error) {
	name := t.Name + identitySecretSuffix
	existing, err := w.clientset.CoreV1().Secrets(t.Namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		if !w.managed(existing) {
			return false, nil
		}
		live, err := w.tokenIsLive(ctx, identityID, string(existing.Data[identityTokenSecretKey]))
		if err != nil {
			return false, err
		}
		if live {
			return false, nil
		}
	case apierrors.IsNotFound(err):
	default:
		return false, err
	}

	plaintext, err := w.adoptToken(ctx, identityID)
	if err != nil {
		return false, err
	}
	if plaintext == "" {
		held, err := w.liveTokenCount(ctx, identityID)
		if err != nil {
			return false, err
		}
		if held > 0 {
			log.Printf("identity-delivery: %s/%s holds %d live token(s) this loop cannot see; not minting",
				t.Namespace, t.Name, held)
			return false, nil
		}
		if plaintext, err = w.mintToken(ctx, identityID); err != nil {
			return false, err
		}
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: t.Namespace,
			Labels: map[string]string{
				identitySecretManagedLabel:  identitySecretManagedValue,
				identitySecretIdentityLabel: identityID.String(),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			identityTokenSecretKey:     []byte(plaintext),
			identityAIBaseURLSecretKey: []byte(w.h.cfg.AIGatewayPublicURL),
		},
	}
	if _, err := w.clientset.CoreV1().Secrets(t.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return false, err
		}
		if _, err := w.clientset.CoreV1().Secrets(t.Namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// managed reports whether this loop owns a Secret. An unlabelled Secret under
// the same name was put there by someone else and is never overwritten.
func (w *identityDeliveryWatcher) managed(s *corev1.Secret) bool {
	return s.Labels[identitySecretManagedLabel] == identitySecretManagedValue
}

// tokenIsLive reports whether a plaintext token is still a live token of this
// identity. An empty token, a revoked one, or one belonging to a different
// identity all answer false.
func (w *identityDeliveryWatcher) tokenIsLive(ctx context.Context, identityID uuid.UUID, plaintext string) (bool, error) {
	if plaintext == "" {
		return false, nil
	}
	var n int
	if err := w.h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM service_identity_tokens
		  WHERE identity_id = $1 AND token_hash = $2 AND revoked_at IS NULL`,
		identityID, hashIdentityToken(plaintext),
	).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// adoptToken recovers this identity's existing plaintext from a Secret this
// loop wrote in some other namespace, or returns "" when there is nothing to
// adopt.
//
// This is what makes a move re-point rather than rotate. The console keeps
// only the hash, so the cluster holds the sole copy of the plaintext: after an
// app changes project its destination namespace is empty while the old
// namespace still carries a perfectly live credential. Minting there would
// invalidate the token the running pod is currently using, which is the exact
// breakage ADR-021 exists to end -- so the Secret moves with the app, and
// pruneStaleSecrets removes the copy left behind.
func (w *identityDeliveryWatcher) adoptToken(ctx context.Context, identityID uuid.UUID) (string, error) {
	list, err := w.clientset.CoreV1().Secrets("").List(ctx, metav1.ListOptions{
		LabelSelector: identitySecretIdentityLabel + "=" + identityID.String(),
	})
	if err != nil {
		return "", err
	}
	for i := range list.Items {
		s := &list.Items[i]
		if !w.managed(s) {
			continue
		}
		plaintext := string(s.Data[identityTokenSecretKey])
		live, err := w.tokenIsLive(ctx, identityID, plaintext)
		if err != nil {
			return "", err
		}
		if live {
			return plaintext, nil
		}
	}
	return "", nil
}

// liveTokenCount reports how many unrevoked tokens the identity holds. It is
// the guard on minting: a non-zero count with nothing to adopt means a live
// credential exists somewhere this loop cannot read.
func (w *identityDeliveryWatcher) liveTokenCount(ctx context.Context, identityID uuid.UUID) (int, error) {
	var n int
	err := w.h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM service_identity_tokens
		  WHERE identity_id = $1 AND revoked_at IS NULL`,
		identityID,
	).Scan(&n)
	return n, err
}

// mintToken revokes the identity's previous tokens and returns a new
// plaintext, in one transaction so an identity never has two live tokens.
func (w *identityDeliveryWatcher) mintToken(ctx context.Context, identityID uuid.UUID) (string, error) {
	plaintext, hash, prefix, err := generateIdentityToken()
	if err != nil {
		return "", err
	}
	tx, err := w.h.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE service_identity_tokens SET revoked_at = now()
		  WHERE identity_id = $1 AND revoked_at IS NULL`,
		identityID,
	); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO service_identity_tokens (identity_id, token_hash, token_prefix)
		 VALUES ($1, $2, $3)`,
		identityID, hash, prefix,
	); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return plaintext, nil
}

// pruneStaleSecrets deletes copies of this identity's Secret left in any
// namespace the app no longer lives in. Without it a moved app leaves a live
// credential behind in the old project's namespace, readable by whoever still
// has access there.
//
// Deletion is gated three ways -- the identity label, this loop's managed-by
// label, and the name suffix -- because the RBAC behind it is necessarily
// cluster-wide on Secrets, so a selector bug here would be a cluster-wide
// delete rather than a local one.
func (w *identityDeliveryWatcher) pruneStaleSecrets(ctx context.Context, t deliverableApp, identityID uuid.UUID) (int, error) {
	list, err := w.clientset.CoreV1().Secrets("").List(ctx, metav1.ListOptions{
		LabelSelector: identitySecretIdentityLabel + "=" + identityID.String(),
	})
	if err != nil {
		return 0, err
	}
	pruned := 0
	for i := range list.Items {
		s := &list.Items[i]
		if s.Namespace == t.Namespace || !w.managed(s) || !strings.HasSuffix(s.Name, identitySecretSuffix) {
			continue
		}
		if err := w.clientset.CoreV1().Secrets(s.Namespace).Delete(ctx, s.Name, metav1.DeleteOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return pruned, err
		}
		pruned++
	}
	return pruned, nil
}
