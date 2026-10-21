package servercli

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

func readFile(p string) (string, error) {
	b, err := os.ReadFile(expandHome(p))
	if err != nil {
		return "", fmt.Errorf("读取文件 %s 失败：%w", p, err)
	}
	return strings.TrimSpace(string(b)), nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

func promptPassword(label string) (string, error) {
	fmt.Print(label)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// localTime renders a stored UTC timestamp in the operator's timezone.
func localTime(t time.Time) string {
	if t.IsZero() {
		return "从未"
	}
	return t.Local().Format("2006-01-02 15:04")
}
