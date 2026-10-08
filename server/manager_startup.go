package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"time"

	pgdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/enrich"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/artex/traffic"
)

// NewManager opens the current business store and initializes the services that
// depend on it. A requested but unreadable traffic store is a startup failure.
func NewManager(dir, proxyAddr string) (*Manager, error) {
	dir, err := prepareManagerDirectory(dir)
	if err != nil {
		return nil, err
	}
	filename := filepath.Join(dir, "artex.sqlite")
	store, err := pgdb.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("업무 SQLite 열기: %w", err)
	}
	log.Printf("[sqlite] 업무 저장소: %s", filename)
	return newManagerFromDB(dir, proxyAddr, store)
}

// newManagerFromDB takes ownership of the opened store, including on failure.
// The storage-opening boundary is separate from service initialization so the
// SQLite cutover does not need to duplicate the startup lifecycle.
func newManagerFromDB(dir, proxyAddr string, store *pgdb.DB) (result *Manager, err error) {
	m := &Manager{dir: dir, pg: store, tasks: map[string]*Task{}}
	defer func() {
		if err != nil {
			err = errors.Join(err, m.Close())
		}
	}()
	if store == nil || store.DB == nil {
		return nil, errors.New("업무 저장소가 준비되지 않았습니다")
	}
	values, err := store.SettingsSnapshot(context.Background())
	if err != nil {
		return nil, fmt.Errorf("실행 설정 복원: %w", err)
	}
	state, err := parseManagerRuntimeSettings(values)
	if err != nil {
		return nil, err
	}
	if state.globalProxy != "" {
		if _, err := traffic.ValidateProxyURL(state.globalProxy); err != nil {
			// URL parse errors can embed user:password; do not expose them.
			return nil, errors.New("저장된 전역 프록시 설정이 유효하지 않습니다")
		}
	}
	if err := store.RecoverFindingRetests(); err != nil {
		return nil, fmt.Errorf("재검증 상태 복원: %w", err)
	}
	m.assets = store.Assets()
	m.interceptor = intercept.New(store)
	if err := m.interceptor.Validate(); err != nil {
		return nil, fmt.Errorf("도구 실행 정책 초기화: %w", err)
	}
	m.trafficOn, m.llmRecOn, m.webSearchOn = state.trafficOn, state.llmRecordOn, state.webSearchOn
	m.webSearchBackend, m.braveKey, m.tavilyKey = state.webSearchBackend, state.braveKey, state.tavilyKey
	m.webSearchProxy, m.globalProxy = state.webSearchProxy, state.globalProxy
	if proxyAddr != "" {
		m.traffic, err = traffic.Open(filepath.Join(dir, "traffic"), proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("트래픽 저장소 초기화: %w", err)
		}
		err = m.traffic.RecoverHostDeleteStages(func(_ int64, taskID int64) (bool, error) {
			if taskID <= 0 {
				return false, errors.New("보관 트래픽 임시 기록에 작업 ID가 없습니다")
			}
			task, err := store.GetTask(taskID)
			if err != nil {
				return false, err
			}
			return task == nil, nil
		})
		if err != nil {
			return nil, fmt.Errorf("트래픽 삭제 임시 상태 복원: %w", err)
		}
		if err := m.traffic.SetUpstreamProxy(m.globalProxy); err != nil {
			return nil, errors.New("트래픽 저장소의 전역 프록시 적용에 실패했습니다")
		}
	}
	if err := m.syncBrowserMCPProxy(); err != nil {
		return nil, err
	}
	if tr := m.traffic; tr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := tr.Start(ctx)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("트래픽 프록시 시작: %w", err)
		}
		log.Printf("[traffic] recording proxy on %s", proxyAddr)
	}
	m.enrich = enrich.New(m.assets, m.ProxyAddr, 4)
	return m, nil
}

func (m *Manager) syncBrowserMCPProxy() error {
	servers, err := m.pg.ListMCP()
	if err != nil {
		return fmt.Errorf("browser MCP 설정 읽기: %w", err)
	}
	var target *pgdb.MCPServer
	for _, server := range servers {
		if server.Name == browserMCPName {
			target = server
			break
		}
	}
	if target == nil {
		return nil // Explicit user removal is not a storage error.
	}
	args, env, err := browserProxySettings(target.Args, target.Env, m.ProxyAddr(), m.ProxyCACert())
	if err != nil {
		return err
	}
	// Preserve concurrent metadata edits and reject a stale args/env snapshot.
	if err := m.pg.CompareAndSwapMCPProxySettings(context.Background(), target, args, env); err != nil {
		return fmt.Errorf("browser MCP 설정 저장: %w", err)
	}
	log.Print("[mcp] browser MCP 프록시 설정을 동기화했습니다")
	return nil
}
