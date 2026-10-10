package traffic

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Publish a complete blob without ever truncating a preexisting file. Root.Link
// refuses replacement and keeps both names inside the recorder's data root.
func (t *Traffic) storeBlob(directory, hash string, body []byte) error {
	root, err := os.OpenRoot(t.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := filepath.Rel(t.dir, directory)
	if err != nil || !filepath.IsLocal(dir) {
		return errors.New("트래픽 blob 경로가 유효하지 않습니다")
	}
	if err := root.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("트래픽 blob 디렉터리 생성: %w", err)
	}
	temporary := filepath.Join(dir, ".blob-"+rand.Text())
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("트래픽 blob 생성: %w", err)
	}
	defer root.Remove(temporary)
	n, writeErr := file.Write(body)
	if writeErr == nil && n != len(body) {
		writeErr = io.ErrShortWrite
	}
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return fmt.Errorf("트래픽 blob 쓰기: %w", err)
	}
	name := filepath.Join(dir, hash+".bin")
	if err := root.Link(temporary, name); err != nil && !os.IsExist(err) {
		return fmt.Errorf("트래픽 blob 게시: %w", err)
	}
	// Check both a new publication and a prior capture. A host-side temporary
	// name replacement must not commit a reference to unchecked bytes.
	existing, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("트래픽 기존 blob 열기: %w", err)
	}
	defer existing.Close()
	info, err := existing.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(body)) {
		return errors.New("트래픽 기존 blob 크기/형식이 유효하지 않습니다")
	}
	digest := sha256.New()
	n64, err := io.Copy(digest, io.LimitReader(existing, int64(len(body))+1))
	if err != nil {
		return fmt.Errorf("트래픽 기존 blob 읽기: %w", err)
	}
	if n64 != int64(len(body)) || hex.EncodeToString(digest.Sum(nil)) != hash {
		return errors.New("트래픽 기존 blob 내용 해시가 일치하지 않습니다")
	}
	return nil
}
