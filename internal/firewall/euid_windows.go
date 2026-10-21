//go:build windows

package firewall

// currentEUID reports -1 on Windows: there is no POSIX euid, and the firewall
// adapter is unsupported there anyway.
func currentEUID() int { return -1 }
