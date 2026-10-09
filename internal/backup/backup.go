// Package backup publishes verified offline snapshots of the complete writable
// ARTEX data set. The backend and maintenance commands share a process lock.
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const manifestName = "manifest.json"
const maxManifestBytes = 16 << 20

var roots = []string{"config.json", "jwt.key", "data", "skills", "chatgpt"}

type entry struct {
	Path       string `json:"path"`
	Directory  bool   `json:"directory,omitempty"`
	Executable bool   `json:"executable,omitempty"`
	Size       int64  `json:"size,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
}

type manifest struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	Entries   []entry   `json:"entries"`
}

type Result struct {
	Event string `json:"event"`
	Path  string `json:"path"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// LockHome excludes other backends and snapshots until the returned file closes.
// It is an OS lock, so a killed owner never leaves a stale lock to remove.
func LockHome(home string) (*os.File, error) {
	if err := checkAbsolute(home, true); err != nil {
		return nil, err
	}
	p := filepath.Join(home, ".artex-data.lock")
	if info, err := os.Lstat(p); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("데이터 홈 잠금은 일반 파일이어야 합니다")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := platformPath(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("다른 ARTEX가 데이터 홈을 사용 중입니다: %w", err)
	}
	return f, nil
}

// Create owns the home lock, snapshots SQLite through the driver's backup API,
// then copies the corresponding external evidence, configuration and skills.
// Validation failures leave the requested destination absent. Existing paths
// are refused, including paths created by a competing publisher.
func Create(ctx context.Context, home, destination string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	lease, err := LockHome(home)
	if err != nil {
		return Result{}, err
	}
	defer lease.Close()
	if err := separatePaths(home, destination); err != nil {
		return Result{}, err
	}
	if err := checkConfiguration(home); err != nil {
		return Result{}, err
	}
	stage, err := newStage(destination)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	payload := filepath.Join(stage, "payload")
	if err := os.Mkdir(payload, 0700); err != nil {
		return Result{}, err
	}
	m := manifest{Format: 1, CreatedAt: time.Now().UTC()}
	for _, root := range roots {
		start := filepath.Join(home, root)
		if _, err := os.Lstat(start); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return Result{}, err
		}
		err := filepath.WalkDir(start, func(source string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(home, source)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if err := validRelative(rel); err != nil {
				return err
			}
			// The native snapshot consumes WAL; sidecars are never a backup entry.
			// Skip before stat because SQLite may remove them on final close.
			if sqliteSidecar(rel) {
				if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
					return errors.New("SQLite 변경 파일은 일반 파일이어야 합니다")
				}
				if err := checkAbsolute(sidecarDatabase(source), false); err != nil {
					return fmt.Errorf("원본 SQLite가 없는 변경 파일: %w", err)
				}
				return nil
			}
			if err := platformPath(source); err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
				return fmt.Errorf("백업할 수 없는 링크/특수 파일: %s", rel)
			}
			target := filepath.Join(payload, filepath.FromSlash(rel))
			e := entry{Path: rel, Directory: info.IsDir(), Executable: !info.IsDir() && info.Mode()&0111 != 0}
			if e.Directory {
				err = os.MkdirAll(target, 0700)
			} else if strings.HasSuffix(rel, ".sqlite") {
				err = snapshotSQLite(ctx, source, target, rel == "data/artex.sqlite")
			} else {
				err = copyFile(ctx, source, target, e.Executable)
			}
			if err != nil {
				return fmt.Errorf("백업 %s: %w", rel, err)
			}
			if !e.Directory {
				e.Size, e.SHA256, err = digest(ctx, target)
				if err != nil {
					return err
				}
			}
			m.Entries = append(m.Entries, e)
			if len(m.Entries) > 100000 {
				return errors.New("백업 파일 수 제한을 초과했습니다")
			}
			return nil
		})
		if err != nil {
			return Result{}, err
		}
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil || len(encoded) > maxManifestBytes {
		return Result{}, errors.New("백업 목록 크기 제한을 초과했습니다")
	}
	if err := writeFile(filepath.Join(stage, manifestName), encoded); err != nil {
		return Result{}, err
	}
	r, err := Verify(ctx, stage)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := publish(stage, destination); err != nil {
		return Result{}, err
	}
	r.Event, r.Path = "backup-complete", filepath.Clean(destination)
	return r, nil
}

