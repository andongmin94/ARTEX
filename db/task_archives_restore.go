package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"
)

// CompleteTaskArchive performs the hot-store compaction only after the external
// package has been fully written and checksummed. The task/exploration rows remain
// as minimal ID stubs; all heavyweight task-owned rows move into the package.
func (d *DB) CompleteTaskArchive(
	archiveID int64,
	snapshot *TaskArchiveSnapshot,
	archivePath, sha256 string,
	originalSize, compressedSize int64,
) error {
	if err := validateTaskArchiveSnapshot(snapshot); err != nil {
		return err
	}
	countsRaw, err := json.Marshal(snapshot.DataCounts)
	if err != nil {
		return err
	}
	statsRaw, err := json.Marshal(snapshot.AggregateStats)
	if err != nil {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var taskID, expID int64
	var state string
	if err := tx.QueryRow(`SELECT archive.task_id,task.exploration_id,archive.state
FROM task_archives archive JOIN tasks task ON task.id=archive.task_id
WHERE archive.id=$1`, archiveID).Scan(&taskID, &expID, &state); err != nil {
		return err
	}
	if state != Archiving || taskID != snapshot.TaskID || expID != snapshot.ExplorationID {
		return fmt.Errorf("%w: archive snapshot identity/state mismatch", ErrTaskArchiveState)
	}
	var dependent int64
	err = tx.QueryRow(`SELECT child.id FROM task_relations relation
JOIN tasks child ON child.id=relation.task_id AND child.deleted_at IS NULL
WHERE relation.source_task_id=$1 LIMIT 1`, taskID).Scan(&dependent)
	if err == nil {
		return fmt.Errorf("%w: task %d", ErrTaskArchiveDependent, dependent)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM intercept_pending WHERE COALESCE(task_id,'')=$1`, strconv.FormatInt(taskID, 10)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM llm_records WHERE COALESCE(task_id,'')=$1`, strconv.FormatInt(taskID, 10)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM llm_usage WHERE COALESCE(task_id,'')=$1 OR exploration_id=$2`, strconv.FormatInt(taskID, 10), expID); err != nil {
		return err
	}
	for _, table := range []string{"skill_usage", "tool_usage"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE task_id=$1 OR exploration_id=$2`, taskID, expID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM side_question_sessions WHERE task_id=$1`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM findings WHERE task_id=$1`, taskID); err != nil {
		return err
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM task_relations WHERE task_id=$1 OR source_task_id=$1`, []any{taskID}},
		{`DELETE FROM task_asset_links WHERE task_id=$1`, []any{taskID}},
		{`DELETE FROM task_llm_profiles WHERE task_id=$1`, []any{taskID}},
		{`DELETE FROM task_scope WHERE task_id=$1`, []any{taskID}},
		{`DELETE FROM task_constraints WHERE exploration_id=$1`, []any{expID}},
		{`DELETE FROM activity WHERE exploration_id=$1`, []any{expID}},
		{`DELETE FROM exploration_nodes WHERE exploration_id=$1`, []any{expID}},
	} {
		if _, err := tx.Exec(statement.query, statement.args...); err != nil {
			return err
		}
	}
	if len(snapshot.ExclusiveAssetIDs) > 0 {
		idsRaw, err := json.Marshal(snapshot.ExclusiveAssetIDs)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM assets AS asset
WHERE asset.id IN (SELECT value FROM json_each($2)) AND asset.company_id IS NULL
AND NOT EXISTS (SELECT 1 FROM tasks task WHERE task.id<>$1 AND task.deleted_at IS NULL AND EXISTS(SELECT 1 FROM task_asset_links link WHERE link.task_id=task.id AND link.asset_id=asset.id))
AND NOT EXISTS (
 SELECT 1 FROM exploration_anchors anchor JOIN exploration_nodes node ON node.id=anchor.node_id
 JOIN tasks task ON task.exploration_id=node.exploration_id
 WHERE anchor.asset_id=asset.id AND task.id<>$1 AND task.deleted_at IS NULL
) AND NOT EXISTS (
 SELECT 1 FROM finding_assets relation JOIN findings finding ON finding.id=relation.finding_id
 JOIN tasks task ON task.id=finding.task_id
 WHERE relation.asset_id=asset.id AND task.id<>$1 AND task.deleted_at IS NULL
)`, taskID, string(idsRaw)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE explorations SET description='',goal='',status='open' WHERE id=$1`, expID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET
 name='',category_id=NULL,description='',goal='',paused=true,queued=false,queued_at=NULL,queue_mode='',
 llm_profile_id=NULL,active_llm_profile_id=NULL,llm_chain_revision=llm_chain_revision+1,
 company_id=NULL,parent_ref=NULL,timeout_seconds=0,coverage_enabled=true,pinned_at=NULL,
 first_run_at=NULL,deadline_at=NULL,archived_at=strftime('%Y-%m-%d %H:%M:%f','now'),deleted_at=strftime('%Y-%m-%d %H:%M:%f','now')
WHERE id=$1`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE task_archives SET
	 state=$2,phase='ready',progress=100,error='',archive_path=$3,sha256=$4,
	 original_size=$5,compressed_size=$6,data_counts=$7,aggregate_stats=$8,
	 format_version=$9,archived_at=strftime('%Y-%m-%d %H:%M:%f','now'),warnings='[]'
WHERE id=$1`, archiveID, ArchiveReady, archivePath, sha256, originalSize, compressedSize,
		string(countsRaw), string(statsRaw), snapshot.FormatVersion); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreTaskArchive restores SQLite rows from a verified manifest. It is
// idempotent for accounting/traffic retry scenarios and returns non-fatal
// warnings for global objects that intentionally are not recreated.
func (d *DB) RestoreTaskArchive(archiveID int64, snapshot *TaskArchiveSnapshot, remainingTimeoutSeconds int64) ([]string, error) {
	return d.restoreTaskArchive(archiveID, snapshot, remainingTimeoutSeconds, nil)
}

// RestoreTaskArchiveWithLLMRecords restores a package whose heavyweight LLM
// record history is stored as a sequence of JSON objects outside manifest.json.
func (d *DB) RestoreTaskArchiveWithLLMRecords(
	archiveID int64,
	snapshot *TaskArchiveSnapshot,
	remainingTimeoutSeconds int64,
	llmRecords io.Reader,
) ([]string, error) {
	if llmRecords == nil {
		return nil, errors.New("nil streamed LLM record reader")
	}
	return d.restoreTaskArchive(archiveID, snapshot, remainingTimeoutSeconds, llmRecords)
}

func (d *DB) restoreTaskArchive(
	archiveID int64,
	snapshot *TaskArchiveSnapshot,
	remainingTimeoutSeconds int64,
	llmRecords io.Reader,
) ([]string, error) {
	if err := validateTaskArchiveRestore(snapshot, llmRecords != nil); err != nil {
		return nil, err
	}
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	warnings, err := d.RestoreTaskArchiveTx(context.Background(), tx, archiveID, snapshot, remainingTimeoutSeconds, llmRecords)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return warnings, nil
}

// RestoreTaskArchiveTx restores archive metadata using the caller's transaction.
// The caller must commit only after evidence bodies and streamed rows are verified.
func (d *DB) RestoreTaskArchiveTx(ctx context.Context, tx *sql.Tx, archiveID int64, snapshot *TaskArchiveSnapshot, remainingTimeoutSeconds int64, llmRecords io.Reader) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, errors.New("nil archive restore transaction")
	}
	if err := validateTaskArchiveRestore(snapshot, llmRecords != nil); err != nil {
		return nil, err
	}
	streamedLLMRecords := snapshot.StreamedTables["llm_records"]
	var taskID, expID int64
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT archive.task_id,task.exploration_id,archive.state
FROM task_archives archive JOIN tasks task ON task.id=archive.task_id
WHERE archive.id=$1`, archiveID).Scan(&taskID, &expID, &state); err != nil {
		return nil, err
	}
	if state != Restoring || taskID != snapshot.TaskID || expID != snapshot.ExplorationID {
		return nil, fmt.Errorf("%w: restore snapshot identity/state mismatch", ErrTaskArchiveState)
	}
	warnings := []string{}
	assetMap, assetWarnings, err := restoreArchiveAssets(tx, taskID, snapshot.Tables["assets"])
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, assetWarnings...)

	// The task row exists as an archived stub. Restore its global references only
	// when the current instance still owns them; never recreate categories/profiles.
	taskRows, err := decodeArchiveRows(snapshot.Tables["tasks"])
	if err != nil || len(taskRows) != 1 {
		return nil, fmt.Errorf("restore task row: expected one row: %w", err)
	}
	taskRow := taskRows[0]
	categoryID, _ := jsonInt64(taskRow["category_id"])
	categoryExists, err := rowExists(tx, "task_categories", categoryID)
	if err != nil {
		return nil, err
	}
	if categoryID > 0 && !categoryExists {
		taskRow["category_id"] = nil
		warnings = append(warnings, fmt.Sprintf("작업 분류 %d가 삭제되어 미분류로 복원했습니다", categoryID))
	}
	companyID, _ := jsonInt64(taskRow["company_id"])
	companyExists, err := rowExists(tx, "companies", companyID)
	if err != nil {
		return nil, err
	}
	if companyID > 0 && !companyExists {
		taskRow["company_id"] = nil
		warnings = append(warnings, fmt.Sprintf("작업의 기업 %d가 삭제되어 기업 연결을 건너뛰었습니다", companyID))
	}
	for _, key := range []string{"llm_profile_id", "active_llm_profile_id"} {
		profileID, _ := jsonInt64(taskRow[key])
		profileExists, err := rowExists(tx, "llm_profiles", profileID)
		if err != nil {
			return nil, err
		}
		if profileID > 0 && !profileExists {
			taskRow[key] = nil
			warnings = append(warnings, fmt.Sprintf("LLM 설정 %d가 삭제되어 작업 설정에서 제거했습니다", profileID))
		}
	}
	if err := restoreExplorationStub(tx, snapshot.Tables["explorations"], expID); err != nil {
		return nil, err
	}
	if err := restoreTaskStub(tx, taskRow, taskID, remainingTimeoutSeconds); err != nil {
		return nil, err
	}

	remappedTables, err := remapArchiveAssetReferences(snapshot.Tables, assetMap)
	if err != nil {
		return nil, err
	}
	// Insert graph rows in foreign-key order. The archived stub has no graph rows,
	// so an ID conflict signals external corruption and must stop the restore.
	for _, table := range []string{"exploration_nodes", "exploration_edges", "exploration_anchors", "task_constraints", "activity"} {
		if err := insertArchiveRows(tx, table, remappedTables[table]); err != nil {
			return nil, fmt.Errorf("restore %s: %w", table, err)
		}
	}
	if warning, err := restoreTaskRelations(tx, taskID, remappedTables["task_relations"]); err != nil {
		return nil, err
	} else {
		warnings = append(warnings, warning...)
	}
	for _, table := range []string{"side_question_sessions", "side_question_requests"} {
		if err := insertArchiveRows(tx, table, remappedTables[table]); err != nil {
			return nil, fmt.Errorf("restore %s: %w", table, err)
		}
	}
	if warning, err := restoreTaskScopes(tx, remappedTables["task_scope"]); err != nil {
		return nil, err
	} else {
		warnings = append(warnings, warning...)
	}
	if warning, err := restoreTaskLLMProfiles(tx, remappedTables["task_llm_profiles"]); err != nil {
		return nil, err
	} else {
		warnings = append(warnings, warning...)
	}
	for _, table := range []string{"task_asset_links", "findings", "finding_assets", "asset_bound_domains", "asset_technologies", "asset_records", "asset_open_ports"} {
		if err := insertArchiveRows(tx, table, remappedTables[table]); err != nil {
			return nil, fmt.Errorf("restore %s: %w", table, err)
		}
	}
	if err := restoreFindingTrafficTx(tx, snapshot); err != nil {
		return nil, fmt.Errorf("restore finding traffic: %w", err)
	}
	if streamedLLMRecords != "" {
		count, err := insertArchiveJSONSequenceRows(tx, "llm_records", llmRecords)
		if err != nil {
			return nil, fmt.Errorf("restore llm_records: %w", err)
		}
		if expected := snapshot.DataCounts["llm_records"]; count != expected {
			return nil, fmt.Errorf("restore llm_records: row count %d does not match manifest %d", count, expected)
		}
	} else if err := insertArchiveRows(tx, "llm_records", remappedTables["llm_records"]); err != nil {
		return nil, fmt.Errorf("restore llm_records: %w", err)
	}
	for _, table := range []string{"llm_usage", "skill_usage", "tool_usage"} {
		if err := insertArchiveRows(tx, table, remappedTables[table]); err != nil {
			return nil, fmt.Errorf("restore %s: %w", table, err)
		}
	}
	if err := restoreInterceptRows(tx, remappedTables["intercept_pending"]); err != nil {
		return nil, err
	}
	warningsRaw, err := json.Marshal(warnings)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE task_archives SET warnings=$2,phase='database_restored',progress=85,error='' WHERE id=$1`, archiveID, string(warningsRaw)); err != nil {
		return nil, err
	}
	return warnings, nil
}

func restoreExplorationStub(tx *sql.Tx, raw json.RawMessage, expID int64) error {
	rows, err := decodeArchiveRows(raw)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return errors.New("archive must contain one exploration")
	}
	return updateArchiveStub(tx, "explorations", expID, rows[0])
}

func restoreTaskStub(tx *sql.Tx, row map[string]any, taskID, remaining int64) error {
	row["queued"] = false
	row["queued_at"] = nil
	row["queue_mode"] = ""
	row["deleted_at"] = nil
	row["archived_at"] = nil
	if paused, _ := row["paused"].(bool); paused && remaining > 0 {
		row["deadline_at"] = time.Now().UTC().Add(time.Duration(remaining) * time.Second)
	}
	return updateArchiveStub(tx, "tasks", taskID, row)
}

func firstArchiveRow(raw json.RawMessage) json.RawMessage {
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return json.RawMessage("{}")
	}
	return rows[0]
}

func decodeArchiveRows(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var rows []map[string]any
	if err := decoder.Decode(&rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, errors.New("archive table must be a JSON array")
	}
	for _, row := range rows {
		if row == nil {
			return nil, errors.New("archive row must be a JSON object")
		}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("archive table contains trailing JSON")
		}
		return nil, err
	}
	return rows, nil
}

func rowExists(tx *sql.Tx, table string, id int64) (bool, error) {
	if table != "task_categories" && table != "llm_profiles" && table != "companies" && table != "tasks" && table != "assets" {
		return false, fmt.Errorf("archive reference table %q is not allowed", table)
	}
	if id <= 0 {
		return false, nil
	}
	var exists bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=$1)`, id).Scan(&exists)
	return exists, err
}

