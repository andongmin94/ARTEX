package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func denyFixtureFileWrites(t *testing.T, path string) func() {
	t.Helper()
	original, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	originalACL, _, err := original.DACL()
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := original.Control()
	if err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	user, err := token.GetTokenUser()
	token.Close()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	const fileDeleteChild = 0x40
	deny := windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | fileDeleteChild | windows.DELETE
	restricted, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(D;;0x%x;;;%s)(A;;FA;;;%s)(A;;FA;;;SY)", deny, sid, sid))
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := restricted.DACL()
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		security := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
		if control&windows.SE_DACL_PROTECTED != 0 {
			security = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, security, nil, nil, originalACL, nil); err != nil {
			t.Errorf("restore temporary fixture ACL: %v", err)
			return
		}
		restored = true
	}
	t.Cleanup(restore)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	return restore
}

func TestWorkspaceWindowsACLWriteFailurePreservesFileAndRecovers(t *testing.T) {
	f := newWorkspaceHTTPFixture(t)
	const relative = "acl-fixture/한글 파일.txt"
	mustWorkspaceOK(t, f.call(t, "write", relative, "old committed bytes"))
	path := filepath.Join(f.s.m.workspaceDir(), filepath.FromSlash(relative))
	restoreDirectory := denyFixtureFileWrites(t, filepath.Dir(path))
	restoreFile := denyFixtureFileWrites(t, path)
	// Prove this is an actual filesystem denial, independent of the API.
	if file, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		file.Close()
		t.Fatal("temporary NTFS ACL did not block write access")
	} else if !os.IsPermission(err) {
		t.Fatalf("ACL probe returned unexpected error: %v", err)
	}
	response := f.call(t, "write", relative, "must not publish")
	if response.Code == 200 {
		t.Fatal("write incorrectly succeeded despite OS ACL denial")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "old committed bytes" {
		t.Fatalf("ACL failure lost existing file: %q %v", got, err)
	}
	entries, err := os.ReadDir(f.s.m.workspaceDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("ACL failure left temporary uploads: count=%d err=%v", len(entries), err)
	}
	restoreFile()
	restoreDirectory()
	mustWorkspaceOK(t, f.call(t, "write", relative, "recovered bytes"))
	if got, err := os.ReadFile(path); err != nil || string(got) != "recovered bytes" {
		t.Fatalf("write did not recover after ACL restoration: %q %v", got, err)
	}
	t.Logf("actual Windows ACL write denied; API status=%d, prior bytes and temporary cleanup preserved, restoration recovered", response.Code)
}

func TestManagedWindowsACLMoveFailureKeepsSourceAndRecovers(t *testing.T) {
	data := t.TempDir()
	from := filepath.Join(data, "workspace", "tasks", "1", "file.txt")
	to := filepath.Join(data, "archives", "staging", "file.txt")
	if err := os.MkdirAll(filepath.Dir(from), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, []byte("preserve source"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := denyFixtureFileWrites(t, filepath.Dir(to))
	if err := managedMove(data, from, to); err == nil || !os.IsPermission(err) {
		t.Fatalf("wanted real ACL-denied move, got %v", err)
	}
	if got, err := os.ReadFile(from); err != nil || string(got) != "preserve source" {
		t.Fatalf("failed move changed source: %q %v", got, err)
	}
	assertPathMissing(t, to)
	restore()
	if err := managedMove(data, from, to); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(to); err != nil || string(got) != "preserve source" {
		t.Fatalf("move did not recover: %q %v", got, err)
	}
}
