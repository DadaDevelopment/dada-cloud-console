package policy

import "strings"

// forbiddenFunctions is a defense-in-depth layer against dangerous
// PostgreSQL functions. Unlike an earlier draft of this feature, there is no
// separate platform-provisioned role whose grants form a primary control
// here -- the connecting credential is the tenant's own database role (see
// backend/internal/api/db_query.go), so this denylist is the only function-
// level control this tool applies on top of whatever that role's own grants
// already permit. It exists so the gateway fails fast with a clear
// QUERY_FORBIDDEN_FUNCTION before a round-trip to Postgres.
var forbiddenFunctions = map[string]bool{
	// blocks the connection indefinitely; caught by statement_timeout too,
	// but that is a 5s wait the caller should never have to pay for.
	"pg_sleep": true, "pg_sleep_for": true, "pg_sleep_until": true,

	// filesystem reads from the server's perspective.
	"pg_read_file": true, "pg_read_binary_file": true,
	"pg_ls_dir": true, "pg_ls_logdir": true, "pg_ls_waldir": true,
	"pg_stat_file": true,

	// large-object filesystem bridge.
	"lo_import": true, "lo_export": true,

	// backend/session control -- read tool has no business touching either.
	"pg_terminate_backend": true, "pg_cancel_backend": true,
	"pg_reload_conf": true, "pg_rotate_logfile": true, "pg_logfile_rotate": true,
	"pg_promote": true, "pg_switch_wal": true, "pg_create_restore_point": true,

	// mutates GUCs for the session/transaction, incl. bypassing SET LOCAL
	// guards the exec layer relies on.
	"set_config": true,

	// runs arbitrary SQL text and returns it framed as a read (an
	// injection vector once query text is even partially caller-influenced).
	"query_to_xml": true, "table_to_xml": true, "cursor_to_xml": true,
	"query_to_xml_and_xmlschema": true, "table_to_xml_and_xmlschema": true,
	"database_to_xml": true, "schema_to_xml": true,
}

// forbiddenPrefixes blocks whole families by prefix: dblink* (SSRF FROM the
// database server, which no gateway-side NetworkPolicy can prevent because
// the outbound connection is made by Postgres itself, not by this process),
// and pg_advisory*/pg_try_advisory* (application-level locks that are a
// covert coordination/DoS channel with no read-only purpose).
var forbiddenPrefixes = []string{"dblink", "pg_advisory", "pg_try_advisory"}

// isForbiddenFunc matches on the LAST identifier segment, so a schema
// qualifier cannot be used to dodge the list: both pg_sleep(...) and
// pg_catalog.pg_sleep(...) resolve to the same check. This is intentionally
// broad -- it also rejects a same-named function in an unrelated schema
// (e.g. myschema.lo_get); accepted trade-off for a read-only gateway.
func isForbiddenFunc(name string) bool {
	n := strings.ToLower(name)
	if forbiddenFunctions[n] {
		return true
	}
	for _, p := range forbiddenPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}
