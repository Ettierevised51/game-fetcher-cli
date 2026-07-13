// Package steam implements provider.Provider on top of steamcmd (spec.md
// sections 2, 4, 8, 10).
//
// Every app download runs a dedicated steamcmd process driven by a generated
// runscript; workshop items go in batches — several workshop_download_item
// commands per process (DownloadBatch). steamcmd keeps its Steam Guard
// sentry files under its own
// directory, so a stable install location across runs preserves the cached
// sentry (section 10).
package steam

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
	"github.com/Austrum-lab/game-fetcher-cli/internal/webapi"
)

// RunFunc executes the steamcmd binary and returns its combined output,
// which drives error classification (spec section 5); replaceable in tests.
type RunFunc func(ctx context.Context, bin string, args ...string) (output string, err error)

// Options configures the provider.
type Options struct {
	// SteamcmdPath is an explicit steamcmd binary (config: steamcmd_path).
	// When empty, $PATH and the auto-install location are tried in turn.
	SteamcmdPath string
	// AllowInstall permits downloading steamcmd from the Valve CDN when it
	// is not found (--allow-install-steamcmd / auto_install_steamcmd).
	// Default false: without it a missing steamcmd is an error.
	AllowInstall bool
	// InstallRoot is where steamcmd gets auto-installed.
	// Default: ~/.local/share/gamefetcher/steamcmd.
	InstallRoot string
	// InstallerURL overrides the Valve CDN archive URL (tests).
	InstallerURL string
	// Credentials for non-anonymous logins; the zero value is anonymous.
	Credentials Credentials
	// ProxyAddr, when set ("127.0.0.1:<port>"), is exported to every
	// steamcmd child as http_proxy so downloads flow through the built-in
	// rate-limiting proxy. Scheme-less on purpose: steamcmd fails to parse
	// a value with http:// (spec section 9).
	ProxyAddr string
	// Stdout receives steamcmd output (both streams, merged); default
	// os.Stdout. What actually gets written is governed by OutputMode.
	Stdout io.Writer
	// Progress receives the tool's own status lines (steamcmd auto-install
	// download progress, Steam Guard notices); default os.Stderr.
	Progress io.Writer
	// OutputMode filters steamcmd output for the user; the default
	// OutputFiltered shows only meaningful lines.
	OutputMode OutputMode
	// Run overrides how steamcmd is executed (tests).
	Run RunFunc
	// WebAPI is the Steam Web API client used by ResolveItems;
	// the zero client (public API) when nil.
	WebAPI *webapi.Client
}

// Provider drives steamcmd.
type Provider struct {
	opts Options
	run  RunFunc
}

var _ provider.Provider = (*Provider)(nil)

func New(opts Options) *Provider {
	p := &Provider{opts: opts, run: opts.Run}
	if p.run == nil {
		p.run = p.execSteamcmd
	}
	return p
}

func (p *Provider) webAPI() *webapi.Client {
	if p.opts.WebAPI != nil {
		return p.opts.WebAPI
	}
	return &webapi.Client{}
}

