package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Tool is the editable catalog surface; handlers remain in Go. Agents is an
// API field assembled from tool_agents, not another stored copy of the relation.
type Tool struct {
	Key         string          `json:"key"`
	System      bool            `json:"system"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Agents      []string        `json:"agents"`
	Enabled     bool            `json:"enabled"`
	Kind        string          `json:"kind"`
	Exec        json.RawMessage `json:"exec"`
	Deferred    bool            `json:"deferred"`
	Calls       int             `json:"calls"`
}

var ErrToolNotFound = errors.New("수정할 도구가 없거나 수정할 수 없는 기본 도구입니다")

// Read the tool and its bindings in ONE SQLite snapshot, including an empty [],
// without N+1 queries or a read transaction that would reserve the writer.
const toolCols = `key, system, description, schema,
(SELECT json_group_array(agent_key) FROM
 (SELECT agent_key FROM tool_agents WHERE tool_key=tools.key ORDER BY agent_key)),
enabled, kind, exec, deferred`

const seedToolSQL = `INSERT INTO tools(key,system,description,schema,enabled)
VALUES (?1,true,?2,?3,true) ON CONFLICT(key) DO NOTHING RETURNING key`
const resetToolSQL = `INSERT INTO tools(key,system,description,schema,enabled)
VALUES (?1,true,?2,?3,true) ON CONFLICT(key) DO UPDATE
SET description=excluded.description,schema=excluded.schema,enabled=true RETURNING key`
const createCustomToolSQL = `INSERT INTO tools(key,system,description,schema,enabled,kind,exec,deferred)
VALUES (?1,false,?2,?3,?4,?5,?6,?7) RETURNING key`
const updateCustomToolSQL = `UPDATE tools SET description=?2,schema=?3,enabled=?4,kind=?5,exec=?6,deferred=?7
WHERE key=?1 AND system=false RETURNING key`
const updateToolSQL = `UPDATE tools SET description=?2,schema=?3,enabled=?4
WHERE key=?1 RETURNING key`
const deleteToolBindingsSQL = `DELETE FROM tool_agents WHERE tool_key=?1`
const insertToolBindingSQL = `INSERT INTO tool_agents(tool_key,agent_key) VALUES (?1,?2)`
const addToolBindingSQL = `INSERT INTO tool_agents(tool_key,agent_key)
SELECT key,?1 FROM tools WHERE key=?2 ON CONFLICT(tool_key,agent_key) DO NOTHING`

// Tool schemas and execution specifications are JSON TEXT in SQLite. Pass Go
// strings, not []byte, so the driver cannot store a JSON-looking BLOB instead.
func toolJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if !json.Valid(raw) {
		return "", errors.New("도구 설정 JSON이 유효하지 않습니다")
	}
	return string(raw), nil
}

func toolAgentKeys(raw json.RawMessage) ([]string, error) {
	var keys []string
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, errors.New("도구 에이전트 연결은 문자열 배열이어야 합니다")
		}
	}
	return uniqueToolAgentKeys(keys)
}

func uniqueToolAgentKeys(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key == "" {
			return nil, errors.New("도구 에이전트 키는 비워둘 수 없습니다")
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out, nil
}

// replaceToolBindings uses the caller's IMMEDIATE transaction. An unknown agent
// fails its FK check and rolls back BOTH the editable tool and all bindings.
func replaceToolBindings(tx *sql.Tx, key string, agents []string) error {
	if _, err := tx.Exec(deleteToolBindingsSQL, key); err != nil {
		return err
	}
	for _, agent := range agents {
		if _, err := tx.Exec(insertToolBindingSQL, key, agent); err != nil {
			return err
		}
	}
	return nil
}

// saveToolAndBindings owns a short transaction for one catalog mutation. The
// SQL must return the changed key; no returned row means an untouched seed or
// a missing/protected edit. It never calls the parent pool inside the transaction.
func (d *DB) saveToolAndBindings(query string, args []any, agents []string, seed bool) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var key string
	if err := tx.QueryRow(query, args...).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if seed {
				return nil // Do not reset user-edited bindings on restart.
			}
			return ErrToolNotFound
		}
		return err
	}
	if err := replaceToolBindings(tx, key, agents); err != nil {
		return err
	}
	return tx.Commit()
}

// SeedTool inserts code defaults only when the catalog entry is absent.
func (d *DB) SeedTool(key, desc string, schema, agents json.RawMessage) error {
	body, err := toolJSON(schema)
	if err != nil {
		return err
	}
	bindings, err := toolAgentKeys(agents)
	if err != nil {
		return err
	}
	return d.saveToolAndBindings(seedToolSQL, []any{key, desc, body}, bindings, true)
}

// AddAgentToToolBinding is an idempotent set insertion. Missing tool keys remain
// no-ops; an invalid agent on an existing tool rolls back the entire batch.
func (d *DB) AddAgentToToolBinding(agentKey string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, err := tx.Exec(addToolBindingSQL, agentKey, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) RemoveAgentFromToolBindings(agentKey string) error {
	_, err := d.Exec(`DELETE FROM tool_agents WHERE agent_key=?1`, agentKey)
	return err
}

func (d *DB) RemoveAgentFromTool(agentKey, toolKey string) error {
	_, err := d.Exec(`DELETE FROM tool_agents WHERE agent_key=?1 AND tool_key=?2`, agentKey, toolKey)
	return err
}

// UpsertToolForce is the explicit reset action. Existing classification and
// execution fields remain unchanged; description/schema/bindings are reset.
func (d *DB) UpsertToolForce(key, desc string, schema, agents json.RawMessage) error {
	body, err := toolJSON(schema)
	if err != nil {
		return err
	}
	bindings, err := toolAgentKeys(agents)
	if err != nil {
		return err
	}
	return d.saveToolAndBindings(resetToolSQL, []any{key, desc, body}, bindings, false)
}

// RefreshToolDefaults changes only a system tool's description and schema.
func (d *DB) RefreshToolDefaults(key, desc string, schema json.RawMessage) error {
	body, err := toolJSON(schema)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE tools SET description=?2,schema=?3 WHERE key=?1 AND system`, key, desc, body)
	return err
}

