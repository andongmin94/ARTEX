package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	pgdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/backup"
	"github.com/Autumn-27/artex/traffic"
	"golang.org/x/sys/windows"
)

// Real ACL mutations happen only after successful publication, with the same
// live manager and an independent writer. No user home or global ACL changes.
func TestRunningStorageWindowsACLFailuresPreserveDataAndRecover(t *testing.T) {
	for _, key := range []string{"ARTEX_LLM_PROVIDER", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	data := filepath.Join(home, "data")
	m, err := NewManager(data, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	prepareStabilityEngine(t, m)
	tr, err := traffic.Open(filepath.Join(data, "traffic"), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m.traffic = tr
	s, err := New(t.Context(), m, home, home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateTask("runtime ACL fixture", "local only", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetTaskPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	body := bytes.Repeat([]byte("old binary body\x00"), 22000)
	request, _ := http.NewRequest("POST", "https://runtime-acl.example.test/old", nil)
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}}}
	if err := tr.RecordBrowser(request, []byte("{}"), response, body); err != nil {
		t.Fatal(err)
	}
	var exchange string
	if err := tr.DB().QueryRow(`SELECT id FROM exchanges WHERE url=?`, request.URL.String()).Scan(&exchange); err != nil {
		t.Fatal(err)
	}
	input := pgdb.RecordFindingInput{TaskID: taskID, ExplorationID: task.ExpID, Name: "runtime ACL evidence", VulnClass: "TEST", Severity: "low", Summary: "fixture", Worker: "fixture"}
	store := s.evidenceStore()
	finding, err := store.Record(t.Context(), input, []pgdb.TrafficRef{{TrafficID: exchange, Role: "proof"}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := finding.Traffic.Bindings[0].Snapshot
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	evidenceBody := filepath.Join(store.Dir, "blobs", hash[:2], hash+".bin")
	trafficBody := filepath.Join(data, "traffic", "_blobs", "sha256", hash[:2], hash+".bin")
	payload := filepath.Join(data, "acl-payload")
	if err := os.MkdirAll(payload, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "fixture.txt"), []byte("old archive bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	packagePath := taskArchivePath(data, 910, task.ID)
	archiveSnapshot := &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: taskID}
	if _, _, _, err := writeTaskArchivePackage(data, packagePath, payload, archiveSnapshot); err != nil {
		t.Fatal(err)
	}
	backupParent := t.TempDir()
	oldBackup := filepath.Join(backupParent, "old")
	if _, err := backup.Create(t.Context(), home, oldBackup); err != nil {
		t.Fatal(err)
	}
	protected := map[string][]byte{}
	for _, path := range []string{evidenceBody, trafficBody, packagePath, filepath.Join(oldBackup, "manifest.json")} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		protected[path] = b
	}
	var progress atomic.Int64
	ctx, stop := context.WithCancel(t.Context())
	writer := make(chan error, 1)
	go func() {
		for n := int64(1); ; n++ {
			if err := m.pg.SetSettingsContext(ctx, map[string]string{"acl-live.a": strconv.FormatInt(n, 10), "acl-live.z": strconv.FormatInt(n, 10)}); err != nil {
				if ctx.Err() != nil {
					writer <- nil
				} else {
					writer <- err
				}
				return
			}
			progress.Add(1)
			select {
			case <-ctx.Done():
				writer <- nil
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	writerJoined := false
	defer func() {
		if !writerJoined {
			stop()
			if err := <-writer; err != nil {
				t.Error(err)
			}
		}
	}()
	if err := waitStabilityEngine(t.Context(), func() (bool, error) { return progress.Load() > 0, nil }); err != nil {
		t.Fatal(err)
	}
	t.Run("workspace atomic write", func(t *testing.T) {
		relative := filepath.Join("tasks", task.ID, "acl.txt")
		if err := s.wsSave(relative, bytes.NewBufferString("committed workspace")); err != nil {
			t.Fatal(err)
		}
		absolute := filepath.Join(m.workspaceDir(), relative)
		restore := denyFixtureFileWrites(t, filepath.Dir(absolute))
		assertFixtureCreateDenied(t, filepath.Join(filepath.Dir(absolute), "acl-probe"))
		if err := s.wsSave(relative, bytes.NewBufferString("failed workspace")); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("workspace write denial=%v", err)
		}
		restore()
		if got, err := os.ReadFile(absolute); err != nil || string(got) != "committed workspace" {
			t.Fatalf("restored access lost prior file: %q %v", got, err)
		}
		if err := s.wsSave(relative, bytes.NewBufferString("recovered workspace")); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(absolute); err != nil || string(got) != "recovered workspace" {
			t.Fatalf("workspace recovery=%q %v", got, err)
		}
	})

	t.Run("evidence temporary write", func(t *testing.T) {
		restore := denyFixtureFileWrites(t, filepath.Join(store.Dir, ".staging"))
		assertFixtureCreateDenied(t, filepath.Join(store.Dir, ".staging", "acl-probe"))
		failed, err := store.Record(t.Context(), input, []pgdb.TrafficRef{{TrafficID: exchange, Role: "proof"}})
		if failed != nil || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("record incorrectly published: value=%+v err=%v", failed, err)
		}
		assertRuntimeFindingCount(t, m, 1)
		restore()
		if _, err := store.Record(t.Context(), input, []pgdb.TrafficRef{{TrafficID: exchange, Role: "proof"}}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("evidence publication", func(t *testing.T) {
		directory := filepath.Dir(evidenceBody)
		restore := denyFixtureFileWrites(t, directory)
		assertFixtureCreateDenied(t, filepath.Join(directory, "acl-probe"))
		// Use a new same-bucket body to require a new final publication.
		changed := []byte("new ACL publication")
		var changedHash string
		for n := 0; ; n++ {
			changed = []byte(fmt.Sprintf("new ACL publication %d", n))
			digest := sha256.Sum256(changed)
			changedHash = hex.EncodeToString(digest[:])
			if changedHash[:2] == hash[:2] {
				break
			}
		}
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		if err := root.MkdirAll(filepath.Join("blobs", changedHash[:2]), 0700); err != nil {
			t.Fatal(err)
		}
		if err := root.WriteFile(filepath.Join("blobs", changedHash[:2], changedHash+".bin"), changed, 0600); err != nil {
			t.Fatal(err)
		}
		if err := root.MkdirAll(filepath.Join("blobs", snapshot.ReqHash[:2]), 0700); err != nil {
			t.Fatal(err)
		}
		if err := root.WriteFile(filepath.Join("blobs", snapshot.ReqHash[:2], snapshot.ReqHash+".bin"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		newSnapshot := snapshot
		newSnapshot.RespHash, newSnapshot.RespLen = changedHash, int64(len(changed))
		newSnapshot.ID = pgdb.TrafficSnapshotID(newSnapshot)
		called := false
		err = store.WithInstalledSnapshots(t.Context(), []pgdb.TrafficEvidenceSnapshot{newSnapshot}, root, func(*sql.Tx) error { called = true; return nil })
		if !errors.Is(err, os.ErrPermission) || called {
			t.Fatalf("publication denial=%v metadata callback=%v", err, called)
		}
		restore()
		if err := store.WithInstalledSnapshots(t.Context(), []pgdb.TrafficEvidenceSnapshot{newSnapshot}, root, func(*sql.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("evidence body read", func(t *testing.T) {
		restore := denyFixtureAccess(t, evidenceBody, windows.FILE_READ_DATA)
		if file, _, err := store.OpenBody(snapshot, "response"); file != nil || !errors.Is(err, os.ErrPermission) {
			if file != nil {
				file.Close()
			}
			t.Fatalf("read denial=%v", err)
		}
		restore()
		file, _, err := store.OpenBody(snapshot, "response")
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(file)
		file.Close()
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("read recovery=%v", err)
		}
	})
	t.Run("traffic blob write", func(t *testing.T) {
		restore := denyFixtureFileWrites(t, filepath.Dir(trafficBody))
		assertFixtureCreateDenied(t, filepath.Join(filepath.Dir(trafficBody), "acl-probe"))
		var before, after int
		if err := tr.DB().QueryRow(`SELECT count(*) FROM exchanges`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err := tr.RecordBrowser(request, []byte("{}"), response, body); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("traffic write denial=%v", err)
		}
		if err := tr.DB().QueryRow(`SELECT count(*) FROM exchanges`).Scan(&after); err != nil || after != before {
			t.Fatalf("traffic failed publication changed rows %d/%d %v", before, after, err)
		}
		restore()
		if err := tr.RecordBrowser(request, []byte("{}"), response, body); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("archive output and input", func(t *testing.T) {
		dest := filepath.Join(filepath.Dir(packagePath), "acl-new.tar.zst")
		restore := denyFixtureFileWrites(t, filepath.Dir(packagePath))
		assertFixtureCreateDenied(t, dest)
		if _, _, _, err := writeTaskArchivePackage(data, dest, payload, archiveSnapshot); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("archive write denial=%v", err)
		}
		assertPathMissing(t, dest)
		restore()
		restoreRead := denyFixtureAccess(t, filepath.Join(payload, "fixture.txt"), windows.FILE_READ_DATA)
		if _, _, _, err := writeTaskArchivePackage(data, dest, payload, archiveSnapshot); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("archive input denial=%v", err)
		}
		assertPathMissing(t, dest)
		restoreRead()
		if _, _, _, err := writeTaskArchivePackage(data, dest, payload, archiveSnapshot); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("backup output and input", func(t *testing.T) {
		dest := filepath.Join(backupParent, "new")
		restore := denyFixtureFileWrites(t, backupParent)
		assertFixtureCreateDenied(t, filepath.Join(backupParent, "acl-probe"))
		if _, err := backup.Create(t.Context(), home, dest); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("backup output denial=%v", err)
		}
		assertPathMissing(t, dest)
		restore()
		restoreRead := denyFixtureAccess(t, filepath.Join(home, "config.json"), windows.FILE_READ_DATA)
		if _, err := backup.Create(t.Context(), home, dest); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("backup input denial=%v", err)
		}
		assertPathMissing(t, dest)
		restoreRead()
		if _, err := backup.Create(t.Context(), home, dest); err != nil {
			t.Fatal(err)
		}
		if _, err := backup.Verify(t.Context(), dest); err != nil {
			t.Fatal(err)
		}
	})
	for path, want := range protected {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("existing bytes changed: %s %v", path, err)
		}
	}
	if progress.Load() < 2 {
		t.Errorf("live business writer did not progress: %d", progress.Load())
	}
	stop()
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	writerJoined = true
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewManager(data, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertRuntimeFindingCount(t, reopened, 2)
	var integrity string
	if err := reopened.pg.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q %v", integrity, err)
	}
	rows, err := reopened.pg.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("FK consistency=%v", rows.Err())
	}
	for path, want := range protected {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("reopened bytes changed: %s %v", path, err)
		}
	}
	t.Logf("live Manager OS ACL failures/recovery and previous body/archive/backup bytes preserved; concurrent business commits=%d", progress.Load())
}

func assertFixtureCreateDenied(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if file != nil {
		file.Close()
		os.Remove(path)
	}
	if !os.IsPermission(err) {
		t.Fatalf("actual filesystem ACL probe=%v", err)
	}
}
