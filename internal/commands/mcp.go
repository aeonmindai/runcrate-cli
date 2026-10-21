package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	mcpServerName = "runcrate"
	mcpServerURL  = "https://mcp.runcrate.ai/mcp"
	mcpDocsURL    = "https://runcrate.ai/docs/mcp/setup"
)

// Result statuses, one per client line.
const (
	mcpConnected = "connected"
	mcpAlready   = "already connected"
	mcpWould     = "would connect"
	mcpManual    = "manual"
	mcpKept      = "kept"
	mcpSkipped   = "skipped (not found)"
	mcpFailed    = "failed"
)

// mcpRunner runs bin with args. Interactive runs inherit stdio and return no
// output; the others return combined output.
type mcpRunner func(interactive bool, bin string, args ...string) (string, error)

// mcpEnv is everything an install touches outside the process, so tests can
// point it at a temp home with no binaries on PATH.
type mcpEnv struct {
	home     string
	goos     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	run      mcpRunner
	now      func() time.Time
	out      io.Writer
	dryRun   bool
	force    bool
}

type mcpResult struct {
	status  string
	detail  string
	snippet string // config the user adds by hand, or would be written
	note    string // extra step for the user
}

type mcpClient struct {
	id      string
	name    string
	detect  func(e *mcpEnv) bool
	install func(e *mcpEnv) mcpResult
}

var mcpClients = []mcpClient{
	{id: "cursor", name: "Cursor", detect: detectCursor, install: installCursor},
	{id: "claude", name: "Claude Code", detect: detectClaude, install: installClaude},
	{id: "vscode", name: "VS Code", detect: detectVSCode, install: installVSCode},
	{id: "codex", name: "Codex", detect: detectCodex, install: installCodex},
	{id: "windsurf", name: "Windsurf", detect: detectWindsurf, install: installWindsurf},
	{id: "gemini", name: "Gemini CLI", detect: detectGemini, install: installGemini},
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Connect Runcrate to your AI clients over MCP",
	Long: `Runcrate runs a remote MCP server at ` + mcpServerURL + `.
Your AI client signs in through the browser on first use. No key to paste.`,
}

var mcpURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Print the Runcrate MCP server URL",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), mcpServerURL)
	},
}

var mcpInstallCmd = &cobra.Command{
	Use:   "install [--client cursor|claude|vscode|codex|windsurf|gemini|all] [--dry-run] [--force]",
	Short: "Add the Runcrate MCP server to the AI clients on this machine",
	Long: `Adds the Runcrate MCP server to your AI clients, so you never edit their config by hand.

With --client all (the default) it sets up every client found on this machine.
Config files are merged: other servers stay, the original is backed up next to
it, and a file it cannot read safely (for example one with comments) is left as
is, with the snippet to add printed instead. An existing runcrate entry that
points somewhere else is kept unless you pass --force.`,
	Example: `  runcrate mcp install
  runcrate mcp install --client cursor --dry-run
  runcrate mcp install --client claude,codex`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding your home directory: %w", err)
		}
		clients, _ := cmd.Flags().GetStringSlice("client")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		force, _ := cmd.Flags().GetBool("force")
		e := &mcpEnv{
			home:     home,
			goos:     runtime.GOOS,
			getenv:   os.Getenv,
			lookPath: exec.LookPath,
			run:      execMCPRunner,
			now:      time.Now,
			out:      cmd.OutOrStdout(),
			dryRun:   dryRun,
			force:    force,
		}
		return runMCPInstall(e, clients)
	},
}

