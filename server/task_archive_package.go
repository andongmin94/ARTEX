package server

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pgdb "github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

const (
	archiveDirMode  = 0o700
	archiveFileMode = 0o600
	maxArchiveFiles = 1_000_000
	maxArchiveBytes = int64(1 << 47) // 128 TiB safety ceiling for corrupt headers.
)

type archiveFileMove struct {
	Source   string `json:"source"`
	Relative string `json:"relative"`
}

type archiveStageJournal struct {
	ArchiveID int64             `json:"archive_id"`
	TaskID    string            `json:"task_id"`
	Moves     []archiveFileMove `json:"moves"`
}

type archiveRestoreJournal struct {
	ArchiveID int64                 `json:"archive_id"`
	TaskID    int64                 `json:"task_id"`
	Moves     []restoredArchivePath `json:"moves"`
}

type taskArchiveFileStage struct {
	dataDir string
	root    string
	payload string
	journal archiveStageJournal
	done    bool
}

type restoredArchivePath struct {
	Source      string
	Destination string
}

type taskArchiveRestoreFiles struct {
	dataDir   string
	extracted string
	moves     []restoredArchivePath
	done      bool
}

func taskArchiveRoot(dataDir string) string {
	return filepath.Join(dataDir, "archives", "tasks")
}

func taskArchivePath(dataDir string, archiveID int64, taskID string) string {
	return filepath.Join(taskArchiveRoot(dataDir), fmt.Sprintf("task-%s-%d.tar.zst", taskID, archiveID))
}

