package main

import "testing"

func TestWantsJSON(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"whoami", "--json"}, true},
		{[]string{"--json", "whoami"}, true},
		{[]string{"tunnel", "list", "--json"}, true},
		{[]string{"whoami", "--json=true"}, true},
		{[]string{"whoami"}, false},
		{[]string{"--jsonx", "whoami"}, false},
		// Everything after "--" is an operand, not a flag.
		{[]string{"--", "--json"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := wantsJSON(c.args); got != c.want {
			t.Errorf("wantsJSON(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}
