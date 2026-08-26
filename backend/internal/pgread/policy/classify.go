// Package policy classifies raw SQL text against the DADA Cloud MCP
// PostgreSQL Read Tools v0.1 spec (section 7-8): only SELECT / WITH ...
// SELECT / EXPLAIN SELECT may reach Postgres, and only through a query that
// was reconstructed from a validated AST -- never the caller's original
// bytes.
//
// The parser is github.com/pganalyze/pg_query_go/v5, a Go binding over the
// real libpg_query grammar (the same C code Postgres itself parses SQL
// with). No SQL grammar is hand-rolled here; this package is policy on top
// of someone else's parser, on purpose -- see the design review this
// package implements.
package policy

import (
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Kind distinguishes the two permitted top-level statement shapes so the
// exec layer can route EXPLAIN through its own fixed-option wrapper instead
// of trusting caller-supplied EXPLAIN syntax.
type Kind int

const (
	KindSelect Kind = iota
	KindExplain
)

// Result is what a caller may safely execute. SQL is a deparse of the
// validated AST, not the input string: whatever survived Classify is what
// runs, so comment tricks, unicode-escape games, and exotic quoting in the
// ORIGINAL text cannot smuggle anything past the checks below, because none
// of that survives the parse -> AST -> deparse round trip.
type Result struct {
	Kind Kind
	// SQL is the statement to execute. For KindExplain this is the INNER
	// select only; the caller wraps it with a fixed, gateway-controlled
	// EXPLAIN option list (see exec.runExplain) so a client can never smuggle
	// ANALYZE/BUFFERS/WAL past classify.go by hiding it somewhere Options
	// parsing does not expect.
	SQL string
	// Relations is every table/view this statement touches (schema.name,
	// deduplicated), collected for the audit log (spec section 16) so an
	// audit event can name what was read without storing the query text
	// itself, which may contain caller-supplied literals.
	Relations []string
}

// Classify parses sql, rejects anything that is not a pure read-only tree at
// ANY depth (top level, inside a CTE, inside a subquery, inside a
// set-operation arm, inside a table function in FROM), and returns a
// re-deparsed, safe-to-execute statement.
//
// The walk is generic over protobuf reflection (walk.go), not a hand-written
// switch over expected fields: a hand-written walker only visits the fields
// its author thought of, so a construct nobody reviewed -- or a brand-new
// field added when the parser is upgraded to track a newer Postgres grammar
// -- passes through unexamined. The generic walk visits every message-typed
// field protoreflect can see and classifies every node type it finds against
// a fixed allow-list (nodes.go): a node that is in neither the allow-list
// nor the deny-list is rejected as unsupported, not passed through. See
// classify_test.go's TestNoUnclassifiedStatementNodes for the corresponding
// CI guard on parser upgrades.
func Classify(sql string) (*Result, error) {
	if strings.TrimSpace(sql) == "" {
		return nil, errf(CodeParseError, "empty query")
	}

	tree, perr := pg_query.Parse(sql)
	if perr != nil {
		return nil, errf(CodeParseError, perr.Error())
	}
	if len(tree.Stmts) != 1 {
		return nil, errf(CodeMultiStatement,
			fmt.Sprintf("Exactly one statement is permitted, got %d.", len(tree.Stmts)))
	}

	raw := tree.Stmts[0]
	top := raw.Stmt
	if top == nil {
		return nil, errf(CodeParseError, "empty statement")
	}

	var kind Kind
	var deparseTarget *pg_query.Node
	col := &relationCollector{}

	switch n := top.Node.(type) {
	case *pg_query.Node_SelectStmt:
		kind = KindSelect
		deparseTarget = top

	case *pg_query.Node_ExplainStmt:
		kind = KindExplain
		if err := checkExplainOptions(n.ExplainStmt); err != nil {
			return nil, err
		}
		inner := n.ExplainStmt.Query
		if inner == nil {
			return nil, errf(CodeParseError, "EXPLAIN with no statement")
		}
		if _, ok := inner.Node.(*pg_query.Node_SelectStmt); !ok {
			return nil, errf(CodeNotReadOnly, "EXPLAIN is permitted only for a SELECT statement.")
		}
		deparseTarget = inner

	default:
		// Route through the same walk/deny-table lookup everything else
		// uses, so a top-level statement that IS on the deny-list (SET,
		// BEGIN, LISTEN, ...) surfaces its specific code/message instead of
		// a generic "not permitted" -- the walk finds and returns that
		// entry's error via nodes.go before falling through here.
		if err := walk(top.ProtoReflect(), col); err != nil {
			return nil, err
		}
		return nil, errf(CodeNotReadOnly,
			"Only SELECT, WITH ... SELECT and EXPLAIN SELECT are permitted.")
	}

	// Walk the FULL top-level node (including, for EXPLAIN, the wrapping
	// ExplainStmt) so a writable statement or forbidden construct anywhere
	// in the tree is caught regardless of which branch above selected
	// deparseTarget.
	if err := walk(top.ProtoReflect(), col); err != nil {
		return nil, err
	}

	deparsed, derr := pg_query.Deparse(&pg_query.ParseResult{
		Version: tree.Version,
		Stmts:   []*pg_query.RawStmt{{Stmt: deparseTarget}},
	})
	if derr != nil {
		// A statement that parsed but cannot be deparsed is not safe to
		// execute: we would be forced to fall back to the caller's raw
		// text, defeating the entire point of the reconstruction step.
		return nil, errf(CodeForbiddenConstruct, "query could not be safely normalized: "+derr.Error())
	}

	return &Result{
		Kind:      kind,
		SQL:       strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(deparsed), ";")),
		Relations: col.list(),
	}, nil
}

