package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type workspaceHTTPFixture struct {
	s       *Server
	handler http.Handler
	token   string
}

func newWorkspaceHTTPFixture(t *testing.T) workspaceHTTPFixture {
	t.Helper()
	m, err := NewManager(filepath.Join(t.TempDir(), "앱 데이터"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	resources := t.TempDir()
	s, err := New(context.Background(), m, resources, resources, resources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	return workspaceHTTPFixture{s, s.Handler(), token}
}
func (f workspaceHTTPFixture) call(t *testing.T, operation, path, content string) *httptest.ResponseRecorder {
	t.Helper()
	method := "GET"
	var body io.Reader
	mime := "application/json"
	endpoint := "/api/workspace/" + operation + "?" + url.Values{"path": {path}}.Encode()
	switch operation {
	case "write", "mkdir":
		method = "POST"
		raw, err := json.Marshal(map[string]string{"path": path, "content": content})
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	case "delete":
		method = "DELETE"
	case "upload":
		method = "POST"
		var data bytes.Buffer
		writer := multipart.NewWriter(&data)
		part, err := writer.CreateFormFile("file", "uploaded.txt")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte(content))
		_ = writer.Close()
		body = &data
		mime = writer.FormDataContentType()
	}
	r := httptest.NewRequest(method, endpoint, body)
	r.Header.Set("Content-Type", mime)
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func mustWorkspaceOK(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("workspace HTTP %d: %s", response.Code, response.Body.String())
	}
}

func TestWorkspaceHTTPProtectsApplicationFilesAndSupportsUserCRUD(t *testing.T) {
	f := newWorkspaceHTTPFixture(t)
	unauthenticated := httptest.NewRecorder()
	f.handler.ServeHTTP(unauthenticated, httptest.NewRequest("GET", "/api/workspace/list", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated workspace=%d", unauthenticated.Code)
	}
	managed := map[string]string{"config.json": "protected config", "evidence/blob.txt": "protected evidence", "traffic/request.txt": "protected traffic", "tasks/old/note.txt": "old user file"}
	for name, content := range managed {
		file := filepath.Join(f.s.m.dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.m.pg.SetSetting("workspace_fixture_secret", "protected DB setting"); err != nil {
		t.Fatal(err)
	}
	list := f.call(t, "list", "", "")
	mustWorkspaceOK(t, list)
	var payload struct {
		Entries []wsEntry `json:"entries"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Entries) != 0 {
		t.Fatalf("app files exposed in workspace: %+v", payload.Entries)
	}
	unsafe := []string{"../artex.sqlite", "../artex.sqlite-wal", "..\\artex.sqlite-shm", "folder/../../config.json", "../evidence/blob.txt", "../traffic", "/artex.sqlite", "\\artex.sqlite", filepath.Join(f.s.m.dir, "config.json"), "file.txt:secret", "file.txt::$DATA", "folder.", "folder "}
	if runtime.GOOS == "windows" {
		unsafe = append(unsafe, "NUL", "COM1.txt", "CONIN$", "C:config.json", "\\\\?\\C:\\config.json")
	}
	for _, path := range unsafe {
		for _, operation := range []string{"list", "read", "download", "write", "upload", "delete", "mkdir"} {
			response := f.call(t, operation, path, "must not escape")
			if response.Code != 400 {
				t.Errorf("%s %q = %d, want 400 (%s)", operation, path, response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), f.s.m.dir) {
				t.Errorf("%s leaked app path", operation)
			}
		}
	}
	for _, operation := range []string{"write", "delete", "mkdir"} {
		if got := f.call(t, operation, "", ""); got.Code != 400 {
			t.Errorf("root %s = %d", operation, got.Code)
		}
	}
	mustWorkspaceOK(t, f.call(t, "mkdir", "한글 폴더", ""))
	mustWorkspaceOK(t, f.call(t, "write", "한글 폴더/메모.txt", "첫 기록"))
	mustWorkspaceOK(t, f.call(t, "write", "한글 폴더/메모.txt", "수정된 기록"))
	read := f.call(t, "read", "한글 폴더/메모.txt", "")
	mustWorkspaceOK(t, read)
	if !strings.Contains(read.Body.String(), "수정된 기록") {
		t.Fatal(read.Body.String())
	}
	download := f.call(t, "download", "한글 폴더/메모.txt", "")
	mustWorkspaceOK(t, download)
	if download.Body.String() != "수정된 기록" || !strings.Contains(download.Header().Get("Content-Disposition"), "filename*=UTF-8''") {
		t.Fatal("download content/filename mismatch")
	}
	mustWorkspaceOK(t, f.call(t, "upload", "한글 폴더", "uploaded fixture"))
	if content, err := os.ReadFile(filepath.Join(f.s.m.workspaceDir(), "한글 폴더", "uploaded.txt")); err != nil || string(content) != "uploaded fixture" {
		t.Fatalf("actual upload=%q %v", content, err)
	}
	mustWorkspaceOK(t, f.call(t, "delete", "한글 폴더", ""))
	assertPathMissing(t, filepath.Join(f.s.m.workspaceDir(), "한글 폴더"))
	for name, want := range managed {
		content, err := os.ReadFile(filepath.Join(f.s.m.dir, filepath.FromSlash(name)))
		if err != nil || string(content) != want {
			t.Fatalf("managed file changed %s: %q %v", name, content, err)
		}
	}
	if got, ok, err := f.s.m.pg.GetSetting("workspace_fixture_secret"); err != nil || !ok || got != "protected DB setting" {
		t.Fatalf("DB setting changed: %q %v %v", got, ok, err)
	}
	var integrity string
	if err := f.s.m.pg.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("SQLite integrity=%q %v", integrity, err)
	}
}

func workspaceOutsideLink(t *testing.T, destination, outside string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", destination, outside).CombinedOutput()
		if err != nil {
			t.Fatalf("create Windows junction: %v %s", err, output)
		}
	} else if err := os.Symlink(outside, destination); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(destination) })
}

func TestWorkspaceHTTPRejectsJunctionsSymlinksAndHardLinks(t *testing.T) {
	f := newWorkspaceHTTPFixture(t)
	outside := filepath.Join(f.s.m.dir, "evidence")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "protected.txt")
	if err := os.WriteFile(file, []byte("protected evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceOutsideLink(t, filepath.Join(f.s.m.workspaceDir(), "linked"), outside)
	for _, operation := range []string{"list", "read", "download", "write", "upload", "delete", "mkdir"} {
		path := "linked/protected.txt"
		if operation == "list" || operation == "upload" || operation == "mkdir" || operation == "delete" {
			path = "linked"
		}
		if got := f.call(t, operation, path, "replace evidence"); got.Code != 400 {
			t.Errorf("junction %s=%d %s", operation, got.Code, got.Body.String())
		}
	}
	link := filepath.Join(f.s.m.workspaceDir(), "hard-link.txt")
	if err := os.Link(file, link); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"read", "download", "write", "delete"} {
		if got := f.call(t, operation, "hard-link.txt", "overwrite"); got.Code != 400 {
			t.Errorf("hard link %s=%d", operation, got.Code)
		}
	}
	list := f.call(t, "list", "", "")
	mustWorkspaceOK(t, list)
	if strings.Contains(list.Body.String(), "linked") || strings.Contains(list.Body.String(), "hard-link") {
		t.Fatal("links listed", list.Body.String())
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "protected evidence" {
		t.Fatalf("external linked file changed: %q %v", got, err)
	}
	if err := os.Link(file, filepath.Join(f.s.m.workspaceDir(), "uploaded.txt")); err != nil {
		t.Fatal(err)
	}
	if got := f.call(t, "upload", "", "overwrite"); got.Code != 400 {
		t.Fatalf("hard-link upload=%d %s", got.Code, got.Body.String())
	}
	// Recursive removal unlinks a nested junction without following its target.
	mustWorkspaceOK(t, f.call(t, "mkdir", "remove-me", ""))
	workspaceOutsideLink(t, filepath.Join(f.s.m.workspaceDir(), "remove-me", "outside"), outside)
	mustWorkspaceOK(t, f.call(t, "delete", "remove-me", ""))
	if got, err := os.ReadFile(file); err != nil || string(got) != "protected evidence" {
		t.Fatalf("recursive delete escaped root: %q %v", got, err)
	}
}

func TestWorkspaceStartupRejectsAliasedRootAndKeepsOldFiles(t *testing.T) {
	data := t.TempDir()
	outside := t.TempDir()
	workspaceOutsideLink(t, filepath.Join(data, "workspace"), outside)
	if root, err := openWorkspaceDirectory(data); err == nil {
		root.Close()
		t.Fatal("aliased workspace root accepted")
	}
	if err := os.Remove(filepath.Join(data, "workspace")); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(data, "tasks", "42", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("old file"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := openWorkspaceDirectory(data)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	if _, err := stageTaskArchiveFiles(data, 1, "42", 1); err == nil {
		t.Fatal("archive silently omitted previous workspace")
	}
	if _, err := deleteTaskFiles(data, "42", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != "old file" {
		t.Fatalf("old user file moved/deleted: %q %v", got, err)
	}
}

func TestWorkspaceManagedToolDirectoryRejectsParentJunctions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ARTEX_HOME", home)
	data := filepath.Join(home, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := openWorkspaceDirectory(data)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	outside := t.TempDir()
	workspaceOutsideLink(t, filepath.Join(workspaceDirectory(data), "tool-workspaces"), outside)
	if _, err := newManagedToolWorkspace("fixture-"); err == nil {
		t.Fatal("tool directory created through parent junction")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("tool creation modified outside directory: %+v %v", entries, err)
	}
}

func TestWorkspaceChatAttachmentsUseBrowsableRootWithoutOverwriting(t *testing.T) {
	f := newWorkspaceHTTPFixture(t)
	for i := 0; i < 2; i++ {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "한글 첨부.txt")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(part, "attachment %d", i)
		_ = writer.Close()
		r := httptest.NewRequest("POST", "/api/chat/upload?scope=staging&id=fixture", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("Authorization", "Bearer "+f.token)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		mustWorkspaceOK(t, w)
		var payload struct {
			Attachments []chatAttachment `json:"attachments"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Attachments) != 1 {
			t.Fatal(w.Body.String())
		}
		rel, err := filepath.Rel(f.s.m.workspaceDir(), payload.Attachments[0].Abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatal("chat attachment outside browsable root")
		}
		read := f.call(t, "read", filepath.ToSlash(rel), "")
		mustWorkspaceOK(t, read)
		if !strings.Contains(read.Body.String(), fmt.Sprintf("attachment %d", i)) {
			t.Fatal(read.Body.String())
		}
	}
	list := f.call(t, "list", "drafts/fixture/uploads", "")
	mustWorkspaceOK(t, list)
	var payload struct {
		Entries []wsEntry `json:"entries"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil || len(payload.Entries) != 2 {
		t.Fatalf("attachments clobbered: %s %v", list.Body.String(), err)
	}
}

func TestWorkspaceMultipartRejectsOriginalPathNamesBeforeSaving(t *testing.T) {
	f := newWorkspaceHTTPFixture(t)
	for _, name := range []string{"../config.json", "folder/file.txt", "folder\\file.txt", "file.txt:stream", "COM1.txt"} {
		var data bytes.Buffer
		writer := multipart.NewWriter(&data)
		for _, fileName := range []string{"valid.txt", name} {
			part, err := writer.CreateFormFile("file", fileName)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write([]byte("must not save"))
		}
		_ = writer.Close()
		r := httptest.NewRequest("POST", "/api/workspace/upload", &data)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("Authorization", "Bearer "+f.token)
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, r)
		if response.Code != 400 {
			t.Fatalf("multipart %q=%d %s", name, response.Code, response.Body.String())
		}
		assertPathMissing(t, filepath.Join(f.s.m.workspaceDir(), "valid.txt"))
	}
}

func TestWorkspaceTaskDeleteReportsPreservedOldFolder(t *testing.T) {
	f := newWorkspaceHTTPFixture(t)
	task, err := f.s.m.CreateTask("workspace fixture", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(f.s.m.dir, "tasks", task.ID, "old.txt")
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("preserve old file"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.s.m.DeleteTask(task.ID, DeleteTaskOptions{DeleteFiles: true})
	if err != nil || result.CleanupWarning == "" || result.FilesDeleted {
		t.Fatalf("old workspace delete=%+v %v", result, err)
	}
	if content, err := os.ReadFile(old); err != nil || string(content) != "preserve old file" {
		t.Fatalf("old file changed: %q %v", content, err)
	}
}
