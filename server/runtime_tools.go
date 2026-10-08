package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	actool "github.com/Autumn-27/norma/tool"
)

const unmanagedDesktopToolsMessage = "앱 전용 외부 도구 배포와 검증이 완료되지 않아 외부 명령 실행이 차단되었습니다"

// A desktop session must never treat host PATH executables as an installed,
// verified app bundle. There is currently no managed tool bundle in this build.
func desktopToolsUnavailable() bool { return os.Getenv("ARTEX_DESKTOP_SESSION") != "" }

type unavailableDesktopTool struct{ actool.CoreTool }

func (t unavailableDesktopTool) Call(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
	return actool.Errorf(unmanagedDesktopToolsMessage), nil
}

func externalProcessTool(name string) bool {
	switch name {
	case "Bash", "shell_open", "shell_send", "shell_read", "shell_close", "shell_list", "TaskOutput", "TaskStop":
		return true
	}
	return false
}

func (s *Server) runtimeTools(w http.ResponseWriter, r *http.Request) {
	type component struct {
		Key   string `json:"key"`
		State string `json:"state"`
	}
	components := make([]component, 0, 6)
	for _, key := range []string{"shell", "pty", "python", "node", "browser", "cli"} {
		components = append(components, component{Key: key, State: "not_prepared"})
	}
	mode, message := "standalone", "앱 전용 도구 배포와 검증이 완료되지 않았습니다. 호스트 도구는 준비 완료로 표시하지 않습니다"
	if desktopToolsUnavailable() {
		mode, message = "desktop", unmanagedDesktopToolsMessage
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": false, "mode": mode, "message": message, "components": components})
}