func scanTool(rows interface{ Scan(...any) error }) (*Tool, error) {
	var tool Tool
	var schema, agents, exec string
	if err := rows.Scan(&tool.Key, &tool.System, &tool.Description, &schema, &agents,
		&tool.Enabled, &tool.Kind, &exec, &tool.Deferred); err != nil {
		return nil, err
	}
	tool.Schema, tool.Exec = json.RawMessage(schema), json.RawMessage(exec)
	if !json.Valid(tool.Schema) || !json.Valid(tool.Exec) {
		return nil, errors.New("저장된 도구 JSON이 유효하지 않습니다")
	}
	if err := json.Unmarshal([]byte(agents), &tool.Agents); err != nil {
		return nil, fmt.Errorf("도구 에이전트 연결 읽기: %w", err)
	}
	if tool.Agents == nil {
		tool.Agents = []string{}
	}
	return &tool, nil
}

func (d *DB) listTools(query string) ([]*Tool, error) {
	rows, err := d.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Tool{}
	for rows.Next() {
		tool, err := scanTool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tool)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *DB) ListTools() ([]*Tool, error) {
	return d.listTools(`SELECT ` + toolCols + ` FROM tools ORDER BY key`)
}

func (d *DB) GetTool(key string) (*Tool, error) {
	tool, err := scanTool(d.QueryRow(`SELECT `+toolCols+` FROM tools WHERE key=?1`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return tool, err
}

func (d *DB) CreateCustomTool(tool *Tool) error {
	return d.saveCustomTool(tool, createCustomToolSQL)
}

func (d *DB) UpdateCustomTool(tool *Tool) error {
	return d.saveCustomTool(tool, updateCustomToolSQL)
}

func (d *DB) saveCustomTool(tool *Tool, query string) error {
	if tool == nil {
		return errors.New("도구 설정이 없습니다")
	}
	body, err := toolJSON(tool.Schema)
	if err != nil {
		return err
	}
	exec, err := toolJSON(tool.Exec)
	if err != nil {
		return err
	}
	bindings, err := uniqueToolAgentKeys(tool.Agents)
	if err != nil {
		return err
	}
	return d.saveToolAndBindings(query,
		[]any{tool.Key, tool.Description, body, tool.Enabled, tool.Kind, exec, tool.Deferred}, bindings, false)
}

// The FK deletes tool_agents as part of the same statement; built-ins are protected.
func (d *DB) DeleteCustomTool(key string) error {
	_, err := d.Exec(`DELETE FROM tools WHERE key=?1 AND system=false`, key)
	return err
}

func (d *DB) ListCustomTools() ([]*Tool, error) {
	return d.listTools(`SELECT ` + toolCols + ` FROM tools WHERE system=false ORDER BY key`)
}

func (d *DB) UpdateTool(key, desc string, schema, agents json.RawMessage, enabled bool) error {
	body, err := toolJSON(schema)
	if err != nil {
		return err
	}
	bindings, err := toolAgentKeys(agents)
	if err != nil {
		return err
	}
	return d.saveToolAndBindings(updateToolSQL, []any{key, desc, body, enabled}, bindings, false)
}
