package db

import (
	"database/sql"
	"fmt"
	"time"
)

// SQLite aggregate expressions have no TIMESTAMP declaration for modernc to
// decode. The business store writes UTC SQLite timestamps or RFC3339 values.
func timeColumn(dest *time.Time) sql.Scanner { return utcTimeColumn{dest} }

type utcTimeColumn struct{ dest *time.Time }

func (s utcTimeColumn) Scan(value any) error {
	if value, ok := value.(time.Time); ok {
		*s.dest = value.UTC()
		return nil
	}
	var text string
	switch value := value.(type) {
	case string:
		text = value
	case []byte:
		text = string(value)
	default:
		return fmt.Errorf("timestamp column has unsupported type %T", value)
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999"} {
		if value, err := time.Parse(layout, text); err == nil {
			*s.dest = value.UTC()
			return nil
		}
	}
	return fmt.Errorf("invalid timestamp %q", text)
}
