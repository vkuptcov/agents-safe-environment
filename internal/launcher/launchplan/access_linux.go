//go:build linux

package launchplan

import "syscall"

const (
	accessRead   = 4
	accessWrite  = 2
	accessSearch = 1
)

func checkEffectiveAccess(path string) error {
	return syscall.Access(path, accessRead|accessWrite|accessSearch)
}
