package renderer

import (
	"reflect"
	"strings"
	"testing"
)

func TestDroppedPathsIgnoresChangedValues(t *testing.T) {
	existing := `
image: repo/app:v1
resources:
  requests:
    cpu: "0.5"
    memory: 512Mi
`
	rendered := `
image: repo/app:v2
resources:
  requests:
    cpu: "1"
    memory: 1Gi
`
	if got := DroppedPaths(existing, rendered); len(got) != 0 {
		t.Fatalf("a deploy that only changes values must drop nothing, got %v", got)
	}
}

func TestDroppedPathsReportsMissingKeys(t *testing.T) {
	existing := `
image: repo/app:v1
serviceDatabase:
  enabled: true
  database: reels
extraVolumes:
  - name: env-file
`
	rendered := `
image: repo/app:v1
`
	got := DroppedPaths(existing, rendered)
	want := []string{"extraVolumes", "serviceDatabase"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped paths = %v, want %v", got, want)
	}
}

func TestDroppedPathsMatchesNamedListEntriesByName(t *testing.T) {
	existing := `
env:
  - name: PG_HOST
    value: db
  - name: PYTHONPATH
    value: /python-logging-setup
  - name: SERVICE_NAME
    value: reels-tracker
`
	rendered := `
env:
  - name: KSERVE_TIMEOUT
    value: "300"
  - name: PG_HOST
    value: db
`
	got := DroppedPaths(existing, rendered)
	want := []string{"env.PYTHONPATH", "env.SERVICE_NAME"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped env vars = %v, want %v", got, want)
	}
}

func TestDroppedPathsIgnoresReorderedNamedEntries(t *testing.T) {
	existing := `
env:
  - name: A
    value: "1"
  - name: B
    value: "2"
`
	rendered := `
env:
  - name: B
    value: "2"
  - name: A
    value: "1"
`
	if got := DroppedPaths(existing, rendered); len(got) != 0 {
		t.Fatalf("reordering a named list is not a deletion, got %v", got)
	}
}

func TestDroppedPathsReportsShrunkUnnamedList(t *testing.T) {
	existing := `
args:
  - --a
  - --b
`
	rendered := `
args:
  - --a
`
	got := DroppedPaths(existing, rendered)
	want := []string{"args"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped = %v, want %v", got, want)
	}
}

func TestDroppedPathsReportsScalarReplacingATree(t *testing.T) {
	existing := `
resources:
  requests:
    cpu: "1"
`
	rendered := `
resources: {}
`
	got := DroppedPaths(existing, rendered)
	want := []string{"resources.requests"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped = %v, want %v", got, want)
	}
}

func TestDroppedPathsStaysSilentOnUnparseableInput(t *testing.T) {
	if got := DroppedPaths("key: [unterminated", "key: value"); len(got) != 0 {
		t.Fatalf("a values.yaml this agent cannot read is not proof of loss, got %v", got)
	}
}

func TestDroppedPathsOnTheReelsTrackerRegression(t *testing.T) {
	existing := `
image: repo/reels:v1
serviceDatabase:
  enabled: true
env:
  - name: PG_USER
    valueFrom: {}
  - name: PG_PASSWORD
    valueFrom: {}
  - name: DATABASE_URL
    value: postgres-dsn
  - name: PYTHONPATH
    value: /python-logging-setup
extraVolumes:
  - name: env-file
  - name: python-logging-setup
resources:
  limits:
    ephemeral-storage: 1Gi
`
	rendered := `
image: repo/reels:v1
env:
  - name: PG_USER
    valueFrom: {}
  - name: PG_PASSWORD
    valueFrom: {}
resources:
  limits:
    ephemeral-storage: 500Mi
`
	got := DroppedPaths(existing, rendered)
	want := []string{
		"env.DATABASE_URL",
		"env.PYTHONPATH",
		"extraVolumes",
		"serviceDatabase",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped = %v, want %v", got, want)
	}
}

func TestDescribeDroppedCapsLongLists(t *testing.T) {
	paths := make([]string, 20)
	for i := range paths {
		paths[i] = "env.VAR"
	}
	got := DescribeDropped(paths)
	if !strings.HasSuffix(got, "and 8 more") {
		t.Fatalf("long lists must be capped, got %q", got)
	}
	if got := DescribeDropped([]string{"a", "b"}); got != "a, b" {
		t.Fatalf("short lists must be listed in full, got %q", got)
	}
}
