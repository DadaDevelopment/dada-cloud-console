package worker

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

func TestSweepLocalAdoption(t *testing.T) {
	root := os.Getenv("SWEEP_ROOT")
	if root == "" {
		t.Skip("SWEEP_ROOT not set")
	}
	var files []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, "/values.yaml") && strings.Contains(p, "/apps/") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	prev := renderer.PgRouterClusterIP
	renderer.PgRouterClusterIP = "10.96.139.238"
	defer func() { renderer.PgRouterClusterIP = prev }()

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		existing := string(b)
		name := filepath.Base(filepath.Dir(f))
		parts := strings.Split(f, "/")
		label := name
		for i, p := range parts {
			if p == "projects" && i+1 < len(parts) {
				label = parts[i+1] + "/" + name
			}
		}
		adopted, err := parseAdoptableValues(existing)
		if err != nil {
			t.Logf("%-55s PARSE-FAIL %v", label, err)
			continue
		}
		env := resolvedEnv{Plain: adopted.Plain, Secret: map[string]string{}, Refs: adopted.Refs}
		spec := renderer.AppSpec{Name: name, Image: adopted.Image, Port: adopted.ServicePort, Resources: adopted.Resources, Profile: "small", WorkloadType: adopted.WorkloadType, StartCommand: adopted.StartCommand}
		if adopted.Replicas != nil {
			spec.Replicas = *adopted.Replicas
		}
		if v := adopted.Volume; v != nil {
			spec.VolumePath, spec.VolumeSize = v.Path, v.Size
			spec.VolumeStorageClass, spec.VolumeFSGroup = v.StorageClass, v.FSGroup
		}
		env.applyTo(&spec, name)
		rendered, err := renderer.RenderAppValues(spec)
		if err != nil {
			t.Logf("%-55s RENDER-FAIL %v", label, err)
			continue
		}
		merged, err := renderer.MergeAppValuesWith(existing, rendered, renderer.MergeOptions{})
		if err != nil {
			t.Logf("%-55s MERGE-FAIL %v", label, err)
			continue
		}
		dropped := renderer.DroppedPaths(existing, merged)
		changed := diffCommonKeys(existing, merged)
		if len(dropped) == 0 && len(changed) == 0 {
			t.Logf("%-55s OK", label)
			continue
		}
		t.Logf("%-55s DROP=%v CHANGE=%v", label, dropped, changed)
	}
}

func diffCommonKeys(a, b string) []string {
	var da, dbb map[string]interface{}
	yaml.Unmarshal([]byte(a), &da)
	yaml.Unmarshal([]byte(b), &dbb)
	ca, _ := da["common"].(map[string]interface{})
	cb, _ := dbb["common"].(map[string]interface{})
	var out []string
	for k, v := range ca {
		w, ok := cb[k]
		if !ok {
			continue
		}
		x, _ := yaml.Marshal(v)
		y, _ := yaml.Marshal(w)
		if string(x) != string(y) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
