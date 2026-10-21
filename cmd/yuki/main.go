// Command yuki is the YukiTunnel client: a full CLI that mirrors the desktop
// GUI and a foreground tunnel runner.
package main

import (
	"fmt"
	"os"

	"github.com/yuuuuukiu/yukitunnel/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误："+cli.Render(err))
		os.Exit(1)
	}
}
