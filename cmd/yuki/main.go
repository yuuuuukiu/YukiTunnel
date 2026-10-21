// Command yuki is the YukiTunnel client: a full CLI that mirrors the desktop
// GUI and a foreground tunnel runner.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/yuuuuukiu/yukitunnel/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		// With --json the caller is a script, so the failure must be machine
		// readable too; mixing a Chinese sentence into stderr breaks parsers
		// that only look at one stream.
		if wantsJSON(os.Args[1:]) {
			enc := json.NewEncoder(os.Stderr)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(map[string]any{
				"error":   cli.Render(err),
				"success": false,
			})
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "错误："+cli.Render(err))
		os.Exit(1)
	}
}

// wantsJSON reports whether --json appears as a standalone flag, accepting
// both "--json" and "--json=true".
func wantsJSON(args []string) bool {
	for _, a := range args {
		// "--" terminates flag parsing; anything after it is an operand.
		if a == "--" {
			return false
		}
		if a == "--json" || a == "--json=true" {
			return true
		}
	}
	return false
}
