//go:build !windows

package toolruntime

import "errors"

type BrowserRendererStatus struct {
	PID int `json:"pid"`
}

func PrepareBrowserRuntimeAccess(string, string) error {
	return errors.New("이 OS의 브라우저 격리는 아직 검증되지 않았습니다")
}

func VerifyBrowserRenderer(int, string) (BrowserRendererStatus, error) {
	return BrowserRendererStatus{}, errors.New("이 OS의 브라우저 격리는 아직 검증되지 않았습니다")
}
