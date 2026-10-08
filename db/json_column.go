package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// JSON columns are TEXT in SQLite. Scan explicitly instead of relying on the
// driver's []byte conversion, which does not apply to named RawMessage types.
func jsonColumn(dest *json.RawMessage) sql.Scanner { return rawJSONColumn{dest} }

type rawJSONColumn struct{ dest *json.RawMessage }

func (s rawJSONColumn) Scan(value any) error {
	var raw []byte
	switch value := value.(type) {
	case nil:
		*s.dest = nil
		return nil
	case string:
		raw = []byte(value)
	case []byte:
		raw = value
	default:
		return fmt.Errorf("JSON column has unsupported type %T", value)
	}
	if !json.Valid(raw) {
		return fmt.Errorf("JSON column contains invalid JSON")
	}
	*s.dest = append((*s.dest)[:0], raw...)
	return nil
}
