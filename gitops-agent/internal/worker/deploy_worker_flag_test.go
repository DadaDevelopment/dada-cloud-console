package worker

import (
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// TestDeployPortAndWorker pins the redeploy-time fix: a worker app must never
// have its old, pre-worker port silently re-substituted into the rendered
// AppSpec, since that would re-enable the Service
// (renderer.RenderAppValues: Common.Service.Enabled = spec.Port > 0) on every
// redeploy and keep a background app's phantom route alive underneath it.
func TestDeployPortAndWorker(t *testing.T) {
	cases := []struct {
		name       string
		cur        map[string]any
		wantPort   float64
		wantWorker bool
	}{
		{
			name:       "ordinary http app keeps its port",
			cur:        map[string]any{"port": float64(8080)},
			wantPort:   8080,
			wantWorker: false,
		},
		{
			name:       "worker app forces port to zero even if the snapshot still carries an old non-zero port",
			cur:        map[string]any{"port": float64(8080), "worker": true},
			wantPort:   0,
			wantWorker: true,
		},
		{
			name:       "worker app already at port zero stays zero",
			cur:        map[string]any{"port": float64(0), "worker": true},
			wantPort:   0,
			wantWorker: true,
		},
		{
			name:       "worker false leaves an ordinary app's port untouched",
			cur:        map[string]any{"port": float64(3000), "worker": false},
			wantPort:   3000,
			wantWorker: false,
		},
		{
			name:       "missing worker key defaults to non-worker",
			cur:        map[string]any{"port": float64(4000)},
			wantPort:   4000,
			wantWorker: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPort, gotWorker := deployPortAndWorker(c.cur)
			if gotPort != c.wantPort || gotWorker != c.wantWorker {
				t.Errorf("deployPortAndWorker(%v) = (%v, %v), want (%v, %v)",
					c.cur, gotPort, gotWorker, c.wantPort, c.wantWorker)
			}
		})
	}
}

// TestDetachManagedDomainKeys_RemovesBothIngressAndPublicApi pins the second
// half of the retrofit-to-worker fix: doAttachDefaultDomain renders BOTH an
// Ingress and a PublicApi (the crossplane DNS route) under the same
// FQDNToName(hostname), but the old detach path only ever removed the
// Ingress entry. Detaching a managed domain that way left the PublicApi CR
// -- the thing that actually publishes the DNS route -- in git forever, so
// the "unstick a phantom-port worker app" flow this ships for would not have
// actually removed the dead route. Exercises the exact key list
// doDetachCustomHostname now passes to removeManifestsFile.
func TestDetachManagedDomainKeys_RemovesBothIngressAndPublicApi(t *testing.T) {
	hostname := "bot-abcd.apps.dada-tuda.ru"
	name := renderer.FQDNToName(hostname)

	ingressYAML, err := renderer.RenderCustomIngress(renderer.CustomIngressSpec{
		Name:            name,
		Namespace:       "ns",
		ProjectSlug:     "proj",
		EnvSlug:         "prod",
		Hostname:        hostname,
		ServiceName:     "bot",
		ServicePortName: renderer.DefaultAppServicePortName,
		OperationID:     "op-1",
		Managed:         true,
	})
	if err != nil {
		t.Fatalf("RenderCustomIngress: %v", err)
	}
	dnsYAML, err := renderer.RenderDefaultDomainDNS(renderer.DefaultDomainDNSSpec{
		Name:        name,
		ProjectSlug: "proj",
		EnvSlug:     "prod",
		Hostname:    hostname,
		ServiceName: "bot",
		ServicePort: 8080,
		OperationID: "op-1",
	})
	if err != nil {
		t.Fatalf("RenderDefaultDomainDNS: %v", err)
	}

	rv, err := renderer.ParseResourcesValues("")
	if err != nil {
		t.Fatalf("ParseResourcesValues: %v", err)
	}
	if err := rv.Upsert(ingressYAML); err != nil {
		t.Fatalf("Upsert ingress: %v", err)
	}
	if err := rv.Upsert(dnsYAML); err != nil {
		t.Fatalf("Upsert dns: %v", err)
	}
	if len(rv.Manifests) != 2 {
		t.Fatalf("want 2 manifests after attach, got %d", len(rv.Manifests))
	}

	keys := [][2]string{
		{"Ingress", name},
		{"PublicApi", name},
	}
	changed := false
	for _, k := range keys {
		if rv.Remove(k[0], k[1]) {
			changed = true
		}
	}
	if !changed {
		t.Fatal("expected at least one entry removed")
	}
	if len(rv.Manifests) != 0 {
		t.Fatalf("detach must clear both the Ingress and the PublicApi DNS route, got %d manifests left: %+v",
			len(rv.Manifests), rv.Manifests)
	}
}
