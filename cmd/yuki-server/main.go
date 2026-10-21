// Command yuki-server is the YukiTunnel server and its administration CLI.
package main

import (
	"fmt"
	"os"

	"github.com/yuuuuukiu/yukitunnel/internal/servercli"
)

func main() {
	if err := servercli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误："+servercli.Render(err))
		os.Exit(1)
	}
}
