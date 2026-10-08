package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ---------- LLM profiles ----------

type LLMProfile struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Format        string  `json:"format"`
	BaseURL       string  `json:"base_url,omitempty"`
	Proxy         string  `json:"proxy,omitempty"` // LLM 出站代理(http/https/socks5);空=用环境变量
	Model         string  `json:"model"`
	APIKey        string  `json:"-"` // never serialized to UI
	APIKeyHint    string  `json:"api_key_hint,omitempty"`
	RatePerSecond float64 `json:"rate_per_second"`
	RatePerMinute float64 `json:"rate_per_minute"`
	// ContextWindowK is the model's context window in K tokens, used to size
	// compaction thresholds. 0 = use a 200K default; capped at 1000 (1M).
	ContextWindowK int `json:"context_window_k"`
	// ThinkingType 独立控制思考「开关」(thinking.type):"" = 不发送(默认);
	// "disabled" = 显式关闭; "enabled" = 开启. 与 ReasoningEffort 解耦.
	ThinkingType string `json:"thinking_type"`
	// ReasoningEffort 独立控制思考「强度」:"" = 不发送(默认);
	// "low"/"medium"/"high"/"xhigh"/"max" = 对应强度. 见 agent.Config.NewProvider.
	ReasoningEffort string `json:"reasoning_effort"`
	IsDefault       bool   `json:"is_default"`
	// Priority orders the failover chain: higher goes first. The ACTIVE profile
	// (IsDefault) always heads the chain regardless of this value.
	Priority int `json:"priority"`
	// PoolExclude=true keeps this profile out of the failover chain — it stays
	// usable when an agent/task binds it explicitly, it just never gets picked up
	// as a fallback target.
	PoolExclude bool `json:"pool_exclude"`
	// Streaming selects the wire protocol: true (default) = streaming (SSE);
	// false = real non-streaming (stream:false, single JSON response via
	// Provider.Complete). Non-streaming sidesteps flaky gateway SSE at the cost of
	// live in-run progress. Maps to agent.Config.Stream.
	Streaming bool `json:"streaming"`
	// MaxTokens caps a single reply's output in tokens. 0 = send no cap and let
	// the endpoint's own default apply (the historical behaviour). Unlike
	// ContextWindowK — the model's total capacity, used locally to size compaction
	// — this value travels with every request.
	MaxTokens int `json:"max_tokens"`
	// MaxTokensField picks the request key carrying MaxTokens, for format
	// "openai" only: "" = max_tokens (default); "max_completion_tokens" = the
	// newer key, which OpenAI's reasoning models require and whose budget covers
	// reasoning tokens plus visible output. Ignored by anthropic and
	// openai-responses, which name the field themselves.
	MaxTokensField string `json:"max_tokens_field"`
	// SessionHeaderKey, when non-empty, names a custom HTTP header sent on every
	// request built from this profile; its value is the current run's session id
	// (chat conversation / worker intent). For gateways that key prompt caching
	// or sticky routing off a session-id header. "" = not sent. Maps to
	// agent.Config.SessionHeaderKey.
	SessionHeaderKey string `json:"session_header_key"`
	// Retry overrides this profile's share of the retry ladder. Zero value =
	// inherit the global policy (LLMRetryPolicy), so an untouched profile behaves
	// exactly as before. See RetryOverride.
	Retry RetryOverride `json:"retry"`
}

// RetryOverride is one profile's optional override of the three retry layers
// that are per-endpoint: 建连(connect) / 空响应(empty) / 同 provider 安全窗口
// (stream). Each rule's zero value means "inherit the global policy"; see
// RetryRule for the -1 / 0 / >0 semantics.
type RetryOverride struct {
	Connect RetryRule `json:"connect"`
	Empty   RetryRule `json:"empty"`
	Stream  RetryRule `json:"stream"`
}

// profileCols is the read column list (hint variant, no api key) shared by the
// list query; profileColsKey is the same with api_key for the single-row loads.
const profileRetryCols = `COALESCE(retry_connect_attempts,0),COALESCE(retry_connect_interval_ms,0),COALESCE(retry_empty_attempts,0),COALESCE(retry_empty_interval_ms,0),COALESCE(retry_stream_attempts,0),COALESCE(retry_stream_interval_ms,0)`
const profileCols = `id,name,format,COALESCE(base_url,''),COALESCE(proxy,''),model,COALESCE(api_key_hint,''),rate_per_second,rate_per_minute,context_window_k,COALESCE(reasoning_effort,''),is_default,priority,pool_exclude,COALESCE(thinking_type,''),COALESCE(streaming,true),COALESCE(max_tokens,0),COALESCE(max_tokens_field,''),COALESCE(session_header_key,''),` + profileRetryCols
const profileColsKey = `id,name,format,COALESCE(base_url,''),COALESCE(proxy,''),model,COALESCE(api_key,''),rate_per_second,rate_per_minute,context_window_k,COALESCE(reasoning_effort,''),is_default,priority,pool_exclude,COALESCE(thinking_type,''),COALESCE(streaming,true),COALESCE(max_tokens,0),COALESCE(max_tokens_field,''),COALESCE(session_header_key,''),` + profileRetryCols

