package toolruntime

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const securityCapabilitiesAttribute = 0x00020009
const jobListAttribute = 0x0002000d

type securityCapabilities struct {
	AppContainerSID *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

var profileMu sync.Mutex
var profiles = make(map[string]int)
var processGuard struct {
	sync.Mutex
	handle windows.Handle
}

var createProfile = windows.NewLazySystemDLL("userenv.dll").NewProc("CreateAppContainerProfile")
var deriveProfile = windows.NewLazySystemDLL("userenv.dll").NewProc("DeriveAppContainerSidFromAppContainerName")
var deleteProfile = windows.NewLazySystemDLL("userenv.dll").NewProc("DeleteAppContainerProfile")
var updateAttribute = windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
var createConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole")

func TerminalAvailable() bool { return createConsole.Find() == nil && Isolation().State == "available" }

// InstallProcessTreeGuard must run before the desktop backend starts services.
// Membership is inherited during process creation, so it has no child-attach
// race. The non-inheritable unnamed handle stays owned by this process until OS
// exit: closing it earlier would also terminate the current backend itself.
func InstallProcessTreeGuard() error {
	processGuard.Lock()
	defer processGuard.Unlock()
	if processGuard.handle != 0 {
		return nil
	}
	job, err := newKillJob()
	if err != nil {
		return err
	}
	if err = windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("백엔드 자손 프로세스 Job 연결: %w", err)
	}
	processGuard.handle = job
	return nil
}

func Isolation() IsolationStatus {
	if createProfile.Find() != nil || deriveProfile.Find() != nil {
		return IsolationStatus{State: "not_prepared", ProcessTree: "not_prepared", Workspace: "not_prepared", Network: "not_prepared", Message: "Windows AppContainer API를 사용할 수 없습니다"}
	}
	return IsolationStatus{State: "available", ProcessTree: "job_object", Workspace: "appcontainer_acl", Network: "denied"}
}

func newKillJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("도구 Job 생성: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	limits.BasicLimitInformation.ActiveProcessLimit = 64
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("도구 Job 자손 종료 설정: %w", err)
	}
	return job, nil
}

func containerSID(name string) (*windows.SID, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	hr, _, _ := createProfile.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(p)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if uint32(hr) == 0x800700b7 {
		hr, _, _ = deriveProfile.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&sid)))
	}
	if int32(hr) < 0 {
		return nil, fmt.Errorf("도구 AppContainer 생성 HRESULT 0x%08x", uint32(hr))
	}
	defer windows.FreeSid(sid)
	return sid.Copy()
}

func grantFolder(path string, sid *windows.SID, access uint32) error {
	return changeFolderAccess(path, sid, access, windows.GRANT_ACCESS)
}

func changeFolderAccess(path string, sid *windows.SID, access uint32, mode windows.ACCESS_MODE) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	old, _, err := sd.DACL()
	if err != nil {
		return err
	}
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.ACCESS_MASK(access), AccessMode: mode, Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_GROUP, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, old)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func allowLowIntegrityWrites(path string) error {
	sd, err := windows.SecurityDescriptorFromString("S:(ML;OICI;NW;;;LW)")
	if err != nil {
		return err
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl)
}

