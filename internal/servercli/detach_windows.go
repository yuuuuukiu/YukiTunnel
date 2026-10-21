//go:build windows

package servercli

import "syscall"

// detachedProcAttr uses DETACHED_PROCESS so the child has no console window and
// keeps running after the parent exits.
func detachedProcAttr() *syscall.SysProcAttr {
	const createNewProcessGroup = 0x00000200
	const detachedProcess = 0x00000008
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}
