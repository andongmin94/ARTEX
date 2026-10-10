package toolruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// BrowserRendererStatus describes the live Chromium renderer token, rather than
// treating sandbox launch flags as evidence that the OS boundary exists.
type BrowserRendererStatus struct {
	PID          int      `json:"pid"`
	AppContainer bool     `json:"app_container"`
	SID          string   `json:"sid"`
	Capabilities int      `json:"capabilities"`
	Restricted   []string `json:"restricted_sids"`
	Integrity    string   `json:"integrity"`
}

func browserRuntimePathsOverlap(root, home string) bool {
	volume := func(path string) string {
		name := strings.ToUpper(filepath.Clean(path))
		// Extended DOS/UNC names must not make the same volume look disjoint.
		for _, prefix := range []string{`\\?\`, `\\.\`, `\??\`} {
			if strings.HasPrefix(name, prefix) {
				name = strings.TrimPrefix(name, prefix)
				if strings.HasPrefix(name, `UNC\`) {
					name = `\\` + strings.TrimPrefix(name, `UNC\`)
				}
				break
			}
		}
		return filepath.VolumeName(name)
	}
	rootVolume, homeVolume := volume(root), volume(home)
	if rootVolume == "" || homeVolume == "" {
		return true
	}
	if rootVolume != homeVolume {
		return false
	}
	for _, pair := range [][2]string{{root, home}, {home, root}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func browserRuntimePath(executable, home string) (string, error) {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(home) {
		return "", errors.New("브라우저 실행 파일과 데이터 홈은 절대 경로여야 합니다")
	}
	executable, home = filepath.Clean(executable), filepath.Clean(home)
	if err := rejectLinks(executable); err != nil {
		return "", err
	}
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	// A new home may not exist yet. Canonicalize its nearest existing parent
	// too, so short names/case/junctions cannot conceal a runtime overlap.
	ancestor := home
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(ancestor) == ancestor {
			return "", errors.New("사용자 데이터 홈 경로를 확인할 수 없습니다")
		}
		ancestor = filepath.Dir(ancestor)
	}
	if err := rejectLinks(ancestor); err != nil {
		return "", err
	}
	canonicalAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	remainder, err := filepath.Rel(ancestor, home)
	if err != nil {
		return "", err
	}
	home = filepath.Join(canonicalAncestor, remainder)
	root := filepath.Dir(executable)
	if browserRuntimePathsOverlap(root, home) {
		return "", errors.New("공개 브라우저 런타임과 사용자 데이터 홈은 겹칠 수 없습니다")
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Electron 실행 파일을 확인할 수 없습니다")
	}
	// This ACL is only for public app resources. Reject misplaced private data
	// before granting application packages read/execute access to the directory.
	for _, private := range []string{"config.json", "jwt.key", "data", "chatgpt", ".active-home.json"} {
		if _, err := os.Lstat(filepath.Join(root, private)); err == nil || !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("공개 브라우저 런타임 폴더에 사용자 데이터가 있습니다")
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(info) {
			return errors.New("공개 브라우저 런타임에 재분석 경로가 있습니다")
		}
		switch strings.ToLower(entry.Name()) {
		case "config.json", "jwt.key", ".active-home.json", "artex.sqlite", "artex.sqlite-wal", "artex.sqlite-shm":
			return errors.New("공개 브라우저 런타임에 사용자 저장소 또는 인증 설정이 있습니다")
		}
		return nil
	}); err != nil {
		return "", err
	}
	return root, nil
}

// PrepareBrowserRuntimeAccess grants read/execute only to the public Electron
// installation. No task workspace, profile, credential, window-station or
// network capability is granted. Electron 44 requires this install-file ACE
// when its renderer is placed in an AppContainer.
func PrepareBrowserRuntimeAccess(executable, home string) error {
	root, err := browserRuntimePath(executable, home)
	if err != nil {
		return err
	}
	sid, err := windows.StringToSid("S-1-15-2-1") // ALL APPLICATION PACKAGES.
	if err != nil {
		return err
	}
	if err := grantFolder(root, sid, windows.GENERIC_READ|windows.GENERIC_EXECUTE); err != nil {
		return fmt.Errorf("공개 Electron 런타임 읽기 권한: %w", err)
	}
	return nil
}

func browserTokenInfo(token windows.Token, class uint32) ([]byte, error) {
	var size uint32
	err := windows.GetTokenInformation(token, class, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size == 0 || size > 1<<20 {
		return nil, errors.New("브라우저 renderer 토큰 크기를 확인할 수 없습니다")
	}
	buf := make([]byte, size)
	if err := windows.GetTokenInformation(token, class, &buf[0], size, &size); err != nil {
		return nil, err
	}
	return buf, nil
}

func VerifyBrowserRenderer(pid int, executable string) (BrowserRendererStatus, error) {
	out := BrowserRendererStatus{PID: pid}
	if pid <= 0 || !filepath.IsAbs(executable) {
		return out, errors.New("브라우저 renderer PID 또는 실행 파일이 유효하지 않습니다")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return out, err
	}
	defer windows.CloseHandle(process)
	name := make([]uint16, 32768)
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(process, 0, &name[0], &size); err != nil {
		return out, err
	}
	actual, err := os.Stat(windows.UTF16ToString(name[:size]))
	if err != nil {
		return out, err
	}
	expected, err := os.Stat(executable)
	if err != nil || !os.SameFile(actual, expected) {
		return out, errors.New("브라우저 renderer가 앱 Electron 실행 파일과 다릅니다")
	}
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return out, err
	}
	defer token.Close()
	// TOKEN_INFORMATION_CLASS values from the Windows SDK: IsAppContainer=29,
	// Capabilities=30 and AppContainerSid=31 are absent from x/sys's enum list.
	b, err := browserTokenInfo(token, 29)
	if err != nil || len(b) < 4 {
		return out, errors.New("브라우저 AppContainer 토큰을 읽지 못했습니다")
	}
	out.AppContainer = *(*uint32)(unsafe.Pointer(&b[0])) != 0
	b, err = browserTokenInfo(token, 31)
	if err != nil || len(b) < int(unsafe.Sizeof(uintptr(0))) {
		return out, errors.New("브라우저 AppContainer SID를 읽지 못했습니다")
	}
	if sid := *(**windows.SID)(unsafe.Pointer(&b[0])); sid != nil {
		out.SID = sid.String()
	}
	b, err = browserTokenInfo(token, 30)
	if err != nil || len(b) < 4 {
		return out, errors.New("브라우저 capability 토큰을 읽지 못했습니다")
	}
	out.Capabilities = int(*(*uint32)(unsafe.Pointer(&b[0])))
	b, err = browserTokenInfo(token, windows.TokenIntegrityLevel)
	if err != nil || len(b) < int(unsafe.Sizeof(windows.SIDAndAttributes{})) {
		return out, errors.New("브라우저 무결성 수준을 읽지 못했습니다")
	}
	out.Integrity = (*windows.SIDAndAttributes)(unsafe.Pointer(&b[0])).Sid.String()
	b, err = browserTokenInfo(token, windows.TokenRestrictedSids)
	offset := int(unsafe.Offsetof(windows.Tokengroups{}.Groups))
	if err != nil || len(b) < offset {
		return out, errors.New("브라우저 제한 SID를 읽지 못했습니다")
	}
	count := int(*(*uint32)(unsafe.Pointer(&b[0])))
	if count > (len(b)-offset)/int(unsafe.Sizeof(windows.SIDAndAttributes{})) {
		return out, errors.New("브라우저 제한 SID 목록이 유효하지 않습니다")
	}
	if count > 0 {
		for _, group := range unsafe.Slice((*windows.SIDAndAttributes)(unsafe.Pointer(&b[offset])), count) {
			out.Restricted = append(out.Restricted, group.Sid.String())
		}
	}
	if !out.AppContainer || out.SID == "" || out.Capabilities != 0 || out.Integrity != "S-1-16-0" || len(out.Restricted) != 1 || out.Restricted[0] != "S-1-0-0" {
		return out, errors.New("브라우저 renderer의 AppContainer·capability·lockdown 격리가 충족되지 않았습니다")
	}
	return out, nil
}
