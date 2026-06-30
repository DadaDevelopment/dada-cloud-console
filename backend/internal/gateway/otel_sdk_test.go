package gateway

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/telemetry"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// TestRealOTelSDKPush is the ADR-012 validation target: a stock OpenTelemetry
// SDK (the official Go SDK + OTLP/HTTP exporter) pushes metrics at the gateway
// with only an endpoint + dmon_ key header, and they land in Prometheus
// remote-write with authoritative tenant labels — no DADA client, no appId in
// the path. This is the in-repo equivalent of the Node.js IoT device scenario.
func TestRealOTelSDKPush(t *testing.T) {
	org, proj := uuid.New(), uuid.New()
	key, row := mintKey(t, org, proj, "prod", "iot-fleet", []string{"metrics:write"})
	store := fakeKeyStore{rows: map[string][]keyRow{telemetry.KeyLookupPrefix(key): {row}}}

	cap := &capturedSeries{}
	gw := httptest.NewServer(NewServer(store, nil, newPromStub(t, cap), nil, Config{}).Handler())
	t.Cleanup(gw.Close)

	ctx := context.Background()

	// Stock OTLP/HTTP exporter — endpoint + key header is the entire device config.
	exp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(gw.URL+"/v1/metrics"),
		otlpmetrichttp.WithInsecure(),
		otlpmetrichttp.WithHeaders(map[string]string{"X-API-Key": key}),
	)
	if err != nil {
		t.Fatalf("exporter: %v", err)
	}

	res, _ := resource.New(ctx, resource.WithAttributes(semconv.ServiceName("device-001")))
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
	)
	t.Cleanup(func() { _ = mp.Shutdown(ctx) })

	meter := mp.Meter("iot")
	temp, err := meter.Float64Gauge("temperature_celsius")
	if err != nil {
		t.Fatalf("gauge: %v", err)
	}
	temp.Record(ctx, 21.5, otelmetric.WithAttributes(attribute.String("sensor", "a")))

	// Flush the SDK -> real OTLP/HTTP POST -> gateway -> prom remote-write.
	if err := mp.ForceFlush(ctx); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	// Allow the stub server to record (ForceFlush is synchronous, but be lenient).
	deadline := time.Now().Add(2 * time.Second)
	var got []map[string]string
	for time.Now().Before(deadline) {
		got = cap.all()
		if len(got) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) == 0 {
		t.Fatal("no series received from real OTel SDK push")
	}

	var found map[string]string
	for _, s := range got {
		if s["__name__"] == "temperature_celsius" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatalf("temperature_celsius not found; got: %+v", got)
	}
	if found["org_id"] != org.String() || found["project_id"] != proj.String() {
		t.Errorf("tenancy not authoritative: org=%s proj=%s", found["org_id"], found["project_id"])
	}
	if found["monitoring_app"] != "iot-fleet" {
		t.Errorf("monitoring_app = %q, want iot-fleet", found["monitoring_app"])
	}
	if found["source"] != "device-001" {
		t.Errorf("source = %q, want device-001 (service.name)", found["source"])
	}
	if found["sensor"] != "a" {
		t.Errorf("sensor attr label = %q, want a", found["sensor"])
	}
}
