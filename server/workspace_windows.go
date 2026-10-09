package server

import (
	"os"
	"syscall"
)

func workspaceLink(info os.FileInfo) bool {
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || (ok && attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0)
}
func workspaceMultipleLinks(file *os.File, _ os.FileInfo) bool {
	var info syscall.ByHandleFileInformation
	return syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info) != nil || info.NumberOfLinks > 1
}
