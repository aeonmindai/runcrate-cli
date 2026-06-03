package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

func pageHTML(title, message string, isError bool) string {
	iconColor := "#10b981"
	iconSVG := `<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/>`
	if isError {
		iconColor = "#ef4444"
		iconSVG = `<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>`
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Runcrate CLI — %s</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  @media (prefers-color-scheme: dark) {
    :root { --bg: #121212; --fg: #ebebeb; --muted: #999; --edge: #242424; --icon-bg-ok: rgba(16,185,129,0.1); --icon-border-ok: rgba(16,185,129,0.2); --icon-bg-err: rgba(239,68,68,0.1); --icon-border-err: rgba(239,68,68,0.2); }
  }
  @media (prefers-color-scheme: light) {
    :root { --bg: #f9f9f9; --fg: #1a1a1a; --muted: #6e6e6e; --edge: #e0e0e0; --icon-bg-ok: rgba(16,185,129,0.1); --icon-border-ok: rgba(16,185,129,0.2); --icon-bg-err: rgba(239,68,68,0.1); --icon-border-err: rgba(239,68,68,0.2); }
  }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
    background: var(--bg);
    color: var(--fg);
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .card {
    text-align: center;
    max-width: 400px;
    padding: 48px 32px;
  }
  .icon {
    width: 48px; height: 48px;
    border-radius: 50%%;
    display: flex; align-items: center; justify-content: center;
    margin: 0 auto 20px;
    background: %s;
    border: 1px solid %s;
  }
  .icon svg { width: 24px; height: 24px; }
  h1 {
    font-size: 20px;
    font-weight: 600;
    margin-bottom: 8px;
  }
  p {
    color: var(--muted);
    font-size: 15px;
    line-height: 1.5;
  }
  .brand {
    margin-top: 32px;
    color: var(--muted);
    font-size: 13px;
    opacity: 0.6;
  }
</style>
</head>
<body>
<div class="card">
  <div class="icon">
    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="%s">%s</svg>
  </div>
  <h1>%s</h1>
  <p>%s</p>
  <div class="brand">Runcrate</div>
</div>
</body>
</html>`,
		title,
		func() string {
			if isError {
				return "var(--icon-bg-err)"
			}
			return "var(--icon-bg-ok)"
		}(),
		func() string {
			if isError {
				return "var(--icon-border-err)"
			}
			return "var(--icon-border-ok)"
		}(),
		iconColor, iconSVG,
		title, message,
	)
}

// CallbackResult holds the result from the browser OAuth callback.
type CallbackResult struct {
	Code  string
	Error error
}

// StartCallbackServer starts a local HTTP server on a random port and waits
// for the browser to redirect back with a one-time auth code.
// Returns the port it's listening on and a channel that receives the code.
func StartCallbackServer(timeout time.Duration) (int, <-chan CallbackResult, func()) {
	resultCh := make(chan CallbackResult, 1)

	// Listen on a random available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		resultCh <- CallbackResult{Error: fmt.Errorf("failed to start local server: %w", err)}
		return 0, resultCh, func() {}
	}

	port := listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	server := &http.Server{Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		// CORS + Private Network Access headers. The browser delivers the
		// auth code via fetch(no-cors) and a hidden <iframe> from the HTTPS
		// /auth/authorized page. From a "public" HTTPS document to "private"
		// loopback (127.0.0.1), Chrome (130+) requires a CORS preflight that
		// carries `Access-Control-Allow-Private-Network: true`; without it
		// both the fetch and the iframe load are blocked and the CLI hangs
		// at "Waiting for browser authentication..." until the timeout, even
		// though the user successfully authorized in the browser.
		// https://developer.chrome.com/blog/private-network-access-preflight/
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.Header().Set("Vary", "Origin, Access-Control-Request-Private-Network")

		// PNA / CORS preflight: respond 204 and DO NOT consume the result
		// channel — the actual GET with ?code=... arrives right after.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		code := r.URL.Query().Get("code")
		errParam := r.URL.Query().Get("error")

		if errParam != "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, pageHTML("Authorization Failed", errParam, true))
			resultCh <- CallbackResult{Error: fmt.Errorf("auth error: %s", errParam)}
			return
		}

		if code == "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, pageHTML("Missing Code", "No authorization code received.", true))
			resultCh <- CallbackResult{Error: fmt.Errorf("no code received in callback")}
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, pageHTML("CLI Authorized", "You can close this tab and return to your terminal.", false))
		resultCh <- CallbackResult{Code: code}
	})

	// Start serving in a goroutine
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			resultCh <- CallbackResult{Error: fmt.Errorf("callback server error: %w", err)}
		}
	}()

	// Timeout handler
	go func() {
		time.Sleep(timeout)
		select {
		case resultCh <- CallbackResult{Error: fmt.Errorf("login timed out after %s — no response from browser", timeout)}:
		default:
		}
	}()

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}

	return port, resultCh, shutdown
}