func validateTaskArchiveRestore(snapshot *TaskArchiveSnapshot, hasLLMReader bool) error {
	if err := validateTaskArchiveSnapshot(snapshot); err != nil {
		return err
	}
	if (snapshot.StreamedTables["llm_records"] != "") != hasLLMReader {
		return fmt.Errorf("%w: streamed LLM record reader does not match manifest", ErrTaskArchiveFormatMismatch)
	}
	return nil
}

func validateTaskArchiveSnapshot(snapshot *TaskArchiveSnapshot) error {
	if snapshot == nil || !IsTaskArchiveFormatSupported(snapshot.FormatVersion) {
		return ErrTaskArchiveFormatMismatch
	}
	streamedLLM := snapshot.StreamedTables["llm_records"]
	if len(snapshot.StreamedTables) > 1 || (len(snapshot.StreamedTables) == 1 && streamedLLM != TaskArchiveLLMRecordsPath) {
		return fmt.Errorf("%w: unsupported streamed table metadata", ErrTaskArchiveFormatMismatch)
	}
	if snapshot.TaskID <= 0 || snapshot.ExplorationID <= 0 {
		return errors.New("archive task/exploration identity must be positive")
	}
	if len(snapshot.Tables) != len(archiveColumns) {
		return fmt.Errorf("archive must contain all %d SQLite tables", len(archiveColumns))
	}
	for table := range snapshot.Tables {
		if _, ok := archiveColumns[table]; !ok {
			return fmt.Errorf("archive table %q is not allowed", table)
		}
	}
	for table, count := range snapshot.DataCounts {
		if _, ok := archiveColumns[table]; !ok && table != "traffic" {
			return fmt.Errorf("archive row count table %q is not allowed", table)
		}
		if count < 0 {
			return fmt.Errorf("archive %s row count must not be negative", table)
		}
	}
	for table, columns := range archiveColumns {
		raw, present := snapshot.Tables[table]
		if !present || len(raw) == 0 {
			return fmt.Errorf("archive %s table is missing", table)
		}
		count, present := snapshot.DataCounts[table]
		if !present {
			return fmt.Errorf("archive %s row count is missing", table)
		}
		rows, err := decodeArchiveRows(raw)
		if err != nil {
			return fmt.Errorf("archive %s: %w", table, err)
		}
		if table == "llm_records" && streamedLLM != "" {
			if len(rows) != 0 {
				return errors.New("streamed LLM records must not also occur in the manifest")
			}
		} else if int64(len(rows)) != count {
			return fmt.Errorf("archive %s row count %d does not match manifest %d", table, len(rows), count)
		}
		if (table == "tasks" || table == "explorations") && len(rows) != 1 {
			return fmt.Errorf("archive %s must contain one row", table)
		}
		known := make(map[string]archiveColumn, len(columns))
		for _, column := range columns {
			known[column.name] = column
		}
		for _, row := range rows {
			for name, value := range row {
				column, ok := known[name]
				if !ok {
					return fmt.Errorf("archive %s contains unsupported column %q", table, name)
				}
				if _, err := archiveValue(column, value); err != nil {
					return fmt.Errorf("archive %s: %w", table, err)
				}
			}
		}
	}
	return nil
}

