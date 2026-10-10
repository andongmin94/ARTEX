package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// This changes only the current test's new SQLite file. Denied NTFS access is
// an actual filesystem error, unlike PRAGMA query_only; hardware faults remain
// a separate validation condition.
func TestSQLiteWindowsACLFailurePreservesCommittedDataAndRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "실제 권한 실패.sqlite")
	d := openBusinessFixture(t, path)
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "committed", "z": "committed"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
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
	denied, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(D;;0x%x;;;%s)(A;;FA;;;%s)(A;;FA;;;SY)", windows.FILE_WRITE_DATA|windows.FILE_APPEND_DATA, sid, sid))
	if err != nil {
		t.Fatal(err)
	}
	deniedACL, _, err := denied.DACL()
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
			t.Errorf("restore test SQLite ACL: %v", err)
			return
		}
		restored = true
	}
	t.Cleanup(restore)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, deniedACL, nil); err != nil {
		t.Fatal(err)
	}
	if file, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
		file.Close()
		t.Fatal("NTFS ACL did not deny write access")
	} else if !os.IsPermission(err) {
		t.Fatalf("ACL probe error: %v", err)
	}
	blocked, err := Open(path)
	if blocked != nil {
		blocked.Close()
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("wanted actual filesystem access denial, got %v", err)
	}
	t.Log("actual Windows ACL rejected the business DB writable-file preflight before modernc initialization")
	restore()
	d = openBusinessFixture(t, path)
	values, err := d.SettingsSnapshot(t.Context())
	if err != nil || values["a"] != "committed" || values["z"] != "committed" {
		t.Fatalf("ACL failure changed settings: values=%v err=%v", values, err)
	}
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "recovered", "z": "recovered"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = openBusinessFixture(t, path)
	values, err = d.SettingsSnapshot(t.Context())
	if err != nil || values["a"] != "recovered" || values["z"] != "recovered" {
		t.Fatalf("ACL recovery did not persist: values=%v err=%v", values, err)
	}
	assertSQLiteIntegrity(t, d)
}
