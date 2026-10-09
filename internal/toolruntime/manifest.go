// Package toolruntime verifies app-owned tools and runs them inside OS boundaries.
// Bundle integrity, OS isolation and per-tool availability are separate states.
package toolruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const ManifestName = "manifest.json"

var ComponentKeys = []string{"shell", "pty", "python", "node", "browser", "cli"}

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Component struct {
	Key        string `json:"key"`
	Version    string `json:"version"`
	Source     string `json:"source"`
	License    string `json:"license"`
	Entrypoint string `json:"entrypoint"`
	Files      []File `json:"files"`
}

type Manifest struct {
	Schema     int         `json:"schema"`
	Platform   string      `json:"platform"`
	Components []Component `json:"components"`
}

type ComponentStatus struct {
	Key     string `json:"key"`
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
	License string `json:"license,omitempty"`
	Message string `json:"message,omitempty"`
}

type Bundle struct {
	Root     string
	Manifest Manifest
}

// Load requires a digest supplied by the trusted application resources. A hash
// stored alongside writable tools would let a modified manifest trust itself.
func Load(root, manifestDigest string) (*Bundle, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("앱 전용 도구 경로가 절대 경로로 지정되지 않았습니다")
	}
	root = filepath.Clean(root)
	if err := rejectLinks(root); err != nil {
		return nil, err
	}
	// Windows can name the same directory through its long and 8.3 paths.
	// Reject redirects before resolving aliases, then retain the canonical path
	// for every relationship check, permission grant and AppContainer identity.
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("도구 설치 경로 정규화: %w", err)
	}
	if err := rejectLinks(canonicalRoot); err != nil {
		return nil, err
	}
	root = canonicalRoot
	if !validDigest(manifestDigest) {
		return nil, errors.New("앱 자원의 도구 매니페스트 SHA256이 유효하지 않습니다")
	}
	p := filepath.Join(root, ManifestName)
	if err := rejectLinks(p); err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("도구 매니페스트 읽기: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("도구 매니페스트가 일반 파일이 아닙니다")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 {
		return nil, errors.New("도구 매니페스트를 읽을 수 없거나 크기 제한을 초과했습니다")
	}
	digest := sha256.Sum256(raw)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), manifestDigest) {
		return nil, errors.New("도구 매니페스트 SHA256이 앱 자원과 일치하지 않습니다")
	}
	var manifest Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("도구 매니페스트 형식: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("도구 매니페스트에 추가 데이터가 있습니다")
	}
	if manifest.Schema != 1 || manifest.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return nil, errors.New("도구 매니페스트 버전 또는 운영체제가 일치하지 않습니다")
	}
	b := &Bundle{Root: root, Manifest: manifest}
	seen := make(map[string]bool)
	fileHashes := make(map[string]string)
	for _, c := range manifest.Components {
		if seen[c.Key] || !knownKey(c.Key) {
			return nil, fmt.Errorf("중복되거나 알 수 없는 도구 구성요소 %q", c.Key)
		}
		seen[c.Key] = true
		if err := b.validateComponent(c); err != nil {
			return nil, fmt.Errorf("도구 %s: %w", c.Key, err)
		}
		for _, f := range c.Files {
			path := strings.ToLower(f.Path)
			if old, ok := fileHashes[path]; ok && !strings.EqualFold(old, f.SHA256) {
				return nil, errors.New("구성요소 간 같은 도구 파일의 SHA256이 다릅니다")
			}
			fileHashes[path] = f.SHA256
		}
	}
	return b, nil
}

func FromEnvironment() (*Bundle, error) {
	return Load(os.Getenv("ARTEX_TOOL_ROOT"), os.Getenv("ARTEX_TOOL_MANIFEST_SHA256"))
}

func (b *Bundle) Component(key string) (Component, bool) {
	for _, c := range b.Manifest.Components {
		if c.Key == key {
			return c, true
		}
	}
	return Component{}, false
}

// Verify checks every runtime dependency, including DLLs, library archives,
// scripts and license files. Entrypoint verification alone is insufficient.
func (b *Bundle) Verify(key string) error {
	return b.verify(key, make(map[string]string))
}

func (b *Bundle) VerifyAll() error {
	seen := make(map[string]string)
	for _, c := range b.Manifest.Components {
		if err := b.verify(c.Key, seen); err != nil {
			return err
		}
	}
	return b.verifyInventory()
}