func execMCPRunner(interactive bool, bin string, args ...string) (string, error) {
	if interactive {
		c := exec.Command(bin, args...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return "", c.Run()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return string(out), err
}

// runMCPInstall installs into the named clients. "all" means every client
// detected on this machine; a client named explicitly is set up even when it
// is not detected.
func runMCPInstall(e *mcpEnv, names []string) error {
	selected, all, err := selectMCPClients(names)
	if err != nil {
		return err
	}

	type row struct {
		client mcpClient
		found  bool
	}
	rows := make([]row, 0, len(selected))
	anyFound := false
	for _, c := range selected {
		found := !all || c.detect(e)
		anyFound = anyFound || found
		rows = append(rows, row{c, found})
	}

	w := e.out
	fmt.Fprintln(w)
	if !anyFound {
		fmt.Fprintln(w, "  No AI clients found on this machine.")
		fmt.Fprintf(w, "  Add this MCP server to your client: %s\n", mcpServerURL)
		fmt.Fprintf(w, "  Setup guides: %s\n", mcpDocsURL)
		fmt.Fprintln(w)
		return nil
	}

	if e.dryRun {
		fmt.Fprintf(w, "  Dry run: nothing is written or run. Server: %s\n\n", mcpServerURL)
	} else {
		fmt.Fprintf(w, "  Connecting Runcrate (%s)\n\n", mcpServerURL)
	}

	var ready, notes []string
	failed := 0
	for _, r := range rows {
		res := mcpResult{status: mcpSkipped}
		if r.found {
			res = r.client.install(e)
		}
		printMCPResult(w, r.client.name, res)
		switch res.status {
		case mcpConnected, mcpAlready, mcpWould, mcpManual:
			ready = append(ready, r.client.name)
		case mcpFailed:
			failed++
		}
		if res.note != "" {
			notes = append(notes, res.note)
		}
	}

	fmt.Fprintln(w)
	if len(ready) > 0 {
		fmt.Fprintf(w, "  Next: open %s; it opens your browser to sign in to Runcrate on first use. No key to paste.\n", joinOr(ready))
	}
	for _, n := range notes {
		fmt.Fprintf(w, "  %s\n", n)
	}
	fmt.Fprintln(w)

	if failed > 0 {
		return fmt.Errorf("%d client(s) could not be connected, see above", failed)
	}
	return nil
}

func selectMCPClients(names []string) (clients []mcpClient, all bool, err error) {
	want := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if n == "all" {
			return mcpClients, true, nil
		}
		want[n] = true
	}
	if len(want) == 0 {
		return mcpClients, true, nil
	}
	for _, c := range mcpClients {
		if want[c.id] {
			clients = append(clients, c)
			delete(want, c.id)
		}
	}
	for n := range want {
		ids := make([]string, len(mcpClients))
		for i, c := range mcpClients {
			ids[i] = c.id
		}
		return nil, false, fmt.Errorf("unknown client %q, use one of: %s, all", n, strings.Join(ids, ", "))
	}
	return clients, false, nil
}

func printMCPResult(w io.Writer, name string, r mcpResult) {
	line := r.status
	if r.detail != "" {
		line += ": " + r.detail
	}
	fmt.Fprintf(w, "  %-12s %s\n", name, line)
	if r.snippet != "" {
		for _, l := range strings.Split(strings.TrimRight(r.snippet, "\n"), "\n") {
			fmt.Fprintf(w, "  %-12s   %s\n", "", l)
		}
	}
}

func joinOr(items []string) string {
	switch len(items) {
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// --- detection -------------------------------------------------------------

func (e *mcpEnv) has(bin string) (string, bool) {
	p, err := e.lookPath(bin)
	return p, err == nil
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func (e *mcpEnv) tilde(path string) string {
	if rel, err := filepath.Rel(e.home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}

func detectCursor(e *mcpEnv) bool {
	_, ok := e.has("cursor")
	return ok || dirExists(filepath.Join(e.home, ".cursor"))
}

func detectClaude(e *mcpEnv) bool {
	_, ok := e.has("claude")
	return ok || dirExists(filepath.Join(e.home, ".claude"))
}

func detectVSCode(e *mcpEnv) bool {
	_, ok := e.has("code")
	return ok || dirExists(vscodeUserDir(e))
}

func detectCodex(e *mcpEnv) bool {
	_, ok := e.has("codex")
	return ok || dirExists(filepath.Join(e.home, ".codex"))
}

func detectWindsurf(e *mcpEnv) bool {
	for _, d := range windsurfDirs(e) {
		if dirExists(d) {
			return true
		}
	}
	return false
}

// windsurfDirs returns Windsurf's config dir and Devin Desktop's, its new name.
func windsurfDirs(e *mcpEnv) []string {
	devin := filepath.Join(e.home, ".config", "devin")
	if e.goos == "windows" {
		devin = filepath.Join(appData(e), "devin")
	}
	return []string{filepath.Join(e.home, ".codeium", "windsurf"), devin}
}

func appData(e *mcpEnv) string {
	if base := e.getenv("APPDATA"); base != "" {
		return base
	}
	return filepath.Join(e.home, "AppData", "Roaming")
}

func detectGemini(e *mcpEnv) bool {
	_, ok := e.has("gemini")
	return ok || dirExists(filepath.Join(e.home, ".gemini"))
}

func vscodeUserDir(e *mcpEnv) string {
	switch e.goos {
	case "darwin":
		return filepath.Join(e.home, "Library", "Application Support", "Code", "User")
	case "windows":
		return filepath.Join(appData(e), "Code", "User")
	default:
		base := e.getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(e.home, ".config")
		}
		return filepath.Join(base, "Code", "User")
	}
}

// --- clients ---------------------------------------------------------------

func installCursor(e *mcpEnv) mcpResult {
	return mergeJSONServer(e, filepath.Join(e.home, ".cursor", "mcp.json"), "mcpServers",
		map[string]string{"url": mcpServerURL})
}

// installWindsurf writes every Windsurf and Devin Desktop config dir that
// exists; named explicitly with neither present, it sets up Windsurf's.
func installWindsurf(e *mcpEnv) mcpResult {
	dirs := windsurfDirs(e)
	var found []string
	for _, d := range dirs {
		if dirExists(d) {
			found = append(found, d)
		}
	}
	if len(found) == 0 {
		found = dirs[:1]
	}
	results := make([]mcpResult, len(found))
	for i, d := range found {
		results[i] = mergeJSONServer(e, filepath.Join(d, "mcp_config.json"), "mcpServers",
			map[string]string{"serverUrl": mcpServerURL})
	}
	return combineMCPResults(results)
}

// combineMCPResults folds the results of one client's config files into one
// line, reporting the status that most needs the user's attention.
func combineMCPResults(results []mcpResult) mcpResult {
	if len(results) == 1 {
		return results[0]
	}
	rank := []string{mcpFailed, mcpManual, mcpKept, mcpConnected, mcpWould, mcpAlready}
	out := mcpResult{status: mcpAlready}
	for _, s := range rank {
		if slices.ContainsFunc(results, func(r mcpResult) bool { return r.status == s }) {
			out.status = s
			break
		}
	}
	var details, snippets []string
	for _, r := range results {
		d := r.detail
		if r.status != out.status {
			d = r.status + ": " + d
		}
		details = append(details, d)
		if r.snippet != "" && !slices.Contains(snippets, r.snippet) {
			snippets = append(snippets, r.snippet)
		}
	}
	out.detail = strings.Join(details, "; ")
	out.snippet = strings.Join(snippets, "")
	return out
}

func installVSCode(e *mcpEnv) mcpResult {
	path := filepath.Join(vscodeUserDir(e), "mcp.json")
	entry := map[string]string{"type": "http", "url": mcpServerURL}
	add, _ := json.Marshal(map[string]string{"name": mcpServerName, "type": "http", "url": mcpServerURL})
	// On Windows `code` is a .cmd script, which mangles the quoted JSON argument,
	// so the file is written directly there.
	var cli []string
	if e.goos != "windows" {
		cli = []string{"code", "--add-mcp", string(add)}
	}
	return installViaCLIOrJSON(e, cli, path, "servers", entry)
}

func installGemini(e *mcpEnv) mcpResult {
	path := filepath.Join(e.home, ".gemini", "settings.json")
	cli := []string{"gemini", "mcp", "add", "--transport", "http", "--scope", "user", mcpServerName, mcpServerURL}
	return installViaCLIOrJSON(e, cli, path, "mcpServers", map[string]string{"httpUrl": mcpServerURL})
}

// installViaCLIOrJSON adds the server with the client's own CLI when it is on
// PATH and the JSON file it writes has no runcrate entry yet; otherwise, or when
// the CLI fails, it merges the file itself.
func installViaCLIOrJSON(e *mcpEnv, cli []string, path, container string, entry map[string]string) mcpResult {
	if len(cli) > 0 {
		if bin, ok := e.has(cli[0]); ok {
			cfg, err := loadJSONConfig(path)
			if err == nil && cfg.parseErr == nil {
				if _, existing, ok := cfg.servers(container); ok && existing == nil {
					if e.dryRun {
						return mcpResult{status: mcpWould, detail: "by running: " + shellLine(cli)}
					}
					out, err := e.run(false, bin, cli[1:]...)
					if err == nil {
						return mcpResult{status: mcpConnected, detail: "added with " + cli[0]}
					}
					res := mergeJSONServer(e, path, container, entry)
					if res.status == mcpConnected {
						res.detail += " (" + cli[0] + " failed: " + firstLine(out, err) + ")"
					}
					return res
				}
			}
		}
	}
	return mergeJSONServer(e, path, container, entry)
}

func installClaude(e *mcpEnv) mcpResult {
	add := []string{"mcp", "add", "--transport", "http", "--scope", "user", mcpServerName, mcpServerURL}
	oneLiner := shellLine(append([]string{"claude"}, add...))
	bin, ok := e.has("claude")
	if !ok {
		return mcpResult{status: mcpManual, detail: "claude is not on your PATH. Run this once it is:", snippet: oneLiner}
	}
	remove := []string{"mcp", "remove", "--scope", "user", mcpServerName}
	return installViaCLI(e, "claude", bin, add, remove, oneLiner, false)
}

func installCodex(e *mcpEnv) mcpResult {
	login := "Codex: if it has not signed you in yet, run: codex mcp login " + mcpServerName
	bin, ok := e.has("codex")
	if !ok {
		res := appendCodexTOML(e, filepath.Join(e.home, ".codex", "config.toml"))
		if res.status != mcpFailed && res.status != mcpKept {
			res.note = login
		}
		return res
	}
	add := []string{"mcp", "add", mcpServerName, "--url", mcpServerURL}
	remove := []string{"mcp", "remove", mcpServerName}
	res := installViaCLI(e, "codex", bin, add, remove, shellLine(append([]string{"codex"}, add...)), true)
	if res.status != mcpFailed && res.status != mcpKept {
		res.note = login
	}
	return res
}

// installViaCLI adds the server with a client CLI that has `mcp get`, `mcp add`
// and `mcp remove`. An entry that already points at our URL is left alone; one
// that points elsewhere is kept unless --force.
func installViaCLI(e *mcpEnv, name, bin string, add, remove []string, oneLiner string, interactive bool) mcpResult {
	if e.dryRun {
		return mcpResult{status: mcpWould, detail: "by running: " + oneLiner}
	}
	out, err := e.run(false, bin, "mcp", "get", mcpServerName)
	if err == nil {
		if strings.Contains(out, mcpServerURL) {
			return mcpResult{status: mcpAlready}
		}
		if !e.force {
			return mcpResult{status: mcpKept, detail: "a different runcrate server is set up in " + name + ". Run with --force to replace it."}
		}
		if out, err := e.run(false, bin, remove...); err != nil {
			return mcpResult{status: mcpManual, detail: "could not remove the existing runcrate server (" + firstLine(out, err) + "). Remove it, then run:", snippet: oneLiner}
		}
	}
	if interactive {
		fmt.Fprintf(e.out, "  %-12s running: %s\n", "", oneLiner)
		_, err = e.run(true, bin, add...)
		out = ""
	} else {
		out, err = e.run(false, bin, add...)
	}
	if err != nil {
		return mcpResult{status: mcpManual, detail: name + " could not add it (" + firstLine(out, err) + "). Run:", snippet: oneLiner}
	}
	return mcpResult{status: mcpConnected, detail: "added with " + name}
}

func shellLine(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsAny(a, " \"'{}$*?") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

func firstLine(out string, err error) string {
	if s := strings.TrimSpace(out); s != "" {
		return strings.SplitN(s, "\n", 2)[0]
	}
	return err.Error()
}

// --- Codex TOML ------------------------------------------------------------

var (
	codexTableRe   = regexp.MustCompile(`(?m)^[ \t]*\[[ \t]*mcp_servers[ \t]*\.[ \t]*"?` + mcpServerName + `"?[ \t]*\][ \t]*(#.*)?$`)
	tomlNextTable  = regexp.MustCompile(`(?m)^[ \t]*\[`)
	codexOurURLRe  = regexp.MustCompile(`(?m)^[ \t]*url[ \t]*=[ \t]*"` + regexp.QuoteMeta(mcpServerURL) + `"`)
	codexTableText = "[mcp_servers." + mcpServerName + "]\nurl = \"" + mcpServerURL + "\"\n"
)

// appendCodexTOML appends a [mcp_servers.runcrate] table to Codex's config.
// It never rewrites TOML: an existing table is left for the user to change.
func appendCodexTOML(e *mcpEnv, path string) mcpResult {
	raw, mode, existed, err := readConfigFile(path)
	if err != nil {
		return mcpResult{status: mcpFailed, detail: err.Error()}
	}
	if loc := codexTableRe.FindIndex(raw); loc != nil {
		body := raw[loc[1]:]
		if next := tomlNextTable.FindIndex(body); next != nil {
			body = body[:next[0]]
		}
		if codexOurURLRe.Match(body) {
			return mcpResult{status: mcpAlready, detail: e.tilde(path)}
		}
		msg := e.tilde(path) + " already has a different [mcp_servers." + mcpServerName + "] table; kept it."
		if e.force {
			return mcpResult{status: mcpManual, detail: msg + " Remove that table yourself, then run this again."}
		}
		return mcpResult{status: mcpKept, detail: msg + " To replace it, remove that table and run this again."}
	}

	data := append([]byte{}, raw...)
	if len(data) > 0 {
		if data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
		data = append(data, '\n')
	}
	data = append(data, codexTableText...)

	if e.dryRun {
		return mcpResult{status: mcpWould, detail: e.tilde(path) + ", appending:", snippet: codexTableText}
	}
	backup, err := writeConfigFile(path, raw, data, mode, existed, e.now())
	if err != nil {
		return mcpResult{status: mcpFailed, detail: err.Error()}
	}
	return mcpResult{status: mcpConnected, detail: written(e, path, backup)}
}

// --- JSON merge --------------------------------------------------------------

type jsonConfig struct {
	raw      []byte
	mode     fs.FileMode
	existed  bool
	root     map[string]any
	parseErr error
}

func loadJSONConfig(path string) (*jsonConfig, error) {
	raw, mode, existed, err := readConfigFile(path)
	if err != nil {
		return nil, err
	}
	c := &jsonConfig{raw: raw, mode: mode, existed: existed, root: map[string]any{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return c, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep numbers exactly as written
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		c.parseErr = err
		return c, nil
	}
	if root == nil {
		c.parseErr = errors.New("the top level is not an object")
		return c, nil
	}
	if _, err := dec.Token(); err != io.EOF {
		c.parseErr = errors.New("extra content after the JSON object")
		return c, nil
	}
	c.root = root
	return c, nil
}

// servers returns the server map under container (nil when absent) and the
// runcrate entry in it (nil when absent). ok is false when container exists
// but is not an object.
func (c *jsonConfig) servers(container string) (servers map[string]any, existing any, ok bool) {
	v, present := c.root[container]
	if !present || v == nil {
		return nil, nil, true
	}
	servers, ok = v.(map[string]any)
	if !ok {
		return nil, nil, false
	}
	return servers, servers[mcpServerName], true
}

// pointsAtUs reports whether an existing entry already connects to our server.
// Clients spell the URL key differently across versions, so any of them counts.
func pointsAtUs(existing any) bool {
	m, ok := existing.(map[string]any)
	if !ok {
		return false
	}
	for _, k := range []string{"url", "httpUrl", "serverUrl"} {
		if s, ok := m[k].(string); ok && s == mcpServerURL {
			return true
		}
	}
	return false
}

func jsonSnippet(container string, entry map[string]string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{container: map[string]any{mcpServerName: entry}})
	return b.String()
}

// mergeJSONServer sets container.runcrate = entry in the JSON file at path,
// keeping every other key. It never writes a file it could not parse.
func mergeJSONServer(e *mcpEnv, path, container string, entry map[string]string) mcpResult {
	snippet := jsonSnippet(container, entry)
	cfg, err := loadJSONConfig(path)
	if err != nil {
		return mcpResult{status: mcpFailed, detail: err.Error()}
	}
	if cfg.parseErr != nil {
		return mcpResult{status: mcpManual, snippet: snippet,
			detail: fmt.Sprintf("%s is not plain JSON (%v), so it was left as is. Add this to it:", e.tilde(path), cfg.parseErr)}
	}
	servers, existing, ok := cfg.servers(container)
	if !ok {
		return mcpResult{status: mcpManual, snippet: snippet,
			detail: fmt.Sprintf("%q in %s is not an object, so it was left as is. Add this to it:", container, e.tilde(path))}
	}
	if existing != nil {
		if pointsAtUs(existing) {
			return mcpResult{status: mcpAlready, detail: e.tilde(path)}
		}
		if !e.force {
			return mcpResult{status: mcpKept, detail: e.tilde(path) + " has a different runcrate entry. Run with --force to replace it."}
		}
	}
	if e.dryRun {
		return mcpResult{status: mcpWould, detail: e.tilde(path) + ", adding:", snippet: snippet}
	}

	if servers == nil {
		servers = map[string]any{}
		cfg.root[container] = servers
	}
	value := make(map[string]any, len(entry))
	for k, v := range entry {
		value[k] = v
	}
	servers[mcpServerName] = value

	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg.root); err != nil {
		return mcpResult{status: mcpFailed, detail: err.Error()}
	}
	backup, err := writeConfigFile(path, cfg.raw, b.Bytes(), cfg.mode, cfg.existed, e.now())
	if err != nil {
		return mcpResult{status: mcpFailed, detail: err.Error()}
	}
	return mcpResult{status: mcpConnected, detail: written(e, path, backup)}
}

func written(e *mcpEnv, path, backup string) string {
	if backup == "" {
		return e.tilde(path)
	}
	return e.tilde(path) + " (backup " + e.tilde(backup) + ")"
}

// --- files -----------------------------------------------------------------

// readConfigFile reads path; a missing file is empty with mode 0600.
func readConfigFile(path string) (raw []byte, mode fs.FileMode, existed bool, err error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0o600, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading %s: %w", path, err)
	}
	if fi.IsDir() {
		return nil, 0, false, fmt.Errorf("%s is a directory", path)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading %s: %w", path, err)
	}
	return raw, fi.Mode().Perm(), true, nil
}

// writeConfigFile backs up the original (when there was one) to
// <file>.bak-<YYYYMMDD-HHMMSS>, then replaces the file atomically through a
// temp file in the same directory, keeping its mode. A symlinked config is
// written through to its target.
func writeConfigFile(path string, original, data []byte, mode fs.FileMode, existed bool, now time.Time) (backup string, err error) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(path); err == nil {
			path = target
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	if existed {
		if backup, err = writeBackup(path, original, mode, now); err != nil {
			return "", err
		}
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return backup, nil
}

func writeBackup(path string, data []byte, mode fs.FileMode, now time.Time) (string, error) {
	base := path + ".bak-" + now.Format("20060102-150405")
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("backing up %s: %w", path, err)
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", fmt.Errorf("backing up %s: %w", path, err)
		}
		return name, nil
	}
}

func init() {
	mcpInstallCmd.Flags().StringSlice("client", []string{"all"}, "Clients to set up: cursor, claude, vscode, codex, windsurf (also Devin Desktop), gemini, or all (every client found)")
	mcpInstallCmd.Flags().Bool("dry-run", false, "Show what would change; write and run nothing")
	mcpInstallCmd.Flags().Bool("force", false, "Replace a runcrate entry that points somewhere else")
	mcpCmd.AddCommand(mcpInstallCmd)
	mcpCmd.AddCommand(mcpURLCmd)
	rootCmd.AddCommand(mcpCmd)
}