func (b *Bundle) startIsolated(ctx context.Context, r Request) (_ *Process, resultErr error) {
	if Isolation().State != "available" {
		return nil, errors.New("Windows AppContainer가 준비되지 않았습니다")
	}
	c, _ := b.Component(r.Component)
	exe, err := b.Path(c.Entrypoint)
	if err != nil {
		return nil, err
	}
	// Workspace-specific identities cannot read another task workspace that was
	// granted to a previous invocation. No network capability is ever requested.
	nameHash := sha256.Sum256([]byte(strings.ToLower(r.WorkingDir) + "\x00" + strings.ToLower(b.Root)))
	profileName := fmt.Sprintf("ARTEX.Tool.%x", nameHash[:16])
	profileMu.Lock()
	sid, err := containerSID(profileName)
	if err == nil && profiles[profileName] == 0 {
		err = grantFolder(b.Root, sid, windows.GENERIC_READ|windows.GENERIC_EXECUTE)
	}
	if err == nil && profiles[profileName] == 0 {
		err = grantFolder(r.WorkingDir, sid, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.GENERIC_EXECUTE|windows.DELETE)
	}
	if err == nil && profiles[profileName] == 0 {
		err = allowLowIntegrityWrites(r.WorkingDir)
	}
	if err == nil {
		profiles[profileName]++
	}
	profileMu.Unlock()
	if err != nil {
		if sid != nil {
			_ = changeFolderAccess(b.Root, sid, 0, windows.REVOKE_ACCESS)
			_ = changeFolderAccess(r.WorkingDir, sid, 0, windows.REVOKE_ACCESS)
		}
		return nil, fmt.Errorf("도구 폴더 권한 격리: %w", err)
	}
	var releaseOnce sync.Once
	var releaseErr error
	release := func() error {
		releaseOnce.Do(func() {
			profileMu.Lock()
			defer profileMu.Unlock()
			profiles[profileName]--
			if profiles[profileName] > 0 {
				return
			}
			delete(profiles, profileName)
			releaseErr = errors.Join(changeFolderAccess(b.Root, sid, 0, windows.REVOKE_ACCESS), changeFolderAccess(r.WorkingDir, sid, 0, windows.REVOKE_ACCESS))
			name, _ := windows.UTF16PtrFromString(profileName)
			hr, _, _ := deleteProfile.Call(uintptr(unsafe.Pointer(name)))
			if int32(hr) < 0 {
				releaseErr = errors.Join(releaseErr, fmt.Errorf("도구 AppContainer 정리 HRESULT 0x%08x", uint32(hr)))
			}
		})
		return releaseErr
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, release())
		}
	}()
	job, err := newKillJob()
	if err != nil {
		return nil, err
	}
	// Each invocation is bounded independently from the backend's lifetime job.
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = 32
	limits.JobMemoryLimit = 2 << 30
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("도구 실행 자원 제한: %w", err)
	}
	defer func() {
		if resultErr != nil {
			windows.CloseHandle(job)
		}
	}()
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		return nil, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return nil, err
	}
	defer stdinR.Close()
	defer stdoutW.Close()
	defer stderrW.Close()
	defer func() {
		if resultErr != nil {
			stdinW.Close()
			stdoutR.Close()
			stderrR.Close()
		}
	}()
	var console windows.Handle
	var consoleOnce sync.Once
	closeConsole := func() {
		consoleOnce.Do(func() {
			if console != 0 {
				windows.ClosePseudoConsole(console)
			}
		})
	}
	if r.Terminal {
		if err = windows.CreatePseudoConsole(windows.Coord{X: int16(r.Cols), Y: int16(r.Rows)}, windows.Handle(stdinR.Fd()), windows.Handle(stdoutW.Fd()), 0, &console); err != nil {
			return nil, fmt.Errorf("관리된 ConPTY 생성: %w", err)
		}
		defer func() {
			if resultErr != nil {
				closeConsole()
			}
		}()
	}
	handles := make([]windows.Handle, 3)
	if !r.Terminal {
		for i, f := range []*os.File{stdinR, stdoutW, stderrW} {
			if err = windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(f.Fd()), windows.CurrentProcess(), &handles[i], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
				return nil, err
			}
			defer windows.CloseHandle(handles[i])
		}
	}
	attrs, err := windows.NewProcThreadAttributeList(3)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	caps := securityCapabilities{AppContainerSID: sid}
	if err = attrs.Update(securityCapabilitiesAttribute, unsafe.Pointer(&caps), unsafe.Sizeof(caps)); err != nil {
		return nil, err
	}
	if r.Terminal {
		// Unlike the other attributes, ConPTY takes the opaque HPCON value itself,
		// not its address. Use the native call to avoid pretending a handle is a Go pointer.
		ok, _, nativeErr := updateAttribute.Call(uintptr(unsafe.Pointer(attrs.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(console), unsafe.Sizeof(console), 0, 0)
		if ok == 0 {
			return nil, fmt.Errorf("ConPTY 시작 속성: %w", nativeErr)
		}
	} else {
		if err = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
			return nil, err
		}
	}
	if err = attrs.Update(jobListAttribute, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return nil, err
	}
	si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESHOWWINDOW, ShowWindow: windows.SW_HIDE}, ProcThreadAttributeList: attrs.List()}
	if r.Terminal {
		si.StartupInfo.Flags |= windows.STARTF_USESTDHANDLES
	}
	if !r.Terminal {
		si.StartupInfo.Flags |= windows.STARTF_USESTDHANDLES
		si.StartupInfo.StdInput, si.StartupInfo.StdOutput, si.StartupInfo.StdErr = handles[0], handles[1], handles[2]
	}
	line := syscall.EscapeArg(exe)
	for _, arg := range r.Args {
		line += " " + syscall.EscapeArg(arg)
	}
	app, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return nil, err
	}
	command, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return nil, err
	}
	dir, err := windows.UTF16PtrFromString(r.WorkingDir)
	if err != nil {
		return nil, err
	}
	env, err := b.environment(r)
	if err != nil {
		return nil, err
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NO_WINDOW)
	if r.Terminal {
		flags &^= windows.CREATE_NO_WINDOW
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = windows.CreateProcess(app, command, nil, nil, !r.Terminal, flags, &env[0], dir, &si.StartupInfo, &pi); err != nil {
		return nil, fmt.Errorf("AppContainer 도구 시작: %w", err)
	}
	windows.CloseHandle(pi.Thread)
	p := &Process{Stdin: stdinW, Stdout: stdoutR, Stderr: stderrR, PID: int(pi.ProcessId)}
	if r.Terminal {
		stderrR.Close()
		p.Stderr = io.NopCloser(strings.NewReader(""))
	}
	done := make(chan struct{})
	var waitOnce sync.Once
	var exit int
	var waitErr error
	var jobOnce sync.Once
	closeJob := func() { jobOnce.Do(func() { windows.CloseHandle(job) }) }
	p.wait = func() (int, error) {
		waitOnce.Do(func() {
			_, waitErr = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
			var code uint32
			if waitErr == nil {
				waitErr = windows.GetExitCodeProcess(pi.Process, &code)
			}
			exit = int(code)
			closeJob() // also removes detached descendants after the root exits.
			closeConsole()
			windows.CloseHandle(pi.Process)
			waitErr = errors.Join(waitErr, release(), ctx.Err())
			close(done)
		})
		return exit, waitErr
	}
	p.kill = func() error { closeJob(); _, err := p.Wait(); return err }
	go func() {
		select {
		case <-ctx.Done():
			closeJob()
		case <-done:
		}
	}()
	runtime.KeepAlive(sid)
	return p, nil
}

