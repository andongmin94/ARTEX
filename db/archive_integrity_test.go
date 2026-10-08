package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func coldArchiveForIntegrity(t *testing.T, d *DB, task *Task) (*TaskArchive, *TaskArchiveSnapshot) {
	t.Helper()
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	archive, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.SnapshotTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "integrity.tar.zst", "fixture", 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimTaskArchiveJob(t.Context()); err != nil {
		t.Fatal(err)
	}
	return archive, snapshot
}

func assertArchiveRemainsCold(t *testing.T, d *DB, task *Task, archiveID int64) {
	t.Helper()
	if got, err := d.GetTask(task.ID); err != nil || got != nil {
		t.Fatalf("failed restore exposed task: %+v, %v", got, err)
	}
	var nodes int
	if err := d.QueryRow(`SELECT count(*) FROM exploration_nodes WHERE exploration_id=?`, task.ExplorationID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if nodes != 0 {
		t.Fatalf("failed restore left %d graph rows", nodes)
	}
	if archive, err := d.GetTaskArchive(archiveID); err != nil || archive == nil || archive.State != Restoring || archive.Phase == "database_restored" {
		t.Fatalf("failed restore changed archive metadata: %+v, %v", archive, err)
	}
}

func TestTaskArchiveRejectsIncompleteAndCorruptManifest(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("manifest integrity", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	archive, valid := coldArchiveForIntegrity(t, d, task)
	cases := []struct {
		name   string
		mutate func(*TaskArchiveSnapshot)
	}{
		{"missing_table", func(s *TaskArchiveSnapshot) { delete(s.Tables, "activity") }},
		{"unknown_table", func(s *TaskArchiveSnapshot) {
			delete(s.Tables, "activity")
			s.Tables["unknown"] = json.RawMessage("[]")
		}},
		{"missing_count", func(s *TaskArchiveSnapshot) { delete(s.DataCounts, "activity") }},
		{"unknown_count", func(s *TaskArchiveSnapshot) { s.DataCounts["unknown"] = 0 }},
		{"wrong_count", func(s *TaskArchiveSnapshot) { s.DataCounts["exploration_nodes"]++ }},
		{"negative_traffic_count", func(s *TaskArchiveSnapshot) { s.DataCounts["traffic"] = -1 }},
		{"invalid_json", func(s *TaskArchiveSnapshot) { s.Tables["activity"] = json.RawMessage("[") }},
		{"null_table", func(s *TaskArchiveSnapshot) { s.Tables["activity"] = json.RawMessage("null") }},
		{"null_row", func(s *TaskArchiveSnapshot) {
			s.Tables["activity"] = json.RawMessage("[null]")
			s.DataCounts["activity"] = 1
		}},
		{"trailing_json", func(s *TaskArchiveSnapshot) { s.Tables["activity"] = json.RawMessage("[] []") }},
		{"invalid_timestamp", func(s *TaskArchiveSnapshot) {
			rows, err := decodeArchiveRows(s.Tables["tasks"])
			if err != nil {
				t.Fatal(err)
			}
			rows[0]["created_at"] = "invalid"
			s.Tables["tasks"], _ = json.Marshal(rows)
		}},
		{"inline_stream_duplicate", func(s *TaskArchiveSnapshot) {
			s.StreamedTables = map[string]string{"llm_records": TaskArchiveLLMRecordsPath}
			s.Tables["llm_records"] = json.RawMessage(`[{"id":1}]`)
			s.DataCounts["llm_records"] = 1
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(valid)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot TaskArchiveSnapshot
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			test.mutate(&snapshot)
			if _, err := d.RestoreTaskArchive(archive.ID, &snapshot, 0); err == nil {
				t.Fatal("corrupt manifest was accepted")
			}
			assertArchiveRemainsCold(t, d, task, archive.ID)
		})
	}
	// A streamed row-count error happens after the task/graph were written, and
	// must roll the entire restore back just like validation failures.
	valid.StreamedTables = map[string]string{"llm_records": TaskArchiveLLMRecordsPath}
	valid.DataCounts["llm_records"] = 1
	if _, err := d.RestoreTaskArchiveWithLLMRecords(archive.ID, valid, 0, bytes.NewReader(nil)); err == nil {
		t.Fatal("streamed row-count mismatch was accepted")
	}
	assertArchiveRemainsCold(t, d, task, archive.ID)
	valid.StreamedTables = nil
	valid.DataCounts["llm_records"] = 0
	if _, err := d.RestoreTaskArchive(archive.ID, valid, 0); err != nil {
		t.Fatalf("uncorrupted retry: %v", err)
	}
}

func TestTaskArchiveRemapsLargeAssetIDsAndMergesSharedValues(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("large IDs and shared values", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	const oldID int64 = 9007199254740993
	if _, err := d.Exec(`INSERT INTO assets(id,type,domain,icp) VALUES(?,'root_domain','archive.example','archived scalar')`, oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_asset_links(task_id,asset_id,source) VALUES(?,?,'manual')`, task.ID, oldID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO asset_bound_domains VALUES(9007199254740993,0,'old.example')`,
		`INSERT INTO asset_technologies VALUES(9007199254740993,0,'old-tech')`,
		`INSERT INTO asset_records VALUES(9007199254740993,0,'old-record')`,
		`INSERT INTO asset_open_ports VALUES(9007199254740993,0,443,'archived-service','{"archived":true}')`,
		`INSERT INTO asset_open_ports VALUES(9007199254740993,1,8443,'old-extra','{}')`,
	} {
		if _, err := d.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	nodeID, err := d.Exploration(task.ExplorationID).AddNode(KindFact, map[string]any{"summary": "large asset", "asset_ids": []int64{oldID}}, 1, "confirmed", "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Exploration(task.ExplorationID).Anchor(nodeID, oldID); err != nil {
		t.Fatal(err)
	}
	findingID, err := d.AddFinding(task.ID, nodeID, "fixture", "large asset", SeverityLow, "summary", "evidence", "fixture", []int64{oldID})
	if err != nil {
		t.Fatal(err)
	}
	archive, snapshot := coldArchiveForIntegrity(t, d, task)
	if !reflect.DeepEqual(snapshot.ExclusiveAssetIDs, []int64{oldID}) {
		t.Fatalf("large asset ID was rounded: %v", snapshot.ExclusiveAssetIDs)
	}
	other, err := d.CreateTask("current owner", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	currentID, err := d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: "archive.example", ICP: "current scalar", TaskID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO asset_bound_domains VALUES(?,0,'current.example')`,
		`INSERT INTO asset_technologies VALUES(?,0,'current-tech')`,
		`INSERT INTO asset_records VALUES(?,0,'current-record')`,
		`INSERT INTO asset_open_ports VALUES(?,0,443,'current-service','{"current":true}')`,
		`INSERT INTO asset_open_ports VALUES(?,1,80,'http','{}')`,
	} {
		if _, err := d.Exec(statement, currentID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.RestoreTaskArchive(archive.ID, snapshot, 0); err != nil {
		t.Fatal(err)
	}
	for _, relation := range []struct {
		table, column string
		want          []string
	}{
		{"asset_bound_domains", "domain", []string{"current.example", "old.example"}},
		{"asset_technologies", "technology", []string{"current-tech", "old-tech"}},
		{"asset_records", "value", []string{"current-record", "old-record"}},
	} {
		var raw string
		if err := d.QueryRow(`SELECT json_group_array(`+relation.column+`) FROM (SELECT `+relation.column+` FROM `+relation.table+` WHERE asset_id=? ORDER BY position)`, currentID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got []string
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, relation.want) {
			t.Fatalf("%s values=%v want %v", relation.table, got, relation.want)
		}
	}
	var ports, service, extra, icp string
	if err := d.QueryRow(`SELECT json_group_array(port) FROM (SELECT port FROM asset_open_ports WHERE asset_id=? ORDER BY position)`, currentID).Scan(&ports); err != nil {
		t.Fatal(err)
	}
	if ports != "[443,80,8443]" {
		t.Fatalf("merged ports=%s", ports)
	}
	if err := d.QueryRow(`SELECT service,extra FROM asset_open_ports WHERE asset_id=? AND port=443`, currentID).Scan(&service, &extra); err != nil {
		t.Fatal(err)
	}
	if service != "current-service" || extra != `{"current":true}` {
		t.Fatalf("current port metadata changed: %q %q", service, extra)
	}
	if err := d.QueryRow(`SELECT icp FROM assets WHERE id=?`, currentID).Scan(&icp); err != nil {
		t.Fatal(err)
	}
	if icp != "current scalar" {
		t.Fatalf("current scalar changed: %s", icp)
	}
	var references int
	if err := d.QueryRow(`SELECT (SELECT count(*) FROM exploration_anchors WHERE node_id=?1 AND asset_id=?2)+(SELECT count(*) FROM task_asset_links WHERE task_id=?3 AND asset_id=?2)+(SELECT count(*) FROM finding_assets WHERE finding_id=?4 AND asset_id=?2)`, nodeID, currentID, task.ID, findingID).Scan(&references); err != nil {
		t.Fatal(err)
	}
	if references != 3 {
		t.Fatalf("remapped relation count=%d", references)
	}
	var payload string
	if err := d.QueryRow(`SELECT payload FROM exploration_nodes WHERE id=?`, nodeID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, fmt.Sprint(currentID)) {
		t.Fatalf("node asset references not remapped: %s", payload)
	}
}

func TestArchiveEvidenceSnapshotsRejectMalformedTable(t *testing.T) {
	for _, snapshot := range []*TaskArchiveSnapshot{
		nil,
		{FormatVersion: 3, Tables: map[string]json.RawMessage{"traffic_evidence_snapshots": json.RawMessage("[]")}},
		{FormatVersion: TaskArchiveFormatVersion},
		{FormatVersion: TaskArchiveFormatVersion, Tables: map[string]json.RawMessage{"traffic_evidence_snapshots": json.RawMessage("[")}},
		{FormatVersion: TaskArchiveFormatVersion, Tables: map[string]json.RawMessage{"traffic_evidence_snapshots": json.RawMessage("null")}},
		{FormatVersion: TaskArchiveFormatVersion, Tables: map[string]json.RawMessage{"traffic_evidence_snapshots": json.RawMessage("[] []")}},
	} {
		if _, err := ArchiveEvidenceSnapshots(snapshot); err == nil {
			t.Fatalf("accepted malformed snapshot: %+v", snapshot)
		}
	}
	if got, err := ArchiveEvidenceSnapshots(&TaskArchiveSnapshot{FormatVersion: TaskArchiveFormatVersion, Tables: map[string]json.RawMessage{"traffic_evidence_snapshots": json.RawMessage("[]")}}); err != nil || len(got) != 0 {
		t.Fatalf("empty evidence=%v, %v", got, err)
	}
}
