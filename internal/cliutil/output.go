package cliutil

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// Printer renders tables or JSON.
type Printer struct {
	JSON bool
	Out  io.Writer
}

// NewPrinter builds a printer bound to stdout.
func NewPrinter(jsonOut bool) *Printer {
	return &Printer{JSON: jsonOut, Out: os.Stdout}
}

// JSONOut prints v as indented JSON.
func (p *Printer) JSONOut(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Table prints rows with headers using a tabwriter.
func (p *Printer) Table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(p.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	_ = w.Flush()
}

// HumanBytes formats byte counts.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// HumanDuration formats a duration in a compact way.
func HumanDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

// StatusDot renders a colored status indicator (falls back to ASCII).
func StatusDot(active bool) string {
	if active {
		return "● 运行中"
	}
	return "○ 已停止"
}

// YesNo renders a boolean.
func YesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}
