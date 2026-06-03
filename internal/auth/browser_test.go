package auth

import (
	"fmt"
	"testing"
)

func TestBrowserCommand(t *testing.T) {
	const url = "https://runcrate.ai/auth/authorize?client=cli&port=51234"

	found := func(string) (string, error) { return "/usr/bin/xdg-open", nil }
	missing := func(string) (string, error) { return "", fmt.Errorf("not found") }
	withDisplay := func(k string) string {
		if k == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	noDisplay := func(string) string { return "" }

	tests := []struct {
		name     string
		goos     string
		lookPath func(string) (string, error)
		getenv   func(string) string
		wantErr  bool
		wantArg  string // a value expected somewhere in cmd.Args when no error
	}{
		{name: "darwin uses open", goos: "darwin", lookPath: missing, getenv: noDisplay, wantArg: url},
		{name: "windows uses rundll32", goos: "windows", lookPath: missing, getenv: noDisplay, wantArg: url},
		{name: "linux with display + xdg-open", goos: "linux", lookPath: found, getenv: withDisplay, wantArg: url},
		{name: "linux headless (no display) errors", goos: "linux", lookPath: found, getenv: noDisplay, wantErr: true},
		{name: "linux no xdg-open errors", goos: "linux", lookPath: missing, getenv: withDisplay, wantErr: true},
		{name: "unsupported platform errors", goos: "plan9", lookPath: found, getenv: withDisplay, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := browserCommand(tt.goos, url, tt.lookPath, tt.getenv)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("browserCommand(%q) = nil error, want error", tt.goos)
				}
				return
			}
			if err != nil {
				t.Fatalf("browserCommand(%q) unexpected error: %v", tt.goos, err)
			}
			if cmd == nil {
				t.Fatalf("browserCommand(%q) returned nil cmd with no error", tt.goos)
			}
			var sawArg bool
			for _, a := range cmd.Args {
				if a == tt.wantArg {
					sawArg = true
				}
			}
			if !sawArg {
				t.Errorf("browserCommand(%q) args %v missing expected %q", tt.goos, cmd.Args, tt.wantArg)
			}
		})
	}
}
