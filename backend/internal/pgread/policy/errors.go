package policy

// Code is the stable machine-readable error identifier returned to the MCP
// client. Values are fixed by the DADA Cloud MCP PostgreSQL Read Tools spec
// (section 18, error model) and must not be renamed without a client-visible
// migration.
type Code string

const (
	CodeParseError         Code = "QUERY_PARSE_ERROR"
	CodeMultiStatement     Code = "QUERY_MULTI_STATEMENT"
	CodeNotReadOnly        Code = "QUERY_NOT_READ_ONLY"
	CodeForbiddenConstruct Code = "QUERY_FORBIDDEN_CONSTRUCT"
	CodeForbiddenFunction  Code = "QUERY_FORBIDDEN_FUNCTION"
)

// Error is the classifier's error type. exec/pgerr.go maps PostgreSQL-side
// rejections (permission denied, read-only-transaction violations) into the
// same shape so a caller cannot tell, from the error alone, whether the
// parser or the database refused the query -- both layers speak one
// vocabulary.
type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func errf(c Code, msg string) *Error { return &Error{Code: c, Message: msg} }
