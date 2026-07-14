// Package terminal contains Linux terminal predicates shared by host and
// container-side process orchestration.
package terminal

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

// IsReader reports whether reader is attached to a terminal device. It uses
// TCGETS instead of ModeCharDevice because devices such as /dev/null are also
// character devices but do not deliver terminal-generated signals.
func IsReader(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		file.Fd(),
		syscall.TCGETS,
		uintptr(unsafe.Pointer(&termios)),
		0,
		0,
		0,
	)
	return errno == 0
}
