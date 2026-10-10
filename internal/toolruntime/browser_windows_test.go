package toolruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsBrowserRuntimeVolumeBoundary(t *testing.T) {
	for _, test := range []struct {
		name, root, home string
		overlap          bool
	}{
		{"different-drives", `D:\ARTEX`, `C:\Users\fixture\ARTEX`, false},
		{"different-drives-reversed", `c:\ARTEX`, `d:\private`, false},
		{"same-drive-case", `C:\ARTEX`, `c:\artex\private`, true},
		{"same-directory", `C:\ARTEX`, `c:\artex`, true},
		{"parent-home", `C:\ARTEX\public`, `c:\artex`, true},
		{"same-drive-siblings", `C:\ARTEX`, `c:\ARTEX-data`, false},
		{"extended-drive-alias", `\\?\C:\ARTEX`, `c:\ARTEX\private`, true},
		{"device-drive-alias", `\\.\C:\ARTEX`, `c:\ARTEX\private`, true},
		{"nt-drive-alias", `\??\C:\ARTEX`, `c:\ARTEX\private`, true},
		{"extended-distinct-drive", `\\?\D:\ARTEX`, `C:\private`, false},
		{"same-unc-volume", `\\fixture\share\ARTEX`, `\\FIXTURE\SHARE\ARTEX\private`, true},
		{"extended-unc-alias", `\\?\UNC\fixture\share\ARTEX`, `\\FIXTURE\SHARE\ARTEX\private`, true},
		{"unknown-device-volume", `\\?\Volume{fixture}\ARTEX`, `C:\private`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := browserRuntimePathsOverlap(test.root, test.home); got != test.overlap {
				t.Fatalf("overlap = %v, want %v", got, test.overlap)
			}
		})
	}
}

func TestWindowsBrowserRuntimeRejectsPrivateAndAliasedPaths(t *testing.T) {
	root := t.TempDir()
	public := filepath.Join(root, "public")
	home := filepath.Join(root, "private")
	if err := os.MkdirAll(public, 0700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(public, "electron.exe")
	if err := os.WriteFile(exe, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := browserRuntimePath(exe, home); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{public, filepath.Join(public, "new-home"), root} {
		if _, err := browserRuntimePath(exe, candidate); err == nil {
			t.Fatalf("runtime/home overlap accepted: %s", candidate)
		}
	}
	nested := filepath.Join(public, "resources")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(nested, "jwt.key")
	if err := os.WriteFile(key, []byte("private-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareBrowserRuntimeAccess(exe, home); err == nil {
		t.Fatal("nested credentials were made public")
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(public, "linked-home")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if err := PrepareBrowserRuntimeAccess(exe, home); err == nil {
		t.Fatal("runtime reparse path accepted")
	}
}

func TestWindowsBrowserRendererRejectsHostAndWrongExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBrowserRenderer(os.Getpid(), exe); err == nil {
		t.Fatal("ordinary Go host process accepted as lockdown AppContainer renderer")
	}
	fake := filepath.Join(t.TempDir(), "electron.exe")
	if err := os.WriteFile(fake, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBrowserRenderer(os.Getpid(), fake); err == nil {
		t.Fatal("foreign process executable accepted")
	}
	if _, err := VerifyBrowserRenderer(0, exe); err == nil {
		t.Fatal("missing PID accepted")
	}
}
