//go:build !windows

package evidence

import (
	"errors"
	"os"
	"path/filepath"
)

func installBodyFile(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
