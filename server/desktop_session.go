package server

import (
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
)

const desktopSessionHeader = "X-Artex-Desktop-Session"

func readDesktopSession() ([]byte, error) {
	value := os.Getenv("ARTEX_DESKTOP_SESSION")
	if value == "" {
		return nil, nil
	}
	if len(value) != 64 {
		return nil, errors.New("데스크톱 세션 키는 32바이트 hex 값이어야 합니다")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return nil, errors.New("데스크톱 세션 키는 32바이트 hex 값이어야 합니다")
	}
	return []byte(value), nil
}

// ConfigureDesktopListener runs after bind and before Serve/ready. Electron's
// session is scoped to this exact loopback origin, including its assigned port.
func (s *Server) ConfigureDesktopListener(addr net.Addr) error {
	if len(s.desktopSession) == 0 {
		return nil
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok || !tcpAddr.IP.IsLoopback() {
		return errors.New("데스크톱 백엔드는 loopback 주소에서만 시작할 수 있습니다")
	}
	s.desktopHost = addr.String()
	return nil
}

func (s *Server) requireDesktopSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.desktopSession) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if !s.validDesktopSession(r) {
			writeErr(w, http.StatusForbidden, "데스크톱 세션이 유효하지 않습니다")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validDesktopSession(r *http.Request) bool {
	values := r.Header.Values(desktopSessionHeader)
	if len(s.desktopSession) == 0 || len(values) != 1 ||
		subtle.ConstantTimeCompare([]byte(values[0]), s.desktopSession) != 1 ||
		s.desktopHost == "" || r.Host != s.desktopHost {
		return false
	}
	origin := r.Header.Get("Origin")
	return len(r.Header.Values("Origin")) <= 1 && (origin == "" || origin == "http://"+s.desktopHost)
}
