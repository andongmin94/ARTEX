package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func ArchiveEvidenceSnapshots(snapshot *TaskArchiveSnapshot) ([]TrafficEvidenceSnapshot, error) {
	if snapshot == nil || !IsTaskArchiveFormatSupported(snapshot.FormatVersion) {
		return nil, ErrTaskArchiveFormatMismatch
	}
	raw, present := snapshot.Tables["traffic_evidence_snapshots"]
	if !present || len(raw) == 0 {
		return nil, errors.New("archive evidence snapshot table is missing")
	}
	if _, err := decodeArchiveRows(raw); err != nil {
		return nil, err
	}
	var out []TrafficEvidenceSnapshot
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func restoreFindingTrafficTx(tx *sql.Tx, snapshot *TaskArchiveSnapshot) error {
	snapshots, err := ArchiveEvidenceSnapshots(snapshot)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, v := range snapshots {
		if allowed[v.ID] {
			return errors.New("duplicate archived evidence snapshot")
		}
		allowed[v.ID] = true
		if err = InsertEvidenceSnapshotTx(tx, v); err != nil {
			return err
		}
	}
	rows, err := decodeArchiveRows(snapshot.Tables["finding_traffic_bindings"])
	if err != nil {
		return err
	}
	for _, row := range rows {
		fid, ok := jsonInt64(row["finding_id"])
		if !ok {
			return errors.New("invalid archived evidence finding id")
		}
		sid, _ := row["snapshot_id"].(string)
		role, _ := row["role"].(string)
		if !allowed[sid] || !ValidTrafficRole(role) {
			return errors.New("invalid archived evidence binding")
		}
		var owned bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM findings WHERE id=$1 AND task_id=$2)`, fid, snapshot.TaskID).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("evidence references finding outside archived task: %d", fid)
		}
	}
	if len(rows) > 0 {
		for _, row := range rows {
			if _, err = insertArchiveRow(tx, "finding_traffic_bindings", row); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`UPDATE traffic_evidence_snapshots AS s SET unreferenced_at=NULL WHERE EXISTS(SELECT 1 FROM finding_traffic_bindings b WHERE b.snapshot_id=s.id)`)
	}
	return err
}
