package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
	"github.com/Austrum-lab/game-fetcher-cli/internal/engine"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/proxy"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
	"github.com/Austrum-lab/game-fetcher-cli/internal/webapi"
)

func newSyncCmd() *cobra.Command {
	var forceAll, recheck, dryRun, jsonOut bool
	cmd := &cobra.Command{
		Use:   "sync [profile|group ...]",
		Short: "Update only new/changed items (and past failures) using the state cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd, args, forceAll || recheck, dryRun, jsonOut)
		},
	}
	cmd.Flags().BoolVarP(&forceAll, "force-all", "f", false, "re-run every item, ignoring the state cache")
	cmd.Flags().BoolVar(&recheck, "recheck", false, "alias for --force-all")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print the plan without launching steamcmd")
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "machine-readable output")
	return cmd
}

func runSync(cmd *cobra.Command, args []string, force, dryRun, jsonOut bool) error {
	ctx := cmd.Context()
	logf := func(format string, a ...any) { fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...) }

	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	profiles, err := selectProfiles(cfg, args)
	if err != nil {
		return err
	}
	path, err := statePath(cfg)
	if err != nil {
		return err
	}
	store, err := state.Open(path)
	if err != nil {
		return err
	}
	client := &webapi.Client{}
	resolver := steam.New(steam.Options{WebAPI: client})
	webPolicy := policyFrom(cfg.Retry.WebAPI, retry.DefaultWebAPIPolicy())

	items, err := itemsFromProfiles(ctx, store, resolver, webPolicy, profiles, logf)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errors.New("nothing to sync: no profiles selected")
	}

	remote, err := remoteTimes(ctx, client, webPolicy, items)
	if err != nil {
		return err
	}

	runItems, skipItems := engine.Plan(items, remote, store, force)
	if outputMode(cmd) != steam.OutputQuiet {
		logf("sync: %d item(s) to update, %d up to date", len(runItems), len(skipItems))
	}
	if dryRun {
		return emitPlan(cmd, runItems, skipItems, jsonOut)
	}
	if len(runItems) == 0 {
		if jsonOut {
			return emitReport(cmd, nil, len(skipItems))
		}
		return nil
	}

	results, err := executeDownloads(cmd, cfg, store, logf, runItems, remote, false)
	if err != nil {
		return err
	}
	if jsonOut {
		if err := emitReport(cmd, results, len(skipItems)); err != nil {
			return err
		}
	}
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d item(s) failed; they will be retried automatically on the next sync", failed, len(results))
	}
	return nil
}

