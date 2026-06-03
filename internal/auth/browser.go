package auth

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// browserCommand builds the command used to open url on the given OS, or returns
// an error when no browser can be launched (for example a headless Linux box).
// It is split out from OpenBrowser so the platform/headless logic is unit-testable
// without actually spawning a browser.
//
// lookPath/getenv are injected so tests can simulate a machine with/without a
// display server and with/without xdg-open. In production they are exec.LookPath
// and os.Getenv.
func browserCommand(goos, url string, lookPath func(string) (string, error), getenv func(string) string) (*exec.Cmd, error) {
	switch goos {
	case "darwin":
		return exec.Command("open", url), nil
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
	case "linux":
		// Without a display server, xdg-open either is missing or hands off to
		// nothing and the caller would hang forever waiting on a browser that
		// never opens. Detect that up front so login can fall back to printing
		// the URL instead.
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return nil, fmt.Errorf("no graphical display detected (DISPLAY/WAYLAND_DISPLAY unset)")
		}
		path, err := lookPath("xdg-open")
		if err != nil {
			return nil, fmt.Errorf("xdg-open not found on PATH")
		}
		return exec.Command(path, url), nil
	default:
		return nil, fmt.Errorf("unsupported platform %s", goos)
	}
}

// OpenBrowser opens the given URL in the user's default browser. It returns an
// error when a browser could not be launched; callers must print the URL so the
// user can open it manually (never assume success means a browser appeared).
func OpenBrowser(url string) error {
	cmd, err := browserCommand(runtime.GOOS, url, exec.LookPath, os.Getenv)
	if err != nil {
		return err
	}
	return cmd.Start()
}
