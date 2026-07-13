package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Austrum-lab/game-fetcher-cli/internal/applist"
	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
	"github.com/Austrum-lab/game-fetcher-cli/internal/picker"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

func newSearchCmd() *cobra.Command {
	var (
		collection string
		jsonOut    bool
		pick       bool
		limit      int
	)
	cmd := &cobra.Command{
		Use:   "search [flags] <query>...",
		Short: "Search games/servers by name, or expand a workshop collection",
		Long: `Search games and dedicated servers by name — an offline fuzzy match over the
local app index (see app_list_urls), printed as "appid<TAB>name" candidates.
No API keys involved. The interactive picker lives where a choice is acted
on: run --game.

Workshop mods are picked by id: either straight in the profile config, or via
--collection <id>, which expands a workshop collection (keyless Steam API)
into "id<TAB>title" lines ready for a profile's mods: list. Add --pick for an
interactive multi-select when you only want a subset. Mod search by free text
is deliberately not supported.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			if collection != "" {
				if query != "" {
					return errors.New("--collection does not take a query")
				}
				// --limit truncates a collection only when asked explicitly:
				// the default is meant for name search, and silently cutting
				// a collection would lose mods.
				if !cmd.Flags().Changed("limit") {
					limit = 0
				}
				return searchCollection(cmd, collection, jsonOut, pick, limit)
			}
			if query == "" {
				return errors.New("a search query is required (or --collection <id>)")
			}
			return searchGames(cmd, query, jsonOut, limit)
		},
	}
	cmd.Flags().StringVarP(&collection, "collection", "C", "", "expand a workshop collection id into its items (keyless)")
	cmd.Flags().BoolVarP(&pick, "pick", "p", false, "with --collection: interactive multi-select instead of listing everything (needs a terminal; ignored with --json)")
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "print candidates as JSON instead of the interactive picker")
	cmd.Flags().IntVarP(&limit, "limit", "l", 20, "maximum number of candidates")
	return cmd
}

// loadIndex returns the local app index, refreshing it from the open
// databases when stale.
func loadIndex(ctx context.Context, cmd *cobra.Command, cfg *config.Config) (*applist.Index, error) {
	logf := func(format string, a ...any) { fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...) }
	cachePath, err := appListPath(cfg)
	if err != nil {
		return nil, err
	}
	policy := policyFrom(cfg.Retry.WebAPI, retry.DefaultWebAPIPolicy())
	urls := cfg.AppListURLs
	if len(urls) == 0 {
		urls = applist.DefaultSources
	}
	fetch := func(ctx context.Context) ([]applist.App, error) {
		var apps []applist.App
		err := policy.Do(ctx, func(ctx context.Context) error {
			var err error
			apps, err = applist.FetchOpen(ctx, nil, urls, logf)
			return err
		})
		return apps, err
	}
	idx, err := applist.Ensure(ctx, cachePath, time.Duration(cfg.AppListMaxAge), fetch)
	if err != nil {
		if idx == nil || len(idx.Apps) == 0 {
			return idx, err
		}
		logf("warning: %v; using the existing local index", err)
	}
	return idx, nil
}

// resolveGameID turns a name query into an app id for `run --game`: the
// interactive picker on a terminal, an unambiguous single match otherwise
// (spec section 7 UX — the picker belongs to the launch flow too).
func resolveGameID(cmd *cobra.Command, cfg *config.Config, query string) (int, error) {
	idx, err := loadIndex(cmd.Context(), cmd, cfg)
	if err != nil {
		return 0, err
	}
	candidates := applist.Match(idx.Apps, query, 20)
	if len(candidates) == 0 {
		return 0, fmt.Errorf("no games found for %q", query)
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		options := make([]picker.Option, len(candidates))
		for i, c := range candidates {
			options[i] = picker.Option{Label: gameLabel(c), Value: strconv.Itoa(c.AppID)}
		}
		choice, err := picker.Pick(os.Stdin, cmd.ErrOrStderr(), fmt.Sprintf("Select a game/server for %q:", query), options)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(choice.Value)
	}
	// Without a terminal: a single candidate or an exact name match wins.
	if len(candidates) == 1 || strings.EqualFold(candidates[0].Name, strings.TrimSpace(query)) {
		fmt.Fprintf(cmd.ErrOrStderr(), "resolved %q to %s\n", query, gameLabel(candidates[0]))
		return candidates[0].AppID, nil
	}
	var names []string
	for _, c := range candidates[:min(5, len(candidates))] {
		names = append(names, gameLabel(c))
	}
	return 0, fmt.Errorf("%q is ambiguous without a terminal: %s — pass --app <id> instead", query, strings.Join(names, ", "))
}

// searchGames lists matching candidates — search only informs; the picker
// lives where the choice is acted on (run --game).
func searchGames(cmd *cobra.Command, query string, jsonOut bool, limit int) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	idx, err := loadIndex(cmd.Context(), cmd, cfg)
	if err != nil {
		return err
	}
	candidates := applist.Match(idx.Apps, query, limit)
	if len(candidates) == 0 {
		return fmt.Errorf("no games found for %q", query)
	}
	return printGameCandidates(cmd, candidates, jsonOut)
}

// searchCollection expands a workshop collection through the provider
// (spec section 2: ResolveItems) and lists its items; with --pick an
// interactive multi-select filters them first (for composing a profile's
// mods: list out of a subset). The JSON output includes each item's app_id —
// the base game id a profile needs for mods.
func searchCollection(cmd *cobra.Command, collectionID string, jsonOut, pick bool, limit int) error {
	ctx := cmd.Context()
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	resolver := steam.New(steam.Options{})
	policy := policyFrom(cfg.Retry.WebAPI, retry.DefaultWebAPIPolicy())

	var items []provider.Item
	err = policy.Do(ctx, func(ctx context.Context) error {
		var err error
		items, err = resolver.ResolveItems(ctx, provider.Source{Collections: []string{collectionID}})
		return err
	})
	if err != nil {
		return err
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	if !pick || jsonOut || !term.IsTerminal(int(os.Stdin.Fd())) {
		return printModCandidates(cmd, items, jsonOut)
	}
	options := make([]picker.Option, len(items))
	for i, it := range items {
		label := it.Name
		if label == "" {
			label = "(no workshop metadata)"
		}
		options[i] = picker.Option{Label: fmt.Sprintf("%s (%s)", label, it.ID), Value: it.ID}
	}
	chosen, err := picker.PickMulti(os.Stdin, cmd.ErrOrStderr(),
		fmt.Sprintf("Collection %s — select mods:", collectionID), options)
	if err != nil {
		return err
	}
	for _, c := range chosen {
		fmt.Fprintln(cmd.OutOrStdout(), c.Value+"\t"+c.Label)
	}
	return nil
}

func gameLabel(a applist.App) string {
	return fmt.Sprintf("%s (%d)", a.Name, a.AppID)
}

func printGameCandidates(cmd *cobra.Command, candidates []applist.App, jsonOut bool) error {
	out := cmd.OutOrStdout()
	if !jsonOut {
		for _, c := range candidates {
			fmt.Fprintf(out, "%d\t%s\n", c.AppID, c.Name)
		}
		return nil
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(candidates)
}

func printModCandidates(cmd *cobra.Command, items []provider.Item, jsonOut bool) error {
	out := cmd.OutOrStdout()
	if !jsonOut {
		for _, it := range items {
			fmt.Fprintf(out, "%s\t%s\n", it.ID, it.Name)
		}
		return nil
	}
	type modJSON struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		AppID string `json:"app_id"` // base/client game the mod belongs to
	}
	list := make([]modJSON, len(items))
	for i, it := range items {
		list[i] = modJSON{it.ID, it.Name, it.AppID}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

// appListPath keeps the app index next to the state file.
func appListPath(cfg *config.Config) (string, error) {
	path, err := statePath(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "applist.json"), nil
}
