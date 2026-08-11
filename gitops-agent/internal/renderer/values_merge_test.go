package renderer

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const mergeFixtureValues = `common:
    image:
        name: nexus.dada-tuda.ru/internal/dada-development-site@sha256
        tag: OLDTAG
    servicePort: 5173
    replicas: 1
    useDotEnv: "false"
    ingress:
        enabled: true
        host: development.dada-tuda.ru
    resources:
        requests:
            cpu: 10m
`

func mergeOrFail(t *testing.T, existing, rendered string) map[string]any {
	t.Helper()
	out, err := MergeAppValues(existing, rendered)
	if err != nil {
		t.Fatalf("MergeAppValues: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("merged output does not parse: %v\n%s", err, out)
	}
	return got
}

func common(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	c, ok := doc["common"].(map[string]any)
	if !ok {
		t.Fatalf("no common mapping in %#v", doc)
	}
	return c
}

func TestMergeAppValuesKeepsUnownedKeys(t *testing.T) {
	rendered := `common:
    image:
        name: nexus.dada-tuda.ru/internal/dada-development-site@sha256
        tag: NEWTAG
    replicas: 1
    useDotEnv: "false"
    resources:
        requests:
            cpu: 10m
`
	c := common(t, mergeOrFail(t, mergeFixtureValues, rendered))

	ingress, ok := c["ingress"].(map[string]any)
	if !ok {
		t.Fatalf("common.ingress was dropped by the merge: %#v", c)
	}
	if ingress["host"] != "development.dada-tuda.ru" {
		t.Errorf("ingress.host = %v, want development.dada-tuda.ru", ingress["host"])
	}

	img := c["image"].(map[string]any)
	if img["tag"] != "NEWTAG" {
		t.Errorf("image.tag = %v, want NEWTAG", img["tag"])
	}
}

func TestMergeAppValuesRemovesOwnedKeyTheRenderOmits(t *testing.T) {
	existing := `common:
    image:
        name: img
        tag: t
    pvc:
        size: 10Gi
        path: /data
    ingress:
        enabled: true
`
	rendered := `common:
    image:
        name: img
        tag: t2
`
	c := common(t, mergeOrFail(t, existing, rendered))

	if _, present := c["pvc"]; present {
		t.Errorf("common.pvc is owned by the render and was omitted, so it must be deleted: %#v", c)
	}
	if _, present := c["ingress"]; !present {
		t.Errorf("common.ingress is not owned by the render and must survive: %#v", c)
	}
}

func TestMergeAppValuesReplacesOwnedListWholesale(t *testing.T) {
	existing := `common:
    image:
        name: img
        tag: t
    extraEnv:
        - name: A
          value: "1"
        - name: B
          value: "2"
`
	rendered := `common:
    image:
        name: img
        tag: t
    extraEnv:
        - name: A
          value: "9"
`
	c := common(t, mergeOrFail(t, existing, rendered))

	env, ok := c["extraEnv"].([]any)
	if !ok {
		t.Fatalf("extraEnv missing: %#v", c)
	}
	if len(env) != 1 {
		t.Fatalf("extraEnv should mirror the render exactly (deleting B), got %#v", env)
	}
	if first := env[0].(map[string]any); first["value"] != "9" {
		t.Errorf("extraEnv[0].value = %v, want 9", first["value"])
	}
}

func TestMergeAppValuesEmptyExistingIsTheRender(t *testing.T) {
	rendered := "common:\n    replicas: 2\n"
	out, err := MergeAppValues("", rendered)
	if err != nil {
		t.Fatalf("MergeAppValues: %v", err)
	}
	if out != rendered {
		t.Errorf("out = %q, want the render verbatim", out)
	}
}

func TestMergeAppValuesRefusesUnparseableExisting(t *testing.T) {
	_, err := MergeAppValues("common:\n  image: [unterminated\n", "common:\n    replicas: 1\n")
	if err == nil {
		t.Fatal("a values.yaml that does not parse must be an error, not an overwrite")
	}
}

func TestMergeAppValuesCoversEveryRenderedCommonKey(t *testing.T) {
	rendered, err := RenderAppValues(AppSpec{
		Name:               "app",
		Image:              "nexus/app:1",
		Port:               8080,
		Replicas:           2,
		WorkloadType:       "worker",
		VolumePath:         "/data",
		VolumeSize:         "10Gi",
		VolumeStorageClass: "longhorn",
		VolumeFSGroup:      1000,
		Env:                map[string]string{"A": "1"},
		Resources: &AppResources{
			CPURequest: "10m", MemoryRequest: "128Mi",
			CPULimit: "250m", MemoryLimit: "256Mi",
		},
	})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered values do not parse: %v", err)
	}
	owned := map[string]bool{}
	for _, k := range ownedCommonKeys {
		owned[k] = true
	}
	var missing []string
	for k := range common(t, doc) {
		if !owned[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("RenderAppValues emits keys ownedCommonKeys does not list, so they could never be removed: %s",
			strings.Join(missing, ", "))
	}
}
