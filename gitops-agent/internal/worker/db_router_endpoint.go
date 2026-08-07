package worker

import (
	"context"
	"encoding/json"
	"net"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// dbDirectEndpointAnnotation records the instance address the connection
	// secret carried before it was pointed at the router. The direct address is
	// the only way back if the router is unavailable, and provider-sql will not
	// re-publish it: it writes connection details when the role is created and
	// when its password changes, never on demand.
	dbDirectEndpointAnnotation = "platform.dada-tuda.ru/db-direct-endpoint"

	// dbRouterOptOutAnnotation, set to "true" on a connection secret, keeps that
	// one database on its direct address regardless of configuration. It is the
	// per-database escape hatch for a workload that cannot live with transaction
	// pooling (session-scoped state: advisory locks, LISTEN/NOTIFY, temp tables).
	dbRouterOptOutAnnotation = "platform.dada-tuda.ru/db-router-opt-out"

	dbSecretEndpointKey = "endpoint"
	dbSecretPortKey     = "port"
)

// routerTarget is the address applications should reach a database through.
type routerTarget struct {
	Host string
	Port string
}

// routerTargetForShard decides whether a database on the given shard should be
// reached through the router, and at what address.
//
// Empty DBRouterHost means the feature is off. A shard listed in
// DBRouterDirectShards stays direct: the platform's own databases must not make
// signing in to the console depend on a second hop being up.
func routerTargetForShard(cfg *config.Config, shard string) (routerTarget, bool) {
	if cfg == nil || cfg.DBRouterHost == "" {
		return routerTarget{}, false
	}
	for _, direct := range cfg.DBRouterDirectShards {
		if direct == shard {
			return routerTarget{}, false
		}
	}
	port := cfg.DBRouterPort
	if port == "" {
		port = "5432"
	}
	return routerTarget{Host: cfg.DBRouterHost, Port: port}, true
}

// routerEndpointPatch is the change a connection secret needs, or Patch=false
// when it is already correct.
type routerEndpointPatch struct {
	Patch          bool
	Host           string
	Port           string
	DirectEndpoint string
}

// planRouterEndpointPatch compares one connection secret against the router
// target. It is deliberately total and side-effect free so every branch —
// opt-out, already-routed, first rewrite, re-rewrite after a password rotation
// re-published the shard address — is decided in one testable place.
func planRouterEndpointPatch(data map[string][]byte, annotations map[string]string, target routerTarget) routerEndpointPatch {
	if annotations[dbRouterOptOutAnnotation] == "true" {
		return routerEndpointPatch{}
	}
	curHost := string(data[dbSecretEndpointKey])
	curPort := string(data[dbSecretPortKey])
	if curHost == target.Host && curPort == target.Port {
		return routerEndpointPatch{}
	}
	plan := routerEndpointPatch{Patch: true, Host: target.Host, Port: target.Port}
	if annotations[dbDirectEndpointAnnotation] == "" && curHost != "" {
		if curPort == "" {
			curPort = "5432"
		}
		plan.DirectEndpoint = net.JoinHostPort(curHost, curPort)
	}
	return plan
}

// syncRouterEndpoint points one database's application-facing connection secret
// at the router. Missing secrets, unbound databases and every API error are
// non-fatal: a database whose DSN is left alone keeps working against its shard,
// while a database blocked from being created does not.
//
// The patch writes data keys rather than stringData so only endpoint and port
// are touched; username and password stay exactly as provider-sql published
// them. Applications read the secret at start-up, so a rewritten endpoint takes
// effect on the pod's next restart, not immediately.
func (r *StatusReconciler) syncRouterEndpoint(ctx context.Context, cr *unstructured.Unstructured) {
	target, ok := routerTargetForShard(r.cfg, crDatabaseShard(cr))
	if !ok {
		return
	}
	appRef, _, _ := unstructured.NestedString(cr.Object, "spec", "appRef")
	namespace, _, _ := unstructured.NestedString(cr.Object, "spec", "namespace")
	if appRef == "" || namespace == "" {
		return
	}
	name := appRef + "-db-credentials"
	secret, err := r.client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return
	}
	plan := planRouterEndpointPatch(secret.Data, secret.GetAnnotations(), target)
	if !plan.Patch {
		return
	}
	patch := map[string]any{
		"data": map[string][]byte{
			dbSecretEndpointKey: []byte(plan.Host),
			dbSecretPortKey:     []byte(plan.Port),
		},
	}
	if plan.DirectEndpoint != "" {
		patch["metadata"] = map[string]any{
			"annotations": map[string]string{dbDirectEndpointAnnotation: plan.DirectEndpoint},
		}
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return
	}
	if _, err := r.client.CoreV1().Secrets(namespace).Patch(ctx, name, types.StrategicMergePatchType, body, metav1.PatchOptions{}); err != nil {
		log.Warn().Err(err).Str("secret", namespace+"/"+name).Msg("status-reconciler: point db credentials at router")
		return
	}
	log.Info().
		Str("secret", namespace+"/"+name).
		Str("endpoint", plan.Host+":"+plan.Port).
		Str("direct", plan.DirectEndpoint).
		Msg("status-reconciler: db credentials pointed at router")
}