// executeDownloads runs the worker pool over items (apps before workshop
// batches, spec section 4), optionally behind the rate-limit proxy, and
// records every outcome in the state store.
func executeDownloads(cmd *cobra.Command, cfg *config.Config, store *state.Store, logf func(string, ...any), items []provider.Item, remote map[string]int64, validate bool) ([]engine.Result, error) {
	ctx := cmd.Context()
	// Informational lines respect --log-level quiet; warnings and failures
	// (logf) always show.
	infof := logf
	if outputMode(cmd) == steam.OutputQuiet {
		infof = func(string, ...any) {}
	}
	// Unwritable install dirs must fail right away, not after appinfo
	// queries and N retry attempts (issues.txt #2).
	seenDirs := map[string]bool{}
	for _, it := range items {
		if seenDirs[it.InstallDir] {
			continue
		}
		seenDirs[it.InstallDir] = true
		if err := steam.PrepareInstallDir(it.InstallDir); err != nil {
			return nil, err
		}
	}
	creds, err := resolveCreds(cmd, cfg)
	if err != nil {
		return nil, err
	}
	provOpts := steam.Options{
		SteamcmdPath: cfg.SteamcmdPath,
		AllowInstall: cfg.AutoInstallSteamcmd,
		Credentials:  creds,
		OutputMode:   outputMode(cmd),
		// steamcmd progress is diagnostics; stdout must stay clean for the
		// command's own output (--json especially).
		Stdout:   cmd.ErrOrStderr(),
		Progress: progressOut(cmd),
	}
	// One shared proxy limits the summary rate across all workers
	// (spec section 9).
	if cfg.DownloadRateLimit != "" {
		rate, err := proxy.ParseRate(cfg.DownloadRateLimit)
		if err != nil {
			return nil, fmt.Errorf("download_rate_limit: %w", err)
		}
		limiter, err := proxy.Listen(rate)
		if err != nil {
			return nil, err
		}
		defer func() {
			infof("%d byte(s) of network traffic through the rate-limit proxy", limiter.Transferred())
			limiter.Close()
		}()
		provOpts.ProxyAddr = limiter.Addr()
		infof("download rate limited to %s/s via local proxy %s", cfg.DownloadRateLimit, limiter.Addr())
	}
	prov := steam.New(provOpts)

	// A non-anonymous login is verified up front: Steam Guard's one-time
	// code gets its interactive prompt here, before any download work.
	if err := prov.EnsureLogin(ctx); err != nil {
		return nil, err
	}

	// Apps without a pinned platform: prefer the native build, offer the
	// Windows one when nothing else exists. The appinfo memo is shared with
	// the disk preflight so steamcmd is asked at most once per app.
	infos := map[string]*steam.AppInfo{}
	if err := resolvePlatforms(ctx, store, prov, infos, items, askConfirm(cmd), infof); err != nil {
		return nil, err
	}
	preflightDisk(ctx, prov, infos, items, logf)

	dl := policyFrom(cfg.Retry.Download, retry.DefaultDownloadPolicy())
	login := policyFrom(cfg.Retry.Login, retry.DefaultLoginPolicy())
	// Login happens inside every steamcmd run, so the login rate-limit delay
	// governs rate-limited download attempts too.
	dl.RateLimitDelay = login.RateLimitDelay

	opts := provider.DownloadOptions{Validate: validate}
	do := func(ctx context.Context, item provider.Item) error {
		return dl.Do(ctx, func(ctx context.Context) error {
			return prov.Download(ctx, item, opts)
		})
	}

	// The pool warns about the non-anon downgrade on every call; once is enough.
	var warnOnce sync.Once
	warnf := func(format string, a ...any) { warnOnce.Do(func() { logf(format, a...) }) }

	// Apps go one steamcmd process each (app_update before workshop batches,
	// spec section 4); mods are grouped into shared processes (issues.txt:
	// one process per mod floods the log and the local Steam bootstrap).
	apps, mods := splitByKind(items)
	results := engine.Run(ctx, apps, cfg.Parallelism, creds.Anonymous(), warnf, do)
	doBatch := func(ctx context.Context, batch []provider.Item) []engine.Result {
		return downloadModBatch(ctx, prov, dl, batch, opts)
	}
	results = append(results, engine.RunBatches(ctx, modBatches(mods, cfg.Parallelism), cfg.Parallelism, creds.Anonymous(), warnf, doBatch)...)

	now := time.Now()
	for _, r := range results {
		key := r.Item.Key()
		st, _ := store.Item(key)
		st.ID = r.Item.ID
		if r.Item.Name != "" {
			st.Name = r.Item.Name
		}
		st.LastSuccess = r.Err == nil
		if t, ok := remote[key]; ok {
			st.LastTimeUpdated = t
		}
		if r.Err == nil {
			st.LastDownloadedAt = now
		} else {
			logf("failed: %s: %v", key, r.Err)
		}
		store.SetItem(key, st)
	}
	if err := store.Save(); err != nil {
		return nil, err
	}
	return results, nil
}

type planItemJSON struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	AppID      string `json:"app_id,omitempty"`
	InstallDir string `json:"install_dir"`
	Name       string `json:"name,omitempty"`
}

func planItems(items []provider.Item) []planItemJSON {
	out := make([]planItemJSON, len(items))
	for i, it := range items {
		out[i] = planItemJSON{
			Key: it.Key(), Kind: string(it.Kind), ID: it.ID,
			AppID: it.AppID, InstallDir: it.InstallDir, Name: it.Name,
		}
	}
	return out
}

// emitPlan prints what would be downloaded (--dry-run).
func emitPlan(cmd *cobra.Command, runItems, skipItems []provider.Item, jsonOut bool) error {
	out := cmd.OutOrStdout()
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Download []planItemJSON `json:"download"`
			Skip     []planItemJSON `json:"skip"`
		}{planItems(runItems), planItems(skipItems)})
	}
	for _, it := range runItems {
		fmt.Fprintf(out, "download\t%s %s\t-> %s\n", it.Kind, it.ID, it.InstallDir)
	}
	for _, it := range skipItems {
		fmt.Fprintf(out, "skip\t%s %s\t(up to date)\n", it.Kind, it.ID)
	}
	return nil
}

