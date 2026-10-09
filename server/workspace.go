package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// User artifacts have their own root, separate from application-owned stores.
const (
	maxWorkspaceRead   = 2 << 20
	maxWorkspaceUpload = 512 << 20
)

var errWorkspacePath = errors.New("허용되지 않는 작업 공간 경로")

func workspaceDirectory(dataDir string) string { return filepath.Join(dataDir, "workspace") }
func (m *Manager) workspaceDir() string        { return workspaceDirectory(m.dir) }

// Staging helpers also own managed transcripts. Check the user side before
// their cross-directory rename, including aliased parent directories.
func validateWorkspaceArtifactPath(dataDir, name string) error {
	root, err := openWorkspaceDirectory(dataDir)
	if err != nil {
		return err
	}
	defer root.Close()
	server := &Server{m: &Manager{dir: dataDir, workRoot: root}}
	_, err = server.wsPath(name, true)
	return err
}

func newManagedToolWorkspace(prefix string) (string, error) {
	home := os.Getenv("ARTEX_HOME")
	if !filepath.IsAbs(home) {
		return "", errors.New("앱 데이터 홈이 지정되지 않았습니다")
	}
	dataDir := filepath.Join(home, "data")
	root, err := openWorkspaceDirectory(dataDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	server := &Server{m: &Manager{dir: dataDir, workRoot: root}}
	parent, err := server.wsPath("tool-workspaces", true)
	if err != nil {
		return "", err
	}
	if err := root.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	name := filepath.Join(parent, prefix+uuid.NewString())
	if err := root.Mkdir(name, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(workspaceDirectory(dataDir), name), nil
}

func openWorkspaceDirectory(dataDir string) (*os.Root, error) {
	data, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	if err := data.Mkdir("workspace", 0o700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := data.Lstat("workspace")
	if err != nil || !info.IsDir() || workspaceLink(info) {
		return nil, errWorkspacePath
	}
	root, err := data.OpenRoot("workspace")
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, errWorkspacePath
	}
	return root, nil
}

// Reject traversal rather than silently rewriting it. os.Root also confines
// actual operations if a checked component is concurrently replaced by a link.
func workspacePath(rel string) (string, error) {
	if strings.ContainsAny(rel, ":\x00\r\n") {
		return "", errWorkspacePath
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || rel == "." {
		return ".", nil
	}
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", errWorkspacePath
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || workspaceReservedName(part) {
			return "", errWorkspacePath
		}
	}
	return filepath.Clean(filepath.FromSlash(rel)), nil
}

func workspaceReservedName(part string) bool {
	base := strings.ToUpper(strings.TrimRight(strings.SplitN(part, ".", 2)[0], " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		suffix := strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT")
		return strings.Contains("123456789¹²³", suffix) && suffix != "" && len([]rune(suffix)) == 1
	}
	return false
}

func workspaceUploadName(hdr *multipart.FileHeader) (string, error) {
	_, params, err := mime.ParseMediaType(hdr.Header.Get("Content-Disposition"))
	if err != nil {
		return "", errWorkspacePath
	}
	name, err := workspacePath(params["filename"])
	if err != nil || name == "." || filepath.Base(name) != name {
		return "", errWorkspacePath
	}
	return name, nil
}

func (s *Server) wsPath(rel string, mutate bool) (string, error) {
	name, err := workspacePath(rel)
	if err != nil || (mutate && name == ".") || s.m.workRoot == nil {
		return "", errWorkspacePath
	}
	current := ""
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := s.m.workRoot.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil || workspaceLink(info) || (!info.IsDir() && !info.Mode().IsRegular()) {
			return "", errWorkspacePath
		}
	}
	return name, nil
}
func wsRel(name string) string {
	if name == "." {
		return ""
	}
	return filepath.ToSlash(name)
}

type wsEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

func (s *Server) wsOpenFile(name string) (*os.File, os.FileInfo, error) {
	file, err := s.m.workRoot.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || workspaceMultipleLinks(file, info)) {
		err = errWorkspacePath
	}
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}
func workspaceError(w http.ResponseWriter, err error) {
	if os.IsNotExist(err) {
		writeErr(w, 404, "경로가 존재하지 않습니다")
	} else if errors.Is(err, errWorkspacePath) || os.IsPermission(err) {
		writeErr(w, 400, errWorkspacePath.Error())
	} else {
		writeErr(w, 500, "작업 파일 처리에 실패했습니다")
	}
}

