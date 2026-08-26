// encode.go fixes the value-encoding rules from the design review: types
// that would otherwise lose precision through a generic JSON float (numeric,
// int8, money, uuid, timestamps, intervals, bytea, arrays of the above) are
// encoded as strings; small integers/floats/bool pass through as native JSON
// values so a client doing simple arithmetic on a count or an int4 does not
// have to parse a string first.
package pgexec

import (
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func describeColumns(fields []pgconn.FieldDescription) []Column {
	out := make([]Column, len(fields))
	for i, f := range fields {
		out[i] = Column{Name: f.Name, Type: oidName(f.DataTypeOID)}
	}
	return out
}

func oidName(oid uint32) string {
	if t, ok := pgtype.NewMap().TypeForOID(oid); ok {
		return t.Name
	}
	return fmt.Sprintf("oid:%d", oid)
}

// stringlyTypedOIDs are the well-known OIDs whose native Go representation
// loses precision or fidelity when passed through encoding/json's float64,
// so they are rendered as strings instead. This is a fixed, reviewed list,
// not "everything pgx cannot decode natively" -- an unrecognized/exotic OID
// falls through to fmt.Sprintf("%v", ...), which is also a string, so the
// failure mode for a type not in this list is "also a string" rather than
// "silently wrong number".
var stringlyTypedOIDs = map[uint32]bool{
	pgtype.NumericOID:     true,
	pgtype.Int8OID:        true,
	pgtype.UUIDOID:        true,
	pgtype.TimestampOID:   true,
	pgtype.TimestamptzOID: true,
	pgtype.DateOID:        true,
	pgtype.IntervalOID:    true,
	pgtype.ByteaOID:       true,
	pgtype.JSONOID:        true,
	pgtype.JSONBOID:       true,
}

func encodeRow(vals []any) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = encodeValue(v)
	}
	return out
}

func encodeValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool, int16, int32, int, float32, float64:
		return x
	case int64:
		// int8/bigint: string, per the fixed rule above (JSON numbers lose
		// precision above 2^53).
		return strconv.FormatInt(x, 10)
	case string:
		return x
	case []byte:
		return fmt.Sprintf("\\x%x", x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case pgtype.Numeric:
		s, err := x.Value()
		if err != nil || s == nil {
			return nil
		}
		return fmt.Sprintf("%v", s)
	case [16]byte: // uuid.Bytes without the pgtype wrapper in some drivers
		return fmt.Sprintf("%x", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func estimateRowBytes(row []any) int {
	n := 0
	for _, v := range row {
		switch x := v.(type) {
		case string:
			n += len(x)
		case nil:
			n += 4
		default:
			n += len(fmt.Sprintf("%v", x))
		}
	}
	return n
}
