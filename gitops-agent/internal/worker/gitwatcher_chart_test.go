package worker

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mimirBucketTemplate is the real divergent manifest that the resources.values.yaml
// scanner misses: a helm-templated S3Bucket under apps/mimir/chart/templates.
const mimirBucketTemplate = `apiVersion: platform.dada-tuda.ru/v1alpha1
kind: S3Bucket
metadata:
  name: dada-mimir-blocks
spec:
  bucketName: {{ .Values.s3.bucketDisplayName | quote }}
  region: {{ .Values.s3.region | quote }}
  public: false
  # Land the connection secret next to Mimir (ns monitoring).
  connectionSecret:
    name: {{ .Values.s3.credentialsSecret | quote }}
    namespace: {{ .Release.Namespace | quote }}
`

func decodeSanitized(t *testing.T, content string) resourceManifest {
	t.Helper()
	var m resourceManifest
	if err := yaml.NewDecoder(strings.NewReader(sanitizeHelmTemplate(content))).Decode(&m); err != nil {
		t.Fatalf("decode sanitized template: %v", err)
	}
	return m
}

func TestSanitizeHelmTemplateIndexesRealChartBucket(t *testing.T) {
	m := decodeSanitized(t, mimirBucketTemplate)
	if m.Kind != "S3Bucket" {
		t.Errorf("kind = %q, want S3Bucket", m.Kind)
	}
	if m.Metadata.Name != "dada-mimir-blocks" {
		t.Errorf("name = %q, want dada-mimir-blocks", m.Metadata.Name)
	}
	if !chartCRKinds[m.Kind] {
		t.Errorf("kind %q not in chartCRKinds allowlist", m.Kind)
	}
	if m.Metadata.Name == "" || strings.Contains(m.Metadata.Name, helmActionPlaceholder) {
		t.Errorf("name %q would be skipped as unindexable", m.Metadata.Name)
	}
}

func TestSanitizeHelmTemplateDropsControlFlow(t *testing.T) {
	tmpl := `{{- if .Values.enabled }}
apiVersion: platform.dada-tuda.ru/v1alpha1
kind: PublicApi
metadata:
  name: {{ .Values.name }}
spec:
  host: {{ .Values.host }}
{{- end }}`
	m := decodeSanitized(t, tmpl)
	if m.Kind != "PublicApi" {
		t.Errorf("kind = %q, want PublicApi", m.Kind)
	}
	if !strings.Contains(m.Metadata.Name, helmActionPlaceholder) {
		t.Errorf("templated name should carry the placeholder so it is skipped, got %q", m.Metadata.Name)
	}
}

func TestChartCRKindsExcludesRawWorkloads(t *testing.T) {
	for _, k := range []string{"Deployment", "StatefulSet", "ConfigMap", "Secret", "Service"} {
		if chartCRKinds[k] {
			t.Errorf("kind %q should not be indexed from chart templates", k)
		}
	}
}
