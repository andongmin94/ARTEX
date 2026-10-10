package server

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pgdb "github.com/Autumn-27/artex/db"
	"github.com/klauspost/compress/zstd"
)

func TestTaskArchivePackageFilesRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	taskID := "42"
	explorationID := int64(73)
	taskFile := filepath.Join(workspaceDirectory(dataDir), "tasks", taskID, "uploads", "evidence.txt")
	transcriptFile := filepath.Join(dataDir, "transcripts", "exp73-worker-1.jsonl")
	unrelatedTranscript := filepath.Join(dataDir, "transcripts", "exp74-worker-1.jsonl")
	for path, body := range map[string]string{
		taskFile:            "task evidence",
		transcriptFile:      "transcript",
		unrelatedTranscript: "leave me hot",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	stage, err := stageTaskArchiveFiles(dataDir, 1, taskID, explorationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(taskFile); !os.IsNotExist(err) {
		t.Fatalf("task file still hot after staging: %v", err)
	}
	if _, err := os.Stat(transcriptFile); !os.IsNotExist(err) {
		t.Fatalf("task transcript still hot after staging: %v", err)
	}
	if _, err := os.Stat(unrelatedTranscript); err != nil {
		t.Fatalf("unrelated transcript was staged: %v", err)
	}

	archivePath := taskArchivePath(dataDir, 1, taskID)
	streamPath := filepath.Join(stage.payload, filepath.FromSlash(pgdb.TaskArchiveLLMRecordsPath))
	if err := os.MkdirAll(filepath.Dir(streamPath), archiveDirMode); err != nil {
		t.Fatal(err)
	}
	streamBody := []byte("{\"id\":1,\"raw_request\":\"large\"}\n")
	if err := os.WriteFile(streamPath, streamBody, archiveFileMode); err != nil {
		t.Fatal(err)
	}
	snapshot := &pgdb.TaskArchiveSnapshot{
		FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42, ExplorationID: explorationID,
		StreamedTables: map[string]string{"llm_records": pgdb.TaskArchiveLLMRecordsPath},
	}
	original, compressed, checksum, err := writeTaskArchivePackage(dataDir, archivePath, stage.payload, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if original == 0 || compressed == 0 || checksum == "" {
		t.Fatalf("invalid package metrics original=%d compressed=%d checksum=%q", original, compressed, checksum)
	}
	if err := stage.commit(); err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(dataDir, "restore")
	if err := extractTaskArchivePackage(dataDir, archivePath, checksum, extracted); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(extracted, filepath.FromSlash(pgdb.TaskArchiveLLMRecordsPath))); err != nil || !bytes.Equal(got, streamBody) {
		t.Fatalf("streamed LLM archive payload=%q err=%v", got, err)
	}
	installed, err := installTaskArchiveFiles(dataDir, extracted, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := installed.commit(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{taskFile: "task evidence", transcriptFile: "transcript"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("restored %s = %q, %v; want %q", path, got, err, want)
		}
	}
	if err := extractTaskArchivePackage(dataDir, archivePath, "deadbeef", filepath.Join(dataDir, "bad-checksum")); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
}

func TestTaskArchivePackageRejectsHardLinksToApplicationFiles(t *testing.T) {
	dataDir := t.TempDir()
	secret := filepath.Join(dataDir, "protected-config.txt")
	if err := os.WriteFile(secret, []byte("private application fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Join(workspaceDirectory(dataDir), "tasks", "42")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(taskDir, "linked-config.txt")
	if err := os.Link(secret, linked); err != nil {
		t.Fatal(err)
	}
	stage, err := stageTaskArchiveFiles(dataDir, 1, "42", 73)
	if err != nil {
		t.Fatal(err)
	}
	archive := taskArchivePath(dataDir, 1, "42")
	_, _, _, err = writeTaskArchivePackage(dataDir, archive, stage.payload, &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42})
	if err == nil {
		t.Fatal("application hard-link bytes published in task archive")
	}
	assertPathMissing(t, archive)
	assertPathMissing(t, archive+".partial")
	if err := stage.rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(secret); err != nil || string(got) != "private application fixture" {
		t.Fatalf("protected file changed: %q %v", got, err)
	}
	assertPathExists(t, linked)
}

func TestTaskFileStagingAndRestoreRejectWorkspaceParentJunctions(t *testing.T) {
	dataDir := t.TempDir()
	root, err := openWorkspaceDirectory(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	protected := filepath.Join(dataDir, "evidence", "42", "protected.txt")
	if err := os.MkdirAll(filepath.Dir(protected), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, []byte("preserve managed evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceOutsideLink(t, filepath.Join(workspaceDirectory(dataDir), "tasks"), filepath.Join(dataDir, "evidence"))
	if _, err := stageTaskFiles(dataDir, "42", 73); err == nil {
		t.Fatal("task delete followed workspace parent junction")
	}
	if _, err := stageTaskArchiveFiles(dataDir, 1, "42", 73); err == nil {
		t.Fatal("task archive followed workspace parent junction")
	}
	extracted := filepath.Join(dataDir, "restore")
	source := filepath.Join(extracted, "files", "tasks", "42", "restore.txt")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("restored file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installTaskArchiveFiles(dataDir, extracted, "42", 1); err == nil {
		t.Fatal("task restore followed workspace parent junction")
	}
	if got, err := os.ReadFile(protected); err != nil || string(got) != "preserve managed evidence" {
		t.Fatalf("protected file changed: %q %v", got, err)
	}
	assertPathExists(t, source)
	assertPathMissing(t, filepath.Join(dataDir, "evidence", "42", "restore.txt"))
}

func TestTaskArchivePackageSkipsSymlink(t *testing.T) {
	dataDir := t.TempDir()
	payload := filepath.Join(dataDir, "payload")
	if err := os.MkdirAll(payload, archiveDirMode); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(payload, "keep.txt")
	if err := os.WriteFile(regular, []byte("keep me"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	// 工作目录里出现的符号链接应被跳过，而不是让整个归档失败。
	if err := os.Symlink(regular, filepath.Join(payload, "link.txt")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	archivePath := filepath.Join(dataDir, "archive.tar.zst")
	snapshot := &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42}
	_, _, checksum, err := writeTaskArchivePackage(dataDir, archivePath, payload, snapshot)
	if err != nil {
		t.Fatalf("archive should skip symlink, not fail: %v", err)
	}
	extracted := filepath.Join(dataDir, "restore")
	if err := extractTaskArchivePackage(dataDir, archivePath, checksum, extracted); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(extracted, "keep.txt")); err != nil || string(got) != "keep me" {
		t.Fatalf("regular file not archived: got=%q err=%v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(extracted, "link.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink should have been skipped, but link.txt exists: %v", err)
	}
}

func TestTaskArchiveStageJournalRollsBackInterruptedMoves(t *testing.T) {
	dataDir := t.TempDir()
	taskFile := filepath.Join(workspaceDirectory(dataDir), "tasks", "42", "evidence.txt")
	transcriptFile := filepath.Join(dataDir, "transcripts", "exp73-worker.jsonl")
	for _, path := range []string{taskFile, transcriptFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stage, err := stageTaskArchiveFiles(dataDir, 9, "42", 73)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(stage.root, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal archiveStageJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		t.Fatal(err)
	}
	if len(journal.Moves) != 2 {
		t.Fatalf("journal moves=%d, want 2", len(journal.Moves))
	}
	recovered := &taskArchiveFileStage{dataDir: dataDir, root: stage.root, payload: stage.payload, journal: journal}
	if err := recovered.rollback(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{taskFile, transcriptFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("interrupted archive did not restore %s: %v", path, err)
		}
	}
}

func TestTaskArchiveRestoreJournalRollsBackInterruptedInstall(t *testing.T) {
	dataDir := t.TempDir()
	extracted := filepath.Join(dataDir, "archives", "tasks", ".restore", "11-test")
	source := filepath.Join(extracted, "files", "tasks", "42", "evidence.txt")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := installTaskArchiveFiles(dataDir, extracted, "42", 11)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(workspaceDirectory(dataDir), "tasks", "42", "evidence.txt")
	if _, err := os.Stat(destination); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(extracted, "restore-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal archiveRestoreJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.ArchiveID != 11 || len(journal.Moves) != 1 {
		t.Fatalf("unexpected restore journal: %+v", journal)
	}
	recovered := &taskArchiveRestoreFiles{dataDir: dataDir, extracted: extracted, moves: journal.Moves}
	if err := recovered.rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("hot destination remains after restore rollback: %v", err)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "evidence" {
		t.Fatalf("restore source=%q err=%v", got, err)
	}
}

func TestTaskArchiveDeletePackageCanResumeFromStagedPath(t *testing.T) {
	dataDir := t.TempDir()
	archivePath := taskArchivePath(dataDir, 21, "42")
	if err := os.MkdirAll(filepath.Dir(archivePath), archiveDirMode); err != nil {
		t.Fatal(err)
	}
	staged := archivePath + ".deleting-21"
	if err := os.WriteFile(staged, []byte("archive"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	got, moved, err := stageTaskArchivePackageDelete(dataDir, archivePath, 21)
	if err != nil || !moved || got != staged {
		t.Fatalf("resume staged package path=%q moved=%v err=%v", got, moved, err)
	}
}

func TestTaskArchivePackageRejectsTraversal(t *testing.T) {
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(encoder)
	content := []byte("escape")
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o600, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "malicious.tar.zst")
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDir, "restore")
	if err := extractTaskArchivePackage(dataDir, path, "", root); err == nil {
		t.Fatal("path traversal archive was accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote outside destination: %v", err)
	}
}

func TestTaskArchivePackagePublicationPreservesExistingFiles(t *testing.T) {
	dataDir := t.TempDir()
	payload := filepath.Join(dataDir, "payload")
	if err := os.Mkdir(payload, archiveDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "record.txt"), []byte("new archive payload"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dataDir, "archive.tar.zst")
	protected := filepath.Join(dataDir, "protected.txt")
	if err := os.WriteFile(protected, []byte("existing package bytes"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(protected, archive); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive+".partial", []byte("existing partial bytes"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	snapshot := &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42}
	if _, _, _, err := writeTaskArchivePackage(dataDir, archive, payload, snapshot); err == nil {
		t.Fatal("preexisting archive replaced")
	}
	for path, want := range map[string]string{archive: "existing package bytes", protected: "existing package bytes", archive + ".partial": "existing partial bytes"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("existing file changed %s: %q %v", path, got, err)
		}
	}
	if files, err := filepath.Glob(archive + ".partial-*"); err != nil || len(files) != 0 {
		t.Fatalf("failed archive left temporary files: %v %v", files, err)
	}
	// Snapshot metadata is written directly into the tar stream. A preexisting
	// hard-linked manifest is rejected and never truncated during packaging.
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(protected, filepath.Join(payload, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := writeTaskArchivePackage(dataDir, archive, payload, snapshot); err == nil {
		t.Fatal("duplicate payload manifest accepted")
	}
	if got, err := os.ReadFile(protected); err != nil || string(got) != "existing package bytes" {
		t.Fatalf("manifest truncated another file: %q %v", got, err)
	}
}

func TestTaskArchiveExtractionRejectsIntroducedParentLinks(t *testing.T) {
	dataDir := t.TempDir()
	payload := filepath.Join(dataDir, "payload")
	if err := os.MkdirAll(filepath.Join(payload, "files"), archiveDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "files", "record.txt"), []byte("archive payload"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dataDir, "archive.tar.zst")
	snapshot := &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42}
	_, _, checksum, err := writeTaskArchivePackage(dataDir, archive, payload, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	protected := filepath.Join(outside, "protected.txt")
	if err := os.WriteFile(protected, []byte("keep outside bytes"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dataDir, "restore")
	if err := os.Mkdir(destination, archiveDirMode); err != nil {
		t.Fatal(err)
	}
	workspaceOutsideLink(t, filepath.Join(destination, "files"), outside)
	if err := extractTaskArchivePackage(dataDir, archive, checksum, destination); err == nil {
		t.Fatal("extraction followed introduced child parent link")
	}
	linked := filepath.Join(dataDir, "linked-restore")
	workspaceOutsideLink(t, linked, outside)
	if err := extractTaskArchivePackage(dataDir, archive, checksum, linked); err == nil {
		t.Fatal("extraction followed introduced destination link")
	}
	for _, name := range []string{"record.txt", "manifest.json"} {
		assertPathMissing(t, filepath.Join(outside, name))
	}
	if got, err := os.ReadFile(protected); err != nil || string(got) != "keep outside bytes" {
		t.Fatalf("outside bytes changed: %q %v", got, err)
	}
}
