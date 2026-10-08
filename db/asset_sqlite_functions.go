package db

import (
	"database/sql/driver"
	"strings"

	"modernc.org/sqlite"
)

// Registered during package initialization, before any business Open. These
// functions expose ARTEX's Go normalization in SQLite predicates; they do not
// accept or translate SQL and do not emulate another database's functions.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("artex_icp_key", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, _ := args[0].(string)
		return NormalizeICP(value), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("artex_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return nil, nil
		}
		value, _ := args[0].(string)
		return strings.ToLower(value), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("artex_ip_family", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, _ := args[0].(string)
		_, family, _, err := sqliteAddress(value)
		if err != nil || family == 0 {
			return nil, nil
		}
		return int64(family), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("artex_ip_address", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, _ := args[0].(string)
		_, _, address, err := sqliteAddress(value)
		if err != nil {
			return nil, nil
		}
		return address, nil
	})
}
