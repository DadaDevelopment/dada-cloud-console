package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// auditMutationVerbs are the name prefixes that mark a gin handler as changing
// something -- state, a secret's exposure, or money. Read handlers are out of
// scope here: the passive-path signals (SessionStart, ViewApp, ViewBuildLogs)
// cover reading, and auditing every list endpoint would bury the write-actions.
var auditMutationVerbs = []string{
	"Add", "Assign", "Attach", "Autoscale", "Cancel", "Connect", "Create",
	"Crystallize", "Delete", "Deploy", "Detach", "Disable", "Disconnect",
	"Enable", "Expose", "Extend", "Generate", "Import", "Move", "Patch",
	"Pin", "Promote", "Redeploy", "Register", "Remove", "Rename", "Reset",
	"Restore", "Resume", "Retry", "Reveal", "Revoke", "Rotate", "Scale",
	"Set", "Suspend", "Sync", "Trigger", "Update", "Upload", "Verify", "Write",
}

// auditCoverageKnownGaps are the mutating handlers that still leave no trace.
//
// The list is a ratchet, not permission: it may shrink, and a new entry means a
// handler shipped without an audit row. Closing one is a one-line deletion here
// plus the recordAudit call. Tracked as the audit-coverage backlog item.
var auditCoverageKnownGaps = map[string]bool{
	"CreateCloudTask":     true,
	"DeleteManagedRecord": true,
	"ImportZone":          true,
}

// auditWriters are the roots of the "this function ends in an audit row" set.
// Coverage is resolved transitively from them because most handlers audit
// through a helper (insertAIModelOperation, auditFS, boxAudit) rather than
// calling recordAudit in the handler body itself.
var auditWriters = []string{
	"recordAudit", "recordSystemAudit", "writeAudit", "writeAuditRow",
	"recordAuditAsync", "recordViewAudit",
}

// TestEveryMutatingHandlerAudits is the ratchet behind "know every click".
//
// A handler that changes state and writes no audit row is invisible: not in the
// admin audit viewer, not in the user_path view, not in any support read of
// what happened to an app. Adding one is easy to forget precisely because
// nothing fails without it -- so this fails instead.
func TestEveryMutatingHandlerAudits(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package sources: %v", err)
	}

	fset := token.NewFileSet()
	bodies := map[string]string{}
	handlers := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var calls []string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.Ident:
					calls = append(calls, v.Name)
				case *ast.SelectorExpr:
					calls = append(calls, v.Sel.Name)
				case *ast.BasicLit:
					if v.Kind == token.STRING && strings.Contains(v.Value, "INSERT INTO audit_events") {
						calls = append(calls, "recordAudit")
					}
				}
				return true
			})
			bodies[fn.Name.Name] = strings.Join(calls, " ")
			if isGinHandler(fn) {
				handlers[fn.Name.Name] = true
			}
		}
	}

	writes := map[string]bool{}
	for _, w := range auditWriters {
		writes[w] = true
	}
	for i := 0; i < 6; i++ {
		grew := false
		for name, calls := range bodies {
			if writes[name] {
				continue
			}
			for _, word := range strings.Fields(calls) {
				if writes[word] {
					writes[name] = true
					grew = true
					break
				}
			}
		}
		if !grew {
			break
		}
	}

	for name := range handlers {
		if !isMutatingHandlerName(name) || writes[name] {
			continue
		}
		if auditCoverageKnownGaps[name] {
			continue
		}
		t.Errorf("%s mutates and writes no audit row: add a recordAudit call, "+
			"or add it to auditCoverageKnownGaps with a reason", name)
	}

	for name := range auditCoverageKnownGaps {
		if !handlers[name] {
			t.Errorf("auditCoverageKnownGaps lists %s, which is no longer a handler: drop the entry", name)
			continue
		}
		if writes[name] {
			t.Errorf("auditCoverageKnownGaps lists %s, which now audits: drop the entry so the gate stays tight", name)
		}
	}
}

func isGinHandler(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "gin" && sel.Sel.Name == "Context"
}

func isMutatingHandlerName(name string) bool {
	if strings.HasSuffix(name, "Impact") {
		return false
	}
	for _, verb := range auditMutationVerbs {
		if strings.HasPrefix(name, verb) {
			return true
		}
	}
	return false
}
