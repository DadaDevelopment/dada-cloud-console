package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The bodies below are the real ones captured from leadgen/prod on 2026-08-24.

const listAppsBody = `{
  "apps": [
    {
      "ref": "leadgen/prod/lead-gen",
      "name": "lead-gen",
      "project": "leadgen",
      "env": "prod",
      "project_id": "8fbbfbc7-37c6-49d1-8e86-e8c9a615436c",
      "environment_id": "0edd114f-a902-43f8-96c5-0a93a32accf2",
      "phase": "Ready",
      "image": "nexus.dada-tuda.ru/leadgen/lead-gen@sha256:09f9",
      "url": "https://lead-gen-795027.dada-tuda.ru"
    }
  ],
  "env": "prod",
  "environment_id": "0edd114f-a902-43f8-96c5-0a93a32accf2",
  "project": "leadgen",
  "project_id": "8fbbfbc7-37c6-49d1-8e86-e8c9a615436c"
}`

func TestSlimListAppsDropsTheAddressTheCallerAlreadySupplied(t *testing.T) {
	out := slimResponse("listApps", []byte(listAppsBody))

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("slimmed body is not JSON: %v\n%s", err, out)
	}
	if len(doc) != 1 {
		t.Errorf("envelope keys = %v, want only apps -- the call was made with ref=leadgen/prod", keysOf(doc))
	}
	apps, _ := doc["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("apps = %d, want 1", len(apps))
	}
	row, _ := apps[0].(map[string]any)
	for _, gone := range []string{"project_id", "environment_id"} {
		if _, present := row[gone]; present {
			t.Errorf("row still carries %q -- every tool on this surface accepts ref", gone)
		}
	}
	for _, kept := range []string{"ref", "name", "project", "env", "phase", "image", "url"} {
		if _, present := row[kept]; !present {
			t.Errorf("row lost %q -- slimming must drop echo, not answers", kept)
		}
	}
}

const searchLogsBody = `{
  "total": 10,
  "entries": [
    {"timestamp":"2026-08-24T20:36:14.261Z","message":"Scheduler has been shut down","vm_name":"lead-gen-deploy-d494c889c-sww42","app":"lead-gen","stream":"stdout"},
    {"timestamp":"2026-08-24T20:36:14.254Z","message":"Polling stopped","vm_name":"lead-gen-deploy-d494c889c-sww42","app":"lead-gen","stream":"stdout"}
  ]
}`

func TestSlimSearchLogsHoistsConstantsAndDropsTheVMName(t *testing.T) {
	out := slimResponse("searchLogs", []byte(searchLogsBody))

	if strings.Contains(string(out), "vm_name") {
		t.Errorf("slimmed logs still say vm_name -- a container app runs in a pod, not a VM:\n%s", out)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("slimmed body is not JSON: %v\n%s", err, out)
	}
	if doc["stream"] != "stdout" || doc["app"] != "lead-gen" || doc["instance"] != "lead-gen-deploy-d494c889c-sww42" {
		t.Errorf("constants not hoisted: %v", doc)
	}
	if doc["total"] != float64(10) {
		t.Errorf("total = %v, want 10 -- the match count is not echo", doc["total"])
	}
	entries, _ := doc["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	for i, e := range entries {
		row, _ := e.(map[string]any)
		if len(row) != 2 || row["timestamp"] == nil || row["message"] == nil {
			t.Errorf("entry %d = %v, want only timestamp+message once the constants are hoisted", i, row)
		}
	}
}

func TestSlimSearchLogsKeepsAFieldThatActuallyVaries(t *testing.T) {
	body := `{"total":2,"entries":[
		{"timestamp":"t1","message":"a","vm_name":"pod-a","stream":"stdout"},
		{"timestamp":"t2","message":"b","vm_name":"pod-b","stream":"stderr"}]}`

	out := slimResponse("searchLogs", []byte(body))

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, hoisted := doc["stream"]; hoisted {
		t.Error("stream hoisted while the two entries disagree -- that invents a fact")
	}
	entries, _ := doc["entries"].([]any)
	first, _ := entries[0].(map[string]any)
	second, _ := entries[1].(map[string]any)
	if first["instance"] != "pod-a" || second["instance"] != "pod-b" {
		t.Errorf("per-entry instance lost: %v / %v", first, second)
	}
	if first["stream"] != "stdout" || second["stream"] != "stderr" {
		t.Errorf("per-entry stream lost: %v / %v", first, second)
	}
}

func TestSlimResponseLeavesUnknownShapesAlone(t *testing.T) {
	for name, body := range map[string]string{
		"not json":       `<html>nope</html>`,
		"unknown tool":   `{"build":{"status":"success"}}`,
		"missing apps":   `{"project":"leadgen"}`,
		"entries object": `{"entries":{"stream":"stdout"}}`,
	} {
		tool := "listApps"
		if strings.HasPrefix(name, "entries") {
			tool = "searchLogs"
		}
		if name == "unknown tool" {
			tool = "getBuild"
		}
		if got := string(slimResponse(tool, []byte(body))); got != body {
			t.Errorf("%s: body was rewritten\n got: %s\nwant: %s", name, got, body)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
