//go:build windows

package evidence

import "golang.org/x/sys/windows"

// The staging file was already flushed. Windows cannot fsync a directory
// handle opened by os.Open; request a synchronous move of the flushed file.
func installBodyFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
