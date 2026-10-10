package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/Autumn-27/artex/internal/toolruntime"

	actool "github.com/Autumn-27/norma/tool"
)

const unmanagedDesktopToolsMessage = "앱 전용 외부 도구 배포와 검증이 완료되지 않아 외부 명령 실행이 차단되었습니다"

func desktopToolSession() bool { return os.Getenv("ARTEX_DESKTOP_SESSION") != "" }

// The existence of host executables, or even a verified bundle, does not prove
// the OS boundaries required by the application executor are available.
func desktopToolsUnavailable() bool {
	if !desktopToolSession() {
		return false
	}
	_, err := toolruntime.FromEnvironment()
	return err != nil || toolruntime.Isolation().State != "available"
}

type unavailableDesktopTool struct{ actool.CoreTool }

func (t unavailableDesktopTool) Call(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
	return actool.Errorf(unmanagedDesktopToolsMessage), nil
}

func externalProcessTool(name string) bool {
	switch name {
	case "Bash", "Monitor", "shell_open", "shell_send", "shell_read", "shell_close", "shell_list", "TaskOutput", "TaskStop", "TaskList":
		return true
	}
	return false
}

func (s *Server) runtimeTools(w http.ResponseWriter, r *http.Request) {
	type component struct {
		toolruntime.ComponentStatus
		Execution string `json:"execution"`
	}
	components := make([]component, 0, len(toolruntime.ComponentKeys))
	for _, key := range toolruntime.ComponentKeys {
		components = append(components, component{ComponentStatus: toolruntime.ComponentStatus{Key: key, State: "not_prepared"}, Execution: "blocked"})
	}
	mode, message := "standalone", "앱 전용 도구 배포와 검증이 완료되지 않았습니다. 호스트 도구는 준비 완료로 표시하지 않습니다"
	isolated := toolruntime.Isolation()
	ready := false
	browser := component{ComponentStatus: toolruntime.ComponentStatus{Key: "browser", State: "not_prepared", Source: "https://github.com/electron/electron", License: "MIT/Chromium third-party notices"}, Execution: "blocked"}
	if desktopToolSession() {
		mode = "desktop"
		bundle, err := toolruntime.FromEnvironment()
		if err != nil {
			message = fmt.Sprintf("%s: %s", unmanagedDesktopToolsMessage, err)
		} else {
			statuses := bundle.Status()
			bundleValid := true
			for _, status := range statuses {
				if status.State == "invalid" {
					bundleValid = false
				}
			}
			for index, status := range statuses {
				components[index].ComponentStatus = status
				if bundleValid && status.State == "verified" && isolated.State == "available" {
					switch status.Key {
					case "shell", "python", "node", "cli":
						components[index].Execution = "available"
					case "pty":
						if toolruntime.TerminalAvailable() {
							components[index].Execution = "available"
						}
					}
				}
				if status.Key == "pty" && status.State == "verified" && !toolruntime.TerminalAvailable() {
					components[index].Message = "관리된 대화형 PTY 연결은 아직 준비되지 않았습니다"
				}
			}
			if s.browser != nil && bundleValid && isolated.State == "available" {
				if available, diagnostic := s.browser.health(r.Context()); available {
					browser.State, browser.Execution, browser.Message = "verified", "available", diagnostic
				} else {
					browser.Message = diagnostic
				}
			}
			ready = browser.Execution == "available"
			for _, component := range components {
				ready = ready && component.Execution == "available"
			}
			message = "명령·스크립트·PTY는 작업별 AppContainer에서 실행하며 브라우저는 Chromium lockdown AppContainer와 작업 승인 대상 중계를 사용합니다"
			if !ready {
				message = "일부 도구의 배포 또는 실제 OS 격리 검증이 완료되지 않았습니다. 진단을 확인하세요"
			}
		}
	}
	components = append(components, browser)
	writeJSON(w, http.StatusOK, map[string]any{"ready": ready, "mode": mode, "message": message, "components": components, "isolation": isolated})
}
