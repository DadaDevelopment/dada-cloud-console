package metrics

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the metric-surface golden file")

const boxSurfaceGolden = "../../tests/golden/box/metrics.txt"

// renderBoxSurface renders the declared surface one metric per line as
// "name type label,label". Sorted, so the diff on a change is minimal.
func renderBoxSurface() string {
	lines := make([]string, 0, len(boxMetricSpecs))
	for _, s := range boxMetricSpecs {
		lines = append(lines, fmt.Sprintf("%s %s %s", s.Name, s.Type, strings.Join(s.Labels, ",")))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// TestBoxMetricSurfaceGolden pins the Box metric surface byte-exactly.
//
// Dashboards, the nightly latency gate and the alert rules all reference these
// names. Renaming one without noticing is how a dashboard goes blank and an alert
// goes quiet at the same time. Regenerate deliberately:
//
//	go test ./internal/metrics -run TestBoxMetricSurfaceGolden -update-golden
func TestBoxMetricSurfaceGolden(t *testing.T) {
	got := renderBoxSurface()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(boxSurfaceGolden), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(boxSurfaceGolden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Log("golden updated")
		return
	}

	want, err := os.ReadFile(boxSurfaceGolden)
	if err != nil {
		t.Fatalf("read golden (run with -update-golden to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("box metric surface changed.\n--- want ---\n%s\n--- got ---\n%s\n"+
			"If this change is intended, rerun with -update-golden and review the diff: "+
			"renaming a metric breaks dashboards and silences alerts.", want, got)
	}
}

var (
	fqNameRe   = regexp.MustCompile(`fqName:\s*"([^"]+)"`)
	varLabelRe = regexp.MustCompile(`variableLabels:\s*[{\[]([^}\]]*)[}\]]`)
)

// descOf returns the single Desc a collector describes.
func descOf(t *testing.T, c prometheus.Collector) *prometheus.Desc {
	t.Helper()
	ch := make(chan *prometheus.Desc, 4)
	c.Describe(ch)
	close(ch)
	var descs []*prometheus.Desc
	for d := range ch {
		descs = append(descs, d)
	}
	if len(descs) != 1 {
		t.Fatalf("expected exactly 1 Desc, got %d", len(descs))
	}
	return descs[0]
}

// TestBoxMetricSpecsMatchCollectors proves the declared table describes the
// metrics that are actually registered — name and variable labels both. Without
// this, boxMetricSpecs could drift into fiction while the golden test stayed
// green, and the golden file would then be pinning a lie.
func TestBoxMetricSpecsMatchCollectors(t *testing.T) {
	for _, s := range boxMetricSpecs {
		t.Run(s.Name, func(t *testing.T) {
			if s.collector == nil {
				t.Fatal("spec has no collector")
			}
			str := descOf(t, s.collector).String()

			m := fqNameRe.FindStringSubmatch(str)
			if m == nil {
				t.Fatalf("could not parse fqName out of Desc: %s", str)
			}
			if m[1] != s.Name {
				t.Errorf("declared name %q but collector registered %q", s.Name, m[1])
			}

			var actual []string
			if lm := varLabelRe.FindStringSubmatch(str); lm != nil && strings.TrimSpace(lm[1]) != "" {
				for _, part := range strings.Split(lm[1], ",") {
					actual = append(actual, strings.TrimSpace(part))
				}
			}
			if strings.Join(actual, ",") != strings.Join(s.Labels, ",") {
				t.Errorf("declared labels %v but collector has %v", s.Labels, actual)
			}
		})
	}
}

// TestBoxMetricSurfaceConventions enforces the naming rules the rest of the
// platform already follows, so a new metric cannot quietly break them.
func TestBoxMetricSurfaceConventions(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range boxMetricSpecs {
		if seen[s.Name] {
			t.Errorf("%s: declared twice", s.Name)
		}
		seen[s.Name] = true

		if !strings.HasPrefix(s.Name, "dada_box") {
			t.Errorf("%s: box metrics must start with dada_box", s.Name)
		}
		switch s.Type {
		case "counter":
			if !strings.HasSuffix(s.Name, "_total") {
				t.Errorf("%s: counters must end in _total", s.Name)
			}
		case "histogram":
			if !strings.HasSuffix(s.Name, "_seconds") {
				t.Errorf("%s: duration histograms must end in _seconds", s.Name)
			}
		case "gauge":
			if strings.HasSuffix(s.Name, "_total") {
				t.Errorf("%s: gauges must not end in _total", s.Name)
			}
		default:
			t.Errorf("%s: unknown type %q", s.Name, s.Type)
		}

		// Per-org and per-box labels would be unbounded. Per-org truth lives in
		// usage_records; per-box detail lives in the boxes table. Prometheus
		// carries fleet aggregates only — the same lesson as the route label.
		for _, l := range s.Labels {
			switch l {
			case "org_id", "org", "box_id", "box", "project_id", "user_id", "email":
				t.Errorf("%s: label %q is unbounded cardinality", s.Name, l)
			}
		}
	}
}

// TestAlertedMetricsAreDeclared closes the hole where renaming a metric silently
// kills the alert that watches it: every dada_* series referenced by an alert
// rule must be a metric declared somewhere in the repo.
//
// It is a static scan rather than a registry lookup on purpose. An unused
// CounterVec has no children, so it never appears in a gathered registry — a
// registry-based check would pass while the metric was misnamed. It also covers
// metrics declared outside this package.
//
// This guards the pre-existing alerts too, not only the Box ones.
func TestAlertedMetricsAreDeclared(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	rule, err := os.ReadFile(filepath.Join(repoRoot, "helm/dada-cloud-console/templates/prometheusrule.yaml"))
	if err != nil {
		t.Fatalf("read prometheusrule: %v", err)
	}

	declared, err := declaredMetricNames(repoRoot)
	if err != nil {
		t.Fatalf("scan declared metrics: %v", err)
	}
	if len(declared) == 0 {
		t.Fatal("found no declared dada_* metrics — the scan itself is broken")
	}

	// Only underscore-separated names: the helm chart's own identifiers
	// (dada-cloud-console.fullname) use hyphens and must not match.
	referenced := regexp.MustCompile(`dada_[a-z0-9_]+`).FindAllString(string(rule), -1)
	if len(referenced) == 0 {
		t.Fatal("found no dada_* metrics referenced in the alert rules — the scan itself is broken")
	}

	for _, name := range referenced {
		// Alerts query counters through rate()/increase() on the base name, and
		// histograms through the generated _bucket/_sum/_count series.
		base := name
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			base = strings.TrimSuffix(base, suffix)
		}
		if !declared[name] && !declared[base] {
			t.Errorf("alert rule references %q, which is not declared as a metric anywhere. "+
				"Either the metric was renamed and the alert is now silently dead, or the alert has a typo.", name)
		}
	}
}

// declaredMetricNames scans Go sources for `Name: "dada_..."` metric declarations.
func declaredMetricNames(repoRoot string) (map[string]bool, error) {
	re := regexp.MustCompile(`Name:\s*"(dada_[a-z0-9_]+)"`)
	names := map[string]bool{}

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", ".next":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			names[m[1]] = true
		}
		return nil
	})
	return names, err
}
