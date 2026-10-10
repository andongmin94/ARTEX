// Command artex runs the ARTEX backend: the SQLite application store,
// SQLite traffic index, event-driven exploration engine, and JSON HTTP API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/internal/backup"
	"github.com/Autumn-27/artex/internal/toolruntime"
	"github.com/Autumn-27/artex/server"
)

// version is the build version, injected at release time via
// -ldflags "-X main.version=<tag>". Defaults to "dev" for local builds.
var version = "dev"

const banner = `
    _    ____ _____ _______  __
   / \  |  _ \_   _| ____\ \/ /
  / _ \ | |_) || | |  _|  \  /
 / ___ \|  _ < | | | |___ /  \
/_/   \_\_| \_\|_| |_____/_/\_\
`

// printBanner writes the startup banner + version/runtime info to stdout.
func printBanner(addr string) {
	fmt.Print(banner)
	fmt.Println("  AI 자율 침투 테스트 시스템")
	fmt.Printf("  버전 %s · %s/%s · %s · 수신 주소 %s\n\n",
		version, runtime.GOOS, runtime.GOARCH, runtime.Version(), addr)
}

// Keep exit outside run so every owned service can drain its resources first.
func main() {
	os.Exit(run())
}

func run() (code int) {
	var (
		addr           = flag.String("addr", "127.0.0.1:8787", "HTTP listen address")
		dataDir        = flag.String("data", "", "data directory (default: data/ under ARTEX_HOME or the executable directory)")
		proxy          = flag.String("proxy", "127.0.0.1:8788", "traffic recording proxy address (empty to disable)")
		readyStdout    = flag.Bool("ready-stdout", false, "emit a JSON ready event to stdout instead of the startup banner")
		parentStdin    = flag.Bool("parent-stdin", false, "shut down when the supervising parent's stdin pipe closes")
		backupPath     = flag.String("backup", "", "create a verified offline snapshot in a new absolute directory")
		restorePath    = flag.String("restore", "", "restore a verified snapshot directory into a new home")
		restoreHome    = flag.String("restore-home", "", "new absolute data home for snapshot restoration")
		verifyBackup   = flag.String("verify-backup", "", "verify every snapshot file and SQLite database")
		browserRuntime = flag.String("prepare-browser-runtime", "", "prepare read-only access to the public Electron runtime")
		browserHome    = flag.String("browser-runtime-home", "", "private data home excluded from public browser runtime access")
	)
	flag.Parse()
	if *browserRuntime != "" {
		if err := toolruntime.PrepareBrowserRuntimeAccess(*browserRuntime, *browserHome); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if handled, result := maintenanceCommand(context.Background(), *backupPath, *restorePath, *restoreHome, *verifyBackup, *dataDir); handled {
		return result
	}

	if err := config.InitHome(); err != nil {
		fmt.Fprintf(os.Stderr, "runtime home: %v\n", err)
		return 1
	}
	home, err := filepath.Abs(config.BaseDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "데이터 홈: %v\n", err)
		return 1
	}
	lease, err := backup.LockHome(home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "데이터 홈 잠금: %v\n", err)
		return 1
	}
	defer lease.Close()
	if os.Getenv("ARTEX_DESKTOP_SESSION") != "" {
		if runtime.GOOS == "windows" {
			if err := toolruntime.InstallProcessTreeGuard(); err != nil {
				log.Printf("외부 도구 프로세스 격리 초기화: %v", err)
				return 1
			}
		}
		// Use the SDK's native Go search implementation. A desktop startup must
		// never install a global npm package or discover host PATH as a tool bundle.
		if err := os.Setenv("NORMA_DISABLE_RIPGREP", "1"); err != nil {
			log.Printf("configure desktop file search: %v", err)
			return 1
		}
	}
	if *dataDir == "" {
		*dataDir = filepath.Join(config.BaseDir(), "data")
	}

	// hand the build version to the server package so GET /api/health can report it
	// to the frontend top bar.
	server.BuildVersion = version

	if !*readyStdout {
		printBanner(*addr)
	}

	// capture backend logs into the in-memory sink (still to stderr) so the /logs
	// page can show a live log stream. Do this first, to catch startup logs too.
	server.StartLogCapture()

	cfgPath := config.Path()
	if abs, e := filepath.Abs(cfgPath); e == nil {
		cfgPath = abs
	}
	if _, e := os.Stat(cfgPath); e == nil {
		log.Printf("[config] 설정 파일: %s", cfgPath)
	} else {
		log.Printf("[config] 설정 파일: %s(파일 없음 — 로컬 기본값을 사용합니다)", cfgPath)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, shutdown := shutdownContext(sigCtx)
	defer shutdown(agent.AbortShutdown)
	if *parentStdin {
		defer os.Stdin.Close()
		go waitForParent(os.Stdin, func() { shutdown(agent.AbortShutdown) })
	}

	mgr, err := server.NewManager(*dataDir, *proxy)
	if err != nil {
		log.Printf("open stores: %v", err)
		return 1
	}
	defer func() {
		shutdown(agent.AbortShutdown)
		if err := mgr.Close(); err != nil {
			log.Printf("store shutdown: %v", err)
			code = 1
		}
	}()
	if ctx.Err() != nil {
		return 0
	}

	skillDir, err := config.SkillDir()
	if err != nil {
		log.Printf("initialize skills: %v", err)
		return 1
	}
	if abs, err := filepath.Abs(skillDir); err == nil {
		skillDir = abs
	}
	log.Printf("[config] 스킬 디렉터리: %s", skillDir)
	srv, err := server.New(ctx, mgr, skillDir, *dataDir, config.BaseDir())
	if err != nil {
		log.Printf("initialize server: %v", err)
		return 1
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Close(closeCtx); err != nil {
			log.Printf("server shutdown: %v", err)
			code = 1
		}
	}()
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, serveDone, err := startHTTP(httpSrv, srv.ConfigureDesktopListener)
	if err != nil {
		log.Printf("listen: %v", err)
		return 1
	}
	defer httpSrv.Close()
	log.Printf("ARTEX %s backend listening on %s (data=%s, workers=%d)", version, listener.Addr(), *dataDir, mgr.Workers())
	if *readyStdout {
		if err := announceReady(os.Stdout, listener.Addr(), version); err != nil {
			log.Printf("announce ready: %v", err)
			return 1
		}
	}

	code = 0
	select {
	case <-ctx.Done():
	case err := <-serveDone:
		log.Printf("HTTP server stopped unexpectedly: %v", err)
		code = 1
	case err := <-mgr.ProxyDone():
		log.Printf("traffic proxy stopped unexpectedly: %v", err)
		code = 1
	}
	shutdown(agent.AbortShutdown)

	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdownHTTP(shutdownCtx, httpSrv); err != nil {
		log.Printf("HTTP shutdown: %v", err)
		code = 1
	}
	return code
}

// shutdownContext deliberately does not derive from signalCtx. If it did, the
// parent's plain context.Canceled could win the race before AbortShutdown was
// attached to the child, losing the diagnostic cause in every running Agent.
func shutdownContext(signalCtx context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, shutdown := context.WithCancelCause(context.Background())
	go func() {
		select {
		case <-signalCtx.Done():
			shutdown(agent.AbortShutdown)
		case <-ctx.Done():
		}
	}()
	return ctx, shutdown
}
