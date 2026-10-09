package backup

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validHash(h string) bool {
	b, err := hex.DecodeString(h)
	return err == nil && len(b) == 32 && h == strings.ToLower(h)
}

// File hashes in the manifest cannot prove that SQLite references exist. Check
// the current evidence and traffic layouts against the frozen DB snapshots too.
func validateAttachments(ctx context.Context, home string) error {
	business, err := sql.Open("sqlite", sqliteURI(filepath.Join(home, "data", "artex.sqlite"), "ro", true))
	if err != nil {
		return err
	}
	defer business.Close()
	rows, err := business.QueryContext(ctx, `SELECT req_hash,req_len,resp_hash,resp_len FROM traffic_evidence_snapshots`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var req, resp string
		var reqLen, respLen int64
		if err := rows.Scan(&req, &reqLen, &resp, &respLen); err != nil {
			rows.Close()
			return err
		}
		for _, body := range []struct {
			hash string
			size int64
		}{{req, reqLen}, {resp, respLen}} {
			if !validHash(body.hash) || body.size < 0 {
				rows.Close()
				return errors.New("증거 DB의 본문 참조가 유효하지 않습니다")
			}
			p := filepath.Join(home, "data", "evidence", "blobs", body.hash[:2], body.hash+".bin")
			if err := verifyBody(ctx, p, body.hash, body.size); err != nil {
				rows.Close()
				return fmt.Errorf("증거 DB/본문 불일치: %w", err)
			}
		}
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	indexPath := filepath.Join(home, "data", "traffic", "_index", "index.sqlite")
	if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	index, err := sql.Open("sqlite", sqliteURI(indexPath, "ro", true))
	if err != nil {
		return err
	}
	defer index.Close()
	rows, err = index.QueryContext(ctx, `SELECT b.req_blob,e.req_len,b.resp_blob,e.resp_len FROM exchange_bodies b JOIN exchanges e ON e.id=b.id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var req, resp sql.NullString
		var reqLen, respLen int64
		if err := rows.Scan(&req, &reqLen, &resp, &respLen); err != nil {
			rows.Close()
			return err
		}
		for _, body := range []struct {
			hash string
			size int64
		}{{req.String, reqLen}, {resp.String, respLen}} {
			if body.hash == "" {
				continue
			}
			if !validHash(body.hash) || body.size < 0 {
				rows.Close()
				return errors.New("트래픽 DB의 본문 참조가 유효하지 않습니다")
			}
			root := filepath.Join(home, "data", "traffic", "_blobs", "sha256", body.hash[:2])
			p := filepath.Join(root, body.hash+".bin")
			if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
				p = filepath.Join(root, body.hash[2:4], body.hash+".bin")
			}
			if err := verifyBody(ctx, p, body.hash, body.size); err != nil {
				rows.Close()
				return fmt.Errorf("트래픽 DB/본문 불일치: %w", err)
			}
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

func verifyBody(ctx context.Context, p, hash string, size int64) error {
	if err := checkAbsolute(p, false); err != nil {
		return err
	}
	n, sum, err := digest(ctx, p)
	if err != nil {
		return err
	}
	if n != size || sum != hash {
		return errors.New("본문 크기/해시가 DB 참조와 다릅니다")
	}
	return nil
}
