package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/sqlitedb"
)

func fixture(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "한글 홈 # 100%")
	if err := os.MkdirAll(filepath.Join(home, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(home, "data", "artex.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetSetting("backup-test", "저장된 값"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for p, b := range map[string]string{"config.json": "{}", "jwt.key": strings.Repeat("k", 32), "skills/user.md": "사용자 스킬", "chatgpt/credentials": "protected fixture", "data/tasks/task/transcript.txt": "작업 기록", "logs/excluded": "not included", "tools/excluded": "not included"} {
		target := filepath.Join(home, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestSnapshotAndNewHomeRestore(t *testing.T) {
	home := fixture(t)
	dest := filepath.Join(t.TempDir(), "일관된 백업 # %")
	r, err := Create(context.Background(), home, dest)
	if err != nil {
		t.Fatal(err)
	}
	if r.Event != "backup-complete" || r.Files < 5 || r.Bytes == 0 {
		t.Fatalf("result=%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dest, "payload", "logs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logs included: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "payload", "tools")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tools included: %v", err)
	}
	if _, err := Verify(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "새 복원 홈 # %")
	r, err = Restore(context.Background(), dest, restored)
	if err != nil {
		t.Fatal(err)
	}
	if r.Event != "restore-complete" {
		t.Fatalf("result=%+v", r)
	}
	d, err := db.Open(filepath.Join(restored, "data", "artex.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	value, _, err := d.GetSetting("backup-test")
	if err != nil || value != "저장된 값" {
		t.Fatalf("setting=%q %v", value, err)
	}
	for p, want := range map[string]string{"jwt.key": strings.Repeat("k", 32), "skills/user.md": "사용자 스킬", "chatgpt/credentials": "protected fixture", "data/tasks/task/transcript.txt": "작업 기록"} {
		b, err := os.ReadFile(filepath.Join(restored, filepath.FromSlash(p)))
		if err != nil || string(b) != want {
			t.Fatalf("restore %s: %q %v", p, b, err)
		}
	}
}

func TestNativeSnapshotIncludesCommittedWAL(t *testing.T) {
	home := fixture(t)
	p := filepath.Join(home, "data", "artex.sqlite")
	d, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSetting("in-wal", "committed later"); err != nil {
		t.Fatal(err)
	}
	wal, err := os.Stat(p + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("WAL not exercised: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "wal.sqlite")
	if err := snapshotSQLite(context.Background(), p, dest, true); err != nil {
		t.Fatal(err)
	}
	pool, err := sql.Open("sqlite", sqliteURI(dest, "ro", true))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var value string
	if err := pool.QueryRow("SELECT value FROM settings WHERE key='in-wal'").Scan(&value); err != nil || value != "committed later" {
		t.Fatalf("WAL snapshot=%q %v", value, err)
	}
	if _, err := os.Stat(dest + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot has WAL: %v", err)
	}
}

func TestHomeLockExcludesSnapshotAndReleasesOnClose(t *testing.T) {
	home := fixture(t)
	lease, err := LockHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := LockHome(home); err == nil {
		second.Close()
		t.Fatal("second backend acquired lock")
	}
	dest := filepath.Join(t.TempDir(), "blocked")
	if _, err := Create(context.Background(), home, dest); err == nil {
		t.Fatal("live home snapshot accepted")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed snapshot visible: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), home, dest); err != nil {
		t.Fatal(err)
	}
}

func TestExistingPathsAndContainedTargetsArePreserved(t *testing.T) {
	home := fixture(t)
	existing := t.TempDir()
	marker := filepath.Join(existing, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{existing, home, filepath.Join(home, "backups", "new"), "relative"} {
		if _, err := Create(context.Background(), home, dest); err == nil {
			t.Fatalf("accepted %s", dest)
		}
	}
	fullHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), home, filepath.Join(fullHome, "data", "nested-backup")); err == nil {
		t.Fatal("equivalent long/short home alias accepted")
	}
	dest := filepath.Join(t.TempDir(), "good")
	if _, err := Create(context.Background(), home, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), dest, existing); err == nil {
		t.Fatal("restore replaced existing home")
	}
	b, err := os.ReadFile(marker)
	if err != nil || string(b) != "keep" {
		t.Fatalf("existing data changed: %q %v", b, err)
	}
}

func TestCorruptMissingUnexpectedAndEscapingFilesAreRejected(t *testing.T) {
	for _, kind := range []string{"corrupt", "missing", "extra", "escape", "duplicate", "sqlite-corrupt", "schema-missing", "sqlite-sidecar"} {
		t.Run(kind, func(t *testing.T) {
			home := fixture(t)
			dest := filepath.Join(t.TempDir(), "backup")
			if _, err := Create(context.Background(), home, dest); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dest, "payload", "skills", "user.md")
			switch kind {
			case "corrupt":
				if err := os.WriteFile(p, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.WriteFile(filepath.Join(dest, "payload", "skills", "extra"), []byte("extra"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				m, err := readManifest(dest)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "escape" {
					m.Entries = append(m.Entries, entry{Path: "data/../../escaped", Directory: true})
				} else if kind == "duplicate" {
					m.Entries = append(m.Entries, m.Entries[0])
				} else if kind == "sqlite-sidecar" {
					p := filepath.Join(dest, "payload", "data", "artex.sqlite-wal")
					if err := os.WriteFile(p, []byte("malicious WAL"), 0600); err != nil {
						t.Fatal(err)
					}
					size, sum, err := digest(context.Background(), p)
					if err != nil {
						t.Fatal(err)
					}
					m.Entries = append(m.Entries, entry{Path: "data/artex.sqlite-wal", Size: size, SHA256: sum})
				} else {
					bad := filepath.Join(dest, "payload", "data", "artex.sqlite")
					if kind == "schema-missing" {
						pool, err := sql.Open("sqlite", sqliteURI(bad, "rw", false))
						if err != nil {
							t.Fatal(err)
						}
						if _, err := pool.Exec("DROP TABLE server_logs"); err != nil {
							pool.Close()
							t.Fatal(err)
						}
						if err := pool.Close(); err != nil {
							t.Fatal(err)
						}
					} else if err := os.WriteFile(bad, []byte("not sqlite"), 0600); err != nil {
						t.Fatal(err)
					}
					for i := range m.Entries {
						if m.Entries[i].Path == "data/artex.sqlite" {
							m.Entries[i].Size, m.Entries[i].SHA256, err = digest(context.Background(), bad)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				b, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dest, manifestName), b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Verify(context.Background(), dest); err == nil {
				t.Fatal("damaged backup verified")
			}
			restore := filepath.Join(t.TempDir(), "new-home")
			if _, err := Restore(context.Background(), dest, restore); err == nil {
				t.Fatal("damaged backup restored")
			}
			if _, err := os.Stat(restore); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed restore visible: %v", err)
			}
		})
	}
}

func TestReplacedManifestCannotIntroduceUnvalidatedPaths(t *testing.T) {
	home := fixture(t)
	dest := filepath.Join(t.TempDir(), "backup")
	if _, err := Create(context.Background(), home, dest); err != nil {
		t.Fatal(err)
	}
	verified, err := readManifest(dest)
	if err != nil {
		t.Fatal(err)
	}
	replacement := verified
	replacement.Entries = append(append([]entry(nil), verified.Entries...), entry{Path: "data/../../escaped", Directory: true})
	b, err := json.Marshal(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, manifestName), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(dest); err == nil {
		t.Fatal("manifest reread accepted an escaping path")
	}
	// An operation that already parsed a manifest verifies only those entries;
	// the replaced manifest never supplies new restoration paths to it.
	if _, err := verifyManifest(context.Background(), dest, verified); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "new-home")
	if _, err := Restore(context.Background(), dest, restored); err == nil {
		t.Fatal("new restore accepted the replaced manifest")
	}
	if _, err := os.Stat(restored); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed restore visible: %v", err)
	}
}

func TestCancelledAndWriteFailureDoNotPublish(t *testing.T) {
	home := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dest := filepath.Join(t.TempDir(), "cancelled")
	if _, err := Create(ctx, home, dest); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled output exists")
	}
	parentFile := filepath.Join(t.TempDir(), "not-directory")
	if err := os.WriteFile(parentFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), home, filepath.Join(parentFile, "snapshot")); err == nil {
		t.Fatal("write failure hidden")
	}
	if err := copyFile(context.Background(), filepath.Join(home, "skills", "user.md"), parentFile, false); err == nil {
		t.Fatal("existing file overwritten")
	}
	b, err := os.ReadFile(parentFile)
	if err != nil || string(b) != "keep" {
		t.Fatalf("write error changed data: %v", err)
	}
}

func TestEvidenceAndTrafficReferencesMustMatchBodies(t *testing.T) {
	home := fixture(t)
	body := []byte("immutable body")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	p := filepath.Join(home, "data", "evidence", "blobs", hash[:2], hash+".bin")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0600); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(home, "data", "artex.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO traffic_evidence_snapshots(id,source_traffic_id,captured_at,url,method,status,req_head,resp_head,req_hash,resp_hash,req_len,resp_len) VALUES('s','source',1,'https://example.invalid','GET',200,'','',?1,?1,?2,?2)`, hash, len(body)); err != nil {
		t.Fatal(err)
	}
	d.Close()
	indexPath := filepath.Join(home, "data", "traffic", "_index", "index.sqlite")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0700); err != nil {
		t.Fatal(err)
	}
	index, err := sqlitedb.Open(context.Background(), indexPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`CREATE TABLE exchanges(id TEXT PRIMARY KEY,req_len INTEGER,resp_len INTEGER)`, `CREATE TABLE exchange_bodies(id TEXT PRIMARY KEY,req_blob TEXT,resp_blob TEXT)`} {
		if _, err := index.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := index.Exec(`INSERT INTO exchanges VALUES('e',?1,?1)`, len(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Exec(`INSERT INTO exchange_bodies VALUES('e',?1,?1)`, hash); err != nil {
		t.Fatal(err)
	}
	index.Close()
	trafficBody := filepath.Join(home, "data", "traffic", "_blobs", "sha256", hash[:2], hash+".bin")
	if err := os.MkdirAll(filepath.Dir(trafficBody), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trafficBody, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), home, filepath.Join(t.TempDir(), "valid")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trafficBody, []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "wrong-traffic")
	if _, err := Create(context.Background(), home, dest); err == nil {
		t.Fatal("traffic inconsistency accepted")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid snapshot published")
	}
	if err := os.WriteFile(trafficBody, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), home, filepath.Join(t.TempDir(), "missing-evidence")); err == nil {
		t.Fatal("missing evidence accepted")
	}
}

func TestLinksAndExternalSkillsAreRejected(t *testing.T) {
	home := fixture(t)
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"skill_dir":"outside"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), home, filepath.Join(t.TempDir(), "external")); err == nil || !strings.Contains(err.Error(), "스킬") {
		t.Fatalf("external config=%v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "skills", "linked")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("OS symlink permission: %v", err)
	}
	if _, err := Create(context.Background(), home, filepath.Join(t.TempDir(), "link")); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestPublicationRefusesConcurrentDestination(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "new")
	stage, err := newStage(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "staged"), []byte("snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "keep"), []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publish(stage, destination); err == nil {
		t.Fatal("concurrently created destination replaced")
	}
	b, err := os.ReadFile(filepath.Join(destination, "keep"))
	if err != nil || string(b) != "user" {
		t.Fatalf("concurrent data changed: %q %v", b, err)
	}
}