func (b *Bundle) environment(r Request) ([]uint16, error) {
	windir, err := windows.GetWindowsDirectory()
	if err != nil {
		return nil, err
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, c := range b.Manifest.Components {
		p, err := b.Path(c.Entrypoint)
		if err != nil {
			return nil, err
		}
		dir := filepath.Dir(p)
		if !seen[dir] {
			seen[dir] = true
			paths = append(paths, dir)
		}
	}
	values := map[string]string{"SystemRoot": windir, "WINDIR": windir, "PATH": strings.Join(paths, ";"), "PATHEXT": ".EXE", "HOME": r.WorkingDir, "USERPROFILE": r.WorkingDir, "APPDATA": r.WorkingDir, "LOCALAPPDATA": r.WorkingDir, "TMP": r.WorkingDir, "TEMP": r.WorkingDir, "PYTHONNOUSERSITE": "1", "PYTHONDONTWRITEBYTECODE": "1", "PYTHONUTF8": "1", "POWERSHELL_TELEMETRY_OPTOUT": "1", "DOTNET_CLI_TELEMETRY_OPTOUT": "1"}
	if shell, ok := b.Component("shell"); ok {
		exe, err := b.Path(shell.Entrypoint)
		if err != nil {
			return nil, err
		}
		values["PSModulePath"] = filepath.Join(filepath.Dir(exe), "Modules")
	}
	for k, v := range r.Environment {
		values[k] = v
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToUpper(keys[i]) < strings.ToUpper(keys[j]) })
	block := ""
	for _, k := range keys {
		block += k + "=" + values[k] + "\x00"
	}
	block += "\x00"
	return utf16.Encode([]rune(block)), nil
}