// Shared assets can acquire new ordered values while another task is cold.
// Match by value (or port), keep the live order, and append archived missing values.
func restoreArchiveAssetRelation(tx *sql.Tx, table string, row map[string]any) error {
	assetID, valid := jsonInt64(row["asset_id"])
	position, validPosition := jsonInt64(row["position"])
	if !valid || assetID <= 0 || !validPosition || position < 0 {
		return errors.New("invalid archived asset relation identity/position")
	}
	column := map[string]string{"asset_bound_domains": "domain", "asset_technologies": "technology", "asset_records": "value", "asset_open_ports": "port"}[table]
	if column == "" {
		return fmt.Errorf("unsupported archived asset relation %q", table)
	}
	value, present := row[column]
	if !present || value == nil {
		return fmt.Errorf("archive %s.%s is missing", table, column)
	}
	if number, ok := value.(json.Number); ok {
		var err error
		value, err = number.Int64()
		if err != nil {
			return err
		}
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE asset_id=?1 AND `+column+`=?2)`, assetID, value).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if table == "asset_open_ports" {
			if service, ok := row["service"].(string); ok && service != "" {
				_, err := tx.Exec(`UPDATE asset_open_ports SET service=?3 WHERE asset_id=?1 AND port=?2 AND COALESCE(service,'')=''`, assetID, value, service)
				return err
			}
		}
		return nil
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(position),-1)+1 FROM `+table+` WHERE asset_id=?1`, assetID).Scan(&position); err != nil {
		return err
	}
	row["position"] = position
	_, err := insertArchiveRow(tx, table, row)
	return err
}

