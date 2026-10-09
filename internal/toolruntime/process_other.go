//go:build !windows

package toolruntime

import (
	"context"
	"errors"
)

func (b *Bundle) startIsolated(context.Context, Request) (*Process, error) {
	return nil, errors.New("이 운영체제의 외부 명령 격리를 아직 검증하지 않았습니다")
}
func Isolation() IsolationStatus {
	return IsolationStatus{State: "not_prepared", ProcessTree: "not_prepared", Workspace: "not_prepared", Network: "not_prepared", Message: "이 운영체제의 도구 격리는 미지원입니다"}
}

func TerminalAvailable() bool { return false }
func InstallProcessTreeGuard() error {
	return errors.New("이 운영체제의 자손 프로세스 격리는 미지원입니다")
}