// relationCollector gathers every RangeVar (table/view reference) seen
// during the walk, for the audit trail (spec section 16). schema-qualified
// and bare names are normalized to schema.name with "public" filled in when
// absent, matching Postgres's own default search_path resolution for
// unqualified names in this gateway's fixed search_path.
type relationCollector struct {
	seen map[string]bool
}

func (c *relationCollector) add(schema, rel string) {
	if rel == "" {
		return
	}
	if schema == "" {
		schema = "public"
	}
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	c.seen[schema+"."+rel] = true
}

func (c *relationCollector) list() []string {
	out := make([]string, 0, len(c.seen))
	for k := range c.seen {
		out = append(out, k)
	}
	return out
}

// walk recursively visits every message-typed field reachable from m via
// protobuf reflection: singular message fields and repeated (list) message
// fields alike. This is what makes the classifier fail-closed instead of
// merely "closed for the fields someone remembered to check": a field
// protoreflect can see but this function's author never enumerated is still
// visited, because the recursion is driven by the message descriptor, not by
// a hand-maintained list of accessors.
//
// Every visited node's short protobuf message name (e.g. "DeleteStmt",
// "FuncCall") is checked against nodes.go's allow-list/deny-list BEFORE
// recursing into its children, so a denied node anywhere in the tree --
// three CTEs deep, inside a subquery inside a table function -- stops the
// walk at the point it is found.
func walk(m protoreflect.Message, col *relationCollector) error {
	if !m.IsValid() {
		return nil
	}
	full := string(m.Descriptor().FullName())
	short := full
	if i := strings.LastIndex(full, "."); i >= 0 {
		short = full[i+1:]
	}

	if d, denied := deniedNodes[short]; denied {
		return errf(d.code, d.msg)
	}
	if !allowedNodes[short] {
		return errf(CodeForbiddenConstruct,
			fmt.Sprintf("unsupported syntax node %q in a read-only query", short))
	}

	if err := checkSemantics(m.Interface(), col); err != nil {
		return err
	}

	var walkErr error
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch fd.Kind() {
		case protoreflect.MessageKind, protoreflect.GroupKind:
			if fd.IsList() {
				l := v.List()
				for i := 0; i < l.Len(); i++ {
					if err := walk(l.Get(i).Message(), col); err != nil {
						walkErr = err
						return false
					}
				}
				return true
			}
			if err := walk(v.Message(), col); err != nil {
				walkErr = err
				return false
			}
		}
		return true
	})
	return walkErr
}

// checkSemantics runs the checks that are cheaper or clearer to express
// against a concrete generated type than against raw reflection: the
// function denylist (needs Funcname parts joined and lower-cased) and
// relation collection (needs Schemaname/Relname). Structural rejections
// (writable statements, locking clauses, SELECT INTO, EXPLAIN options) are
// already fully handled by nodes.go's allow/deny tables during the walk
// above; this function does not repeat them.
func checkSemantics(msg proto.Message, col *relationCollector) error {
	switch n := msg.(type) {
	case *pg_query.RangeVar:
		col.add(n.GetSchemaname(), n.GetRelname())

	case *pg_query.FuncCall:
		name := lastFuncNamePart(n.GetFuncname())
		if name != "" && isForbiddenFunc(name) {
			return errf(CodeForbiddenFunction, fmt.Sprintf("function %q is not permitted", name))
		}
	}
	return nil
}

// lastFuncNamePart returns the final identifier segment of a (possibly
// schema-qualified) function name node list, e.g. ["pg_catalog","pg_sleep"]
// -> "pg_sleep". Matching on the last segment means pg_sleep(...) and
// pg_catalog.pg_sleep(...) hit the same denylist entry; the accepted
// trade-off is that a same-named function in an unrelated schema (e.g.
// myschema.lo_get) is also rejected, which errs toward caution for a
// read-only gateway.
func lastFuncNamePart(parts []*pg_query.Node) string {
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	if s, ok := last.Node.(*pg_query.Node_String_); ok {
		return s.String_.GetSval()
	}
	return ""
}

// checkExplainOptions rejects any EXPLAIN option that would cause the
// wrapped statement to actually execute (ANALYZE and the options that only
// make sense alongside it). The gateway's own EXPLAIN wrapper (exec layer)
// never sets these; this check exists so a caller cannot request them
// directly, since ExplainStmt.Options is parsed and walked like any other
// field and would otherwise only be caught by nodes.go's generic DefElem
// allow rather than this specific, denylist-aware check.
func checkExplainOptions(e *pg_query.ExplainStmt) error {
	blocked := map[string]bool{
		"analyze": true, "buffers": true, "wal": true,
		"timing": true, "serialize": true, "memory": true,
	}
	for _, opt := range e.GetOptions() {
		de, ok := opt.Node.(*pg_query.Node_DefElem)
		if !ok {
			continue
		}
		name := strings.ToLower(de.DefElem.GetDefname())
		if blocked[name] {
			return errf(CodeForbiddenConstruct,
				fmt.Sprintf("EXPLAIN %s executes the statement and is not permitted", strings.ToUpper(name)))
		}
	}
	return nil
}
