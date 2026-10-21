//go:build !windows

package store

import "syscall"

// privateUmask restricts newly created files to the service user and returns a
// function that restores the previous mask.
func privateUmask() func() {
	prev := syscall.Umask(0o077)
	return func() { syscall.Umask(prev) }
}
