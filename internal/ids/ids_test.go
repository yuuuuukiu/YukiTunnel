package ids

import (
	"regexp"
	"testing"
)

func TestToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		v := Token()
		if v == "" {
			t.Fatal("empty token")
		}
		if seen[v] {
			t.Fatalf("duplicate token after %d iterations: %s", i, v)
		}
		seen[v] = true
	}
}

func TestHex(t *testing.T) {
	hexRe := regexp.MustCompile(`^[0-9a-f]+$`)
	for _, n := range []int{1, 6, 12, 24} {
		v := Hex(n)
		if len(v) != n*2 {
			t.Fatalf("Hex(%d) length = %d, want %d", n, len(v), n*2)
		}
		if !hexRe.MatchString(v) {
			t.Fatalf("Hex(%d) = %q is not lowercase hex", n, v)
		}
	}
	// Distinctness matters: these become primary keys.
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		v := Hex(12)
		if seen[v] {
			t.Fatalf("Hex collision after %d iterations: %s", i, v)
		}
		seen[v] = true
	}
}
