package db

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type archiveColumn struct{ name, kind string }

// These are the explicit columns of the v1 SQLite business schema. Restoration
// accepts only these tables and columns; it never interpolates archive keys.
var archiveColumns = map[string][]archiveColumn{
	"tasks":                      {{"id", ""}, {"name", ""}, {"category_id", ""}, {"description", ""}, {"goal", ""}, {"exploration_id", ""}, {"status", ""}, {"paused", "bool"}, {"queued", "bool"}, {"queued_at", "time"}, {"queue_mode", ""}, {"llm_profile_id", ""}, {"active_llm_profile_id", ""}, {"llm_chain_revision", ""}, {"company_id", ""}, {"parent_ref", ""}, {"timeout_seconds", ""}, {"plan_heartbeat_seconds", ""}, {"coverage_enabled", "bool"}, {"pinned_at", "time"}, {"first_run_at", "time"}, {"deadline_at", "time"}, {"archived_at", "time"}, {"deleted_at", "time"}, {"completed_at", "time"}, {"created_at", "time"}, {"updated_at", "time"}},
	"explorations":               {{"id", ""}, {"description", ""}, {"goal", ""}, {"status", ""}, {"created_at", "time"}, {"updated_at", "time"}, {"round_no", ""}},
	"exploration_nodes":          {{"id", ""}, {"exploration_id", ""}, {"kind", ""}, {"payload", "json"}, {"priority", ""}, {"state", ""}, {"origin", ""}, {"owner", ""}, {"blocked_reason", ""}, {"delete_reason", ""}, {"created_at", "time"}, {"updated_at", "time"}, {"completed_at", "time"}, {"content_version", ""}, {"cold_since_round", ""}},
	"exploration_edges":          {{"exploration_id", ""}, {"src_id", ""}, {"dst_id", ""}, {"rel", ""}, {"created_at", "time"}},
	"exploration_anchors":        {{"node_id", ""}, {"asset_id", ""}},
	"task_constraints":           {{"id", ""}, {"exploration_id", ""}, {"kind", ""}, {"text", ""}, {"origin", ""}, {"created_at", "time"}, {"updated_at", "time"}},
	"activity":                   {{"id", ""}, {"exploration_id", ""}, {"node_id", ""}, {"worker", ""}, {"kind", ""}, {"tool", ""}, {"tool_use_id", ""}, {"is_error", "bool"}, {"summary", ""}, {"detail", ""}, {"metadata", "json"}, {"input_tokens", ""}, {"output_tokens", ""}, {"cache_read_tokens", ""}, {"cache_write_tokens", ""}, {"created_at", "time"}, {"main_seg", ""}},
	"task_relations":             {{"task_id", ""}, {"source_task_id", ""}, {"created_at", "time"}},
	"task_asset_links":           {{"task_id", ""}, {"asset_id", ""}, {"source", ""}, {"source_summary", ""}, {"source_node_id", ""}, {"created_at", "time"}, {"updated_at", "time"}},
	"task_llm_profiles":          {{"task_id", ""}, {"profile_id", ""}, {"position", ""}, {"status", ""}, {"last_error", ""}, {"exhausted_at", "time"}, {"created_at", "time"}, {"updated_at", "time"}},
	"task_scope":                 {{"id", ""}, {"task_id", ""}, {"kind", ""}, {"company_id", ""}, {"domain", ""}, {"net", ""}, {"value", ""}, {"source", ""}, {"reason", ""}, {"created_at", "time"}, {"net_family", ""}, {"net_prefix", ""}, {"net_first", "blob"}, {"net_last", "blob"}},
	"findings":                   {{"id", ""}, {"task_id", ""}, {"node_id", ""}, {"vulnclass", ""}, {"name", ""}, {"severity", ""}, {"summary", ""}, {"evidence", ""}, {"worker", ""}, {"status", ""}, {"report", ""}, {"created_at", "time"}, {"evidence_version", ""}, {"report_evidence_version", ""}},
	"finding_assets":             {{"finding_id", ""}, {"asset_id", ""}, {"position", ""}},
	"finding_traffic_bindings":   {{"id", ""}, {"finding_id", ""}, {"snapshot_id", ""}, {"role", ""}, {"note", ""}, {"position", ""}, {"created_at", "time"}},
	"traffic_evidence_snapshots": {{"id", ""}, {"source_traffic_id", ""}, {"captured_at", ""}, {"url", ""}, {"method", ""}, {"status", ""}, {"content_type", ""}, {"req_head", ""}, {"resp_head", ""}, {"req_hash", ""}, {"resp_hash", ""}, {"req_len", ""}, {"resp_len", ""}, {"unreferenced_at", "time"}, {"created_at", "time"}},
	"llm_records":                {{"id", ""}, {"ts", "time"}, {"model", ""}, {"profile_name", ""}, {"session_id", ""}, {"task_id", ""}, {"worker", ""}, {"latency_ms", ""}, {"input_tokens", ""}, {"output_tokens", ""}, {"cache_read", ""}, {"cache_write", ""}, {"status", ""}, {"error", ""}, {"request_body", ""}, {"response_body", ""}, {"raw_request", ""}, {"raw_response", ""}},
	"llm_usage":                  {{"id", ""}, {"ts", "time"}, {"task_id", ""}, {"exploration_id", ""}, {"worker", ""}, {"model", ""}, {"profile_name", ""}, {"latency_ms", ""}, {"input_tokens", ""}, {"output_tokens", ""}, {"cache_read", ""}, {"cache_write", ""}, {"status", ""}},
	"skill_usage":                {{"id", ""}, {"ts", "time"}, {"skill", ""}, {"agent_key", ""}, {"task_id", ""}, {"exploration_id", ""}, {"intent_id", ""}, {"session_id", ""}, {"args_len", ""}, {"found", "bool"}},
	"tool_usage":                 {{"id", ""}, {"ts", "time"}, {"tool_key", ""}, {"agent_key", ""}, {"task_id", ""}, {"exploration_id", ""}, {"intent_id", ""}, {"session_id", ""}},
	"intercept_pending":          {{"id", ""}, {"rule_id", ""}, {"conversation_id", ""}, {"task_id", ""}, {"agent_name", ""}, {"tool_name", ""}, {"tool_input", "json"}, {"status", ""}, {"reason", ""}, {"decided_at", "time"}, {"created_at", "time"}, {"audit", "json"}, {"decision_source", ""}},
	"side_question_sessions":     {{"session_key", ""}, {"conversation_id", ""}, {"task_id", ""}, {"exploration_id", ""}, {"intent_id", ""}, {"run_id", ""}, {"version", ""}, {"snapshot", "json"}, {"generation", ""}, {"memory", "json"}},
	"side_question_requests":     {{"id", ""}, {"ordinal", ""}, {"session_key", ""}, {"generation", ""}, {"client_id", ""}, {"question", ""}, {"answer", ""}, {"status", ""}, {"error", ""}, {"model", "json"}, {"snapshot_at", "time"}, {"created_at", "time"}, {"sequence", ""}, {"usage", "json"}, {"context_info", "json"}},
	"assets":                     {{"id", ""}, {"type", ""}, {"company_id", ""}, {"company_source", ""}, {"domain", ""}, {"root_domain", ""}, {"ip", ""}, {"c_segment", ""}, {"port", ""}, {"icp", ""}, {"record_type", ""}, {"bundle_id", ""}, {"app_name", ""}, {"category", ""}, {"app_description", ""}, {"app_icp", ""}, {"url", ""}, {"service_type", ""}, {"service_name", ""}, {"favicon_mmh3", ""}, {"status_code", ""}, {"content_length", ""}, {"page_title", ""}, {"auth", "json"}, {"method", ""}, {"params", "json"}, {"extra", "json"}, {"created_at", "time"}, {"last_seen", "time"}, {"updated_at", "time"}, {"ip_family", ""}, {"ip_address", "blob"}},
	"asset_bound_domains":        {{"asset_id", ""}, {"position", ""}, {"domain", ""}},
	"asset_technologies":         {{"asset_id", ""}, {"position", ""}, {"technology", ""}},
	"asset_records":              {{"asset_id", ""}, {"position", ""}, {"value", ""}},
	"asset_open_ports":           {{"asset_id", ""}, {"position", ""}, {"port", ""}, {"service", ""}, {"extra", "json"}},
}

