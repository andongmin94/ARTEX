//go:build !windows

package toolruntime

import "os"

func isReparsePoint(os.FileInfo) bool { return false }
