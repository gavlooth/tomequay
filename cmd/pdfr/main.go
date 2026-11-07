package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"

	"terminal-pdf-reader/internal/app"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pdfr", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cacheMB := flags.Int64("cache-mb", 128, "PNG cache limit in MiB (0 disables caching)")
	page := flags.Int("page", 1, "initial page, clamped to the PDF's page count")
	help := flags.Bool("help", false, "show usage")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: pdfr [--cache-mb 128] [--page N] [file.pdf ...]")
		fmt.Fprintln(flags.Output(), "No files opens the picker. F1 shows vim-style keys; q closes a tab, Q quits.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *help {
		flags.SetOutput(stdout)
		flags.Usage()
		return 0
	}
	if *page < 1 {
		fmt.Fprintln(stderr, "pdfr: --page must be positive")
		return 2
	}
	if *cacheMB < 0 || *cacheMB > math.MaxInt64/(1024*1024) {
		fmt.Fprintln(stderr, "pdfr: --cache-mb is out of range")
		return 2
	}
	if err := app.Run(context.Background(), app.Options{Paths: flags.Args(), Page: *page, CacheBytes: *cacheMB * 1024 * 1024}); err != nil {
		fmt.Fprintln(stderr, "pdfr:", err)
		return 1
	}
	return 0
}