// ResolveItems expands a source into concrete items (spec.md section 2):
// collections are expanded and every workshop item resolved via the keyless
// Steam Web API. Returned items carry the workshop title and the base/client
// game id (consumer_app_id); InstallDir is the caller's business. Items the
// workshop has no metadata for (hidden/removed) come back bare — empty Name
// and AppID — so the caller can decide instead of them being dropped
// silently.
func (p *Provider) ResolveItems(ctx context.Context, source provider.Source) ([]provider.Item, error) {
	ids := slices.Clone(source.ModIDs)
	for _, c := range source.Collections {
		children, err := p.webAPI().GetCollectionDetails(ctx, c)
		if err != nil {
			return nil, fmt.Errorf("collection %s: %w", c, err)
		}
		ids = append(ids, children...)
	}
	var uniq []string
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return nil, errors.New("empty source: give collection ids or mod ids")
	}
	details, err := p.webAPI().PublishedFileDetails(ctx, uniq)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]webapi.Mod, len(details))
	for _, m := range details {
		byID[m.ID] = m
	}
	items := make([]provider.Item, 0, len(uniq))
	for _, id := range uniq {
		item := provider.Item{ID: id, Kind: provider.KindMod}
		if m, ok := byID[id]; ok {
			item.Name = m.Title
			if m.ConsumerAppID != 0 {
				item.AppID = strconv.Itoa(m.ConsumerAppID)
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// Download installs or updates one item via a dedicated steamcmd run and
// verifies the result on disk instead of trusting the exit code.
func (p *Provider) Download(ctx context.Context, item provider.Item, opts provider.DownloadOptions) error {
	if err := validateItem(item); err != nil {
		return err
	}
	if err := prepareInstallDir(item.InstallDir); err != nil {
		return err
	}
	bin, err := p.steamcmdPath(ctx)
	if err != nil {
		return err
	}
	script, err := buildRunScript(item, p.opts.Credentials, opts)
	if err != nil {
		return err
	}
	scriptPath, cleanup, err := writeRunScript(script)
	if err != nil {
		return err
	}
	defer cleanup()

	out, err := p.run(withRunLabel(ctx, runLabelFor(item)), bin, "+runscript", scriptPath)
	if err != nil {
		return retry.Mark(fmt.Errorf("steamcmd failed for %s: %w", itemRef(item), err), classifyOutput(out))
	}

	// steamcmd is known to exit 0 after writing into the wrong library
	// (spec section 4), so success is what's on disk, not the exit code.
	// The output still gets classified: "No subscription" with exit 0 must
	// not be retried.
	installed, err := p.IsInstalled(ctx, item)
	if err != nil {
		return err
	}
	if !installed {
		return retry.Mark(fmt.Errorf("steamcmd reported success for %s, but %s is missing — the download did not land in %s",
			itemRef(item), verificationTarget(item), item.InstallDir), classifyOutput(out))
	}
	return nil
}

// IsInstalled checks the on-disk artifacts steamcmd leaves behind: the app
// manifest for apps, the workshop content directory for mods.
func (p *Provider) IsInstalled(_ context.Context, item provider.Item) (bool, error) {
	if err := validateItem(item); err != nil {
		return false, err
	}
	if item.Kind == provider.KindApp {
		_, err := os.Stat(manifestPath(item))
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, fs.ErrNotExist):
			return false, nil
		default:
			return false, err
		}
	}
	entries, err := os.ReadDir(workshopDir(item))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

// LocateSteamcmd resolves the steamcmd binary without running it
// (health-check: "steamcmd available").
func (p *Provider) LocateSteamcmd(ctx context.Context) (string, error) {
	return p.steamcmdPath(ctx)
}

// loginProbe runs a bare login/quit script, validating the binary, Steam
// connectivity and the account in one go; returns the combined output.
func (p *Provider) loginProbe(ctx context.Context) (string, error) {
	bin, err := p.steamcmdPath(ctx)
	if err != nil {
		return "", err
	}
	script := "@ShutdownOnFailedCommand 1\n@NoPromptForPassword 1\n" + loginCommand(p.opts.Credentials) + "quit\n"
	scriptPath, cleanup, err := writeRunScript(script)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return p.run(withRunLabel(ctx, "login"), bin, "+runscript", scriptPath)
}

// EnsureLogin verifies a non-anonymous account can log in without prompts
// BEFORE any download work. When Steam Guard wants its one-time code and a
// terminal is available, steamcmd gets the terminal to ask for the code —
// the sentry is cached after that and every later run is non-interactive
// (spec section 10). Anonymous logins are a no-op.
func (p *Provider) EnsureLogin(ctx context.Context) error {
	if p.opts.Credentials.Anonymous() {
		return nil
	}
	out, err := p.loginProbe(ctx)
	if err == nil {
		return nil
	}
	if !needsGuardCode(out) {
		return retry.Mark(fmt.Errorf("steamcmd login failed: %w", err), classifyOutput(out))
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return retry.Mark(errors.New("Steam Guard asks for a one-time code but stdin is not a terminal; run `gamefetcher health-check` once interactively to cache the sentry"), retry.Fatal)
	}
	// The prompt goes straight to stderr: prompts are exempt from
	// --log-level quiet, and hiding it would hang the read on stdin.
	code, err := askGuardCode(p.opts.Credentials.Username, os.Stderr, os.Stdin)
	if err != nil {
		return retry.Mark(err, retry.Fatal)
	}
	return p.guardLogin(ctx, code)
}

var guardCodeRe = regexp.MustCompile(`^[A-Za-z0-9]{4,12}$`)

// askGuardCode prompts for the one-time Steam Guard code ourselves — handing
// the raw terminal to steamcmd floods it with bootstrap noise. For email
// Guard the code exists because the probe login just failed: that rejected
// attempt is what makes Steam send the mail. The code goes into the
// runscript, so its shape is validated.
func askGuardCode(username string, w io.Writer, in io.Reader) (string, error) {
	fmt.Fprintf(w, "Steam Guard: this machine is not authenticated for %s yet.\n", username)
	fmt.Fprintf(w, "Steam has just emailed a one-time code to the account's address (mobile authenticator users: take the current code from the app).\n")
	fmt.Fprintf(w, "Enter the code: ")
	code, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading Steam Guard code: %w", err)
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if !guardCodeRe.MatchString(code) {
		return "", fmt.Errorf("%q does not look like a Steam Guard code", code)
	}
	return code, nil
}

// guardLogin retries the login with the one-time code via steamcmd's
// set_steam_guard_code; on success steamcmd caches the sentry and every
// later run is non-interactive.
func (p *Provider) guardLogin(ctx context.Context, code string) error {
	bin, err := p.steamcmdPath(ctx)
	if err != nil {
		return err
	}
	script := "@ShutdownOnFailedCommand 1\n@NoPromptForPassword 1\n" +
		"set_steam_guard_code " + code + "\n" +
		loginCommand(p.opts.Credentials) + "quit\n"
	scriptPath, cleanup, err := writeRunScript(script)
	if err != nil {
		return err
	}
	defer cleanup()
	out, err := p.run(withRunLabel(ctx, "login"), bin, "+runscript", scriptPath)
	if err != nil {
		return retry.Mark(fmt.Errorf("Steam Guard login failed: %w", err), classifyOutput(out))
	}
	return nil
}

func manifestPath(item provider.Item) string {
	return filepath.Join(item.InstallDir, "steamapps", "appmanifest_"+item.ID+".acf")
}

// ManifestPath exposes the verification artifact location (status/preflight).
func ManifestPath(item provider.Item) string { return manifestPath(item) }

func workshopDir(item provider.Item) string {
	return filepath.Join(item.InstallDir, "steamapps", "workshop", "content", item.AppID, item.ID)
}

func verificationTarget(item provider.Item) string {
	if item.Kind == provider.KindMod {
		return workshopDir(item)
	}
	return manifestPath(item)
}

func itemRef(item provider.Item) string {
	if item.Kind == provider.KindMod {
		return fmt.Sprintf("workshop item %s (app %s)", item.ID, item.AppID)
	}
	return "app " + item.ID
}

// runLabelFor is the short output-line marker for one item's steamcmd run.
func runLabelFor(item provider.Item) string {
	if item.Kind == provider.KindMod {
		return "mod " + item.ID
	}
	return "app " + item.ID
}

var (
	numericID  = regexp.MustCompile(`^[0-9]+$`)
	branchName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// validateItem rejects anything that could corrupt the generated runscript
// or point steamcmd at the wrong place.
func validateItem(item provider.Item) error {
	if item.InstallDir == "" {
		return fmt.Errorf("%s: no install dir", itemRef(item))
	}
	for _, r := range item.InstallDir {
		if r < ' ' || r == '"' {
			return fmt.Errorf("install dir %q contains characters unsafe for a steamcmd script", item.InstallDir)
		}
	}
	if !numericID.MatchString(item.ID) {
		return fmt.Errorf("item id %q: must be a numeric Steam id", item.ID)
	}
	if item.Platform != "" && !validPlatforms[item.Platform] {
		return fmt.Errorf("%s: platform %q is not one of windows/linux/macos", itemRef(item), item.Platform)
	}
	if item.Branch != "" && !branchName.MatchString(item.Branch) {
		return fmt.Errorf("%s: branch %q contains characters unsafe for a steamcmd script", itemRef(item), item.Branch)
	}
	for _, r := range item.BranchPassword {
		if r < ' ' || r == 0x7f {
			return fmt.Errorf("%s: branch password contains control characters", itemRef(item))
		}
	}
	switch item.Kind {
	case provider.KindApp:
	case provider.KindMod:
		if !numericID.MatchString(item.AppID) {
			return fmt.Errorf("mod %s: AppID %q must be the numeric base game app id", item.ID, item.AppID)
		}
	default:
		return fmt.Errorf("unsupported item kind %q", item.Kind)
	}
	return nil
}

// prepareInstallDir pre-creates install_dir and install_dir/steamapps and
// probes both for writability. Without this steamcmd may silently download
// into its default library or die with "Disk write failure" (spec section 4).
// Local filesystem errors never heal by retrying, so they are Fatal — the
// caller's retry loop must not burn attempts on a permission problem.
func prepareInstallDir(dir string) error {
	steamapps := filepath.Join(dir, "steamapps")
	if err := os.MkdirAll(steamapps, 0o755); err != nil {
		return retry.Mark(fmt.Errorf("creating install dir: %w%s", err, permissionHint(err, dir)), retry.Fatal)
	}
	for _, d := range []string{dir, steamapps} {
		probe, err := os.CreateTemp(d, ".gamefetcher-write-check-*")
		if err != nil {
			return retry.Mark(fmt.Errorf("install dir %s is not writable: %w%s", d, err, permissionHint(err, dir)), retry.Fatal)
		}
		probe.Close()
		os.Remove(probe.Name())
	}
	return nil
}

// PrepareInstallDir exposes the install-dir preflight so callers can fail on
// an unwritable directory immediately, before any steamcmd work (appinfo
// queries, platform prompts). Download still re-runs it — it is idempotent.
func PrepareInstallDir(dir string) error { return prepareInstallDir(dir) }

// permissionHint appends a short fix recipe to permission-denied errors on
// the install dir; empty for every other error kind.
func permissionHint(err error, dir string) string {
	if !errors.Is(err, fs.ErrPermission) {
		return ""
	}
	u := "<your-user>"
	if cur, uerr := user.Current(); uerr == nil && cur.Username != "" {
		u = cur.Username
	}
	return fmt.Sprintf("\nfix it as a sudo-capable user, then retry as %s:\n  sudo mkdir -p %s\n  sudo chown %s: %s", u, dir, u, dir)
}

func (p *Provider) execSteamcmd(ctx context.Context, bin string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	if p.opts.ProxyAddr != "" {
		cmd.Env = append(os.Environ(), "http_proxy="+p.opts.ProxyAddr)
	}
	var sink io.Writer
	var flush func()
	switch p.opts.OutputMode {
	case OutputRaw:
		sink = orWriter(p.opts.Stdout, os.Stdout)
	case OutputQuiet:
		sink = io.Discard
	default:
		lf := newLineFilter(orWriter(p.opts.Stdout, os.Stdout), runLabel(ctx))
		sink = lf
		flush = lf.Flush
	}
	// The identical writer on both streams makes exec.Cmd share one pipe, so
	// the classification buffer sees output in order and without data races.
	w := io.MultiWriter(sink, &buf)
	cmd.Stdout = w
	cmd.Stderr = w
	err := cmd.Run()
	if flush != nil {
		flush()
	}
	return buf.String(), err
}

func orWriter(w, def io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return def
}
