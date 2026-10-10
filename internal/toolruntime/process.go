package toolruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Request struct {
	Component  string
	Args       []string
	WorkingDir string
	Terminal   bool
	Rows       int
	Cols       int
	// Environment deliberately does not inherit os.Environ. Only scalar tool
	// parameters and explicit MCP values accepted by the caller are forwarded.
	Environment map[string]string
}

type Process struct {
	Stdin     io.WriteCloser
	Stdout    io.ReadCloser
	Stderr    io.ReadCloser
	PID       int
	wait      func() (int, error)
	kill      func() error
	closeOnce sync.Once
	closeErr  error
}

func (p *Process) Wait() (int, error) { return p.wait() }
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.kill()
		_ = p.Stdin.Close()
		_ = p.Stdout.Close()
		_ = p.Stderr.Close()
	})
	return p.closeErr
}

func (b *Bundle) Start(ctx context.Context, r Request) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.VerifyAll(); err != nil {
		return nil, err
	}
	if r.Terminal {
		if r.Rows == 0 {
			r.Rows = 24
		}
		if r.Cols == 0 {
			r.Cols = 80
		}
		if r.Rows < 1 || r.Rows > 200 || r.Cols < 1 || r.Cols > 400 {
			return nil, errors.New("PTY 화면 크기가 허용 범위를 벗어났습니다")
		}
	}
	if _, ok := b.Component(r.Component); !ok {
		return nil, errors.New("도구 구성요소가 배포되지 않았습니다")
	}
	if !filepath.IsAbs(r.WorkingDir) {
		return nil, errors.New("도구 작업 폴더가 절대 경로가 아닙니다")
	}
	r.WorkingDir = filepath.Clean(r.WorkingDir)
	if err := rejectLinks(r.WorkingDir); err != nil {
		return nil, err
	}
	canonicalWork, err := filepath.EvalSymlinks(r.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("도구 작업 경로 정규화: %w", err)
	}
	if err := rejectLinks(canonicalWork); err != nil {
		return nil, err
	}
	r.WorkingDir = canonicalWork
	info, err := os.Stat(r.WorkingDir)
	if err != nil || !info.IsDir() {
		return nil, errors.New("도구 작업 폴더가 없습니다")
	}
	if rel, err := filepath.Rel(r.WorkingDir, b.Root); err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return nil, errors.New("도구 설치 경로를 포함하는 작업 폴더를 허용하지 않습니다")
	}
	if rel, err := filepath.Rel(b.Root, r.WorkingDir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("도구 설치 폴더에서 작업을 실행할 수 없습니다")
	}
	for k, v := range r.Environment {
		upper := strings.ToUpper(k)
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, '\x00') {
			return nil, errors.New("잘못된 도구 환경 변수입니다")
		}
		switch upper {
		case "PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TMP", "TEMP", "PYTHONPATH", "PYTHONHOME", "PYTHONNOUSERSITE", "PYTHONDONTWRITEBYTECODE", "PYTHONUTF8", "NODE_OPTIONS", "NODE_PATH", "BASH_ENV", "ENV", "COMSPEC", "ARTEX_DESKTOP_SESSION", "PSMODULEPATH", "POWERSHELL_TELEMETRY_OPTOUT", "DOTNET_CLI_TELEMETRY_OPTOUT":
			return nil, errors.New("도구 실행 경로나 호스트 설정을 환경 변수로 바꿀 수 없습니다")
		}
	}
	return b.startIsolated(ctx, r)
}

type IsolationStatus struct {
	State       string `json:"state"`
	ProcessTree string `json:"process_tree"`
	Workspace   string `json:"workspace"`
	Network     string `json:"network"`
	Message     string `json:"message,omitempty"`
}
