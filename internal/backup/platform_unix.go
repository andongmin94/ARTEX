//go:build linux || darwin

package backup

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func platformPath(p string) error     { return nil }
func protectDirectory(p string) error { return os.Chmod(p, 0700) }
func lockFile(f *os.File) error       { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func syncDirectory(p string) (err error) {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func publish(from, to string) error {
	// Flush all staged directory entries before exposing the snapshot name.
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return syncDirectory(p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := renameExclusive(from, to); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(to))
}
