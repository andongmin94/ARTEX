package server

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	jwtKeyFilename = "jwt.key"
	authPassKey    = "auth.password_hash"
	jwtTTL         = 7 * 24 * time.Hour
	keyChars       = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// 최소 길이는 setup 화면의 검증과 같다. API 직접 호출도 서버에서 검증한다.
	// bcrypt는 72바이트를 넘으면 ErrPasswordTooLong을 반환하므로 저장 전에 차단한다.
	minPasswordRunes = 8
	maxPasswordBytes = 72
)

// 비밀번호 조회 실패는 설정이 없는 상태로 취급하지 않는다.
// 저장소 오류에서 인증되지 않은 요청이 기존 관리자 비밀번호를 덮어쓰지 않게 한다.
const errDataSourceUnavailable = "데이터 저장소를 일시적으로 사용할 수 없습니다. 잠시 후 다시 시도하세요"

// validatePassword는 검증에 통과하면 빈 문자열, 실패하면 표시할 한국어 이유를 반환한다.
func validatePassword(pw string) string {
	if utf8.RuneCountInString(pw) < minPasswordRunes {
		return fmt.Sprintf("비밀번호는 %d자 이상이어야 합니다", minPasswordRunes)
	}
	if len(pw) > maxPasswordBytes {
		return fmt.Sprintf("비밀번호는 %d바이트 이하여야 합니다", maxPasswordBytes)
	}
	return ""
}

// loadOrCreateJWTKey reads the 32-byte signing key from keyDir/jwt.key. keyDir is
// the project base dir (next to the executable), NOT the browsable workspace root
// (dataDir) — the signing key must never be listable/downloadable via the file
// manager. Legacy installs kept it at dataDir/jwt.key; if present there and not yet
// at the new location, it is migrated (key preserved, so sessions stay valid) and
// the old file removed so it disappears from the workspace. On first run a random
// key is generated and persisted.
func loadOrCreateJWTKey(keyDir, dataDir string) ([]byte, error) {
	path := filepath.Join(keyDir, jwtKeyFilename)
	// one-time migration out of the old in-workspace location.
	if legacy := filepath.Join(dataDir, jwtKeyFilename); legacy != path {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if data, rerr := os.ReadFile(legacy); rerr == nil {
				if werr := os.WriteFile(path, data, 0o600); werr == nil {
					_ = os.Remove(legacy)
					log.Printf("[auth] JWT 키를 %s에서 %s로 이동했습니다(탐색 가능한 작업 공간에서 분리)", legacy, path)
				}
			}
		}
	}
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) >= 32 {
		return []byte(strings.TrimSpace(string(data))), nil
	}
	buf := make([]byte, 32)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(keyChars))))
		if err != nil {
			return nil, fmt.Errorf("generate jwt key: %w", err)
		}
		buf[i] = keyChars[n.Int64()]
	}
	if err := os.WriteFile(path, buf, 0600); err != nil {
		return nil, fmt.Errorf("write jwt key: %w", err)
	}
	log.Printf("[auth] 새 JWT 키를 %s에 저장했습니다", path)
	return buf, nil
}

// signJWT issues a 7-day HS256 token for user ARTEX.
func signJWT(key []byte) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "ARTEX",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(jwtTTL)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}).SignedString(key)
}

// verifyJWT returns true when tokenStr is a valid, non-expired HS256 token.
func verifyJWT(tokenStr string, key []byte) bool {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	return err == nil && t.Valid
}

// extractToken reads the JWT from Authorization: Bearer header,
// artex_token cookie, or ?token= query param (for SSE connections).
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie("artex_token"); err == nil && c.Value != "" {
		return c.Value
	}
	return r.URL.Query().Get("token")
}

