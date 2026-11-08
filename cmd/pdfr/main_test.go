package main

import (
	"bytes"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSelectionModeOptions(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		unset   bool
		args    []string
		want    string
		wantErr bool
	}{
		{name: "unset defaults to text", unset: true, want: "text"},
		{name: "environment text", env: "text", want: "text"},
		{name: "environment page", env: "page", want: "page"},
		{name: "CLI overrides page preference", env: "page", args: []string{"--selection-mode", "text"}, want: "text"},
		{name: "CLI overrides text preference", env: "text", args: []string{"--selection-mode=page"}, want: "page"},
		{name: "CLI overrides invalid environment", env: "garbage", args: []string{"--selection-mode=page"}, want: "page"},
		{name: "CLI overrides empty environment", args: []string{"--selection-mode=text"}, want: "text"},
		{name: "last CLI value wins", env: "text", args: []string{"--selection-mode=page", "--selection-mode=text"}, want: "text"},
		{name: "invalid environment", env: "garbage", wantErr: true},
		{name: "empty environment", wantErr: true},
		{name: "invalid CLI overrides valid environment", env: "page", args: []string{"--selection-mode=garbage"}, wantErr: true},
		{name: "empty CLI value", env: "page", args: []string{"--selection-mode="}, wantErr: true},
		{name: "case sensitive", env: "Page", wantErr: true},
		{name: "no whitespace normalization", env: " page ", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PDFR_SELECTION_MODE", test.env)
			if test.unset {
				if err := os.Unsetenv("PDFR_SELECTION_MODE"); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			options, err := parseOptions(test.args, &stdout, &stderr)
			if test.wantErr {
				if err == nil || !strings.Contains(stderr.String(), "selection mode") {
					t.Fatalf("invalid mode: options %+v, error %v, stderr %q", options, err, stderr.String())
				}
				if options.SelectionMode != "" {
					t.Fatalf("invalid mode returned usable options: %+v", options)
				}
				return
			}
			if err != nil || options.SelectionMode != test.want {
				t.Fatalf("mode = %q, error %v; want %q", options.SelectionMode, err, test.want)
			}
			if options.Page != 0 || options.CacheBytes != 128*1024*1024 || len(options.Paths) != 0 {
				t.Fatalf("selection mode changed other defaults: %+v", options)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("successful parsing wrote stdout %q, stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestSelectionModePreservesOtherOptions(t *testing.T) {
	t.Setenv("PDFR_SELECTION_MODE", "page")
	var stdout, stderr bytes.Buffer
	options, err := parseOptions([]string{"--page", "1", "--cache-mb", "0", "--selection-mode", "text", "first.pdf", "second file.pdf"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if options.SelectionMode != "text" || options.Page != 1 || options.CacheBytes != 0 || !reflect.DeepEqual(options.Paths, []string{"first.pdf", "second file.pdf"}) {
		t.Fatalf("options = %+v", options)
	}
}

func TestSelectionModeHelpAndErrors(t *testing.T) {
	t.Setenv("PDFR_SELECTION_MODE", "garbage")
	var stdout, stderr bytes.Buffer
	if _, err := parseOptions([]string{"--help"}, &stdout, &stderr); err != flag.ErrHelp {
		t.Fatalf("help error = %v", err)
	}
	if !strings.Contains(stdout.String(), "--selection-mode text|page") || !strings.Contains(stdout.String(), "PDFR_SELECTION_MODE") || stderr.Len() != 0 {
		t.Fatalf("help stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := run([]string{"--selection-mode=invalid", "file.pdf"}, &stdout, &stderr); status != 2 {
		t.Fatalf("invalid mode exit status = %d, want 2", status)
	}
	if !strings.Contains(stderr.String(), "invalid selection mode") || stdout.Len() != 0 {
		t.Fatalf("invalid mode stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}
