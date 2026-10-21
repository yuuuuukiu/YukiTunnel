package store

import "time"

// nowUTC is a tiny seam so tests can reason about timestamps deterministically.
func nowUTC() time.Time { return time.Now().UTC() }