func insertArchiveRows(tx *sql.Tx, table string, raw json.RawMessage) error {
	if _, ok := archiveColumns[table]; !ok {
		return fmt.Errorf("archive restore table %q is not allowed", table)
	}
	rows, err := decodeArchiveRows(raw)
	if err != nil {
		return err
	}
	assetRelation := table == "asset_bound_domains" || table == "asset_technologies" || table == "asset_records" || table == "asset_open_ports"
	if assetRelation {
		sort.SliceStable(rows, func(i, j int) bool {
			iPosition, _ := jsonInt64(rows[i]["position"])
			jPosition, _ := jsonInt64(rows[j]["position"])
			return iPosition < jPosition
		})
	}
	for _, row := range rows {
		if assetRelation {
			if err := restoreArchiveAssetRelation(tx, table, row); err != nil {
				return err
			}
			continue
		}
		if _, err := insertArchiveRow(tx, table, row); err != nil {
			return err
		}
	}
	return nil
}

func insertArchiveJSONSequenceRows(tx *sql.Tx, table string, reader io.Reader) (int64, error) {
	if table != "llm_records" {
		return 0, fmt.Errorf("archive streamed restore table %q is not allowed", table)
	}
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	var count int64
	for {
		var row map[string]any
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return 0, err
		}
		if row == nil {
			return 0, errors.New("streamed archive row must be a JSON object")
		}
		if _, err := insertArchiveRow(tx, table, row); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

func restoreArchiveAssets(tx *sql.Tx, taskID int64, raw json.RawMessage) (map[int64]int64, []string, error) {
	rows, err := decodeArchiveRows(raw)
	if err != nil {
		return nil, nil, err
	}
	mapping := make(map[int64]int64, len(rows))
	warnings := []string{}
	for _, row := range rows {
		oldID, ok := jsonInt64(row["id"])
		if !ok || oldID <= 0 {
			return nil, nil, errors.New("archived asset has invalid id")
		}
		if companyID, ok := jsonInt64(row["company_id"]); ok && companyID > 0 {
			exists, err := rowExists(tx, "companies", companyID)
			if err != nil {
				return nil, nil, err
			}
			if !exists {
				row["company_id"] = nil
				row["company_source"] = "explicit"
				warnings = append(warnings, fmt.Sprintf("자산 %d의 기업 %d가 삭제되어 미지정 상태로 복원했습니다", oldID, companyID))
			}
		}
		if existing, found, err := findArchiveAssetNaturalID(tx, row); err != nil {
			return nil, nil, err
		} else if found {
			mapping[oldID] = existing
			continue
		}
		candidate := oldID
		idExists, err := rowExists(tx, "assets", oldID)
		if err != nil {
			return nil, nil, err
		}
		if idExists {
			if err := tx.QueryRow(`SELECT max(COALESCE((SELECT seq FROM sqlite_sequence WHERE name='assets'),0),COALESCE((SELECT max(id) FROM assets),0))+1`).Scan(&candidate); err != nil {
				return nil, nil, err
			}
			row["id"] = candidate
			warnings = append(warnings, fmt.Sprintf("자산 ID %d가 이미 사용 중이므로 %d로 복원했습니다", oldID, candidate))
		}
		if _, err := insertArchiveRow(tx, "assets", row); err != nil {
			return nil, nil, err
		}

		mapping[oldID] = candidate
	}
	return mapping, warnings, nil
}

func findArchiveAssetNaturalID(tx *sql.Tx, row map[string]any) (int64, bool, error) {
	typeName, _ := row["type"].(string)
	stringValue := func(key string) string {
		value, _ := row[key].(string)
		return value
	}
	var query string
	var args []any
	switch typeName {
	case "root_domain":
		query, args = `SELECT id FROM assets WHERE type='root_domain' AND domain=$1`, []any{stringValue("domain")}
	case "ip":
		query, args = `SELECT id FROM assets WHERE type='ip' AND ip=$1`, []any{stringValue("ip")}
	case "subdomain":
		query, args = `SELECT id FROM assets WHERE type='subdomain' AND domain=$1 AND COALESCE(record_type,'')=$2`, []any{stringValue("domain"), stringValue("record_type")}
	case "app":
		if bundle := stringValue("bundle_id"); bundle != "" {
			query, args = `SELECT id FROM assets WHERE type='app' AND bundle_id=$1`, []any{bundle}
		} else {
			query, args = `SELECT id FROM assets WHERE type='app' AND bundle_id IS NULL AND app_name=$1`, []any{stringValue("app_name")}
		}
	case "service":
		if stringValue("service_type") == "http" {
			query, args = `SELECT id FROM assets WHERE type='service' AND service_type='http' AND url=$1`, []any{stringValue("url")}
		} else {
			port, _ := jsonInt64(row["port"])
			query, args = `SELECT id FROM assets WHERE type='service' AND service_type='other' AND COALESCE(domain,'')=$1 AND COALESCE(ip,'')=$2 AND port=$3 AND service_name=$4`, []any{stringValue("domain"), stringValue("ip"), port, stringValue("service_name")}
		}
	case "endpoint":
		query, args = `SELECT id FROM assets WHERE type='endpoint' AND url=$1 AND method=$2`, []any{stringValue("url"), stringValue("method")}
	default:
		return 0, false, fmt.Errorf("unsupported archived asset type %q", typeName)
	}
	var id int64
	err := tx.QueryRow(query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func remapArchiveAssetReferences(tables map[string]json.RawMessage, mapping map[int64]int64) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(tables))
	for name, raw := range tables {
		out[name] = raw
	}
	for _, table := range []string{"exploration_anchors", "task_asset_links", "finding_assets", "asset_bound_domains", "asset_technologies", "asset_records", "asset_open_ports"} {
		rows, err := decodeArchiveRows(tables[table])
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			old, ok := jsonInt64(row["asset_id"])
			replacement, exists := mapping[old]
			if !ok || !exists {
				return nil, fmt.Errorf("archive %s references missing asset %d", table, old)
			}
			row["asset_id"] = replacement
		}
		out[table], _ = json.Marshal(rows)
	}
	for _, table := range []string{"exploration_nodes"} {
		rows, err := decodeArchiveRows(tables[table])
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			key := "asset_ids"
			container := row
			if table == "exploration_nodes" {
				payload, ok := row["payload"].(map[string]any)
				if !ok {
					continue
				}
				container = payload
			}
			if values, ok := container[key].([]any); ok {
				for i, value := range values {
					if old, ok := jsonInt64(value); ok {
						if replacement, exists := mapping[old]; exists {
							values[i] = replacement
						}
					}
				}
				container[key] = values
			}
		}
		out[table], _ = json.Marshal(rows)
	}
	return out, nil
}

