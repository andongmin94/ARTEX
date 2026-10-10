package server

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf16"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	"golang.org/x/sys/windows"
)

func TestManagedWindowsParentsRemainPinnedDuringConcurrentReplacement(t *testing.T) {
	data := t.TempDir()
	parent := filepath.Join(data, "workspace", "tasks")
	from := filepath.Join(parent, "1")
	to := filepath.Join(data, "archives", "destination", "1")
	if err := os.MkdirAll(filepath.Dir(from), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, []byte("original task bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, unlockSource, err := lockManagedDirectory(root, filepath.Join("workspace", "tasks"), false)
	if err != nil {
		t.Fatal(err)
	}
	_, unlockDestination, err := lockManagedDirectory(root, filepath.Join("archives", "destination"), true)
	if err != nil {
		unlockSource()
		t.Fatal(err)
	}
	var swapped atomic.Int64
	var attempts atomic.Int64
	var group sync.WaitGroup
	for _, path := range []string{filepath.Join(data, "workspace"), parent, filepath.Dir(to)} {
		group.Add(1)
		go func(path string) {
			defer group.Done()
			for range 100 {
				attempts.Add(1)
				if err := os.Rename(path, path+"-replacement"); err == nil {
					swapped.Add(1)
					_ = os.Rename(path+"-replacement", path)
				}
			}
		}(path)
	}
	for range 25 {
		if err := managedMove(data, from, to); err != nil {
			t.Error(err)
			break
		}
		if err := managedMove(data, to, from); err != nil {
			t.Error(err)
			break
		}
	}
	group.Wait()
	if attempts.Load() != 300 || swapped.Load() != 0 {
		t.Errorf("parent replacement attempts=%d successful=%d", attempts.Load(), swapped.Load())
	}
	if err := unlockDestination(); err != nil {
		t.Error(err)
	}
	if err := unlockSource(); err != nil {
		t.Error(err)
	}
	// Prove the fixture can rename once the application's handles are released.
	if err := os.Rename(parent, parent+"-after-release"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent+"-after-release", parent); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(from); err != nil || string(got) != "original task bytes" {
		t.Fatalf("moved wrong source: %q %v", got, err)
	}
	t.Log("300 concurrent ancestor replacement attempts rejected during 50 handle-relative moves; release permits rename")
}

func TestTaskArchiveTrafficRootStaysBoundAfterWindowsJunctionMutation(t *testing.T) {
	data := t.TempDir()
	out := t.TempDir()
	path := filepath.Join(data, "restore", "traffic")
	payload, closePayload, err := openManagedDirectory(data, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer closePayload()
	if err := os.Rename(filepath.Dir(path), filepath.Dir(path)+"-replacement"); err == nil {
		t.Fatal("archive traffic ancestor replaced while Root is held")
	}
	poison, err := json.Marshal(traffic.ArchiveSnapshot{Version: 1, Exchanges: []traffic.ArchiveExchange{{ID: "outside-archive-fixture", Host: "outside.fixture.example", Method: "GET", URL: "http://outside.fixture.example/", Status: 200}}})
	if err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(out, "traffic.json")
	if err := os.WriteFile(protected, poison, 0o600); err != nil {
		t.Fatal(err)
	}
	clear, closeJunction := mutateArchiveTrafficTestJunction(t, path, out)
	defer closeJunction()
	defer clear()
	tr, err := traffic.Open(filepath.Join(data, "recorder"), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	count, importErr := tr.ImportArchive(payload)
	if count != 0 {
		t.Fatalf("mutated archive Root imported outside rows: %d %v", count, importErr)
	}
	var rows int
	if err := tr.DB().QueryRow(`SELECT count(*) FROM exchanges`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("outside manifest reached SQLite: rows=%d err=%v", rows, err)
	}
	_, exportErr := tr.ExportHosts([]string{"empty.fixture.example"}, payload)
	if got, err := os.ReadFile(protected); err != nil || string(got) != string(poison) {
		t.Fatalf("outside manifest changed during archive export: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(out, "blobs")); !os.IsNotExist(err) {
		t.Fatalf("archive export created outside blob directory: %v", err)
	}
	if err := clear(); err != nil {
		t.Fatal(err)
	}
	t.Logf("actual pinned traffic Root ancestor replacement rejected; in-place junction mutation did not import or write outside; import=%v export=%v", importErr, exportErr)
}

// Reparse mutation is allowed despite no-delete-share parent handles. Keep the
// native write handle to remove the actual junction before temporary cleanup.
func mutateArchiveTrafficTestJunction(t *testing.T, path, outside string) (func() error, func()) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	substitute := utf16.Encode([]rune(`\??\` + outside))
	printed := utf16.Encode([]rune(outside))
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
	if err := windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil); err != nil {
		windows.CloseHandle(handle)
		t.Fatal(err)
	}
	var cleared bool
	clear := func() error {
		if cleared {
			return nil
		}
		var deletion [8]byte
		binary.LittleEndian.PutUint32(deletion[:], windows.IO_REPARSE_TAG_MOUNT_POINT)
		err := windows.DeviceIoControl(handle, windows.FSCTL_DELETE_REPARSE_POINT, &deletion[0], uint32(len(deletion)), nil, 0, &returned, nil)
		cleared = err == nil
		return err
	}
	return clear, func() { windows.CloseHandle(handle) }
}

func TestTaskArchiveEvidenceRootStaysBoundAfterWindowsJunctionMutation(t *testing.T) {
	s, finding, _ := trafficEvidenceServer(t)
	body := []byte("preserve portable evidence body")
	seedServerEvidenceFlow(t, s, "root-junction-fixture", body)
	bindings, err := s.evidenceStore().Bind(t.Context(), finding.FindingID, []db.TrafficRef{{TrafficID: "root-junction-fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := bindings.Bindings[0].Snapshot
	path := filepath.Join(s.m.dir, "restore", "evidence")
	payload, closePayload, err := openManagedDirectory(s.m.dir, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer closePayload()
	outside := t.TempDir()
	for hash, content := range map[string][]byte{snapshot.ReqHash: {}, snapshot.RespHash: body} {
		name := filepath.Join(outside, "blobs", hash[:2], hash+".bin")
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clear, closeJunction := mutateArchiveTrafficTestJunction(t, path, outside)
	defer closeJunction()
	defer clear()
	copyErr := s.evidenceStore().CopySnapshots(context.Background(), []db.TrafficEvidenceSnapshot{snapshot}, payload)
	if copyErr == nil {
		t.Fatal("evidence export followed mutated Root outside")
	}
	called := false
	installErr := s.evidenceStore().WithInstalledSnapshots(context.Background(), []db.TrafficEvidenceSnapshot{snapshot}, payload, func(*sql.Tx) error { called = true; return nil })
	if installErr == nil || called {
		t.Fatalf("outside evidence reached metadata commit: err=%v called=%v", installErr, called)
	}
	protected := filepath.Join(outside, "blobs", snapshot.RespHash[:2], snapshot.RespHash+".bin")
	if got, err := os.ReadFile(protected); err != nil || string(got) != string(body) {
		t.Fatalf("outside evidence body changed: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(outside, ".staging")); !os.IsNotExist(err) {
		t.Fatalf("evidence export wrote staging outside Root: %v", err)
	}
	if err := clear(); err != nil {
		t.Fatal(err)
	}
	t.Logf("in-place junction mutation refused evidence copy and metadata restore; outside body/staging preserved; copy=%v install=%v", copyErr, installErr)
}

func TestManagedWindowsPinnedParentCannotRedirectRenameAfterReparseMutation(t *testing.T) {
	data := t.TempDir()
	outside := t.TempDir()
	parentPath := filepath.Join(data, "destination")
	if err := os.Mkdir(parentPath, 0o700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(outside, "protected.txt")
	if err := os.WriteFile(protected, []byte("preserve outside bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(data, "source.txt")
	if err := os.WriteFile(from, []byte("original source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	parent, unlock, err := lockManagedDirectory(root, "destination", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	path, err := windows.UTF16PtrFromString(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	openForReparse := func() (windows.Handle, error) {
		return windows.CreateFile(path, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	}
	// A write-capable process can mutate a directory into a junction even while
	// no-delete-share handles are held. The existing parent handle must keep
	// naming the original directory object rather than resolving the new link.
	handle, err := openForReparse()
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	substitute := utf16.Encode([]rune(`\??\` + outside))
	printed := utf16.Encode([]rune(outside))
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
	err = windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
	if err != nil {
		t.Fatal(err)
	}
	clearReparse := func() error {
		var deletion [8]byte
		binary.LittleEndian.PutUint32(deletion[:], windows.IO_REPARSE_TAG_MOUNT_POINT)
		return windows.DeviceIoControl(handle, windows.FSCTL_DELETE_REPARSE_POINT, &deletion[0], uint32(len(deletion)), nil, 0, &returned, nil)
	}
	defer clearReparse()
	fromParent, unlockFrom, err := lockManagedDirectory(root, ".", false)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockFrom()
	source, err := openManagedHandle(fromParent, "source.txt", windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, false)
	if err != nil {
		t.Fatal(err)
	}
	moveErr := renameManagedHandle(source, parent, "moved.txt")
	if err := windows.CloseHandle(source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "moved.txt")); !os.IsNotExist(err) {
		t.Fatalf("pinned destination escaped after reparse mutation: %v", err)
	}
	if err := managedRemoveAll(data, filepath.Join(parentPath, "protected.txt")); err == nil {
		t.Fatal("newly introduced junction accepted by cleanup")
	}
	if got, err := os.ReadFile(protected); err != nil || string(got) != "preserve outside bytes" {
		t.Fatalf("outside bytes changed: %q %v", got, err)
	}
	if err := clearReparse(); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(parentPath, "moved.txt")
	if moveErr != nil {
		resultPath = from
	}
	if got, err := os.ReadFile(resultPath); err != nil || string(got) != "original source bytes" {
		t.Fatalf("source bytes lost after reparse mutation: %q %v (rename: %v)", got, err, moveErr)
	}
	t.Logf("real junction mutation while parent is pinned did not redirect native rename; source preserved, outside unchanged; rename result=%v", moveErr)
}

func TestManagedWindowsSourceHandleCannotBeReplacedBeforeRename(t *testing.T) {
	data := t.TempDir()
	from := filepath.Join(data, "1")
	if err := os.WriteFile(from, []byte("original bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	parent, unlock, err := lockManagedDirectory(root, ".", false)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	source, err := openManagedHandle(parent, "1", windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, from+"-substitute"); err == nil {
		windows.CloseHandle(source)
		t.Fatal("held source object replaced")
	}
	if err := windows.CloseHandle(source); err != nil {
		t.Fatal(err)
	}
	if err := managedMove(data, from, filepath.Join(data, "2")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(data, "2")); err != nil || string(got) != "original bytes" {
		t.Fatalf("source changed: %q %v", got, err)
	}
}
