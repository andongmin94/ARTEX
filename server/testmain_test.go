package server

import (
	"path/filepath"
	"testing"
)

func testBusinessPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "artex.sqlite")
}
