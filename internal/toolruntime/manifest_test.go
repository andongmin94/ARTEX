package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeManifestFixture(t *testing.T, root string, files map[string][]byte) string {
	t.Helper()
	entries := []File{}
	for path, body := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0o700); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		entries = append(entries, File{Path: path, SHA256: hex.EncodeToString(digest[:])})
	}
	manifest := Manifest{Schema: 1, Platform: runtime.GOOS + "-" + runtime.GOARCH, Components: []Component{{Key: "node", Version: "fixture-1", Source: "https://example.invalid/runtime/fixture-1", License: "MIT", Entrypoint: "bin/helper.exe", Files: entries}}}
	return writeManifestValue(t, root, manifest)
}

func writeManifestValue(t *testing.T, root string, m Manifest) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func TestManifestRejectsChangedFilesAndUntrustedManifest(t *testing.T) {
	root := t.TempDir()
	digest := writeManifestFixture(t, root, map[string][]byte{"bin/helper.exe": []byte("fixture"), "LICENSE": []byte("fixture license")})
	b, err := Load(root, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Verify("node"); err != nil {
		t.Fatal(err)
	}
	if status := b.Status(); status[3].State != "verified" || status[0].State != "not_prepared" {
		t.Fatal(status)
	}
	if err := os.WriteFile(filepath.Join(root, "unlisted.exe"), []byte("unexpected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.VerifyAll(); err == nil {
		t.Fatal("unlisted runtime executable accepted")
	}
	if status := b.Status(); status[3].State != "invalid" {
		t.Fatal("unlisted file reported executable", status)
	}
	if err := os.Remove(filepath.Join(root, "unlisted.exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Verify("node"); err == nil {
		t.Fatal("modified runtime dependency accepted")
	}
	if status := b.Status(); status[3].State != "invalid" {
		t.Fatal(status)
	}
	if _, err := Load(root, strings.Repeat("0", 64)); err == nil {
		t.Fatal("self-modified manifest accepted")
	}
	if _, err := Load(root, ""); err == nil {
		t.Fatal("manifest digest missing")
	}
}

func TestManifestRejectsMalformedContract(t *testing.T) {
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Schema = 2 },
		func(m *Manifest) { m.Platform = "unsupported" },
		func(m *Manifest) { m.Components[0].Key = "host" },
		func(m *Manifest) { m.Components = append(m.Components, m.Components[0]) },
		func(m *Manifest) { m.Components[0].Version = "" },
		func(m *Manifest) { m.Components[0].License = "" },
		func(m *Manifest) { m.Components[0].Source = "http://example.invalid" },
		func(m *Manifest) { m.Components[0].Entrypoint = "../../escape.exe" },
		func(m *Manifest) { m.Components[0].Files[0].Path = "bin/../escape.exe" },
		func(m *Manifest) { m.Components[0].Files[0].Path = "bin/escape.exe:stream" },
		func(m *Manifest) { m.Components[0].Files[0].SHA256 = "invalid" },
	} {
		root := t.TempDir()
		digest := writeManifestFixture(t, root, map[string][]byte{"bin/helper.exe": []byte("fixture")})
		b, err := Load(root, digest)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&b.Manifest)
		digest = writeManifestValue(t, root, b.Manifest)
		if _, err := Load(root, digest); err == nil {
			t.Fatalf("bad manifest accepted: %+v", b.Manifest)
		}
	}
}

func TestManagedRequestRejectsHostEnvironmentAndUnsafeWorkDir(t *testing.T) {
	root := t.TempDir()
	digest := writeManifestFixture(t, root, map[string][]byte{"bin/helper.exe": []byte("fixture")})
	b, err := Load(root, digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, work := range []string{root, filepath.Dir(root), "relative"} {
		if _, err := b.Start(context.Background(), Request{Component: "node", WorkingDir: work}); err == nil {
			t.Fatal("unsafe workspace accepted", work)
		}
	}
	for _, key := range []string{"PATH", "Path", "PYTHONPATH", "NODE_OPTIONS", "BASH_ENV", "ARTEX_DESKTOP_SESSION"} {
		if _, err := b.Start(context.Background(), Request{Component: "node", WorkingDir: t.TempDir(), Environment: map[string]string{key: "host"}}); err == nil {
			t.Fatal("host environment accepted", key)
		}
	}
}