func scanArchiveRow(rows interface {
	Columns() ([]string, error)
	Scan(...any) error
}, table string) ([]byte, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	kinds := map[string]string{}
	for _, column := range archiveColumns[table] {
		kinds[column.name] = column.kind
	}
	row := make(map[string]any, len(columns))
	for i, name := range columns {
		value := values[i]
		if value != nil {
			switch kinds[name] {
			case "json":
				var raw []byte
				switch v := value.(type) {
				case string:
					raw = []byte(v)
				case []byte:
					raw = v
				default:
					return nil, fmt.Errorf("archive %s.%s JSON type %T", table, name, value)
				}
				if !json.Valid(raw) {
					return nil, fmt.Errorf("archive %s.%s is invalid JSON", table, name)
				}
				value = json.RawMessage(raw)
			case "bool":
				switch v := value.(type) {
				case int64:
					value = v != 0
				case bool:
					value = v
				default:
					return nil, fmt.Errorf("archive %s.%s boolean type %T", table, name, value)
				}
			default:
				if v, ok := value.([]byte); ok && kinds[name] != "blob" {
					value = string(v)
				}
			}
		}
		row[name] = value
	}
	return json.Marshal(row)
}

func archiveValue(column archiveColumn, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch column.kind {
	case "json":
		raw, err := json.Marshal(value)
		return string(raw), err
	case "blob":
		encoded, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("archive %s blob type %T", column.name, value)
		}
		return base64.StdEncoding.DecodeString(encoded)
	case "time":
		if text, ok := value.(string); ok {
			parsed, err := time.Parse(time.RFC3339Nano, text)
			if err != nil {
				return nil, fmt.Errorf("archive %s timestamp: %w", column.name, err)
			}
			return parsed.UTC(), nil
		}
		if value, ok := value.(time.Time); ok {
			return value.UTC(), nil
		}
		return nil, fmt.Errorf("archive %s timestamp type %T", column.name, value)
	default:
		if number, ok := value.(json.Number); ok {
			return number.Int64()
		}
		return value, nil
	}
}