// Restore only publishes to a new home. The source and every restored byte are
// validated before publication; no active or previous user home is replaced.
func Restore(ctx context.Context, source, destination string) (Result, error) {
	m, err := readManifest(source)
	if err != nil {
		return Result{}, err
	}
	r, err := verifyManifest(ctx, source, m)
	if err != nil {
		return Result{}, err
	}
	if err := separatePaths(source, destination); err != nil {
		return Result{}, err
	}
	stage, err := newStage(destination)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	for _, e := range m.Entries {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		target := filepath.Join(stage, filepath.FromSlash(e.Path))
		if e.Directory {
			err = os.MkdirAll(target, 0700)
		} else {
			err = copyFile(ctx, filepath.Join(source, "payload", filepath.FromSlash(e.Path)), target, e.Executable)
		}
		if err != nil {
			return Result{}, err
		}
		if !e.Directory {
			size, sum, err := digest(ctx, target)
			if err != nil {
				return Result{}, err
			}
			if size != e.Size || sum != e.SHA256 {
				return Result{}, errors.New("복원 파일 해시가 일치하지 않습니다")
			}
		}
	}
	if err := checkConfiguration(stage); err != nil {
		return Result{}, err
	}
	for _, e := range m.Entries {
		if !e.Directory && strings.HasSuffix(e.Path, ".sqlite") {
			if err := validateSQLite(ctx, filepath.Join(stage, filepath.FromSlash(e.Path)), e.Path == "data/artex.sqlite", true); err != nil {
				return Result{}, err
			}
		}
	}
	if err := validateAttachments(ctx, stage); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := publish(stage, destination); err != nil {
		return Result{}, err
	}
	r.Event, r.Path = "restore-complete", filepath.Clean(destination)
	return r, nil
}

func Verify(ctx context.Context, source string) (Result, error) {
	m, err := readManifest(source)
	if err != nil {
		return Result{}, err
	}
	return verifyManifest(ctx, source, m)
}