func restoreTaskRelations(tx *sql.Tx, taskID int64, raw json.RawMessage) ([]string, error) {
	rows, err := decodeArchiveRows(raw)
	if err != nil {
		return nil, err
	}
	warnings := []string{}
	for _, row := range rows {
		sourceID, ok := jsonInt64(row["source_task_id"])
		exists, err := liveTaskExists(tx, sourceID)
		if err != nil {
			return nil, err
		}
		if !ok || !exists {
			warnings = append(warnings, fmt.Sprintf("소스 작업 %d를 사용할 수 없어 상속 연결을 건너뛰었습니다", sourceID))
			continue
		}
		created, err := archiveValue(archiveColumn{"created_at", "time"}, row["created_at"])
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO task_relations(task_id,source_task_id,created_at) VALUES($1,$2,COALESCE($3,strftime('%Y-%m-%d %H:%M:%f','now'))) ON CONFLICT DO NOTHING`, taskID, sourceID, created); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}

func restoreTaskScopes(tx *sql.Tx, raw json.RawMessage) ([]string, error) {
	rows, err := decodeArchiveRows(raw)
	if err != nil {
		return nil, err
	}
	warnings := []string{}
	kept := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if companyID, ok := jsonInt64(row["company_id"]); ok && companyID > 0 {
			exists, err := rowExists(tx, "companies", companyID)
			if err != nil {
				return nil, err
			}
			if !exists {
				warnings = append(warnings, fmt.Sprintf("기업 %d가 삭제되어 해당 범위를 건너뛰었습니다", companyID))
				continue
			}
		}
		kept = append(kept, row)
	}
	if len(kept) == 0 {
		return warnings, nil
	}
	for _, row := range kept {
		if _, err := insertArchiveRow(tx, "task_scope", row); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}

func restoreTaskLLMProfiles(tx *sql.Tx, raw json.RawMessage) ([]string, error) {
	rows, err := decodeArchiveRows(raw)
	if err != nil {
		return nil, err
	}
	warnings := []string{}
	position := 0
	for _, row := range rows {
		profileID, ok := jsonInt64(row["profile_id"])
		exists, err := rowExists(tx, "llm_profiles", profileID)
		if err != nil {
			return nil, err
		}
		if !ok || !exists {
			warnings = append(warnings, fmt.Sprintf("LLM 설정 %d가 삭제되어 설정 체인에서 건너뛰었습니다", profileID))
			continue
		}
		taskID, _ := jsonInt64(row["task_id"])
		status, _ := row["status"].(string)
		lastError, _ := row["last_error"].(string)
		exhaustedAt, err := archiveValue(archiveColumn{"exhausted_at", "time"}, row["exhausted_at"])
		if err != nil {
			return nil, err
		}
		createdAt, err := archiveValue(archiveColumn{"created_at", "time"}, row["created_at"])
		if err != nil {
			return nil, err
		}
		updatedAt, err := archiveValue(archiveColumn{"updated_at", "time"}, row["updated_at"])
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO task_llm_profiles(task_id,profile_id,position,status,last_error,exhausted_at,created_at,updated_at)
	VALUES($1,$2,$3,$4,NULLIF($5,''),$6,COALESCE($7,strftime('%Y-%m-%d %H:%M:%f','now')),COALESCE($8,strftime('%Y-%m-%d %H:%M:%f','now')))
	ON CONFLICT DO NOTHING`, taskID, profileID, position, status, lastError,
			exhaustedAt, createdAt, updatedAt); err != nil {
			return nil, err
		}
		position++
	}
	return warnings, nil
}