// scanProfile reads one row in the profileCols / profileColsKey column order. The
// 7th column lands in APIKeyHint or APIKey depending on which list the caller used.
func scanProfile(sc interface{ Scan(...any) error }, into *string, p *LLMProfile) error {
	return sc.Scan(&p.ID, &p.Name, &p.Format, &p.BaseURL, &p.Proxy, &p.Model, into,
		&p.RatePerSecond, &p.RatePerMinute, &p.ContextWindowK, &p.ReasoningEffort, &p.IsDefault, &p.Priority, &p.PoolExclude, &p.ThinkingType, &p.Streaming,
		&p.MaxTokens, &p.MaxTokensField, &p.SessionHeaderKey,
		&p.Retry.Connect.Attempts, &p.Retry.Connect.IntervalMS,
		&p.Retry.Empty.Attempts, &p.Retry.Empty.IntervalMS,
		&p.Retry.Stream.Attempts, &p.Retry.Stream.IntervalMS)
}

func (d *DB) ListProfiles() ([]*LLMProfile, error) {
	rows, err := d.Query(`SELECT ` + profileCols + ` FROM llm_profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LLMProfile
	for rows.Next() {
		var p LLMProfile
		if err := scanProfile(rows, &p.APIKeyHint, &p); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// ActiveProfile returns the default (active) profile with its api key, or nil.
func (d *DB) ActiveProfile() (*LLMProfile, error) {
	var p LLMProfile
	err := scanProfile(d.QueryRow(`SELECT `+profileColsKey+` FROM llm_profiles WHERE is_default LIMIT 1`), &p.APIKey, &p)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ProfileByID returns one profile with its api key by id, or nil if not found.
// Used to run a task on a specific (non-default) LLM profile.
func (d *DB) ProfileByID(id int64) (*LLMProfile, error) {
	var p LLMProfile
	err := scanProfile(d.QueryRow(`SELECT `+profileColsKey+` FROM llm_profiles WHERE id=$1`, id), &p.APIKey, &p)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// PoolProfiles returns the failover chain in run order, api keys included: the
// active profile first, then every other keyed profile that isn't excluded, by
// priority DESC (id ASC to stay stable). Profiles without an api key can't serve
// a request, so they never enter the chain. The ordering IS the policy — callers
// walk the slice front to back.
func (d *DB) PoolProfiles() ([]*LLMProfile, error) {
	rows, err := d.Query(`SELECT ` + profileColsKey + ` FROM llm_profiles
WHERE COALESCE(api_key,'') <> '' AND (is_default OR NOT pool_exclude)
ORDER BY is_default DESC, priority DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LLMProfile
	for rows.Next() {
		var p LLMProfile
		if err := scanProfile(rows, &p.APIKey, &p); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// SaveProfile inserts (id==0) or updates a profile. Empty apiKey on update keeps existing.
func (d *DB) SaveProfile(p *LLMProfile) (int64, error) {
	if p == nil {
		return 0, errors.New("LLM profile is required")
	}
	hint := p.APIKeyHint
	if len(p.APIKey) >= 4 {
		hint = "…" + p.APIKey[len(p.APIKey)-4:]
	}
	r := p.Retry.Clamped()
	if p.ID == 0 {
		var id int64
		err := d.QueryRow(`INSERT INTO llm_profiles(name,format,base_url,proxy,model,api_key,api_key_hint,rate_per_second,rate_per_minute,context_window_k,reasoning_effort,priority,pool_exclude,thinking_type,streaming,max_tokens,max_tokens_field,session_header_key,retry_connect_attempts,retry_connect_interval_ms,retry_empty_attempts,retry_empty_interval_ms,retry_stream_attempts,retry_stream_interval_ms)
VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24) RETURNING id`,
			p.Name, p.Format, p.BaseURL, p.Proxy, p.Model, p.APIKey, hint, p.RatePerSecond, p.RatePerMinute, p.ContextWindowK, p.ReasoningEffort, p.Priority, p.PoolExclude, p.ThinkingType, p.Streaming, p.MaxTokens, p.MaxTokensField, p.SessionHeaderKey,
			r.Connect.Attempts, r.Connect.IntervalMS, r.Empty.Attempts, r.Empty.IntervalMS, r.Stream.Attempts, r.Stream.IntervalMS).Scan(&id)
		return id, err
	}
	var id int64
	if p.APIKey == "" {
		err := d.QueryRow(`UPDATE llm_profiles SET name=$1,format=$2,base_url=NULLIF($3,''),proxy=NULLIF($4,''),model=$5,rate_per_second=$6,rate_per_minute=$7,context_window_k=$8,reasoning_effort=$9,priority=$10,pool_exclude=$11,thinking_type=$12,streaming=$13,max_tokens=$14,max_tokens_field=$15,session_header_key=$16,retry_connect_attempts=$17,retry_connect_interval_ms=$18,retry_empty_attempts=$19,retry_empty_interval_ms=$20,retry_stream_attempts=$21,retry_stream_interval_ms=$22 WHERE id=$23 RETURNING id`,
			p.Name, p.Format, p.BaseURL, p.Proxy, p.Model, p.RatePerSecond, p.RatePerMinute, p.ContextWindowK, p.ReasoningEffort, p.Priority, p.PoolExclude, p.ThinkingType, p.Streaming, p.MaxTokens, p.MaxTokensField, p.SessionHeaderKey,
			r.Connect.Attempts, r.Connect.IntervalMS, r.Empty.Attempts, r.Empty.IntervalMS, r.Stream.Attempts, r.Stream.IntervalMS, p.ID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrLLMProfileNotFound
		}
		return id, err
	}
	err := d.QueryRow(`UPDATE llm_profiles SET name=$1,format=$2,base_url=NULLIF($3,''),proxy=NULLIF($4,''),model=$5,api_key=$6,api_key_hint=$7,rate_per_second=$8,rate_per_minute=$9,context_window_k=$10,reasoning_effort=$11,priority=$12,pool_exclude=$13,thinking_type=$14,streaming=$15,max_tokens=$16,max_tokens_field=$17,session_header_key=$18,retry_connect_attempts=$19,retry_connect_interval_ms=$20,retry_empty_attempts=$21,retry_empty_interval_ms=$22,retry_stream_attempts=$23,retry_stream_interval_ms=$24 WHERE id=$25 RETURNING id`,
		p.Name, p.Format, p.BaseURL, p.Proxy, p.Model, p.APIKey, hint, p.RatePerSecond, p.RatePerMinute, p.ContextWindowK, p.ReasoningEffort, p.Priority, p.PoolExclude, p.ThinkingType, p.Streaming, p.MaxTokens, p.MaxTokensField, p.SessionHeaderKey,
		r.Connect.Attempts, r.Connect.IntervalMS, r.Empty.Attempts, r.Empty.IntervalMS, r.Stream.Attempts, r.Stream.IntervalMS, p.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrLLMProfileNotFound
	}
	return id, err
}

var (
	ErrActiveLLMProfileDelete = errors.New("cannot delete the active LLM profile; activate another profile first")
	ErrLLMProfileNotFound     = errors.New("LLM profile not found")
)

func (d *DB) DeleteProfile(id int64) error {
	return d.DeleteProfileContext(context.Background(), id)
}

// DeleteProfileContext removes a non-default profile and repairs every affected
// task cursor in one IMMEDIATE transaction. The business opener acquires SQLite's
// writer before the first read; concurrent activation/binding cannot interleave.
// Foreign keys clear agent/conversation references and remove chain memberships.
func (d *DB) DeleteProfileContext(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var isDefault bool
	if err := tx.QueryRowContext(ctx, `SELECT is_default FROM llm_profiles WHERE id=$1`, id).Scan(&isDefault); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLLMProfileNotFound
		}
		return err
	}
	if isDefault {
		return ErrActiveLLMProfileDelete
	}

	// Include direct references as well as chain members. Even a task with no
	// explicit chain needs a new revision after its FK is cleared by deletion.
	rows, err := tx.QueryContext(ctx, `SELECT t.id, x.position, COALESCE(t.active_llm_profile_id=$1, false)
FROM tasks t
LEFT JOIN task_llm_profiles x ON x.task_id=t.id AND x.profile_id=$1
WHERE t.llm_profile_id=$1 OR t.active_llm_profile_id=$1 OR x.profile_id IS NOT NULL
ORDER BY t.id`, id)
	if err != nil {
		return err
	}
	type affectedTask struct {
		id        int64
		position  sql.NullInt64
		wasActive bool
	}
	var affected []affectedTask
	for rows.Next() {
		var task affectedTask
		if err := rows.Scan(&task.id, &task.position, &task.wasActive); err != nil {
			rows.Close()
			return err
		}
		affected = append(affected, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM llm_profiles WHERE id=$1`, id); err != nil {
		return err
	}

	for _, task := range affected {
		if task.wasActive {
			var next sql.NullInt64
			// A missing old position cannot justify reviving an earlier model.
			if task.position.Valid {
				err := tx.QueryRowContext(ctx, `SELECT profile_id FROM task_llm_profiles
WHERE task_id=$1 AND position>$2 AND status='ready'
ORDER BY position LIMIT 1`, task.id, task.position.Int64).Scan(&next)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			if !next.Valid {
				if _, err := tx.ExecContext(ctx, `DELETE FROM task_llm_profiles WHERE task_id=$1`, task.id); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET active_llm_profile_id=$2, llm_profile_id=$2
WHERE id=$1`, task.id, next); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET llm_chain_revision=llm_chain_revision+1 WHERE id=$1`, task.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetActiveProfile makes one profile the global default (single-default invariant).
func (d *DB) SetActiveProfile(id int64) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE llm_profiles SET is_default=false WHERE is_default`); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE llm_profiles SET is_default=true WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLLMProfileNotFound
	}
	return tx.Commit()
}
