package renderer

import (
	"reflect"
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

// TestMergeAppValuesCoversEveryRenderedCommonKey reads the ownership contract
// off the commonValues struct rather than off a rendered fixture.
//
// The fixture form of this test could not see a field it did not populate, and
// every field on commonValues is omitempty. startCommand shipped that way: the
// struct emitted it, ownedCommonKeys did not list it, and the merge therefore
// dropped it from every app that already had a values.yaml in git -- the lever
// answered 200 and changed nothing on the pod.
func TestMergeAppValuesCoversEveryRenderedCommonKey(t *testing.T) {
	owned := map[string]bool{}
	for _, k := range ownedCommonKeys {
		owned[k] = true
	}
	typ := reflect.TypeOf(commonValues{})
	var missing []string
	for i := 0; i < typ.NumField(); i++ {
		key, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		if !owned[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("commonValues emits keys ownedCommonKeys does not list, so a merge would never write or remove them: %s",
			strings.Join(missing, ", "))
	}
}

func TestMergeAppValuesWritesStartCommandIntoExistingFile(t *testing.T) {
	rendered, err := RenderAppValues(AppSpec{
		Name:         "app",
		Image:        "nexus/app:1",
		Replicas:     1,
		StartCommand: "python main.py --surname Ivanov",
	})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	merged, err := MergeAppValues(mergeFixtureValues, rendered)
	if err != nil {
		t.Fatalf("MergeAppValues: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(merged), &doc); err != nil {
		t.Fatalf("merged values do not parse: %v", err)
	}
	if got := common(t, doc)["startCommand"]; got != "python main.py --surname Ivanov" {
		t.Fatalf("startCommand = %v, want the rendered command; the merge dropped the key", got)
	}

	cleared, err := RenderAppValues(AppSpec{Name: "app", Image: "nexus/app:1", Replicas: 1})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	back, err := MergeAppValues(merged, cleared)
	if err != nil {
		t.Fatalf("MergeAppValues: %v", err)
	}
	if err := yaml.Unmarshal([]byte(back), &doc); err != nil {
		t.Fatalf("merged values do not parse: %v", err)
	}
	if _, ok := common(t, doc)["startCommand"]; ok {
		t.Fatal("clearing the start command must delete the key, not leave the old one in git")
	}
}

// assertExtraEnvDSNHasHostAlias re-checks, on a merged common mapping, the
// same invariant RenderAppValues guarantees on a fresh render: an extraEnv
// entry naming pgRouterHostAliasHostname is worthless unless hostAliases
// resolves that same name inside the pod. MergeAppValues treats extraEnv and
// hostAliases as two independently owned keys (ownedCommonKeys above), each
// copied from the render or deleted on its own -- nothing in the merge
// itself ties one to the other, so a future change to either key's handling
// could silently split them.
func assertExtraEnvDSNHasHostAlias(t *testing.T, common map[string]any) {
	t.Helper()
	dsnHostFound := false
	if extraEnv, ok := common["extraEnv"].([]any); ok {
		for _, e := range extraEnv {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if value, _ := entry["value"].(string); strings.Contains(value, pgRouterHostAliasHostname) {
				dsnHostFound = true
			}
		}
	}
	if !dsnHostFound {
		t.Fatalf("test setup did not emit a %s DSN into extraEnv, so this assertion proves nothing: %#v", pgRouterHostAliasHostname, common)
	}

	aliasIP := ""
	if hostAliases, ok := common["hostAliases"].([]any); ok {
		for _, a := range hostAliases {
			alias, ok := a.(map[string]any)
			if !ok {
				continue
			}
			hostnames, _ := alias["hostnames"].([]any)
			for _, h := range hostnames {
				if h == pgRouterHostAliasHostname {
					aliasIP, _ = alias["ip"].(string)
				}
			}
		}
	}
	if aliasIP == "" {
		t.Fatalf("merged values carry a %s DSN in extraEnv but no non-empty hostAliases IP for it: %#v", pgRouterHostAliasHostname, common)
	}
}

// TestMergeAppValuesNeverLeavesDSNExtraEnvWithoutHostAlias pins the merge
// half of the DSN invariant: a render that carries both a
// db.pv.dada-tuda.ru DSN in extraEnv and its matching hostAliases entry must
// land both in the merged file, even when the existing git file had neither
// key. MergeAppValues copies extraEnv and hostAliases independently -- the
// consistency between them is an accident of both being ownedCommonKeys, not
// something the merge logic enforces -- so this guards against a change to
// one code path (e.g. an ownedCommonKeys edit) quietly detaching them.
func TestMergeAppValuesNeverLeavesDSNExtraEnvWithoutHostAlias(t *testing.T) {
	rendered, err := RenderAppValues(AppSpec{
		Name:     "app",
		Image:    "nexus/app:1",
		Replicas: 1,
		Env: map[string]string{
			"DATABASE_URL": "postgresql://appuser:secret@db.pv.dada-tuda.ru:5432/appdb?sslmode=require",
		},
		PgRouterHostAliasIP: "10.43.7.9",
	})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}

	existing := `common:
    image:
        name: nexus/app
        tag: OLD
    extraEnv:
        - name: OTHER
          value: unrelated
`
	c := common(t, mergeOrFail(t, existing, rendered))
	assertExtraEnvDSNHasHostAlias(t, c)
}

func TestMergeAppValuesWritesHostAliasIntoExistingFile(t *testing.T) {
	rendered, err := RenderAppValues(AppSpec{
		Name: "app", Image: "nexus/app:1", Replicas: 1,
		PgRouterHostAliasIP: "10.43.7.9",
	})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	merged, err := MergeAppValues(mergeFixtureValues, rendered)
	if err != nil {
		t.Fatalf("MergeAppValues: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(merged), &doc); err != nil {
		t.Fatalf("merged values do not parse: %v", err)
	}
	if _, ok := common(t, doc)["hostAliases"]; !ok {
		t.Fatal("hostAliases must survive the merge onto an existing values.yaml; check ownedCommonKeys")
	}

	cleared, err := RenderAppValues(AppSpec{Name: "app", Image: "nexus/app:1", Replicas: 1})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	back, err := MergeAppValues(merged, cleared)
	if err != nil {
		t.Fatalf("MergeAppValues: %v", err)
	}
	if err := yaml.Unmarshal([]byte(back), &doc); err != nil {
		t.Fatalf("merged values do not parse: %v", err)
	}
	if _, ok := common(t, doc)["hostAliases"]; ok {
		t.Fatal("clearing the host alias must delete the key, not leave the stale IP in git")
	}
}