func insertArchiveRow(tx *sql.Tx, table string, row map[string]any) (sql.Result, error) {
	columns, ok := archiveColumns[table]
	if !ok {
		return nil, fmt.Errorf("archive restore table %q is not allowed", table)
	}
	names := make([]string, 0, len(columns))
	marks := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	known := map[string]bool{}
	for _, column := range columns {
		known[column.name] = true
		value, exists := row[column.name]
		if !exists {
			continue
		}
		encoded, err := archiveValue(column, value)
		if err != nil {
			return nil, err
		}
		names = append(names, column.name)
		marks = append(marks, "?")
		args = append(args, encoded)
	}
	for name := range row {
		if !known[name] {
			return nil, fmt.Errorf("archive %s contains unsupported column %q", table, name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("archive %s row has no columns", table)
	}
	return tx.Exec("INSERT INTO "+table+" ("+strings.Join(names, ",")+") VALUES ("+strings.Join(marks, ",")+")", args...)
}

func updateArchiveStub(tx *sql.Tx, table string, id int64, row map[string]any) error {
	if table != "tasks" && table != "explorations" {
		return fmt.Errorf("archive stub table %q is not allowed", table)
	}
	if archivedID, ok := jsonInt64(row["id"]); !ok || archivedID != id {
		return errors.New("archive stub identity mismatch")
	}
	known := map[string]bool{}
	sets := []string{}
	args := []any{}
	for _, column := range archiveColumns[table] {
		known[column.name] = true
		if column.name == "id" {
			continue
		}
		value, present := row[column.name]
		if !present {
			continue
		}
		value, err := archiveValue(column, value)
		if err != nil {
			return err
		}
		sets = append(sets, column.name+"=?")
		args = append(args, value)
	}
	for column := range row {
		if !known[column] {
			return fmt.Errorf("archive %s contains unsupported column %q", table, column)
		}
	}
	if len(sets) == 0 {
		return errors.New("archive stub has no fields")
	}
	args = append(args, id)
	result, err := tx.Exec("UPDATE "+table+" SET "+strings.Join(sets, ",")+" WHERE id=?", args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrTaskArchiveNotFound
	}
	return nil
}
