package toolruntime

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func shortAndLongPaths(t *testing.T, path string) (string, string) {
	t.Helper()
	long, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	n, err := windows.GetShortPathName(source, nil, 0)
	if err != nil || n == 0 {
		t.Fatal("Windows short path unavailable", err)
	}
	buffer := make([]uint16, n+1)
	n, err = windows.GetShortPathName(source, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		t.Fatal("Windows short path lookup failed", err)
	}
	short := windows.UTF16ToString(buffer[:n])
	if strings.EqualFold(long, short) {
		t.Fatal("fixture has no actual Windows 8.3 alias", long)
	}
	return long, short
}

func TestWindowsCanonicalWorkspaceAliasesShareIsolationIdentity(t *testing.T) {
	b, work := executableFixture(t)
	longWork, shortWork := shortAndLongPaths(t, work)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var processes []*Process
	defer func() {
		cancel()
		for _, process := range processes {
			_ = process.Close()
		}
	}()
	for _, workPath := range []string{longWork, shortWork} {
		process, err := b.Start(ctx, Request{Component: "node", Args: []string{"-test.run=^TestManagedHelperProcess$"}, WorkingDir: workPath, Environment: map[string]string{"ARTEX_TEST_RUNTIME_HELPER": "sleep"}})
		if err != nil {
			t.Fatal(err)
		}
		processes = append(processes, process)
	}
	profileMu.Lock()
	identities := len(profiles)
	references := 0
	for _, count := range profiles {
		references += count
	}
	profileMu.Unlock()
	if identities != 1 || references != 2 {
		t.Fatalf("same workspace aliases created separate permissions: identities=%d references=%d", identities, references)
	}
}

func junctionFixture(t *testing.T, path, target string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	substitute := utf16.Encode([]rune(`\??\` + target))
	printed := utf16.Encode([]rune(target))
	buffer := make([]byte, 16+2*(len(substitute)+len(printed)+2))
	binary.LittleEndian.PutUint32(buffer, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buffer[4:], uint16(len(buffer)-8))
	binary.LittleEndian.PutUint16(buffer[10:], uint16(len(substitute)*2))
	binary.LittleEndian.PutUint16(buffer[12:], uint16((len(substitute)+1)*2))
	binary.LittleEndian.PutUint16(buffer[14:], uint16(len(printed)*2))
	for i, c := range substitute {
		binary.LittleEndian.PutUint16(buffer[16+i*2:], c)
	}
	for i, c := range printed {
		binary.LittleEndian.PutUint16(buffer[16+(len(substitute)+1+i)*2:], c)
	}
	var returned uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsCanonicalizationStillRejectsJunctions(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "bundle")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := writeManifestFixture(t, root, map[string][]byte{"bin/helper.exe": []byte("fixture"), "LICENSE": []byte("fixture license")})
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "bundle-link")
	junctionFixture(t, link, canonicalRoot)
	if _, err := Load(link, digest); err == nil || !strings.Contains(err.Error(), "재분석") {
		t.Fatal("bundle junction accepted", err)
	}
	b, err := Load(root, digest)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(parent, "workspace")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalWork, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	workLink := filepath.Join(parent, "workspace-link")
	junctionFixture(t, workLink, canonicalWork)
	process, err := b.Start(context.Background(), Request{Component: "node", WorkingDir: workLink})
	if process != nil {
		process.Close()
		t.Fatal("workspace junction accepted")
	}
	if err == nil || !strings.Contains(err.Error(), "재분석") {
		t.Fatal("workspace junction resolved before rejection", err)
	}
}

func TestWindowsBundleRejectsShortLongWorkspaceAliases(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Managed Runtime Bundle")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := writeManifestFixture(t, root, map[string][]byte{"bin/helper.exe": []byte("fixture"), "LICENSE": []byte("fixture license")})
	longRoot, shortRoot := shortAndLongPaths(t, root)
	longChild, shortChild := shortAndLongPaths(t, filepath.Join(root, "bin"))
	longParent, shortParent := shortAndLongPaths(t, parent)
	for _, bundlePath := range []struct{ name, path string }{{"long", longRoot}, {"short", shortRoot}} {
		t.Run(bundlePath.name, func(t *testing.T) {
			bundle, err := Load(bundlePath.path, digest)
			if err != nil {
				t.Fatal(err)
			}
			if bundle.Root != longRoot {
				t.Errorf("bundle root was not canonicalized: got %q want %q", bundle.Root, longRoot)
			}
			for _, work := range []struct{ name, path string }{
				{"same-long", longRoot}, {"same-short", shortRoot},
				{"descendant-long", longChild}, {"descendant-short", shortChild},
				{"ancestor-long", longParent}, {"ancestor-short", shortParent},
			} {
				t.Run(work.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					process, err := bundle.Start(ctx, Request{Component: "node", WorkingDir: work.path})
					if process != nil {
						process.Close()
						t.Fatal("bundle alias accepted as writable workspace")
					}
					if err == nil || !strings.Contains(err.Error(), "도구 설치") {
						t.Fatalf("bundle alias bypassed the relationship check: %v", err)
					}
				})
			}
		})
	}
}
