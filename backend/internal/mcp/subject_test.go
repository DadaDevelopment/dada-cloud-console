package mcp

import (
	"strings"
	"testing"
)

func operationTool() GeneratedTool {
	return GeneratedTool{
		Name:         "getOperation",
		PathTemplate: "/projects/{projectId}/deploy/operations/{operationId}",
		PathParams:   []string{"projectId", "operationId"},
		InputSchema: map[string]any{
			"properties": map[string]any{
				"projectId":   map[string]any{"type": "string"},
				"operationId": map[string]any{"type": "string", "description": "Operation UUID"},
			},
			"required": []string{"projectId", "operationId"},
		},
	}
}

// The call this reproduces: updateAppImage answered {"operation":{"id":"be0bd..."}},
// so the follow-up was written with id=, and the server refused it outright.
func TestSubjectAliasAcceptsTheNameThePreviousResultUsed(t *testing.T) {
	g := operationTool()
	args := map[string]any{"ref": "leadgen/prod", "id": "be0bd7ba-a6ef-4217-88b2-e08cf4cd61f2"}

	applySubjectAliases(g, args)

	if args["operationId"] != "be0bd7ba-a6ef-4217-88b2-e08cf4cd61f2" {
		t.Errorf("operationId = %v, want the value passed as id", args["operationId"])
	}
	if _, stray := args["id"]; stray {
		t.Error("the alias survived the fold and would travel on as a stray argument")
	}
}

func TestSubjectAliasAcceptsSnakeCaseAndTheBareNoun(t *testing.T) {
	for alias, want := range map[string]string{"operation_id": "a", "operation": "b", "id": "c"} {
		args := map[string]any{alias: want}
		applySubjectAliases(operationTool(), args)
		if args["operationId"] != want {
			t.Errorf("alias %q: operationId = %v, want %q", alias, args["operationId"], want)
		}
	}
}

func TestSubjectAliasNeverOverwritesAnExplicitValue(t *testing.T) {
	args := map[string]any{"operationId": "canonical", "id": "alias"}
	applySubjectAliases(operationTool(), args)
	if args["operationId"] != "canonical" {
		t.Errorf("operationId = %v, want canonical -- an alias must not beat the real name", args["operationId"])
	}
}

func TestBoxToolsTakeTheBareNounToo(t *testing.T) {
	g := GeneratedTool{
		Name:       "getBoxState",
		PathParams: []string{"projectId", "boxName"},
	}
	args := map[string]any{"project": "agent-sandbox", "box": "scratch"}
	applySubjectAliases(g, args)
	if args["boxName"] != "scratch" {
		t.Errorf("boxName = %v, want scratch", args["boxName"])
	}
}

// Two non-address path parameters mean no unambiguous subject: setEnvVar's key
// must never be filled from a bare "id".
func TestNoSubjectWhenTheToolHasTwoNonAddressParams(t *testing.T) {
	g := GeneratedTool{
		Name:       "setEnvVar",
		PathParams: []string{"projectId", "envId", "appName", "key"},
	}
	if got := subjectParam(g); got != "key" {
		t.Fatalf("subjectParam = %q, want key -- appName is an address, key is the subject", got)
	}

	two := GeneratedTool{Name: "hypothetical", PathParams: []string{"projectId", "aId", "bId"}}
	if got := subjectParam(two); got != "" {
		t.Errorf("subjectParam = %q, want empty -- guessing between two ids can write to the wrong one", got)
	}
	args := map[string]any{"id": "x"}
	applySubjectAliases(two, args)
	if _, filled := args["aId"]; filled {
		t.Error("an ambiguous subject was filled anyway")
	}
}

func TestMissingParamMessageNamesTheAliasesAndWhatWasPassed(t *testing.T) {
	g := operationTool()
	msg := missingParamMessage(g, "operationId", map[string]any{"ref": "leadgen/prod"})

	for _, want := range []string{`"operationId"`, `"id"`, `"operation_id"`, "this call passed: ref"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %s", msg, want)
		}
	}
}

func TestAliasesAreAdvertisedInTheSchema(t *testing.T) {
	g := operationTool()
	advertiseSubjectAliases(&g)

	props, _ := g.InputSchema["properties"].(map[string]any)
	prop, _ := props["operationId"].(map[string]any)
	desc, _ := prop["description"].(string)
	if !strings.Contains(desc, "Operation UUID") {
		t.Errorf("description lost its original text: %q", desc)
	}
	if !strings.Contains(desc, `"id"`) {
		t.Errorf("description %q does not advertise the id alias -- the model reads the schema, not this test", desc)
	}
}

// An alias that is also a real parameter of the same tool must be left alone.
func TestAliasNeverEatsARealParameter(t *testing.T) {
	g := GeneratedTool{
		Name:        "hypotheticalBoxTool",
		PathParams:  []string{"projectId", "boxName"},
		QueryParams: []string{"box"},
		InputSchema: map[string]any{"properties": map[string]any{"box": map[string]any{"type": "string"}}},
	}
	args := map[string]any{"box": "a-query-value"}

	applySubjectAliases(g, args)

	if args["box"] != "a-query-value" {
		t.Error("the tool's own query parameter was folded away as if it were an alias")
	}
	if _, filled := args["boxName"]; filled {
		t.Error("boxName was filled from a parameter that means something else")
	}
}