func (b *Bundle) verifyInventory() error {
	listed := make(map[string]bool)
	for _, c := range b.Manifest.Components {
		for _, f := range c.Files {
			listed[f.Path] = true
		}
	}
	return filepath.WalkDir(b.Root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := rejectLinks(path); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(b.Root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != ManifestName {
			if !listed[rel] {
				return fmt.Errorf("매니페스트에 없는 도구 파일 %s", rel)
			}
		}
		return nil
	})
}

func (b *Bundle) verify(key string, seen map[string]string) error {
	c, ok := b.Component(key)
	if !ok {
		return errors.New("도구 구성요소가 배포되지 않았습니다")
	}
	for _, item := range c.Files {
		if old, ok := seen[item.Path]; ok && old == item.SHA256 {
			continue
		}
		p, err := b.Path(item.Path)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("도구 파일 읽기 %s: %w", item.Path, err)
		}
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			f.Close()
			return fmt.Errorf("도구 파일 %s가 일반 파일이 아닙니다", item.Path)
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("도구 파일 %s 무결성 읽기 실패", item.Path)
		}
		if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), item.SHA256) {
			return fmt.Errorf("도구 파일 %s SHA256이 일치하지 않습니다", item.Path)
		}
		seen[item.Path] = item.SHA256
	}
	return nil
}

func (b *Bundle) Status() []ComponentStatus {
	statuses := make([]ComponentStatus, 0, len(ComponentKeys))
	seen := make(map[string]string)
	for _, key := range ComponentKeys {
		s := ComponentStatus{Key: key, State: "not_prepared"}
		if c, ok := b.Component(key); ok {
			s.Version, s.Source, s.License = c.Version, c.Source, c.License
			if err := b.verify(key, seen); err != nil {
				s.State, s.Message = "invalid", err.Error()
			} else {
				s.State = "verified"
			}
		}
		statuses = append(statuses, s)
	}
	if err := b.verifyInventory(); err != nil {
		for index := range statuses {
			if statuses[index].State == "verified" {
				statuses[index].State, statuses[index].Message = "invalid", err.Error()
			}
		}
	}
	return statuses
}

func (b *Bundle) Path(relative string) (string, error) {
	if !safeRelative(relative) {
		return "", errors.New("도구 파일 경로가 전용 디렉터리를 벗어납니다")
	}
	p := filepath.Join(b.Root, filepath.FromSlash(relative))
	if err := rejectLinks(p); err != nil {
		return "", err
	}
	return p, nil
}

func (b *Bundle) validateComponent(c Component) error {
	u, err := url.Parse(c.Source)
	if c.Version == "" || c.License == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("고정 버전·HTTPS 출처·라이선스가 필요합니다")
	}
	if !safeRelative(c.Entrypoint) || len(c.Files) == 0 {
		return errors.New("실행 파일과 전체 파일 목록이 필요합니다")
	}
	seen := make(map[string]bool)
	entry := false
	for _, f := range c.Files {
		name := strings.ToLower(f.Path)
		if !safeRelative(f.Path) || !validDigest(f.SHA256) || seen[name] {
			return errors.New("도구 파일 경로·SHA256·중복을 확인하세요")
		}
		seen[name] = true
		if f.Path == c.Entrypoint {
			entry = true
		}
	}
	if !entry {
		return errors.New("실행 파일이 무결성 목록에 없습니다")
	}
	return nil
}

func safeRelative(p string) bool {
	return p != "" && !strings.ContainsAny(p, "\\:\x00") && !strings.HasPrefix(p, "/") && filepath.IsLocal(filepath.FromSlash(p)) && filepath.ToSlash(filepath.Clean(filepath.FromSlash(p))) == p
}
func validDigest(d string) bool {
	raw, err := hex.DecodeString(d)
	return err == nil && len(raw) == sha256.Size
}
func knownKey(key string) bool {
	for _, k := range ComponentKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Every ancestor is checked too: junctions and symbolic-link directories must
// not redirect an otherwise relative verified path outside the bundle.
func rejectLinks(p string) error {
	for current := filepath.Clean(p); ; current = filepath.Dir(current) {
		st, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("도구 경로 확인: %w", err)
		}
		if st.Mode()&os.ModeSymlink != 0 || isReparsePoint(st) {
			return errors.New("도구 경로에 심볼릭 링크 또는 재분석 지점이 있습니다")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func ValidateDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("도구 디렉터리가 절대 경로가 아닙니다")
	}
	if err := rejectLinks(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("도구 작업 경로가 폴더가 아닙니다")
	}
	return nil
}
