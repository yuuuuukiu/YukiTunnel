// Command yuki-server is the YukiTunnel server and its administration CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/yuuuuukiu/yukitunnel/internal/servercli"
)

func main() {
	if err := servercli.Execute(os.Args[1:]); err != nil {
		// Mirror the client CLI: with --json the caller is a script and needs a
		// machine-readable failure on stderr, not a Chinese sentence.
		if wantsJSON(os.Args[1:]) {
			enc := json.NewEncoder(os.Stderr)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(map[string]any{
				"error":   servercli.Render(err),
				"success": false,
			})
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "错误："+servercli.Render(err))
		os.Exit(1)
	}
}

// wantsJSON scans raw arguments because flag parsing may not have run yet when
// an error occurs. "--" terminates flag parsing.
func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--json" || a == "--json=true" {
			return true
		}
	}
	return false
}