func restoreInterceptRows(tx *sql.Tx, raw json.RawMessage) error {
	rows, err := decodeArchiveRows(raw)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if status, _ := row["status"].(string); status == "pending" {
			row["status"] = "timeout"
			row["reason"] = "작업 보관 중 승인 대기 시간이 초과되었습니다"
			row["decided_at"] = time.Now().UTC()
			if audit, ok := row["audit"].(map[string]any); ok {
				audit["effective_action"] = "deny"
				audit["decision_reason"] = "작업 보관 중 승인 대기 시간이 초과되었습니다"
				audit["execution_status"] = "not_executed"
			}
		} else if audit, ok := row["audit"].(map[string]any); ok && audit["execution_status"] == "awaiting_result" {
			// An archived run cannot resume its former result callback.
			audit["execution_status"] = "unknown"
		}
	}
	if len(rows) == 0 {
		return nil
	}
	for _, row := range rows {
		if _, err := insertArchiveRow(tx, "intercept_pending", row); err != nil {
			return err
		}
	}
	return nil
}

func liveTaskExists(tx *sql.Tx, id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	var exists bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists)
	return exists, err
}

// CompleteTaskArchiveRestore removes compact metadata after every external
// component has been verified and the package has been consumed.
func (d *DB) CompleteTaskArchiveRestore(archiveID int64) error {
	res, err := d.Exec(`DELETE FROM task_archives WHERE id=$1 AND EXISTS(SELECT 1 FROM tasks task WHERE task_archives.task_id=task.id AND task.deleted_at IS NULL AND task.archived_at IS NULL)`, archiveID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrTaskArchiveState
	}
	return nil
}

