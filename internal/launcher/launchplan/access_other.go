//go:build !linux

package launchplan

import "fmt"

func checkEffectiveAccess(string) error {
	return fmt.Errorf("host-backed dependency caches require Linux")
}
