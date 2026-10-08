package db

import (
	"database/sql"
	"time"
)

// CurrentPrompt returns the agent's active template text ("" if none set yet).
func (d *DB) CurrentPrompt(agentID int64) (string, error) {
	var tmpl sql.NullString
	err := d.QueryRow(`SELECT p.template_text FROM agents a JOIN agent_prompts p ON p.id=a.current_prompt_id WHERE a.id=$1`, agentID).Scan(&tmpl)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tmpl.String, err
}

// SeedPromptIfEmpty writes the code-default template as the agent's first prompt
// version ONLY when it has none yet (current_prompt_id IS NULL). Mirrors
// SeedTool's first-insert-only philosophy: a user's edited prompt is never
// clobbered on restart. Idempotent — a no-op once any version exists.
func (d *DB) SeedPromptIfEmpty(agentID int64, tmpl string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, err := lockPromptAgent(tx, agentID)
	if err != nil {
		return err
	}
	if !cur.Valid {
		if _, err := savePromptTx(tx, agentID, tmpl, "기본 제공 값", "system"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResetPromptToDefault appends the code-default template as a new version and
// points current at it — the explicit "恢复为内置默认" action.
func (d *DB) ResetPromptToDefault(agentID int64, tmpl string) (int, error) {
	return d.SavePrompt(agentID, tmpl, "기본 제공 값으로 복원", "system")
}

// SavePrompt appends a new version and points current_prompt_id at it.
func (d *DB) SavePrompt(agentID int64, template, note, by string) (int, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := lockPromptAgent(tx, agentID); err != nil {
		return 0, err
	}
	ver, err := savePromptTx(tx, agentID, template, note, by)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return ver, nil
}

// lockPromptAgent acquires the write lock before reading the current prompt or
// version counter. SQLite must not upgrade an old read snapshot into a writer.
// The same row update serializes edits to this agent in the current SQL store.
// All subsequent queries use tx, never the parent pool (including a one-connection pool).
func lockPromptAgent(tx *sql.Tx, agentID int64) (sql.NullInt64, error) {
	var current sql.NullInt64
	result, err := tx.Exec(`UPDATE agents SET current_prompt_id=current_prompt_id WHERE id=$1`, agentID)
	if err != nil {
		return current, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return current, err
	}
	if n != 1 {
		return current, sql.ErrNoRows
	}
	err = tx.QueryRow(`SELECT current_prompt_id FROM agents WHERE id=$1`, agentID).Scan(&current)
	return current, err
}

// savePromptTx requires lockPromptAgent in the same transaction. Inserting the
// version and publishing its pointer either commit together or roll back together.
func savePromptTx(tx *sql.Tx, agentID int64, template, note, by string) (int, error) {
	var ver int
	if err := tx.QueryRow(`SELECT COALESCE(max(version),0)+1 FROM agent_prompts WHERE agent_id=$1`, agentID).Scan(&ver); err != nil {
		return 0, err
	}
	var pid int64
	if err := tx.QueryRow(`INSERT INTO agent_prompts(agent_id,version,template_text,note,updated_by) VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,'')) RETURNING id`,
		agentID, ver, template, note, by).Scan(&pid); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE agents SET current_prompt_id=$1 WHERE id=$2`, pid, agentID); err != nil {
		return 0, err
	}
	return ver, nil
}

type PromptVersion struct {
	Version   int       `json:"version"`
	Template  string    `json:"template_text"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"ts"`
}

func (d *DB) ListPromptVersions(agentID int64) ([]PromptVersion, error) {
	rows, err := d.Query(`SELECT version,template_text,COALESCE(note,''),created_at FROM agent_prompts WHERE agent_id=$1 ORDER BY version DESC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PromptVersion{}
	for rows.Next() {
		var v PromptVersion
		if err := rows.Scan(&v.Version, &v.Template, &v.Note, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