// DeleteTaskArchiveStub permanently removes the cold task after its package has
// been staged for deletion. Dependency protection is rechecked transactionally.
func (d *DB) DeleteTaskArchiveStub(archiveID int64) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var taskID, expID int64
	var state string
	if err := tx.QueryRow(`SELECT archive.task_id,task.exploration_id,archive.state
FROM task_archives archive JOIN tasks task ON task.id=archive.task_id
WHERE archive.id=$1`, archiveID).Scan(&taskID, &expID, &state); err != nil {
		return err
	}
	if state != Deleting {
		return ErrTaskArchiveState
	}
	var dependent int64
	err = tx.QueryRow(`SELECT task_id FROM task_archives WHERE id<>$1 AND EXISTS(SELECT 1 FROM task_archive_sources s WHERE s.archive_id=task_archives.id AND s.source_task_id=$2) LIMIT 1`, archiveID, taskID).Scan(&dependent)
	if err == nil {
		return fmt.Errorf("%w: task %d", ErrTaskArchiveDeleteBlocked, dependent)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE id=$1 AND archived_at IS NOT NULL`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM explorations WHERE id=$1`, expID); err != nil {
		return err
	}
	return tx.Commit()
}

// ArchivedAggregateStats returns compact summaries used by global dashboards so
// cold data does not disappear from historical totals.
func (d *DB) ArchivedAggregateStats() ([]json.RawMessage, error) {
	rows, err := d.Query(`SELECT archive.aggregate_stats
FROM task_archives archive
JOIN tasks task ON task.id=archive.task_id
WHERE task.archived_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(jsonColumn(&raw)); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}
