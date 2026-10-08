//go:build !windows

package chatgpt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func protectData(data []byte) ([]byte, error)   { return append([]byte(nil), data...), nil }
func unprotectData(data []byte) ([]byte, error) { return append([]byte(nil), data...), nil }
func protectPermissions(path string, directory bool) error {
	if directory {
		return os.Chmod(path, 0700)
	}
	return os.Chmod(path, 0600)
}
func checkPlatformPath(string) error { return nil }
func replaceCredentialFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func lockFile(ctx context.Context, f *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func unlockFile(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
