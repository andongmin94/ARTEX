//go:build linux || darwin

package server

import (
	"os"
	"path/filepath"
	"strings"
)

func openManagedParent(root *os.Root, name string, create bool) (*os.Root, func() error, error) {
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, nil, err
	}
	roots := []*os.Root{current}
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		if create {
			if err := current.Mkdir(part, 0o700); err != nil && !os.IsExist(err) {
				closeManagedRoots(roots)
				return nil, nil, err
			}
		}
		before, err := current.Lstat(part)
		if err != nil || !before.IsDir() || workspaceLink(before) {
			closeManagedRoots(roots)
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, errWorkspacePath
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			closeManagedRoots(roots)
			return nil, nil, err
		}
		roots = append(roots, next)
		after, err := next.Stat(".")
		if err != nil || !os.SameFile(before, after) {
			closeManagedRoots(roots)
			return nil, nil, errWorkspacePath
		}
		current = next
	}
	return current, func() error { return closeManagedRoots(roots) }, nil
}

func moveManagedPath(root *os.Root, from, to string) error {
	sourceRoot, closeSource, err := openManagedParent(root, filepath.Dir(from), false)
	if err != nil {
		return err
	}
	defer closeSource()
	destRoot, closeDest, err := openManagedParent(root, filepath.Dir(to), true)
	if err != nil {
		return err
	}
	defer closeDest()
	info, err := sourceRoot.Lstat(filepath.Base(from))
	if err != nil || workspaceLink(info) {
		if err != nil {
			return err
		}
		return errWorkspacePath
	}
	sourceDir, err := sourceRoot.Open(".")
	if err != nil {
		return err
	}
	defer sourceDir.Close()
	destDir, err := destRoot.Open(".")
	if err != nil {
		return err
	}
	defer destDir.Close()
	return managedPathError("rename", from, to, renameManagedExclusive(sourceDir, filepath.Base(from), destDir, filepath.Base(to)))
}
