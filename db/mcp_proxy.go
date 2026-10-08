package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ErrMCPProxySettingsChanged includes removal: synchronization must not recreate
// a deleted server or overwrite arguments edited since its snapshot was read.
var ErrMCPProxySettingsChanged = errors.New("browser MCP 설정이 다른 요청에서 변경되거나 삭제되었습니다. 설정을 확인한 뒤 다시 시도하세요")

const compareAndSwapMCPProxySQL = `UPDATE mcp_servers
SET args=$1, env=$2
WHERE id=$3 AND args=$4 AND env=$5
  AND name=$6 AND transport=$7 AND COALESCE(command,'')=$8
  AND COALESCE(url,'')=$9 AND enabled=$10 AND insecure=$11
RETURNING id`

// CompareAndSwapMCPProxySettings publishes only the proxy-owned JSON fields.
// Metadata such as command, name, enabled and insecure is never written from an
// old ListMCP snapshot. Any concurrent server-configuration edit makes the
// entire write fail, including rename, disable and transport/command changes.
// String parameters are intentional: SQLite must store JSON as TEXT, not BLOB.
// This is one conditional statement, without read-then-write transactions,
// database selection, SQL translation or retries that could hide a user edit.
func (d *DB) CompareAndSwapMCPProxySettings(ctx context.Context, previous *MCPServer, args, env json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if previous == nil || previous.ID <= 0 {
		return ErrMCPProxySettingsChanged
	}
	var updatedID int64
	err := d.QueryRowContext(ctx, compareAndSwapMCPProxySQL,
		string(args), string(env), previous.ID, string(previous.Args), string(previous.Env),
		previous.Name, previous.Transport, previous.Command, previous.URL, previous.Enabled, previous.Insecure).Scan(&updatedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMCPProxySettingsChanged
	}
	return err
}
