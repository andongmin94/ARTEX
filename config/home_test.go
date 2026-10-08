package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolateHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("ARTEX_HOME", home)
	t.Setenv("ARTEX_CONFIG", "")
	t.Setenv("ARTEX_SKILL_DIR", "")
}

func TestHomeInitializesWritableUserDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "한국어 사용자", "ARTEX data")
	isolateHome(t, home)
	for i := 0; i < 2; i++ {
		if err := InitHome(); err != nil {
			t.Fatal(err)
		}
	}
	if got := BaseDir(); got != home {
		t.Fatalf("BaseDir = %q, want %q", got, home)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("write probe left files: %v, %v", entries, err)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("new home is accessible to other users: %v", info.Mode())
	}
	got, err := SkillDir()
	if err != nil || got != filepath.Join(home, "skills") {
		t.Fatalf("SkillDir = %q", got)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("skills not initialized: %v", err)
	}
}

func TestHomeRejectsRelativePath(t *testing.T) {
	isolateHome(t, filepath.Join("relative", "artex"))
	if err := InitHome(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path error, got %v", err)
	}
}

func TestHomeRejectsFile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(home, []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
	isolateHome(t, home)
	if err := InitHome(); err == nil {
		t.Fatal("accepted a file as home")
	}
	data, err := os.ReadFile(home)
	if err != nil || string(data) != "preserve me" {
		t.Fatalf("changed an existing file: %q %v", data, err)
	}
}

func TestHomeDoesNotUseWorkingDirectoryConfig(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "config.json"), []byte(`{"skill_dir":"wrong","database":{"dsn":"postgres://wrong/db"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Error(err)
		}
	})
	home := filepath.Join(t.TempDir(), "home")
	isolateHome(t, home)
	if err := InitHome(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "config.json")
	if got := Path(); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	cfg, err := Load()
	if err != nil || cfg.SkillDir != "" {
		t.Fatal("loaded unrelated CWD config when home config was missing")
	}
	if err := os.WriteFile(want, []byte(`{"skill_dir":"home-skills"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(); err != nil || got.SkillDir != "home-skills" {
		t.Fatalf("home config not loaded: %+v %v", got, err)
	}
}

func TestHomePreservesExplicitOverrides(t *testing.T) {
	home := t.TempDir()
	isolateHome(t, home)
	configPath := filepath.Join(t.TempDir(), "explicit.json")
	skills := filepath.Join(t.TempDir(), "explicit-skills")
	t.Setenv("ARTEX_CONFIG", configPath)
	t.Setenv("ARTEX_SKILL_DIR", skills)
	got, err := SkillDir()
	if err != nil || Path() != configPath || got != skills {
		t.Fatal("explicit overrides were ignored")
	}
}

func TestHomeUnsetRemainsStandalone(t *testing.T) {
	isolateHome(t, "")
	if err := InitHome(); err != nil {
		t.Fatal(err)
	}
	if BaseDir() == "" || Path() == "" {
		t.Fatal("standalone paths are empty")
	}
}