func (s *Server) wsList(w http.ResponseWriter, r *http.Request) {
	name, err := s.wsPath(r.URL.Query().Get("path"), false)
	if err != nil {
		workspaceError(w, err)
		return
	}
	dir, err := s.m.workRoot.Open(name)
	if err != nil {
		workspaceError(w, err)
		return
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		workspaceError(w, err)
		return
	}
	out := make([]wsEntry, 0, len(entries))
	for _, entry := range entries {
		child, err := s.wsPath(filepath.Join(name, entry.Name()), false)
		if err != nil {
			continue
		}
		info, err := s.m.workRoot.Lstat(child)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			file, _, err := s.wsOpenFile(child)
			if err != nil {
				continue
			}
			file.Close()
		}
		out = append(out, wsEntry{entry.Name(), wsRel(child), info.IsDir(), info.Size(), info.ModTime().UnixMilli()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, 200, map[string]any{"path": wsRel(name), "entries": out})
}
func (s *Server) wsRead(w http.ResponseWriter, r *http.Request) {
	name, err := s.wsPath(r.URL.Query().Get("path"), false)
	if err != nil {
		workspaceError(w, err)
		return
	}
	file, info, err := s.wsOpenFile(name)
	if err != nil {
		workspaceError(w, err)
		return
	}
	defer file.Close()
	if info.Size() > maxWorkspaceRead {
		writeJSON(w, 200, map[string]any{"path": wsRel(name), "size": info.Size(), "too_large": true, "binary": true})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWorkspaceRead+1))
	if err != nil {
		workspaceError(w, err)
		return
	}
	if len(data) > maxWorkspaceRead {
		writeJSON(w, 200, map[string]any{"path": wsRel(name), "size": len(data), "too_large": true, "binary": true})
		return
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		writeJSON(w, 200, map[string]any{"path": wsRel(name), "size": len(data), "binary": true})
		return
	}
	writeJSON(w, 200, map[string]any{"path": wsRel(name), "size": len(data), "binary": false, "content": string(data)})
}

// A fresh file and root-relative rename replace the directory entry instead of
// truncating a concurrently introduced hard link to an app-managed file.
func (s *Server) wsSave(name string, content io.Reader) error {
	if file, _, err := s.wsOpenFile(name); err == nil {
		file.Close()
	} else if !os.IsNotExist(err) {
		return err
	}
	root := s.m.workRoot
	if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	temp := ".upload-" + uuid.NewString()
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, copyErr := io.Copy(file, content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	return root.Rename(temp, name)
}
func (s *Server) wsWrite(w http.ResponseWriter, r *http.Request) {
	var req struct{ Path, Content string }
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	name, err := s.wsPath(req.Path, true)
	if err == nil {
		err = s.wsSave(name, strings.NewReader(req.Content))
	}
	if err != nil {
		workspaceError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": wsRel(name)})
}
func (s *Server) wsMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct{ Path string }
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	name, err := s.wsPath(req.Path, true)
	if err == nil {
		err = s.m.workRoot.MkdirAll(name, 0o700)
	}
	if err != nil {
		workspaceError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": wsRel(name)})
}
func (s *Server) wsDelete(w http.ResponseWriter, r *http.Request) {
	name, err := s.wsPath(r.URL.Query().Get("path"), true)
	if err == nil {
		var info os.FileInfo
		info, err = s.m.workRoot.Stat(name)
		if err == nil && !info.IsDir() {
			var file *os.File
			file, _, err = s.wsOpenFile(name)
			if file != nil {
				file.Close()
			}
		}
	}
	if err == nil {
		err = s.m.workRoot.RemoveAll(name)
	}
	if err != nil {
		workspaceError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) wsDownload(w http.ResponseWriter, r *http.Request) {
	name, err := s.wsPath(r.URL.Query().Get("path"), false)
	if err != nil {
		workspaceError(w, err)
		return
	}
	file, info, err := s.wsOpenFile(name)
	if err != nil {
		workspaceError(w, err)
		return
	}
	defer file.Close()
	base := filepath.Base(name)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(base)+"\"; filename*=UTF-8''"+url.PathEscape(base))
	http.ServeContent(w, r, base, info.ModTime(), file)
}
func (s *Server) wsUpload(w http.ResponseWriter, r *http.Request) {
	name, err := s.wsPath(r.URL.Query().Get("path"), false)
	if err != nil {
		workspaceError(w, err)
		return
	}
	info, err := s.m.workRoot.Stat(name)
	if err != nil || !info.IsDir() {
		writeErr(w, 400, "대상 디렉터리가 존재하지 않습니다")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkspaceUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "업로드 해석 실패 또는 크기 상한 초과")
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeErr(w, 400, "업로드 파일이 없습니다(폼 필드 file)")
		return
	}
	destinations := make([]string, len(files))
	for i, hdr := range files {
		fileName, err := workspaceUploadName(hdr)
		if err != nil {
			workspaceError(w, errWorkspacePath)
			return
		}
		destinations[i], err = s.wsPath(filepath.Join(name, fileName), true)
		if err != nil {
			workspaceError(w, err)
			return
		}
	}
	for i, hdr := range files {
		src, err := hdr.Open()
		if err != nil {
			workspaceError(w, err)
			return
		}
		err = s.wsSave(destinations[i], src)
		src.Close()
		if err != nil {
			workspaceError(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"uploaded": len(files)})
}

func saveChatUpload(root *os.Root, hdr *multipart.FileHeader, dir, name string) (string, error) {
	src, err := hdr.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		dest := filepath.Join(dir, candidate)
		out, err := root.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(out, src)
		return dest, errors.Join(copyErr, out.Close())
	}
}
func sanitizeFilename(name string) string {
	return strings.NewReplacer("\"", "", "\\", "", "\n", "", "\r", "").Replace(name)
}
