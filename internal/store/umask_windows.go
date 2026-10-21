//go:build windows

package store

// privateUmask is a no-op on Windows, where file ACLs (not the umask) control
// access and the data directory already inherits a per-user ACL.
func privateUmask() func() { return func() {} }
