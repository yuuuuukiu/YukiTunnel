//go:build !windows

package servercli

import "syscall"

// detachedProcAttr starts the background server in its own session so it
// survives the terminal that launched it.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