// emitReport prints the machine-readable outcome (--json).
func emitReport(cmd *cobra.Command, results []engine.Result, skipped int) error {
	type failureJSON struct {
		Key   string `json:"key"`
		Error string `json:"error"`
	}
	report := struct {
		Updated []string      `json:"updated"`
		Skipped int           `json:"skipped"`
		Failed  []failureJSON `json:"failed"`
	}{Updated: []string{}, Skipped: skipped, Failed: []failureJSON{}}
	for _, r := range results {
		if r.Err == nil {
			report.Updated = append(report.Updated, r.Item.Key())
		} else {
			report.Failed = append(report.Failed, failureJSON{Key: r.Item.Key(), Error: r.Err.Error()})
		}
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// selectProfiles expands the positional args (profile or group names) into
// profiles; no args means every configured profile.
func selectProfiles(cfg *config.Config, names []string) (map[string]config.Profile, error) {
	if len(names) == 0 {
		return cfg.Profiles, nil
	}
	out := map[string]config.Profile{}
	for _, name := range names {
		if members, ok := cfg.Groups[name]; ok {
			// Group members are validated against profiles at config load.
			for _, m := range members {
				out[m] = cfg.Profiles[m]
			}
			continue
		}
		if p, ok := cfg.Profiles[name]; ok {
			out[name] = p
			continue
		}
		return nil, fmt.Errorf("unknown profile or group %q", name)
	}
	return out, nil
}

// itemsFromProfiles expands profiles into download items. Explicit mod ids
// and collections go through Provider.ResolveItems (spec section 2); a
// collection is re-expanded on every run so additions flow in automatically.
// The base/client game id comes from base_app_id, the cached AppLink, or the
// mods' own consumer_app_id — a dedicated-server app_id never silently leaks
// into workshop downloads.
func itemsFromProfiles(ctx context.Context, store *state.Store, resolver provider.Provider, policy retry.Policy, profiles map[string]config.Profile, logf func(string, ...any)) ([]provider.Item, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var items []provider.Item
	linksDirty := false
	for _, name := range slices.Sorted(maps.Keys(profiles)) {
		p := profiles[name]
		if p.AppID <= 0 {
			return nil, fmt.Errorf("profile %q: app_id is required", name)
		}
		if p.InstallDir == "" {
			return nil, fmt.Errorf("profile %q: install_dir is required", name)
		}
		if p.Platform != "" && p.Platform != "windows" && p.Platform != "linux" && p.Platform != "macos" {
			return nil, fmt.Errorf("profile %q: platform %q is not one of windows/linux/macos", name, p.Platform)
		}
		items = append(items, provider.Item{
			ID:             strconv.Itoa(p.AppID),
			Name:           name,
			Kind:           provider.KindApp,
			InstallDir:     p.InstallDir,
			Platform:       p.Platform,
			Branch:         p.Branch,
			BranchPassword: p.BranchPassword,
		})
		if len(p.Mods) == 0 && len(p.Collections) == 0 {
			continue
		}

		base := p.BaseAppID
		if base == 0 {
			if link, ok := store.AppLink(strconv.Itoa(p.AppID)); ok {
				base, _ = strconv.Atoi(link.BaseAppID)
			}
		}

		if len(p.Collections) == 0 && base != 0 {
			// Cheap offline path: explicit ids with a known base game.
			for _, mod := range p.Mods {
				items = append(items, provider.Item{
					ID:         strconv.FormatInt(mod, 10),
					Kind:       provider.KindMod,
					AppID:      strconv.Itoa(base),
					InstallDir: p.InstallDir,
				})
			}
			continue
		}

		src := provider.Source{ModIDs: int64Strings(p.Mods), Collections: int64Strings(p.Collections)}
		var resolved []provider.Item
		err := policy.Do(ctx, func(ctx context.Context) error {
			var err error
			resolved, err = resolver.ResolveItems(ctx, src)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("profile %q: resolving mods: %w", name, err)
		}
		if base == 0 {
			for _, it := range resolved {
				if it.AppID != "" {
					base, _ = strconv.Atoi(it.AppID)
					break
				}
			}
			if base == 0 {
				return nil, fmt.Errorf("profile %q: cannot determine the base game for its mods; set base_app_id explicitly", name)
			}
			logf("profile %q: base game for mods resolved as %d (from workshop metadata)", name, base)
			store.SetAppLink(state.AppLink{
				ServerAppID: strconv.Itoa(p.AppID),
				BaseAppID:   strconv.Itoa(base),
				Name:        name,
			})
			linksDirty = true
		}
		for _, it := range resolved {
			it.InstallDir = p.InstallDir
			if p.BaseAppID != 0 {
				it.AppID = strconv.Itoa(p.BaseAppID)
			} else if it.AppID == "" {
				logf("warning: mod %s has no workshop metadata (hidden or removed?); assuming base game %d", it.ID, base)
				it.AppID = strconv.Itoa(base)
			}
			items = append(items, it)
		}
	}
	if linksDirty {
		if err := store.Save(); err != nil {
			return nil, err
		}
	}
	// ResolveItems dedups mods vs collections within one profile; this pass
	// also catches duplicates in a plain mods: list and identical items from
	// different profiles sharing an install dir.
	seen := map[string]bool{}
	uniq := items[:0]
	for _, it := range items {
		if key := it.Key(); !seen[key] {
			seen[key] = true
			uniq = append(uniq, it)
		}
	}
	return uniq, nil
}

func int64Strings(v []int64) []string {
	out := make([]string, len(v))
	for i, n := range v {
		out[i] = strconv.FormatInt(n, 10)
	}
	return out
}

// modBatches groups mods by install dir (one force_install_dir per steamcmd
// script) and splits every group evenly across the workers, so a whole batch
// is served by a single steamcmd process.
func modBatches(mods []provider.Item, workers int) [][]provider.Item {
	if workers < 1 {
		workers = 1
	}
	byDir := map[string][]provider.Item{}
	var dirs []string
	for _, m := range mods {
		if _, ok := byDir[m.InstallDir]; !ok {
			dirs = append(dirs, m.InstallDir)
		}
		byDir[m.InstallDir] = append(byDir[m.InstallDir], m)
	}
	var batches [][]provider.Item
	for _, dir := range dirs {
		group := byDir[dir]
		size := (len(group) + workers - 1) / workers
		for start := 0; start < len(group); start += size {
			batches = append(batches, group[start:min(start+size, len(group))])
		}
	}
	return batches
}

// downloadModBatch drives one batch through the retry policy: every attempt
// re-runs only the items that failed retryably, keeping the last per-item
// error. Fatal items ("No subscription") drop out immediately.
func downloadModBatch(ctx context.Context, prov provider.Provider, policy retry.Policy, batch []provider.Item, opts provider.DownloadOptions) []engine.Result {
	lastErr := map[string]error{}
	remaining := batch
	_ = policy.Do(ctx, func(ctx context.Context) error {
		errs := prov.DownloadBatch(ctx, remaining, opts)
		var next []provider.Item
		class := retry.Retryable
		for i, err := range errs {
			lastErr[remaining[i].Key()] = err
			if err == nil || retry.ClassOf(err) == retry.Fatal {
				continue
			}
			if retry.ClassOf(err) == retry.RateLimited {
				class = retry.RateLimited
			}
			next = append(next, remaining[i])
		}
		remaining = next
		if len(next) > 0 {
			return retry.Mark(fmt.Errorf("%d workshop item(s) still failing", len(next)), class)
		}
		return nil
	})
	results := make([]engine.Result, len(batch))
	for i, it := range batch {
		results[i] = engine.Result{Item: it, Err: lastErr[it.Key()]}
	}
	return results
}

func splitByKind(items []provider.Item) (apps, mods []provider.Item) {
	for _, it := range items {
		if it.Kind == provider.KindApp {
			apps = append(apps, it)
		} else {
			mods = append(mods, it)
		}
	}
	return apps, mods
}

// remoteTimes resolves time_updated for all workshop items via the Web API,
// keyed by item key. Apps have no cheap remote time and stay absent.
func remoteTimes(ctx context.Context, client *webapi.Client, policy retry.Policy, items []provider.Item) (map[string]int64, error) {
	var ids []string
	seen := map[string]bool{}
	for _, it := range items {
		if it.Kind == provider.KindMod && !seen[it.ID] {
			ids = append(ids, it.ID)
			seen[it.ID] = true
		}
	}
	out := map[string]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	var byID map[string]int64
	err := policy.Do(ctx, func(ctx context.Context) error {
		var err error
		byID, err = client.TimesUpdated(ctx, ids)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("resolving time_updated: %w", err)
	}
	for _, it := range items {
		if it.Kind != provider.KindMod {
			continue
		}
		if t, ok := byID[it.ID]; ok {
			out[it.Key()] = t
		}
	}
	return out, nil
}

func policyFrom(b config.Backoff, def retry.Policy) retry.Policy {
	if b.MaxAttempts > 0 {
		def.MaxAttempts = b.MaxAttempts
	}
	if b.BaseDelay > 0 {
		def.BaseDelay = time.Duration(b.BaseDelay)
	}
	if b.MaxDelay > 0 {
		def.MaxDelay = time.Duration(b.MaxDelay)
	}
	if b.RateLimitDelay > 0 {
		def.RateLimitDelay = time.Duration(b.RateLimitDelay)
	}
	return def
}

func statePath(cfg *config.Config) (string, error) {
	if cfg.StatePath != "" {
		return cfg.StatePath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "gamefetcher", "state.json"), nil
}