func stageTaskArchiveFiles(dataDir string, archiveID int64, taskID string, explorationID int64) (*taskArchiveFileStage, error) {
	if err := validateWorkspaceArtifactPath(dataDir, filepath.Join("tasks", taskID)); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "tasks", taskID)); err == nil {
		return nil, errors.New("이전 작업 폴더가 남아 있어 파일을 빠뜨리지 않도록 보관을 중단했습니다. 기존 파일은 이동하거나 삭제하지 않았습니다")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	root := filepath.Join(taskArchiveRoot(dataDir), ".staging", strconv.FormatInt(archiveID, 10))
	stage := &taskArchiveFileStage{dataDir: dataDir, root: root, payload: filepath.Join(root, "payload")}
	journalPath := filepath.Join(root, "journal.json")
	if raw, err := os.ReadFile(journalPath); err == nil {
		if err := json.Unmarshal(raw, &stage.journal); err != nil {
			return nil, fmt.Errorf("read task archive staging journal: %w", err)
		}
		// The journal is written before the first rename, so its presence does not mean
		// the payload is complete: a previous round may have failed mid-loop with a
		// rollback that itself errored, which leaves the root in place. Replay the moves
		// rather than packaging a payload that is missing transcripts or workspace files.
		if err := managedMkdirAll(dataDir, filepath.Join(stage.payload, "files")); err != nil {
			return nil, err
		}
		if err := stage.applyMoves(); err != nil {
			_ = stage.rollback()
			return nil, err
		}
		return stage, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := managedMkdirAll(dataDir, filepath.Join(stage.payload, "files")); err != nil {
		return nil, err
	}
	stage.journal = archiveStageJournal{ArchiveID: archiveID, TaskID: taskID}
	targets := []archiveFileMove{}
	taskDir := filepath.Join(workspaceDirectory(dataDir), "tasks", taskID)
	if _, err := os.Lstat(taskDir); err == nil {
		targets = append(targets, archiveFileMove{Source: taskDir, Relative: filepath.Join("files", "tasks", taskID)})
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	transcriptDir := filepath.Join(dataDir, "transcripts")
	entries, err := os.ReadDir(transcriptDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	prefix := fmt.Sprintf("exp%d-", explorationID)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || (!entry.IsDir() && !strings.HasSuffix(name, ".jsonl")) {
			continue
		}
		targets = append(targets, archiveFileMove{
			Source: filepath.Join(transcriptDir, name), Relative: filepath.Join("files", "transcripts", name),
		})
	}
	// Persist the complete plan before the first rename. Recovery can therefore
	// roll back any prefix of the moves after an abrupt process termination.
	stage.journal.Moves = targets
	if err := writeArchiveJournal(dataDir, journalPath, stage.journal); err != nil {
		_ = stage.rollback()
		return nil, err
	}
	if err := stage.applyMoves(); err != nil {
		_ = stage.rollback()
		return nil, err
	}
	return stage, nil
}

// applyMoves performs the renames recorded in the journal. Entries already sitting
// in the payload are skipped, so an interrupted staging round can be resumed in
// place without moving anything twice.
func (s *taskArchiveFileStage) applyMoves() error {
	for _, move := range s.journal.Moves {
		destination := filepath.Join(s.payload, move.Relative)
		if _, err := os.Lstat(destination); err == nil {
			if _, sourceErr := os.Lstat(move.Source); sourceErr == nil {
				return fmt.Errorf("archive staging source and destination both exist: %s", move.Source)
			} else if !os.IsNotExist(sourceErr) {
				return sourceErr
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if _, err := os.Lstat(move.Source); err != nil {
			if os.IsNotExist(err) {
				// Neither side exists: the path was removed outside the archive flow
				// after the journal was written. Nothing can be staged for it.
				continue
			}
			return err
		}
		if err := managedMove(s.dataDir, move.Source, destination); err != nil {
			return fmt.Errorf("stage task archive path %s: %w", move.Source, err)
		}
	}
	return nil
}

func (s *taskArchiveFileStage) rollback() error {
	if s == nil || s.done {
		return nil
	}
	var errs []error
	for i := len(s.journal.Moves) - 1; i >= 0; i-- {
		move := s.journal.Moves[i]
		staged := filepath.Join(s.payload, move.Relative)
		if _, err := os.Lstat(staged); os.IsNotExist(err) {
			continue
		} else if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := os.Lstat(move.Source); err == nil {
			errs = append(errs, fmt.Errorf("archive rollback destination exists: %s", move.Source))
			continue
		} else if !os.IsNotExist(err) {
			errs = append(errs, err)
			continue
		}
		if err := managedMove(s.dataDir, staged, move.Source); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		if err := managedRemoveAll(s.dataDir, s.root); err != nil {
			errs = append(errs, err)
		}
	}
	s.done = true
	return errors.Join(errs...)
}

func (s *taskArchiveFileStage) commit() error {
	if s == nil || s.done {
		return nil
	}
	s.done = true
	return managedRemoveAll(s.dataDir, s.root)
}

func installTaskArchiveFiles(dataDir, extractedDir, taskID string, archiveID int64) (*taskArchiveRestoreFiles, error) {
	stage := &taskArchiveRestoreFiles{dataDir: dataDir, extracted: extractedDir}
	numericTaskID, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil || numericTaskID <= 0 {
		return nil, fmt.Errorf("invalid restore task id %q", taskID)
	}
	if err := validateWorkspaceArtifactPath(dataDir, filepath.Join("tasks", taskID)); err != nil {
		return nil, err
	}
	sources := []restoredArchivePath{}
	workspace := filepath.Join(extractedDir, "files", "tasks", taskID)
	if _, err := os.Lstat(workspace); err == nil {
		sources = append(sources, restoredArchivePath{Source: workspace, Destination: filepath.Join(workspaceDirectory(dataDir), "tasks", taskID)})
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	transcripts := filepath.Join(extractedDir, "files", "transcripts")
	entries, err := os.ReadDir(transcripts)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		sources = append(sources, restoredArchivePath{
			Source: filepath.Join(transcripts, entry.Name()), Destination: filepath.Join(dataDir, "transcripts", entry.Name()),
		})
	}
	for _, move := range sources {
		if _, err := os.Lstat(move.Destination); err == nil {
			return nil, fmt.Errorf("restore destination already exists: %s", move.Destination)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	stage.moves = sources
	journal := archiveRestoreJournal{ArchiveID: archiveID, TaskID: numericTaskID, Moves: sources}
	if err := writeArchiveJournal(dataDir, filepath.Join(extractedDir, "restore-journal.json"), journal); err != nil {
		return nil, err
	}
	for _, move := range sources {
		if err := managedMove(dataDir, move.Source, move.Destination); err != nil {
			_ = stage.rollback()
			return nil, err
		}
	}
	return stage, nil
}

func (s *taskArchiveRestoreFiles) rollback() error {
	if s == nil || s.done {
		return nil
	}
	var errs []error
	for i := len(s.moves) - 1; i >= 0; i-- {
		move := s.moves[i]
		if _, err := os.Lstat(move.Destination); os.IsNotExist(err) {
			continue
		} else if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := os.Lstat(move.Source); err == nil {
			errs = append(errs, fmt.Errorf("restore rollback source exists: %s", move.Source))
			continue
		} else if !os.IsNotExist(err) {
			errs = append(errs, err)
			continue
		}
		if err := managedMove(s.dataDir, move.Destination, move.Source); err != nil {
			errs = append(errs, err)
		}
	}
	s.done = true
	return errors.Join(errs...)
}

func (s *taskArchiveRestoreFiles) commit() error {
	if s == nil || s.done {
		return nil
	}
	s.done = true
	return managedRemoveAll(s.dataDir, s.extracted)
}

// recoverTaskArchiveRestoreStages resolves file installs left by an interrupted
// restore. If SQLite committed, installed files are authoritative and only
// the extraction directory is stale. Otherwise all completed renames are moved
// back so the persistent restore job can retry from a clean destination.
func recoverTaskArchiveRestoreStages(dataDir string, pg *pgdb.DB) error {
	parent := filepath.Join(taskArchiveRoot(dataDir), ".restore")
	entries, err := os.ReadDir(parent)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(parent, entry.Name())
		raw, err := os.ReadFile(filepath.Join(root, "restore-journal.json"))
		if os.IsNotExist(err) {
			// Extraction was interrupted before any destination rename.
			if err := managedRemoveAll(dataDir, root); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var journal archiveRestoreJournal
		if err := json.Unmarshal(raw, &journal); err != nil {
			errs = append(errs, err)
			continue
		}
		restored, err := pg.IsTaskArchiveRestored(journal.ArchiveID)
		if errors.Is(err, pgdb.ErrTaskArchiveNotFound) && journal.TaskID > 0 {
			task, taskErr := pg.GetTask(journal.TaskID)
			if taskErr != nil {
				errs = append(errs, taskErr)
				continue
			}
			restored = task != nil
			err = nil
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if restored {
			if err := managedRemoveAll(dataDir, root); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		stage := &taskArchiveRestoreFiles{dataDir: dataDir, extracted: root, moves: journal.Moves}
		if err := stage.rollback(); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := managedRemoveAll(dataDir, root); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// recoverTaskArchiveStages resolves file renames left by an interrupted archive.
// A committed cold task already has a verified package, so stale staging can be
// discarded; otherwise files are moved back before the persistent job retries.
func recoverTaskArchiveStages(dataDir string, pg *pgdb.DB) error {
	parent := filepath.Join(taskArchiveRoot(dataDir), ".staging")
	entries, err := os.ReadDir(parent)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(parent, entry.Name())
		raw, err := os.ReadFile(filepath.Join(root, "journal.json"))
		if os.IsNotExist(err) {
			// Either staging died between creating the directory tree and writing the
			// journal — no rename had run, so payload holds nothing — or commit/rollback
			// failed part-way through removing the root, in which case payload only holds
			// copies already inside the package. Without a journal there is nothing to
			// roll back, and reporting an error here keeps the archive worker from ever
			// starting, so discard the directory instead.
			if err := managedRemoveAll(dataDir, root); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var journal archiveStageJournal
		if err := json.Unmarshal(raw, &journal); err != nil {
			errs = append(errs, err)
			continue
		}
		archive, getErr := pg.GetTaskArchive(journal.ArchiveID)
		if getErr != nil {
			errs = append(errs, getErr)
			continue
		}
		if archive != nil && archive.State == pgdb.ArchiveReady {
			if err := managedRemoveAll(dataDir, root); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		stage := &taskArchiveFileStage{dataDir: dataDir, root: root, payload: filepath.Join(root, "payload"), journal: journal}
		if err := stage.rollback(); err != nil {
			errs = append(errs, err)
		}
		if archive != nil && archive.ArchivePath != "" {
			if err := managedRemoveAll(dataDir, archive.ArchivePath); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func recoverTaskArchiveDeletePackages(dataDir string, pg *pgdb.DB) error {
	root := taskArchiveRoot(dataDir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		marker := strings.LastIndex(name, ".deleting-")
		if marker < 0 {
			continue
		}
		archiveID, err := strconv.ParseInt(name[marker+len(".deleting-"):], 10, 64)
		if err != nil || archiveID <= 0 {
			continue
		}
		staged := filepath.Join(root, name)
		original := filepath.Join(root, name[:marker])
		archive, err := pg.GetTaskArchive(archiveID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if archive == nil {
			if err := managedRemoveAll(dataDir, staged); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
			continue
		}
		if archive.State == pgdb.DeleteQueued || archive.State == pgdb.Deleting || archive.State == pgdb.DeleteFailed {
			// The idempotent delete worker consumes the staged path directly.
			continue
		}
		if _, err := os.Lstat(original); err == nil {
			errs = append(errs, fmt.Errorf("archive delete recovery destination exists: %s", original))
			continue
		} else if !os.IsNotExist(err) {
			errs = append(errs, err)
			continue
		}
		if err := managedMove(dataDir, staged, original); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func stageTaskArchivePackageDelete(dataDir, archivePath string, archiveID int64) (string, bool, error) {
	staged := archivePath + fmt.Sprintf(".deleting-%d", archiveID)
	if _, err := os.Lstat(staged); err == nil {
		if _, originalErr := os.Lstat(archivePath); originalErr == nil {
			return staged, false, errors.New("보관 패키지 원본과 삭제 임시 파일이 동시에 존재합니다")
		} else if !os.IsNotExist(originalErr) {
			return staged, false, originalErr
		}
		return staged, true, nil
	} else if !os.IsNotExist(err) {
		return staged, false, err
	}
	if err := managedMove(dataDir, archivePath, staged); err == nil {
		return staged, true, nil
	} else if !os.IsNotExist(err) {
		return staged, false, err
	}
	return staged, false, nil
}

func writeTaskArchivePackage(dataDir, path, payloadDir string, snapshot *pgdb.TaskArchiveSnapshot) (originalSize, compressedSize int64, checksum string, err error) {
	if snapshot == nil {
		return 0, 0, "", errors.New("nil task archive snapshot")
	}
	payload, closePayload, err := openManagedDirectory(dataDir, payloadDir, false)
	if err != nil {
		return 0, 0, "", err
	}
	defer closePayload()
	if _, err := managedRelative(dataDir, path); err != nil {
		return 0, 0, "", err
	}
	parent, closeParent, err := openManagedDirectory(dataDir, filepath.Dir(path), true)
	if err != nil {
		return 0, 0, "", err
	}
	defer closeParent()
	manifest, err := json.Marshal(snapshot)
	if err != nil {
		return 0, 0, "", err
	}
	temporary := filepath.Base(path) + ".partial-" + uuid.NewString()
	file, err := parent.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, archiveFileMode)
	if err != nil {
		return 0, 0, "", err
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = parent.Remove(temporary)
		}
	}()
	hasher := sha256.New()
	multi := io.MultiWriter(file, hasher)
	encoder, err := zstd.NewWriter(multi,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(10)),
		zstd.WithEncoderCRC(true),
	)
	if err != nil {
		return 0, 0, "", err
	}
	tarWriter := tar.NewWriter(encoder)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "manifest.json", Mode: int64(archiveFileMode), Size: int64(len(manifest))}); err != nil {
		tarWriter.Close()
		encoder.Close()
		return 0, 0, "", err
	}
	if _, err := tarWriter.Write(manifest); err != nil {
		tarWriter.Close()
		encoder.Close()
		return 0, 0, "", err
	}
	originalSize = int64(len(manifest))
	walkErr := fs.WalkDir(payload.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if relative == "." {
			return nil
		}
		if relative == "manifest.json" {
			return errors.New("보관 payload에 중복 manifest.json 파일이 있습니다")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if workspaceLink(info) {
			// 归档格式端到端只支持普通文件与目录（解包端对其它类型直接报错），
			// 无法还原符号链接。跳过而非整包失败：不读取链接目标(lstat，不越出目录树)，
			// 也不写入 symlink 条目；链接指向树内时目标文件本身仍会被单独遍历归档。
			log.Printf("[task-archive] 심볼릭 링크 건너뜀(보관에서 지원하지 않으며 다른 파일에는 영향 없음): %s", relative)
			return nil
		}
		var input *os.File
		if !info.IsDir() {
			input, err = payload.Open(filepath.FromSlash(relative))
			if err != nil {
				return err
			}
			defer input.Close()
			info, err = input.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || workspaceMultipleLinks(input, info) {
				return errors.New("연결된 파일 또는 일반 파일이 아닌 항목이 있어 작업 보관을 중단했습니다")
			}
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		originalSize += info.Size()
		_, copyErr := io.Copy(tarWriter, input)
		closeErr := input.Close()
		return errors.Join(copyErr, closeErr)
	})
	if walkErr != nil {
		_ = tarWriter.Close()
		_ = encoder.Close()
		return 0, 0, "", walkErr
	}
	if err := tarWriter.Close(); err != nil {
		_ = encoder.Close()
		return 0, 0, "", err
	}
	if err := encoder.Close(); err != nil {
		return 0, 0, "", err
	}
	if err := file.Sync(); err != nil {
		return 0, 0, "", err
	}
	stat, err := file.Stat()
	if err != nil {
		return 0, 0, "", err
	}
	compressedSize = stat.Size()
	checksum = hex.EncodeToString(hasher.Sum(nil))
	if err := file.Close(); err != nil {
		return 0, 0, "", err
	}
	if err := managedMove(dataDir, filepath.Join(filepath.Dir(path), temporary), path); err != nil {
		return 0, 0, "", err
	}
	committed = true
	return originalSize, compressedSize, checksum, nil
}

func extractTaskArchivePackage(dataDir, path, expectedSHA, destination string) error {
	if _, err := managedRelative(dataDir, path); err != nil {
		return err
	}
	parent, closeParent, err := openManagedDirectory(dataDir, filepath.Dir(path), false)
	if err != nil {
		return err
	}
	defer closeParent()
	before, err := parent.Lstat(filepath.Base(path))
	if err != nil {
		return err
	}
	if workspaceLink(before) || !before.Mode().IsRegular() {
		return errors.New("연결된 보관 패키지는 복원할 수 없습니다")
	}
	file, err := parent.Open(filepath.Base(path))
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, info) || !info.Mode().IsRegular() || workspaceMultipleLinks(file, info) {
		return errors.New("보관 패키지가 일반 파일이 아니거나 다른 파일과 연결돼 있습니다")
	}
	if expectedSHA != "" {
		hasher := sha256.New()
		if _, err := io.Copy(hasher, file); err != nil {
			return err
		}
		if hex.EncodeToString(hasher.Sum(nil)) != strings.ToLower(expectedSHA) {
			return errors.New("task archive checksum mismatch")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return err
	}
	defer decoder.Close()
	tarReader := tar.NewReader(decoder)
	extracted, closeExtracted, err := openManagedDirectory(dataDir, destination, true)
	if err != nil {
		return err
	}
	defer closeExtracted()
	var entries int
	var total int64
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxArchiveFiles || header.Size < 0 || total+header.Size > maxArchiveBytes {
			return errors.New("task archive exceeds extraction safety limits")
		}
		total += header.Size
		clean, err := workspacePath(header.Name)
		if err != nil || clean == "." {
			return fmt.Errorf("unsafe task archive path %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := extracted.MkdirAll(clean, archiveDirMode); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := extracted.MkdirAll(filepath.Dir(clean), archiveDirMode); err != nil {
				return err
			}
			output, err := extracted.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(output, tarReader, header.Size)
			closeErr := output.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported task archive entry type %d", header.Typeflag)
		}
	}
	return nil
}
