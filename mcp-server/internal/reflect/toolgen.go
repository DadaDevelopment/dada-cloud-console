package reflect

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// GeneratedTool is the reflection output for one OpenAPI operation. It carries
// everything proxy.go needs to build a backend request, plus the JSON-Schema
// the MCP tool advertises.
type GeneratedTool struct {
	Name         string
	Description  string
	Method       string // GET, POST, PUT, PATCH, DELETE
	PathTemplate string // e.g. "/projects/{projectId}/operations/{operationId}"
	InputSchema  map[string]any

	PathParams  []string // ordered names of path params
	QueryParams []string // names of query params
	BodyProps   []string // top-level flattened body property names

	ReadOnly    bool // GET
	Destructive bool // DELETE

	// FallbackName is true when operationId was missing and the name was
	// synthesised from method+path. Used for boot logging.
	FallbackName bool
}

var slugRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// GenerateTools walks every operation in the spec and produces one tool each.
func GenerateTools(spec *Spec) []GeneratedTool {
	doc := spec.Doc
	var tools []GeneratedTool

	// Stable order: sort path keys.
	paths := make([]string, 0, doc.Paths.Len())
	for p := range doc.Paths.Map() {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		item := doc.Paths.Value(p)
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			op := item.GetOperation(method)
			if op == nil {
				continue
			}
			tools = append(tools, genTool(spec, p, method, item, op))
		}
	}
	return tools
}

func genTool(spec *Spec, path, method string, item *openapi3.PathItem, op *openapi3.Operation) GeneratedTool {
	g := GeneratedTool{
		Method:       method,
		PathTemplate: path,
		ReadOnly:     method == http.MethodGet,
		Destructive:  method == http.MethodDelete,
	}

	// Name.
	if op.OperationID != "" {
		g.Name = op.OperationID
	} else {
		g.Name = fmt.Sprintf("%s_%s", strings.ToLower(method), slug(path))
		g.FallbackName = true
	}

	// Description: summary + "\n\n" + description.
	g.Description = strings.TrimSpace(strings.Join(nonEmpty(op.Summary, op.Description), "\n\n"))

	// Build JSON-Schema.
	props := map[string]any{}
	var required []string

	// Path + query params (path params can be declared on the path item or the op).
	params := append(openapi3.Parameters{}, item.Parameters...)
	params = append(params, op.Parameters...)
	for _, pref := range params {
		if pref.Value == nil {
			continue
		}
		pv := pref.Value
		switch pv.In {
		case openapi3.ParameterInPath:
			props[pv.Name] = paramSchema(pv)
			required = append(required, pv.Name)
			g.PathParams = append(g.PathParams, pv.Name)
		case openapi3.ParameterInQuery:
			props[pv.Name] = paramSchema(pv)
			if pv.Required {
				required = append(required, pv.Name)
			}
			g.QueryParams = append(g.QueryParams, pv.Name)
		}
	}

	// Flattened request body (application/json).
	additionalProps := false
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		if mt := op.RequestBody.Value.Content.Get("application/json"); mt != nil && mt.Schema != nil {
			bodySchema := resolveSchema(spec, mt.Schema)
			if bodySchema != nil {
				bodyRequired := map[string]bool{}
				for _, r := range bodySchema.Required {
					bodyRequired[r] = true
				}
				bodyReqAll := op.RequestBody.Value.Required
				for name, sref := range bodySchema.Properties {
					props[name] = schemaToMap(spec, sref)
					g.BodyProps = append(g.BodyProps, name)
					if bodyReqAll && bodyRequired[name] {
						required = append(required, name)
					}
				}
				// Free-form / map body: allow loose props through.
				if len(bodySchema.Properties) == 0 || hasAdditional(bodySchema) {
					additionalProps = true
				}
			}
		}
	}

	sort.Strings(g.BodyProps)
	sort.Strings(g.QueryParams)
	sort.Strings(required)

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	if additionalProps {
		schema["additionalProperties"] = true
	}
	g.InputSchema = schema

	return g
}

// resolveSchema dereferences a schema ref against the doc components.
func resolveSchema(spec *Spec, ref *openapi3.SchemaRef) *openapi3.Schema {
	if ref == nil {
		return nil
	}
	if ref.Value != nil {
		return ref.Value
	}
	// Fall back to component lookup by ref name.
	name := refName(ref.Ref)
	if name == "" {
		return nil
	}
	if c := spec.Doc.Components; c != nil {
		if s := c.Schemas[name]; s != nil {
			return s.Value
		}
	}
	return nil
}

// schemaToMap turns a property schema into a minimal JSON-Schema map for the
// tool input. Refs are resolved one level so the agent sees a concrete type.
func schemaToMap(spec *Spec, ref *openapi3.SchemaRef) map[string]any {
	s := resolveSchema(spec, ref)
	if s == nil {
		return map[string]any{}
	}
	m := map[string]any{}
	if t := schemaType(s); t != "" {
		m["type"] = t
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if s.Format != "" {
		m["format"] = s.Format
	}
	if schemaType(s) == "array" && s.Items != nil {
		m["items"] = schemaToMap(spec, s.Items)
	}
	if len(m) == 0 {
		// Unknown / free-form object.
		m["type"] = "object"
	}
	return m
}

func paramSchema(p *openapi3.Parameter) map[string]any {
	m := map[string]any{"type": "string"}
	if p.Schema != nil && p.Schema.Value != nil {
		if t := schemaType(p.Schema.Value); t != "" {
			m["type"] = t
		}
		if len(p.Schema.Value.Enum) > 0 {
			m["enum"] = p.Schema.Value.Enum
		}
	}
	if p.Description != "" {
		m["description"] = p.Description
	}
	return m
}

func schemaType(s *openapi3.Schema) string {
	if s == nil || s.Type == nil || len(*s.Type) == 0 {
		return ""
	}
	return (*s.Type)[0]
}

func hasAdditional(s *openapi3.Schema) bool {
	if s == nil {
		return false
	}
	if s.AdditionalProperties.Has != nil && *s.AdditionalProperties.Has {
		return true
	}
	return s.AdditionalProperties.Schema != nil
}

func refName(ref string) string {
	if ref == "" {
		return ""
	}
	i := strings.LastIndex(ref, "/")
	if i < 0 {
		return ref
	}
	return ref[i+1:]
}

func slug(s string) string {
	s = slugRe.ReplaceAllString(s, "_")
	return strings.Trim(strings.ToLower(s), "_")
}

func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}
