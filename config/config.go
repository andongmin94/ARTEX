// Package config locates writable runtime files and local skill configuration.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk config file shape.
type Config struct {
	SkillDir string `json:"skill_dir"`
}

// InitHome validates the explicit writable runtime root before opening stores.
// Electron will supply its userData directory as ARTEX_HOME. No files are moved
// from an older installation, and an invalid path never falls back to the CWD.
// Call this once at process startup, before starting any worker goroutines.
func InitHome() error {
	home := strings.TrimSpace(os.Getenv("ARTEX_HOME"))
	if home == "" {
		return nil
	}
	if !filepath.IsAbs(home) {
		return fmt.Errorf("ARTEX_HOME must be an absolute path: %q", home)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create ARTEX_HOME: %w", err)
	}
	probe, err := os.CreateTemp(home, ".artex-write-check-*")
	if err != nil {
		return fmt.Errorf("ARTEX_HOME is not writable: %w", err)
	}
	closeErr := probe.Close()
	removeErr := os.Remove(probe.Name())
	if closeErr != nil {
		return fmt.Errorf("check ARTEX_HOME: %w", closeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("clean ARTEX_HOME write check: %w", removeErr)
	}
	return nil
}

// BaseDir anchors writable runtime artifacts. An explicit ARTEX_HOME is
// authoritative; it is validated by InitHome at startup. Without it, the current
// standalone executable still uses its own directory (or CWD for go run).
func BaseDir() string {
	if home := strings.TrimSpace(os.Getenv("ARTEX_HOME")); home != "" {
		return filepath.Clean(home)
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	if isGoRunDir(dir) {
		return "." // throwaway `go run` binary → use CWD
	}
	return dir
}

// isGoRunDir reports whether dir is where `go run` parked its executable: under
// the system temp dir (cache miss), or anywhere inside a Go build cache
// (…/go-build/…, the cache-hit case — NOT under os.TempDir(), which is why the
// old temp-only check failed intermittently). In both cases the binary is
// throwaway, so config/data must resolve against the CWD.
func isGoRunDir(dir string) bool {
	if tmp := os.TempDir(); tmp != "" {
		if rel, err := filepath.Rel(tmp, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	for _, seg := range strings.Split(filepath.ToSlash(dir), "/") {
		if seg == "go-build" {
			return true
		}
	}
	return false
}

// Path resolves an explicit ARTEX_CONFIG first, then ARTEX_HOME/config.json.
// When ARTEX_HOME is set, a missing file must not select an unrelated CWD config.
// Without either override, the standalone executable searches CWD and BaseDir.
func Path() string {
	if v := strings.TrimSpace(os.Getenv("ARTEX_CONFIG")); v != "" {
		return v
	}
	if strings.TrimSpace(os.Getenv("ARTEX_HOME")) != "" {
		return filepath.Join(BaseDir(), "config.json")
	}
	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "config.json"))
	}
	candidates = append(candidates, filepath.Join(BaseDir(), "config.json"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// Load accepts a missing optional config file, but never hides unreadable or
// malformed configuration supplied by the user.
func Load() (Config, error) {
	var c Config
	b, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("설정 파일 읽기: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("설정 파일 JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, fmt.Errorf("설정 파일에는 하나의 JSON 객체만 허용됩니다")
	}
	return c, nil
}

// SkillDir returns the skill root directory with precedence:
//
//	env ARTEX_SKILL_DIR  >  config file (skill_dir)  >  BaseDir()/skills
//
// The directory is created if it does not exist.
func SkillDir() (string, error) {
	cfg, err := Load()
	if err != nil {
		return "", err
	}
	var d string
	if v := strings.TrimSpace(os.Getenv("ARTEX_SKILL_DIR")); v != "" {
		d = v
	} else if v := strings.TrimSpace(cfg.SkillDir); v != "" {
		d = v
	} else {
		d = filepath.Join(BaseDir(), "skills")
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("스킬 디렉터리 초기화: %w", err)
	}
	return d, nil
}