// Restore uses the exact parsed manifest that was verified, so another process
// cannot replace the manifest between verification and restoration.
func verifyManifest(ctx context.Context, source string, m manifest) (Result, error) {
	if err := checkAbsolute(source, true); err != nil {
		return Result{}, err
	}
	expected := map[string]entry{}
	for _, e := range m.Entries {
		expected[e.Path] = e
	}
	r := Result{Event: "backup-verified", Path: filepath.Clean(source)}
	count := 0
	err := filepath.WalkDir(source, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := platformPath(p); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("백업에는 링크/특수 파일을 사용할 수 없습니다")
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == "payload" {
			if !info.IsDir() {
				return errors.New("백업 디렉터리가 유효하지 않습니다")
			}
			return nil
		}
		if rel == manifestName {
			if !info.Mode().IsRegular() {
				return errors.New("백업 목록은 일반 파일이어야 합니다")
			}
			return nil
		}
		if !strings.HasPrefix(rel, "payload/") {
			return errors.New("백업에 목록에 없는 파일이 있습니다")
		}
		e, ok := expected[strings.TrimPrefix(rel, "payload/")]
		if !ok || e.Directory != info.IsDir() {
			return errors.New("백업 파일과 목록이 일치하지 않습니다")
		}
		count++
		if e.Directory {
			return nil
		}
		size, sum, err := digest(ctx, p)
		if err != nil {
			return err
		}
		if size != e.Size || sum != e.SHA256 {
			return fmt.Errorf("백업 손상: %s", e.Path)
		}
		if strings.HasSuffix(e.Path, ".sqlite") {
			if err := validateSQLite(ctx, p, e.Path == "data/artex.sqlite", true); err != nil {
				return err
			}
		}
		r.Files++
		r.Bytes += size
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if count != len(expected) {
		return Result{}, errors.New("백업에서 파일이 누락되었습니다")
	}
	if err := checkConfiguration(filepath.Join(source, "payload")); err != nil {
		return Result{}, err
	}
	if err := validateAttachments(ctx, filepath.Join(source, "payload")); err != nil {
		return Result{}, err
	}
	return r, nil
}

func readManifest(source string) (manifest, error) {
	p := filepath.Join(source, manifestName)
	if err := checkAbsolute(p, false); err != nil {
		return manifest{}, err
	}
	f, err := os.Open(p)
	if err != nil {
		return manifest{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil || len(b) > maxManifestBytes {
		return manifest{}, errors.New("백업 목록 크기가 유효하지 않습니다")
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	var m manifest
	if err := decoder.Decode(&m); err != nil {
		return manifest{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest{}, errors.New("백업 목록에 추가 JSON이 있습니다")
	}
	if m.Format != 1 || m.CreatedAt.IsZero() || len(m.Entries) == 0 || len(m.Entries) > 100000 {
		return manifest{}, errors.New("지원하지 않거나 유효하지 않은 백업 목록입니다")
	}
	seen := map[string]bool{}
	var businessSQLite bool
	for _, e := range m.Entries {
		if err := validRelative(e.Path); err != nil {
			return manifest{}, err
		}
		if sqliteSidecar(e.Path) {
			return manifest{}, errors.New("백업에는 SQLite WAL/SHM/저널 파일을 포함할 수 없습니다")
		}
		key := strings.ToLower(e.Path)
		if seen[key] {
			return manifest{}, errors.New("백업 목록에 중복 경로가 있습니다")
		}
		seen[key] = true
		if e.Directory {
			if e.Size != 0 || e.SHA256 != "" || e.Executable {
				return manifest{}, errors.New("백업 디렉터리 목록이 유효하지 않습니다")
			}
		} else if e.Size < 0 || len(e.SHA256) != 64 {
			return manifest{}, errors.New("백업 파일 목록이 유효하지 않습니다")
		} else if decoded, err := hex.DecodeString(e.SHA256); err != nil || len(decoded) != sha256.Size || strings.ToLower(e.SHA256) != e.SHA256 {
			return manifest{}, errors.New("백업 해시가 유효하지 않습니다")
		}
		if e.Path == "data/artex.sqlite" && !e.Directory {
			businessSQLite = true
		}
	}
	if !businessSQLite {
		return manifest{}, errors.New("업무 SQLite가 없는 백업입니다")
	}
	return m, nil
}

func validRelative(p string) error {
	if !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, `\:`) {
		return errors.New("백업 경로가 유효하지 않습니다")
	}
	root := strings.SplitN(p, "/", 2)[0]
	if root != "data" && root != "skills" && root != "chatgpt" && p != "config.json" && p != "jwt.key" {
		return errors.New("허용하지 않는 백업 경로입니다")
	}
	for _, segment := range strings.Split(p, "/") {
		upper := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
		if strings.TrimRight(segment, ". ") != segment || upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || (len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9') {
			return errors.New("지원하지 않는 백업 파일 이름입니다")
		}
	}
	return nil
}

func checkAbsolute(p string, directory bool) error {
	if !filepath.IsAbs(p) || strings.HasPrefix(filepath.ToSlash(p), "//") || strings.IndexByte(p, 0) >= 0 {
		return errors.New("로컬 절대 경로를 지정해야 합니다")
	}
	for current := filepath.Clean(p); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("데이터 경로에는 심볼릭 링크를 사용할 수 없습니다")
		}
		if err := platformPath(current); err != nil {
			return err
		}
		if current == filepath.Clean(p) && (info.IsDir() != directory || (!directory && !info.Mode().IsRegular())) {
			return errors.New("데이터 경로의 파일 형식이 유효하지 않습니다")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return nil
}

func newStage(destination string) (string, error) {
	if !filepath.IsAbs(destination) || destination == filepath.Dir(destination) {
		return "", errors.New("새 로컬 절대 폴더를 지정해야 합니다")
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", errors.New("기존 폴더나 파일에 덮어쓸 수 없습니다")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(destination)
	if err := checkAbsolute(parent, true); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(parent, ".artex-snapshot-*")
	if err != nil {
		return "", err
	}
	if err := protectDirectory(stage); err != nil {
		os.Remove(stage)
		return "", err
	}
	return stage, nil
}

func inside(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// EvalSymlinks also expands Windows 8.3 aliases. Compare the actual enclosing
// directories before creating staging files, including a not-yet-existing name.
func separatePaths(a, b string) error {
	a, err := canonicalTarget(a)
	if err != nil {
		return err
	}
	b, err = canonicalTarget(b)
	if err != nil {
		return err
	}
	if inside(a, b) || inside(b, a) {
		return errors.New("백업 폴더와 데이터 홈은 서로 포함할 수 없습니다")
	}
	return nil
}

func canonicalTarget(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", errors.New("로컬 절대 경로를 지정해야 합니다")
	}
	current := filepath.Clean(p)
	var suffix []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if current == filepath.Dir(current) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = filepath.Dir(current)
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	return resolved, nil
}

func sqliteSidecar(p string) bool {
	return strings.HasSuffix(p, ".sqlite-wal") || strings.HasSuffix(p, ".sqlite-shm") || strings.HasSuffix(p, ".sqlite-journal")
}

func sidecarDatabase(p string) string {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if strings.HasSuffix(p, suffix) {
			return strings.TrimSuffix(p, suffix)
		}
	}
	return p
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func copyFile(ctx context.Context, source, target string, executable bool) (err error) {
	if err := checkAbsolute(source, false); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	before, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	mode := fs.FileMode(0600)
	if executable {
		mode = 0700
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	if _, err := io.Copy(out, contextReader{ctx, in}); err != nil {
		return err
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	current, err := os.Stat(source)
	if err != nil {
		return err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Size() != current.Size() || !before.ModTime().Equal(current.ModTime()) || !os.SameFile(before, current) {
		return errors.New("복사 중 원본 파일이 변경되었습니다. 파일 편집을 마친 뒤 다시 시도하세요")
	}
	return out.Sync()
}

func digest(ctx context.Context, p string) (int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, contextReader{ctx, f})
	return n, hex.EncodeToString(h.Sum(nil)), err
}

func writeFile(p string, b []byte) (err error) {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if _, err := f.Write(b); err != nil {
		return err
	}
	return f.Sync()
}

func checkConfiguration(home string) error {
	keyPath := filepath.Join(home, "jwt.key")
	if _, err := os.Lstat(keyPath); err == nil {
		if err := checkAbsolute(keyPath, false); err != nil {
			return err
		}
		info, err := os.Stat(keyPath)
		if err != nil {
			return err
		}
		if info.Size() != 32 {
			return errors.New("인증 키는 32바이트 일반 파일이어야 합니다")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	p := filepath.Join(home, "config.json")
	if _, err := os.Lstat(p); err == nil {
		if err := checkAbsolute(p, false); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var c struct {
		SkillDir string `json:"skill_dir"`
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return fmt.Errorf("백업 설정 JSON: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("백업 설정 JSON이 유효하지 않습니다")
	}
	if c.SkillDir != "" {
		return errors.New("백업은 앱 데이터 홈의 기본 skills 폴더만 지원합니다. 외부 스킬 경로 설정을 먼저 해제하세요")
	}
	return nil
}
