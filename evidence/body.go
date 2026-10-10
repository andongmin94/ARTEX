package evidence

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

// Return the same descriptor whose complete bytes were checked. Reopening its
// name after validation could return a concurrently replaced, unchecked file.
func openVerifiedBody(root *os.Root, hash string, length int64) (*os.File, error) {
	name, err := hashPath(".", hash)
	if err != nil {
		return nil, err
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || length < 0 || info.Size() != length {
		f.Close()
		return nil, fmt.Errorf("증거 본문 크기/형식 검증 실패: %s", hash)
	}
	digest := sha256.New()
	n, readErr := io.Copy(digest, io.LimitReader(f, length+1))
	if readErr != nil || n != length || hex.EncodeToString(digest.Sum(nil)) != hash {
		f.Close()
		return nil, errors.Join(readErr, fmt.Errorf("증거 본문 검증 실패: %s", hash))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Keep final names untouched until complete bytes have been synced. Link is
// exclusive publication; an existing body is read and checked, never modified.
func publishBody(root *os.Root, source io.Reader, length int64, expectedHash string) (string, error) {
	if length < 0 {
		return "", errors.New("증거 본문 길이가 유효하지 않습니다")
	}
	if expectedHash != "" {
		if _, err := hashPath(".", expectedHash); err != nil {
			return "", err
		}
	}
	if err := root.MkdirAll(".staging", 0o700); err != nil {
		return "", err
	}
	temporary := filepath.Join(".staging", "body-"+rand.Text())
	f, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer func() { f.Close(); root.Remove(temporary) }()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, digest), io.LimitReader(source, length+1))
	if err != nil {
		return "", err
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	if n != length {
		return "", fmt.Errorf("본문이 완전하지 않습니다. 예상 %d바이트, 읽은 크기 %d바이트", length, n)
	}
	if expectedHash != "" && expectedHash != hash {
		return "", errors.New("원본 증거 본문 해시가 일치하지 않습니다")
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return "", err
	}
	name, err := hashPath(".", hash)
	if err != nil {
		return "", err
	}
	if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return "", err
	}
	if err := root.Link(temporary, name); err != nil && !os.IsExist(err) {
		return "", err
	}
	// The temporary name can change inside the Root while its descriptor is
	// open. Verify the published bytes too before any database can reference it.
	existing, err := openVerifiedBody(root, hash, length)
	if err != nil {
		return "", err
	}
	return hash, existing.Close()
}
