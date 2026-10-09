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
				if status.Key == "browser" && status.State == "verified" {
					components[index].Message = "브라우저 파일은 검증됐지만 Windows AppContainer 내부 IPC와 승인 대상 네트워크 중계가 아직 준비되지 않았습니다"
				}
			}
			message = "명령·스크립트·PTY는 검증된 앱 도구를 AppContainer 작업 폴더와 네트워크 차단 안에서 실행합니다. 브라우저의 격리된 대상 연결은 아직 준비되지 않았습니다"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": false, "mode": mode, "message": message, "components": components, "isolation": isolated})
}
