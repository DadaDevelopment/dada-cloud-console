package policy

import (
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v5"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// allStmtMessageNames enumerates every protobuf message in the pg_query
// schema whose name ends in "Stmt" -- i.e. every top-level-statement-shaped
// node libpg_query knows how to produce, for the current pinned parser
// version. TestNoUnclassifiedStatementNodes uses this to fail the build the
// moment a `go get -u` on pg_query_go introduces a node this package has
// never reviewed, instead of that node silently reaching allowedNodes'
// default-reject path only in production.
func allStmtMessageNames() []string {
	fd := (&pg_query.ParseResult{}).ProtoReflect().Descriptor().ParentFile()
	msgs := fd.Messages()
	out := make([]string, 0, msgs.Len())
	collectStmtNames(msgs, &out)
	return out
}

func collectStmtNames(msgs protoreflect.MessageDescriptors, out *[]string) {
	for i := 0; i < msgs.Len(); i++ {
		md := msgs.Get(i)
		name := string(md.Name())
		if strings.HasSuffix(name, "Stmt") {
			*out = append(*out, name)
		}
		collectStmtNames(md.Messages(), out)
	}
}
