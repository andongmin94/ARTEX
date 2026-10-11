package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pgdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/evidence"
	"github.com/Autumn-27/artex/traffic"
)

type storageCrashCheckpoint struct {
	Mode       string
	TaskID     int64
	ArchiveID  int64
	ExchangeID string
	Snapshot   pgdb.TrafficEvidenceSnapshot
}

// The helper is inert during ordinary package execution. Its parent owns the
// exact process and disposable home; no production hook or external target is
// involved. Process.Kill tests abrupt exit, not device failure or power loss.
func TestStorageCrashHelper(t *testing.T) {
	home := os.Getenv("ARTEX_TEST_CRASH_HOME")
	if home == "" {
		return
	}
	mode := os.Getenv("ARTEX_TEST_CRASH_MODE")
	data := filepath.Join(home, "data")
	m, err := NewManager(data, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	prepareStabilityEngine(t, m)
	if _, err := m.pg.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.SetSettingsContext(t.Context(), map[string]string{"crash.a": "committed", "crash.z": "committed"}); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateTask("crash recovery fixture", "local only", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetTaskPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	checkpoint := storageCrashCheckpoint{Mode: mode, TaskID: taskID}
	checkpointAndWait := func() {
		raw, err := json.Marshal(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		temporary := filepath.Join(home, "checkpoint.partial")
		file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(raw)
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temporary, filepath.Join(home, "checkpoint.json")); err != nil {
			t.Fatal(err)
		}
		// The parent kills only this child after reading the complete synced
		// checkpoint. Normal cleanup cannot accidentally stand in for a crash.
		<-t.Context().Done()
		t.Fatal("parent did not terminate checkpoint process")
	}
	if mode == "business transaction" {
		tx, err := m.pg.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE settings SET value=? WHERE key IN ('crash.a','crash.z')`, strings.Repeat("uncommitted", 500000)); err != nil {
			t.Fatal(err)
		}
		checkpointAndWait()
		return
	}
	if mode == "traffic transaction" {
		tr, err := traffic.Open(filepath.Join(data, "traffic"), "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer tr.Close()
		request, _ := http.NewRequest("POST", "https://crash.example.test/traffic", nil)
		body := storageCrashBody()
		if err := tr.RecordBrowser(request, []byte("{}"), &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}}}, body); err != nil {
			t.Fatal(err)
		}
		if err := tr.DB().QueryRow(`SELECT id FROM exchanges LIMIT 1`).Scan(&checkpoint.ExchangeID); err != nil {
			t.Fatal(err)
		}
		tx, err := tr.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE exchange_bodies SET resp_head='uncommitted' WHERE id=?`, checkpoint.ExchangeID); err != nil {
			t.Fatal(err)
		}
		checkpointAndWait()
		return
	}
	if mode == "evidence publication" {
		store := evidence.New(m.pg, nil, filepath.Join(data, "evidence"))
		source, snapshot := storageCrashEvidenceSource(t, home)
		defer source.Close()
		checkpoint.Snapshot = snapshot
		if err := store.WithInstalledSnapshots(t.Context(), []pgdb.TrafficEvidenceSnapshot{snapshot}, source, func(tx *sql.Tx) error {
			_, err := pgdb.RecordFindingTx(t.Context(), tx, storageCrashFindingInput(taskID, task.ExpID), []pgdb.PreparedTrafficEvidence{{Ref: pgdb.TrafficRef{TrafficID: snapshot.SourceTrafficID, Role: "proof"}, Snapshot: snapshot}})
			if err != nil {
				return err
			}
			checkpointAndWait()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	workspace := filepath.Join("tasks", task.ID, "한글.txt")
	if err := m.workRoot.MkdirAll(filepath.Dir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.workRoot.WriteFile(workspace, []byte("committed workspace bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(data, "transcripts", fmt.Sprintf("exp%d-fixture.jsonl", task.ExpID))
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte("committed transcript bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	archive, err := m.pg.QueueTaskArchive(taskID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.ArchiveID = archive.ID
	job, err := m.pg.ClaimTaskArchiveJob(t.Context())
	if err != nil || job == nil {
		t.Fatalf("claim=%+v %v", job, err)
	}
	stage, err := stageTaskArchiveFiles(data, archive.ID, task.ID, task.ExpID)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "archive before commit" {
		checkpointAndWait()
		return
	}
	snapshot, err := m.pg.SnapshotTaskArchive(taskID)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := taskArchivePath(data, archive.ID, task.ID)
	original, compressed, digest, err := writeTaskArchivePackage(data, archivePath, stage.payload, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pg.CompleteTaskArchive(archive.ID, snapshot, archivePath, digest, original, compressed); err != nil {
		t.Fatal(err)
	}
	if mode == "archive after commit" {
		checkpointAndWait()
		return
	}
	if err := stage.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	job, err = m.pg.ClaimTaskArchiveJob(t.Context())
	if err != nil || job == nil {
		t.Fatalf("restore claim=%+v %v", job, err)
	}
	extracted := filepath.Join(taskArchiveRoot(data), ".restore", fmt.Sprintf("%d-crash", archive.ID))
	if err := extractTaskArchivePackage(data, archivePath, digest, extracted); err != nil {
		t.Fatal(err)
	}
	if _, err := installTaskArchiveFiles(data, extracted, task.ID, archive.ID); err != nil {
		t.Fatal(err)
	}
	if mode == "restore before commit" {
		checkpointAndWait()
		return
	}
	if mode != "restore after commit" {
		t.Fatalf("unknown helper mode: %s", mode)
	}
	if _, err := m.pg.RestoreTaskArchive(archive.ID, snapshot, 0); err != nil {
		t.Fatal(err)
	}
	checkpointAndWait()
}

func TestAbruptStorageProcessExitRecoversCommittedData(t *testing.T) {
	for _, key := range []string{"ARTEX_LLM_PROVIDER", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
	for _, mode := range []string{"business transaction", "traffic transaction", "evidence publication", "archive before commit", "archive after commit", "restore before commit", "restore after commit"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			checkpoint := runStorageCrashChild(t, home, mode)
			data := filepath.Join(home, "data")
			m, err := NewManager(data, "")
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			values, err := m.pg.SettingsSnapshot(t.Context())
			if err != nil || values["crash.a"] != "committed" || values["crash.z"] != "committed" {
				t.Fatalf("uncommitted settings survived: %v %v", values, err)
			}
			if mode == "traffic transaction" {
				tr, err := traffic.Open(filepath.Join(data, "traffic"), "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer tr.Close()
				var head string
				if err := tr.DB().QueryRow(`SELECT resp_head FROM exchange_bodies WHERE id=?`, checkpoint.ExchangeID).Scan(&head); err != nil || head == "uncommitted" {
					t.Fatalf("traffic transaction not rolled back: %q %v", head, err)
				}
				if err := tr.ReadEvidence(t.Context(), []string{checkpoint.ExchangeID}, func(v traffic.EvidenceExchange) error {
					got, err := io.ReadAll(v.Response)
					if err == nil && !bytes.Equal(got, storageCrashBody()) {
						return errors.New("committed traffic body changed")
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				assertStorageCrashSQLite(t, tr.DB())
			}
			if mode == "evidence publication" {
				assertRuntimeFindingCount(t, m, 0)
				var snapshots int
				if err := m.pg.QueryRow(`SELECT count(*) FROM traffic_evidence_snapshots`).Scan(&snapshots); err != nil || snapshots != 0 {
					t.Fatalf("uncommitted metadata remained: %d %v", snapshots, err)
				}
				store := evidence.New(m.pg, nil, filepath.Join(data, "evidence"))
				file, _, err := store.OpenBody(checkpoint.Snapshot, "response")
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(file)
				file.Close()
				if err != nil || !bytes.Equal(got, storageCrashBody()) {
					t.Fatalf("published evidence was lost: %v", err)
				}
				source, err := os.OpenRoot(filepath.Join(home, "source-evidence"))
				if err != nil {
					t.Fatal(err)
				}
				defer source.Close()
				task, err := m.pg.GetTask(checkpoint.TaskID)
				if err != nil || task == nil {
					t.Fatalf("task=%+v %v", task, err)
				}
				if err := store.WithInstalledSnapshots(t.Context(), []pgdb.TrafficEvidenceSnapshot{checkpoint.Snapshot}, source, func(tx *sql.Tx) error {
					_, err := pgdb.RecordFindingTx(t.Context(), tx, storageCrashFindingInput(task.ID, task.ExplorationID), []pgdb.PreparedTrafficEvidence{{Ref: pgdb.TrafficRef{TrafficID: checkpoint.Snapshot.SourceTrafficID, Role: "proof"}, Snapshot: checkpoint.Snapshot}})
					return err
				}); err != nil {
					t.Fatal(err)
				}
				assertRuntimeFindingCount(t, m, 1)
			}
			if strings.HasPrefix(mode, "archive ") || strings.HasPrefix(mode, "restore ") {
				s, err := New(t.Context(), m, home, home, home)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close(context.Background())
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				if mode == "archive before commit" {
					failed, err := m.pg.GetTaskArchive(checkpoint.ArchiveID)
					if err != nil || failed == nil || failed.State != pgdb.ArchiveFailed {
						t.Fatalf("interrupted archive not marked failed: %+v %v", failed, err)
					}
					relative := filepath.Join("tasks", strconv.FormatInt(checkpoint.TaskID, 10), "한글.txt")
					if got, err := m.workRoot.ReadFile(relative); err != nil || string(got) != "committed workspace bytes" {
						t.Fatalf("startup rollback lost source: %q %v", got, err)
					}
					if _, err := m.pg.QueueTaskArchive(checkpoint.TaskID); err != nil {
						t.Fatal(err)
					}
					s.notifyTaskArchiveWorker()
				}
				if strings.HasPrefix(mode, "archive ") {
					if err := waitStabilityEngine(ctx, func() (bool, error) {
						a, err := m.pg.GetTaskArchive(checkpoint.ArchiveID)
						if err != nil {
							return false, err
						}
						if a != nil && a.State == pgdb.ArchiveFailed {
							return false, fmt.Errorf("retry failed: %+v", a)
						}
						return a != nil && a.State == pgdb.ArchiveReady, nil
					}); err != nil {
						t.Fatal(err)
					}
					if _, err := m.pg.QueueTaskArchiveRestore(checkpoint.ArchiveID); err != nil {
						t.Fatal(err)
					}
					s.notifyTaskArchiveWorker()
				}
				if err := waitStabilityEngine(ctx, func() (bool, error) {
					a, err := m.pg.GetTaskArchive(checkpoint.ArchiveID)
					if err != nil {
						return false, err
					}
					if a != nil && a.State == pgdb.RestoreFailed {
						return false, fmt.Errorf("restore retry failed: %+v", a)
					}
					return a == nil, nil
				}); err != nil {
					t.Fatal(err)
				}
				relative := filepath.Join("tasks", strconv.FormatInt(checkpoint.TaskID, 10), "한글.txt")
				if got, err := m.workRoot.ReadFile(relative); err != nil || string(got) != "committed workspace bytes" {
					t.Fatalf("workspace recovery=%q %v", got, err)
				}
				task, err := m.pg.GetTask(checkpoint.TaskID)
				if err != nil || task == nil {
					t.Fatalf("restored task=%+v %v", task, err)
				}
				path := filepath.Join(data, "transcripts", fmt.Sprintf("exp%d-fixture.jsonl", task.ExplorationID))
				if got, err := os.ReadFile(path); err != nil || string(got) != "committed transcript bytes" {
					t.Fatalf("transcript recovery=%q %v", got, err)
				}
				for _, name := range []string{".staging", ".restore"} {
					entries, err := os.ReadDir(filepath.Join(taskArchiveRoot(data), name))
					if err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					if len(entries) != 0 {
						t.Fatalf("unresolved recovery stage %s: %d", name, len(entries))
					}
				}
			}
			assertStorageCrashSQLite(t, m.pg.DB)
			t.Log("exact test child forcibly terminated at synced checkpoint; actual reopening/recovery and preserved data verified")
		})
	}
}

func runStorageCrashChild(t *testing.T, home, mode string) storageCrashCheckpoint {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStorageCrashHelper$", "-test.v", "-test.timeout=30s")
	command.Env = append(os.Environ(), "ARTEX_TEST_CRASH_HOME="+home, "ARTEX_TEST_CRASH_MODE="+mode)
	logPath := filepath.Join(home, "child.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	joined := false
	defer func() {
		if !joined {
			command.Process.Kill()
			<-done
		}
	}()
	var checkpoint storageCrashCheckpoint
	if err := waitStabilityEngine(ctx, func() (bool, error) {
		raw, err := os.ReadFile(filepath.Join(home, "checkpoint.json"))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal(raw, &checkpoint); err != nil {
			return false, nil
		}
		return checkpoint.Mode == mode, nil
	}); err != nil {
		raw, _ := os.ReadFile(logPath)
		t.Fatalf("checkpoint failed: %v\n%s", err, raw)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	exitErr := <-done
	joined = true
	if exitErr == nil {
		t.Fatal("checkpoint process exited normally")
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	childOutput, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(childOutput, []byte("WARNING: DATA RACE")) {
		t.Fatalf("child race failure:\n%s", childOutput)
	}
	t.Logf("abrupt child exit checkpoint=%s pid=%d", mode, command.Process.Pid)
	return checkpoint
}

func storageCrashBody() []byte { return bytes.Repeat([]byte{0, 1, 255, 'k'}, 100000) }

func storageCrashFindingInput(taskID, expID int64) pgdb.RecordFindingInput {
	return pgdb.RecordFindingInput{TaskID: taskID, ExplorationID: expID, Name: "crash fixture", VulnClass: "TEST", Severity: "low", Summary: "local fixture", Worker: "fixture"}
}

func storageCrashEvidenceSource(t *testing.T, home string) (*os.Root, pgdb.TrafficEvidenceSnapshot) {
	t.Helper()
	dir := filepath.Join(home, "source-evidence")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	hashes := make([]string, 2)
	for i, body := range [][]byte{[]byte("{}"), storageCrashBody()} {
		sum := sha256.Sum256(body)
		hashes[i] = hex.EncodeToString(sum[:])
		if err := root.MkdirAll(filepath.Join("blobs", hashes[i][:2]), 0700); err != nil {
			t.Fatal(err)
		}
		if err := root.WriteFile(filepath.Join("blobs", hashes[i][:2], hashes[i]+".bin"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := pgdb.TrafficEvidenceSnapshot{SourceTrafficID: "local-crash-evidence", CapturedAt: time.Now().Unix(), URL: "https://crash.example.test/evidence", Method: "POST", Status: 200, ContentType: "application/octet-stream", ReqHash: hashes[0], RespHash: hashes[1], ReqLen: 2, RespLen: int64(len(storageCrashBody()))}
	snapshot = snapshot.Normalize()
	snapshot.ID = pgdb.TrafficSnapshotID(snapshot)
	return root, snapshot
}

func assertStorageCrashSQLite(t *testing.T, db *sql.DB) {
	t.Helper()
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q %v", integrity, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("FK integrity=%v", rows.Err())
	}
}

func assertRuntimeFindingCount(t *testing.T, m *Manager, want int) {
	t.Helper()
	var got int
	if err := m.pg.QueryRow(`SELECT count(*) FROM findings`).Scan(&got); err != nil || got != want {
		t.Fatalf("finding rows=%d want=%d err=%v", got, want, err)
	}
}
