package server

import (
	"net/http"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

func (s *Server) configureProfileAuth(cfg *agent.Config, p *db.LLMProfile) bool {
	if p.AuthMethod != "chatgpt" {
		return cfg.APIKey != ""
	}
	if s.chatGPT == nil || p.Format != "openai-responses" {
		return false
	}
	status := s.chatGPT.Status()
	if !status.Connected || !status.Sharing {
		return false
	}
	cfg.ChatGPT = s.chatGPT
	cfg.APIKey, cfg.BaseURL, cfg.Proxy, cfg.SessionHeaderKey = "", "", "", ""
	cfg.Stream, cfg.MaxTokens, cfg.MaxTokensField, cfg.ThinkingType = true, 0, "", ""
	return true
}

func (s *Server) subscriptionProfile(id int64) (bool, error) {
	profile, err := s.m.pg.ProfileByID(id)
	return profile != nil && profile.AuthMethod == "chatgpt", err
}

// An unavailable explicit subscription selection is a hard stop, so it cannot
// disappear into the existing API-key fallback path.
func (s *Server) subscriptionBinding(agentKey string) (bool, error) {
	if s == nil || s.m == nil || s.m.pg == nil {
		return false, nil
	}
	a, err := s.m.pg.GetAgentByKey(agentKey)
	if err != nil || a == nil || a.LLMProfileID == nil {
		return false, err
	}
	return s.subscriptionProfile(*a.LLMProfileID)
}

// Called while cfgMu is held by getLLM.
func (s *Server) activeAuthMethod() string {
	if s.llmCfg.ChatGPT != nil {
		return "chatgpt"
	}
	return "api-key"
}

func (s *Server) chatGPTAvailable(w http.ResponseWriter) bool {
	if s.chatGPT == nil {
		writeErr(w, 503, "ChatGPT 연결 저장소가 준비되지 않았습니다")
		return false
	}
	return true
}

func (s *Server) chatGPTStatus(w http.ResponseWriter, r *http.Request) {
	if !s.chatGPTAvailable(w) {
		return
	}
	writeJSON(w, 200, s.chatGPT.Status())
}

func (s *Server) chatGPTLogin(w http.ResponseWriter, r *http.Request) {
	if !s.chatGPTAvailable(w) {
		return
	}
	// The browser redirect outlives this HTTP request; app shutdown owns it.
	authorize, err := s.chatGPT.Begin(s.ctx)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"authorization_url": authorize})
}

func (s *Server) chatGPTCancel(w http.ResponseWriter, r *http.Request) {
	if !s.chatGPTAvailable(w) {
		return
	}
	s.chatGPT.Cancel()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) chatGPTLogout(w http.ResponseWriter, r *http.Request) {
	if !s.chatGPTAvailable(w) {
		return
	}
	err := s.chatGPT.Logout()
	s.invalidateProfileAgents()
	s.reapplyActiveProfile()
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) chatGPTModels(w http.ResponseWriter, r *http.Request) {
	if !s.chatGPTAvailable(w) {
		return
	}
	models, err := s.chatGPT.Models(r.Context())
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"models": models})
}

func (s *Server) chatGPTProfile(w http.ResponseWriter, r *http.Request) {
	if !s.chatGPTAvailable(w) {
		return
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	models, err := s.chatGPT.Models(r.Context())
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	var name string
	for _, model := range models {
		if model.Slug == body.Model {
			name = model.DisplayName
			break
		}
	}
	if name == "" {
		writeErr(w, 400, "현재 ChatGPT 계정에서 선택할 수 없는 모델입니다")
		return
	}
	profiles, err := s.m.pg.ListProfiles()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var id int64
	for _, profile := range profiles {
		if profile.AuthMethod == "chatgpt" && profile.Model == body.Model {
			id = profile.ID
			break
		}
	}
	if id == 0 {
		id, err = s.m.pg.SaveProfile(&db.LLMProfile{Name: "ChatGPT 구독 · " + strings.TrimSpace(name), Format: "openai-responses", AuthMethod: "chatgpt", Model: body.Model, Streaming: true, PoolExclude: true})
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if err := s.m.pg.SetActiveProfile(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.invalidateProfileAgents()
	s.reapplyActiveProfile()
	writeJSON(w, 200, map[string]any{"id": id, "ok": true})
}
