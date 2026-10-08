package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"

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
	dsn, source, err := pgdb.DSN()
	if err != nil {
		return nil, err
	}
	log.Printf("[pg] DB 설정 출처: %s", source)
	store, err := pgdb.Open(dsn)
	if err != nil {
		return nil, err
	}
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
	if err := store.EnsureLLMRecordsTable(); err != nil {
		return nil, fmt.Errorf("LLM 기록 저장소 초기화: %w", err)
	}
	if err := store.EnsureLLMUsageTable(); err != nil {
		return nil, fmt.Errorf("LLM 사용량 저장소 초기화: %w", err)
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
	m.enrich = enrich.New(m.assets, m.ProxyAddr, 4)
	// Do not start background serving before every synchronous initialization
	// step has succeeded. Socket bind/readiness is still owned by Traffic.Start.
	if tr := m.traffic; tr != nil {
		go func() {
			log.Printf("[traffic] recording proxy on %s", proxyAddr)
			if err := tr.Start(); err != nil {
				log.Printf("[traffic] proxy stopped: %v", err)
			}
		}()
	}
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
