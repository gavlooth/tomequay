package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"

	"tomequay/internal/app"
	"tomequay/internal/fzf"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "--preview-page" {
		if err := fzf.Preview(context.Background(), args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, "pdfr preview:", err)
			return 1
		}
		return 0
	}
	options, err := parseOptions(args, stdout, stderr)
	if err == flag.ErrHelp {
		return 0
	}
	if err != nil {
		return 2
	}
	if err := app.Run(context.Background(), options); err != nil {
		fmt.Fprintln(stderr, "pdfr:", err)
		return 1
	}
	return 0
}

func parseOptions(args []string, stdout, stderr io.Writer) (app.Options, error) {
	defaultMode, configured := os.LookupEnv("PDFR_SELECTION_MODE")
	if !configured {
		defaultMode = "text"
	}
	flags := flag.NewFlagSet("pdfr", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cacheMB := flags.Int64("cache-mb", 128, "PNG cache limit in MiB (0 disables caching)")
	page := flags.Int("page", 1, "initial page, clamped to the PDF's page count")
	selectionMode := flags.String("selection-mode", defaultMode, "default selection mode: text or page (PDFR_SELECTION_MODE)")
	help := flags.Bool("help", false, "show usage")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: pdfr [--cache-mb 128] [--page N] [--selection-mode text|page] [file.pdf ...]")
		fmt.Fprintln(flags.Output(), "No files opens the picker. F1 shows vim-style keys; q closes a tab, Q quits.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return app.Options{}, err
	}
	if *help {
		flags.SetOutput(stdout)
		flags.Usage()
		return app.Options{}, flag.ErrHelp
	}
	var validationError error
	switch {
	case *page < 1:
		validationError = fmt.Errorf("--page must be positive")
	case *cacheMB < 0 || *cacheMB > math.MaxInt64/(1024*1024):
		validationError = fmt.Errorf("--cache-mb is out of range")
	case *selectionMode != "text" && *selectionMode != "page":
		validationError = fmt.Errorf("invalid selection mode %q: use text or page", *selectionMode)
	}
	if validationError != nil {
		fmt.Fprintln(stderr, "pdfr:", validationError)
		return app.Options{}, validationError
	}
	initialPage := 0
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "page" {
			initialPage = *page
		}
	})
	return app.Options{
		Paths:         flags.Args(),
		Page:          initialPage,
		CacheBytes:    *cacheMB * 1024 * 1024,
		SelectionMode: *selectionMode,
	}, nil
}
