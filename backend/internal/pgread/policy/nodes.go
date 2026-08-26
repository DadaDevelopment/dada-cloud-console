package policy

// deniedNodes carries a specific, agent-readable error for AST node types
// that show up on the parse tree of statements we explicitly do not permit.
// Their presence is checked by short protobuf message name (e.g.
// "InsertStmt"), which classify.go's generic walk reaches at ANY depth --
// top level, inside a CTE, inside a subquery, inside a set-operation arm --
// without walkSelect-style code needing to know about that depth. A writable
// CTE such as:
//
//	WITH x AS (DELETE FROM payments RETURNING *) SELECT * FROM x
//
// is rejected here because DeleteStmt is reachable from CommonTableExpr's
// Ctequery field, not because classify.go special-cased CTEs.
var deniedNodes = map[string]struct {
	code Code
	msg  string
}{
	"InsertStmt":          {CodeNotReadOnly, "INSERT is not permitted."},
	"UpdateStmt":          {CodeNotReadOnly, "UPDATE is not permitted."},
	"DeleteStmt":          {CodeNotReadOnly, "DELETE is not permitted."},
	"MergeStmt":           {CodeNotReadOnly, "MERGE is not permitted."},
	"CopyStmt":            {CodeNotReadOnly, "COPY is not permitted."},
	"CreateTableAsStmt":   {CodeNotReadOnly, "CREATE TABLE AS / SELECT INTO is not permitted."},
	"IntoClause":          {CodeNotReadOnly, "SELECT INTO creates a table."},
	"LockingClause":       {CodeForbiddenConstruct, "FOR UPDATE/SHARE/NO KEY UPDATE takes row locks."},
	"VariableSetStmt":     {CodeForbiddenConstruct, "SET/RESET is not permitted."},
	"TransactionStmt":     {CodeForbiddenConstruct, "Explicit transaction control is not permitted."},
	"DoStmt":              {CodeNotReadOnly, "DO blocks are not permitted."},
	"CallStmt":            {CodeNotReadOnly, "CALL is not permitted."},
	"GrantStmt":           {CodeNotReadOnly, "GRANT/REVOKE is not permitted."},
	"GrantRoleStmt":       {CodeNotReadOnly, "GRANT/REVOKE ROLE is not permitted."},
	"LockStmt":            {CodeForbiddenConstruct, "LOCK is not permitted."},
	"VacuumStmt":          {CodeNotReadOnly, "VACUUM/ANALYZE is not permitted."},
	"TruncateStmt":        {CodeNotReadOnly, "TRUNCATE is not permitted."},
	"DeclareCursorStmt":   {CodeForbiddenConstruct, "Client-declared cursors are not permitted."},
	"FetchStmt":           {CodeForbiddenConstruct, "FETCH/MOVE is not permitted."},
	"ClosePortalStmt":     {CodeForbiddenConstruct, "CLOSE is not permitted."},
	"PrepareStmt":         {CodeForbiddenConstruct, "PREPARE is not permitted."},
	"ExecuteStmt":         {CodeForbiddenConstruct, "EXECUTE is not permitted."},
	"DeallocateStmt":      {CodeForbiddenConstruct, "DEALLOCATE is not permitted."},
	"ListenStmt":          {CodeForbiddenConstruct, "LISTEN is not permitted."},
	"NotifyStmt":          {CodeForbiddenConstruct, "NOTIFY is not permitted."},
	"UnlistenStmt":        {CodeForbiddenConstruct, "UNLISTEN is not permitted."},
	"CurrentOfExpr":       {CodeForbiddenConstruct, "WHERE CURRENT OF is not permitted."},
	"CreateStmt":          {CodeNotReadOnly, "CREATE TABLE is not permitted."},
	"DropStmt":            {CodeNotReadOnly, "DROP is not permitted."},
	"AlterTableStmt":      {CodeNotReadOnly, "ALTER TABLE is not permitted."},
	"IndexStmt":           {CodeNotReadOnly, "CREATE INDEX is not permitted."},
	"ViewStmt":            {CodeNotReadOnly, "CREATE VIEW is not permitted."},
	"RefreshMatViewStmt":  {CodeNotReadOnly, "REFRESH MATERIALIZED VIEW is not permitted."},
	"CreateFunctionStmt":  {CodeNotReadOnly, "CREATE FUNCTION is not permitted."},
	"CreateExtensionStmt": {CodeNotReadOnly, "CREATE EXTENSION is not permitted."},
	"CreateRoleStmt":      {CodeNotReadOnly, "CREATE ROLE is not permitted."},
	"AlterRoleStmt":       {CodeNotReadOnly, "ALTER ROLE is not permitted."},
	"CopyDbStmt":          {CodeNotReadOnly, "Database-copy statements are not permitted."},
}

// allowedNodes is the fixed set of AST node types that may appear anywhere in
// a permitted read-only query. classify.go's walk is fail-closed: a node
// type that is neither here nor in deniedNodes is rejected as
// CodeForbiddenConstruct ("unsupported syntax node"), so a parser upgrade
// that introduces a new construct we have never reviewed cannot silently
// start passing through -- it starts failing closed instead, and
// classify_test.go's TestNoUnclassifiedStatementNodes catches new *Stmt
// message types at CI time, before a version bump ships.
//
// Extend deliberately: every addition is new attack surface.
var allowedNodes = map[string]bool{
	"ParseResult": true, "RawStmt": true, "Node": true, "List": true,

	"SelectStmt": true, "ExplainStmt": true, "DefElem": true,
	"WithClause": true, "CommonTableExpr": true,

	"ResTarget": true, "ColumnRef": true, "A_Star": true, "A_Const": true,
	"Integer": true, "Float": true, "Boolean": true, "String": true, "BitString": true,
	"ParamRef": true, "Alias": true,

	"A_Expr": true, "BoolExpr": true, "NullTest": true, "BooleanTest": true,
	"CaseExpr": true, "CaseWhen": true, "CoalesceExpr": true, "MinMaxExpr": true,
	"SQLValueFunction": true, "TypeCast": true, "TypeName": true, "CollateClause": true,
	"A_ArrayExpr": true, "A_Indirection": true, "A_Indices": true, "RowExpr": true,
	"NamedArgExpr": true, "SubLink": true, "XmlExpr": false,

	"FuncCall": true, "WindowDef": true, "SortBy": true,
	"GroupingSet": true, "GroupingFunc": true,

	"JoinExpr": true, "RangeVar": true, "RangeSubselect": true, "RangeFunction": true,
	"FromExpr": true,
}

func init() {
	// XmlExpr deliberately not allowed yet (no reviewed use case); the false
	// entry above documents that it was considered and rejected on purpose,
	// as opposed to simply missing. Remove the entry so the lookup falls
	// through to "not present -> reject", matching every other unreviewed
	// node.
	delete(allowedNodes, "XmlExpr")
}
