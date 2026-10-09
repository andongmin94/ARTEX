package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Autumn-27/artex/internal/backup"
)

func maintenanceCommand(ctx context.Context, create, restore, home, verify, data string) (bool, int) {
	count := 0
	for _, p := range []string{create, restore, verify} {
		if p != "" {
			count++
		}
	}
	if count == 0 && home == "" {
		return false, 0
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	fail := func(err error) (bool, int) { fmt.Fprintf(os.Stderr, "백업/복원 실패: %v\n", err); return true, 1 }
	if count != 1 || (restore == "" && home != "") || (restore != "" && home == "") || data != "" {
		return fail(fmt.Errorf("백업·복원·검사 중 하나와 올바른 새 홈 옵션을 지정하세요"))
	}
	for _, key := range []string{"ARTEX_CONFIG", "ARTEX_SKILL_DIR"} {
		if os.Getenv(key) != "" {
			return fail(fmt.Errorf("%s를 해제한 기본 앱 데이터 홈만 백업/복원할 수 있습니다", key))
		}
	}
	var r backup.Result
	var err error
	switch {
	case create != "":
		source := strings.TrimSpace(os.Getenv("ARTEX_HOME"))
		if !filepath.IsAbs(source) {
			return fail(fmt.Errorf("백업할 절대 ARTEX_HOME을 지정하세요"))
		}
		r, err = backup.Create(ctx, source, create)
	case restore != "":
		r, err = backup.Restore(ctx, restore, home)
	case verify != "":
		r, err = backup.Verify(ctx, verify)
	}
	if err != nil {
		return fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		return fail(err)
	}
	return true, 0
}
