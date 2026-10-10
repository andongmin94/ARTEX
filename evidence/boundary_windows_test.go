package evidence

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestEvidenceActiveChildJunctionRejectsRecordReadAndCollection(t *testing.T) {
	s, in, _ := evidenceFixture(t)
	body := []byte("preserve outside evidence fixture")
	hash := evidenceBodyHash(body)
	outside := t.TempDir()
	protected, err := hashPath(outside, hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(protected), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, body, 0o600); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(outside, ".staging", "private-fixture")
	if err := os.MkdirAll(filepath.Dir(stage), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte("preserve original staging bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(946684800, 0)
	for _, path := range []string{protected, stage} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", s.Dir, outside).CombinedOutput()
	if err != nil {
		t.Fatalf("create actual Windows child junction: %v %s", err, output)
	}
	t.Cleanup(func() { os.Remove(s.Dir) })
	seedExchange(t, s, "child-root-fixture", body, false)
	if _, err := s.Record(t.Context(), in, []db.TrafficRef{{TrafficID: "child-root-fixture"}}); err == nil {
		t.Fatal("record followed evidence child junction outside app-data Root")
	}
	var rows int
	if err := s.DB.QueryRow(`SELECT count(*) FROM findings WHERE task_id=?`, in.TaskID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("outside evidence record committed: rows=%d err=%v", rows, err)
	}
	file, _, readErr := s.OpenBody(db.TrafficEvidenceSnapshot{RespHash: hash, RespLen: int64(len(body))}, "response")
	if file != nil {
		file.Close()
	}
	if readErr == nil {
		t.Fatal("body read followed evidence child junction outside app-data Root")
	}
	if err := s.Collect(t.Context(), time.Now().Add(72*time.Hour)); err == nil {
		t.Fatal("collection accepted outside evidence child junction")
	}
	for path, expected := range map[string][]byte{protected: body, stage: []byte("preserve original staging bytes")} {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("outside file changed or removed: %s %q %v", path, actual, err)
		}
		info, err := os.Stat(path)
		if err != nil || !info.ModTime().Equal(old) {
			t.Fatalf("outside file timestamp changed: %s %v %v", path, info, err)
		}
	}
	t.Log("actual Windows evidence child junction rejected record/read/GC; SQLite rows zero and outside old body/staging bytes+mtime preserved")
}
