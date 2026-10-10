package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Open each component relative to the previous directory handle, with no
// delete sharing. Parent rename is refused while the handle is held. Never
// reopen the source by an absolute pathname.
func lockManagedDirectory(root *os.Root, name string, create bool) (windows.Handle, func() error, error) {
	base, err := root.Open(".")
	if err != nil {
		return 0, nil, err
	}
	defer base.Close()
	var handles []windows.Handle
	closeAll := func() error {
		var errs []error
		for i := len(handles) - 1; i >= 0; i-- {
			errs = append(errs, windows.CloseHandle(handles[i]))
		}
		return errors.Join(errs...)
	}
	parent := windows.Handle(base.Fd())
	parts := append([]string{""}, strings.Split(name, string(filepath.Separator))...)
	for _, part := range parts {
		if (part == "" || part == ".") && len(handles) > 0 {
			continue
		}
		disposition := uint32(windows.FILE_OPEN)
		if create && part != "" {
			disposition = windows.FILE_OPEN_IF
		}
		handle, err := openManagedHandle(parent, part, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, disposition, true)
		if err != nil {
			_ = closeAll()
			return 0, nil, err
		}
		handles = append(handles, handle)
		parent = handle
	}
	return parent, closeAll, nil
}

func openManagedHandle(parent windows.Handle, name string, access, disposition uint32, directory bool) (windows.Handle, error) {
	text, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: text, Attributes: windows.OBJ_CASE_INSENSITIVE}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access, &attributes, &status, nil, windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, disposition, options, 0, 0)
	if err != nil {
		var native windows.NTStatus
		if errors.As(err, &native) {
			err = native.Errno()
		}
		return 0, &os.PathError{Op: "open managed path", Path: name, Err: err}
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(handle, &info)
	if err == nil && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		err = errWorkspacePath
	}
	if err != nil {
		windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func openManagedParent(root *os.Root, name string, create bool) (*os.Root, func() error, error) {
	handle, unlock, err := lockManagedDirectory(root, name, create)
	if err != nil {
		return nil, nil, err
	}
	parent, err := root.OpenRoot(name)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	// Parent ancestors are pinned; also compare the opened directory identity.
	file, err := parent.Open(".")
	if err == nil {
		var locked, opened windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(handle, &locked)
		if err == nil {
			err = windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &opened)
		}
		if err == nil && (locked.VolumeSerialNumber != opened.VolumeSerialNumber || locked.FileIndexHigh != opened.FileIndexHigh || locked.FileIndexLow != opened.FileIndexLow) {
			err = errWorkspacePath
		}
		file.Close()
	}
	if err != nil {
		parent.Close()
		unlock()
		return nil, nil, err
	}
	return parent, func() error { return errors.Join(parent.Close(), unlock()) }, nil
}

type managedRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func moveManagedPath(root *os.Root, from, to string) error {
	fromParent, closeFrom, err := lockManagedDirectory(root, filepath.Dir(from), false)
	if err != nil {
		return managedPathError("rename", from, to, err)
	}
	defer closeFrom()
	toParent, closeTo, err := lockManagedDirectory(root, filepath.Dir(to), true)
	if err != nil {
		return managedPathError("rename", from, to, err)
	}
	defer closeTo()
	source, err := openManagedHandle(fromParent, filepath.Base(from), windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, false)
	if err != nil {
		return managedPathError("rename", from, to, err)
	}
	defer windows.CloseHandle(source)
	return managedPathError("rename", from, to, renameManagedHandle(source, toParent, filepath.Base(to)))
}

func renameManagedHandle(source, toParent windows.Handle, leaf string) error {
	name, err := windows.UTF16FromString(leaf)
	if err != nil {
		return err
	}
	var layout managedRenameInformation
	length := (len(name) - 1) * 2
	buffer := make([]byte, max(int(unsafe.Sizeof(layout)), int(unsafe.Offsetof(layout.FileName))+length))
	info := (*managedRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.RootDirectory = toParent
	info.FileNameLength = uint32(length)
	copy(unsafe.Slice(&info.FileName[0], len(name)-1), name[:len(name)-1])
	var status windows.IO_STATUS_BLOCK
	// Native handle-relative rename, with replacement disabled. The Win32
	// wrapper may resolve a relative name against CWD; use the existing x/sys
	// native API so destination resolution uses the pinned parent handle.
	err = windows.NtSetInformationFile(source, &status, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
	var native windows.NTStatus
	if errors.As(err, &native) {
		err = native.Errno()
	}
	return err
}
