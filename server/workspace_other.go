//go:build !windows

package server

import (
	"os"
	"syscall"
)

func workspaceLink(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
func workspaceMultipleLinks(_ *os.File, info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return !ok || stat.Nlink > 1
}
