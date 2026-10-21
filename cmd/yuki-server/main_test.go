package main

import "testing"

func TestWantsJSON(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"profile", "list", "--json"}, true},
		{[]string{"--json", "status"}, true},
		{[]string{"profile", "install", "x", "--json=true"}, true},
		{[]string{"status"}, false},
		// An operand that looks like the flag must not enable JSON mode.
		{[]string{"--", "--json"}, false},
		{[]string{"profile", "install", "--", "--json"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := wantsJSON(c.args); got != c.want {
			t.Errorf("wantsJSON(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}