// requireAuth wraps h with JWT validation.
// /api/auth/* and /api/health are exempt.
func (s *Server) requireAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/auth/") || p == "/api/health" {
			h.ServeHTTP(w, r)
			return
		}
		tok := extractToken(r)
		if tok == "" {
			writeErr(w, 401, "인증이 필요합니다")
			return
		}
		if !verifyJWT(tok, s.jwtKey) {
			writeErr(w, 401, "token이 유효하지 않거나 만료되었습니다")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// GET /api/auth/status — reports whether the admin password has been initialised.
// 조회 오류는 503으로 반환한다. initialized:false를 반환하면 기존 비밀번호가 있어도
// 화면에서 사용자를 /setup으로 보내므로 데이터베이스 오류를 성공으로 처리하지 않는다.
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	hash, exists, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if exists && hash == "" {
		writeErr(w, 500, "인증 설정을 읽을 수 없습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"initialized": exists})
}

// POST /api/auth/init — sets the password for the first time; rejected if already set.
func (s *Server) authInit(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	existing, exists, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if exists && existing == "" {
		writeErr(w, 500, "인증 설정을 읽을 수 없습니다")
		return
	}
	if exists {
		writeErr(w, 403, "비밀번호가 이미 설정되었습니다")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil || req.Password == "" {
		writeErr(w, 400, "비밀번호는 비워둘 수 없습니다")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "비밀번호 암호화 실패")
		return
	}
	// 최초 설정은 INSERT ... ON CONFLICT DO NOTHING과 기본 키 제약으로 보장한다.
	// 앞선 조회 이후 bcrypt 처리 중에도 다른 요청이 먼저 설정할 수 있으므로 덮어쓰지 않는다.
	created, err := pg.SetSettingIfAbsent(r.Context(), authPassKey, string(hash))
	if err != nil {
		writeErr(w, 500, "인증 설정을 저장할 수 없습니다")
		return
	}
	if !created {
		writeErr(w, 403, "비밀번호가 이미 설정되었습니다")
		return
	}
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		writeErr(w, 500, "token 생성 실패")
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok})
}

// POST /api/auth/change-password — changes the admin password. Requires a valid
// token (this route is under /api/auth/* which requireAuth exempts, so the token
// is validated here) AND the current password.
func (s *Server) authChangePassword(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if !verifyJWT(extractToken(r), s.jwtKey) {
		writeErr(w, 401, "인증이 필요합니다")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "요청 형식이 잘못되었습니다")
		return
	}
	if req.NewPassword == "" {
		writeErr(w, 400, "새 비밀번호는 비워둘 수 없습니다")
		return
	}
	if msg := validatePassword(req.NewPassword); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	hash, ok, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if ok && hash == "" {
		writeErr(w, 500, "인증 설정을 읽을 수 없습니다")
		return
	}
	if !ok || hash == "" {
		writeErr(w, 403, "비밀번호가 초기화되지 않았습니다. 먼저 비밀번호를 설정하세요")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.OldPassword)); err != nil {
		writeErr(w, 401, "현재 비밀번호가 올바르지 않습니다")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "비밀번호 암호화 실패")
		return
	}
	changed, err := pg.CompareAndSwapSetting(r.Context(), authPassKey, hash, string(newHash))
	if err != nil {
		writeErr(w, 500, "인증 설정을 저장할 수 없습니다")
		return
	}
	if !changed {
		writeErr(w, 409, "다른 요청에서 비밀번호가 변경되었습니다. 다시 로그인하세요")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/auth/login — validates username/password and returns a JWT.
func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "요청 형식이 잘못되었습니다")
		return
	}
	if req.Username != "ARTEX" {
		writeErr(w, 401, "사용자 이름 또는 비밀번호가 올바르지 않습니다")
		return
	}
	hash, ok, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if ok && hash == "" {
		writeErr(w, 500, "인증 설정을 읽을 수 없습니다")
		return
	}
	if !ok || hash == "" {
		writeErr(w, 403, "비밀번호가 초기화되지 않았습니다. 먼저 비밀번호를 설정하세요")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		writeErr(w, 401, "사용자 이름 또는 비밀번호가 올바르지 않습니다")
		return
	}
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		writeErr(w, 500, "token 생성 실패")
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok})
}
