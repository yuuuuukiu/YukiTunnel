//go:build !windows

package firewall

import "os"

func currentEUID() int { return os.Geteuid() }
