package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRejectsInvalidAndUnreadableFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"broken.json", "directory"} {
		path := filepath.Join(dir, name)
		if name == "broken.json" {
			if err := os.WriteFile(path, []byte(`{invalid`), 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ARTEX_CONFIG", path)
		if _, err := Load(); err == nil {
			t.Fatal("invalid config accepted", name)
		}
		if _, err := SkillDir(); err == nil {
			t.Fatal("invalid config accepted for skill directory", name)
		}
	}
	t.Setenv("ARTEX_CONFIG", filepath.Join(dir, "missing.json"))
	if _, err := Load(); err != nil {
		t.Fatal("missing optional config rejected", err)
	}
}

func TestSkillDirRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("ARTEX_SKILL_DIR", path)
	if _, err := SkillDir(); err == nil {
		t.Fatal("file accepted as skill directory")
	}
}

func TestConfigRejectsRemovedDatabaseSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"database":{"dsn":"postgres://obsolete"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_CONFIG", path)
	if _, err := Load(); err == nil {
		t.Fatal("removed database configuration was silently accepted")
	}
}
