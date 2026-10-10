package server

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Both ends of a managed move must remain inside the application data root.
// The platform implementation pins the parent directories and refuses links,
// so a host-side parent replacement cannot redirect an archive or task delete.
func managedRelative(dataDir, path string) (string, error) {
	if !filepath.IsAbs(dataDir) || !filepath.IsAbs(path) {
		return "", errWorkspacePath
	}
	name, err := filepath.Rel(dataDir, path)
	if err != nil || name == "." || !filepath.IsLocal(name) {
		return "", errWorkspacePath
	}
	return workspacePath(name)
}

func managedMove(dataDir, source, destination string) error {
	from, err := managedRelative(dataDir, source)
	if err != nil {
		return err
	}
	to, err := managedRelative(dataDir, destination)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return moveManagedPath(root, from, to)
}

func managedRemoveAll(dataDir, path string) error {
	name, err := managedRelative(dataDir, path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return err
	}
	defer root.Close()
	parent, closeParent, err := openManagedParent(root, filepath.Dir(name), false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer closeParent()
	return parent.RemoveAll(filepath.Base(name))
}

func managedMkdirAll(dataDir, path string) error {
	name, err := managedRelative(dataDir, path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return err
	}
	defer root.Close()
	_, closeParent, err := openManagedParent(root, name, true)
	if err != nil {
		return err
	}
	return closeParent()
}

func openManagedDirectory(dataDir, path string, create bool) (*os.Root, func() error, error) {
	name := "."
	var err error
	if !filepath.IsAbs(dataDir) || !filepath.IsAbs(path) || filepath.Clean(dataDir) != filepath.Clean(path) {
		name, err = managedRelative(dataDir, path)
	}
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, nil, err
	}
	parent, closeParent, err := openManagedParent(root, name, create)
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	return parent, func() error { return errors.Join(closeParent(), root.Close()) }, nil
}

func managedMkdirTemp(dataDir, path, prefix string) (string, error) {
	name, err := managedRelative(dataDir, path)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	parent, closeParent, err := openManagedParent(root, name, true)
	if err != nil {
		return "", err
	}
	defer closeParent()
	base := prefix + uuid.NewString()
	if err := parent.Mkdir(base, archiveDirMode); err != nil {
		return "", err
	}
	return filepath.Join(path, base), nil
}

func writeArchiveJournal(dataDir, path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	name, err := managedRelative(dataDir, path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return err
	}
	defer root.Close()
	parent, closeParent, err := openManagedParent(root, filepath.Dir(name), false)
	if err != nil {
		return err
	}
	defer closeParent()
	// Journals are published once, before moves. Never truncate a preexisting
	// journal or an introduced hard link to another application file.
	temporary := ".journal-" + uuid.NewString()
	file, err := parent.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, archiveFileMode)
	if err != nil {
		return err
	}
	defer parent.Remove(temporary)
	n, writeErr := file.Write(raw)
	if writeErr == nil && n != len(raw) {
		writeErr = io.ErrShortWrite
	}
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	return moveManagedPath(root, filepath.Join(filepath.Dir(name), temporary), name)
}

func managedPathError(op, from, to string, err error) error {
	if err == nil {
		return nil
	}
	return &os.LinkError{Op: op, Old: from, New: to, Err: err}
}

func closeManagedRoots(roots []*os.Root) error {
	var errs []error
	for i := len(roots) - 1; i >= 0; i-- {
		errs = append(errs, roots[i].Close())
	}
	return errors.Join(errs...)
}
