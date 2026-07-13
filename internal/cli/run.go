package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
	"github.com/Austrum-lab/game-fetcher-cli/internal/webapi"
)

func newRunCmd() *cobra.Command {
	var (
		appID       int
		game        string
		dir         string
		mods        []int64
		collections []int64
		baseApp     int
		platform    string
		branch      string
		branchPw    string
		validate    bool
		dryRun      bool
		jsonOut     bool
		saveAs      string
	)
	cmd := &cobra.Command{
		Use:   "run [profile|group ...]",
		Short: "Install or update an app/mods from explicit flags or profiles",
		Long: `Install or update unconditionally (no state-cache diffing — that is sync's
job): either named profiles/groups, or a one-off via explicit flags:

  gamefetcher run --app 258550 --dir /srv/rust --mod 111 --mod 222

--save-as-profile <name> persists the explicit flags as a profile before the
downloads start (a failed run keeps it; the resolved base_app_id is added
after a successful one); overwriting an existing profile shows a diff and
asks for confirmation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd, args, runFlags{
				appID: appID, game: game, dir: dir, mods: mods, collections: collections,
				baseApp: baseApp, platform: platform,
				branch: branch, branchPw: branchPw,
				validate: validate, dryRun: dryRun, jsonOut: jsonOut, saveAs: saveAs,
			})
		},
	}
	cmd.Flags().IntVarP(&appID, "app", "a", 0, "app id to install (explicit mode)")
	cmd.Flags().StringVarP(&game, "game", "g", "", "resolve the app by name instead of --app (interactive picker on a terminal)")
	cmd.Flags().StringVarP(&dir, "dir", "d", "", "install directory (explicit mode)")
	cmd.Flags().Int64SliceVarP(&mods, "mod", "m", nil, "workshop mod id (repeatable)")
	cmd.Flags().Int64SliceVarP(&collections, "collection", "C", nil, "workshop collection id, expanded into its items live (repeatable)")
	cmd.Flags().IntVarP(&baseApp, "base-app", "B", 0, "base/client game app id for mods (default: resolved from workshop metadata)")
	cmd.Flags().StringVarP(&platform, "platform", "p", "", "force the build platform (windows/linux/macos); default: native, offering Windows when nothing else exists")
	cmd.Flags().StringVarP(&branch, "branch", "b", "", "beta branch to install (steamcmd -beta)")
	cmd.Flags().StringVar(&branchPw, "branch-password", "", "password for a protected beta branch (open betas need none)")
	cmd.Flags().BoolVarP(&validate, "validate", "v", false, "make steamcmd validate installed files (apps and mods)")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print the plan without launching steamcmd")
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "machine-readable output")
	cmd.Flags().StringVarP(&saveAs, "save-as-profile", "s", "", "persist the explicit flags as a named profile (saved before the run; enriched after)")
	return cmd
}

type runFlags struct {
	appID       int
	game        string
	dir         string
	mods        []int64
	collections []int64
	baseApp     int
	platform    string
	branch      string
	branchPw    string
	validate    bool
	dryRun      bool
	jsonOut     bool
	saveAs      string
}

func runRun(cmd *cobra.Command, args []string, f runFlags) error {
	ctx := cmd.Context()
	logf := func(format string, a ...any) { fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...) }

	explicit := f.appID != 0 || f.game != "" || f.dir != "" || len(f.mods) > 0 || len(f.collections) > 0 ||
		f.baseApp != 0 || f.platform != "" || f.branch != "" || f.branchPw != ""
	if explicit && len(args) > 0 {
		return errors.New("use either profile names or explicit flags (--app/--dir), not both")
	}
	if !explicit && len(args) == 0 {
		return errors.New("nothing to run: pass profile/group names or --app and --dir")
	}
	if f.saveAs != "" && !explicit {
		return errors.New("--save-as-profile only makes sense with explicit flags (--app/--dir)")
	}

	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	var profiles map[string]config.Profile
	var manual config.Profile
	if explicit {
		if f.appID != 0 && f.game != "" {
			return errors.New("--app and --game are mutually exclusive")
		}
		// Check --dir before the --game picker: nobody should pick a game
		// interactively only to be told a flag is missing.
		if f.dir == "" {
			return errors.New("explicit mode needs --app (or --game) and --dir")
		}
		if f.game != "" {
			f.appID, err = resolveGameID(cmd, cfg, f.game)
			if err != nil {
				return err
			}
		}
		if f.appID == 0 || f.dir == "" {
			return errors.New("explicit mode needs --app (or --game) and --dir")
		}
		manual = config.Profile{
			AppID: f.appID, BaseAppID: f.baseApp, InstallDir: f.dir,
			Mods: f.mods, Collections: f.collections,
			Platform: f.platform, Branch: f.branch, BranchPassword: f.branchPw,
		}
		name := f.saveAs
		if name == "" {
			name = "manual"
		}
		profiles = map[string]config.Profile{name: manual}
	} else {
		profiles, err = selectProfiles(cfg, args)
		if err != nil {
			return err
		}
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
		return errors.New("nothing to run: no items resolved")
	}
	if f.dryRun {
		return emitPlan(cmd, items, nil, f.jsonOut)
	}

	// The profile is persisted BEFORE the downloads (owner's decision): a
	// failed run — bad login, network — must not lose the profile. It gets
	// enriched with resolved fields after a successful run below.
	var cfgPath string
	if f.saveAs != "" {
		cfgPath = writeConfigPath(cmd)
		if err := saveProfile(cfgPath, f.saveAs, manual, askConfirm(cmd), cmd.ErrOrStderr()); err != nil {
			return fmt.Errorf("saving profile %q: %w", f.saveAs, err)
		}
	}

	// run installs unconditionally, but still records time_updated so the
	// next sync has a coherent cache.
	remote, err := remoteTimes(ctx, client, webPolicy, items)
	if err != nil {
		return err
	}
	results, err := executeDownloads(cmd, cfg, store, logf, items, remote, f.validate)
	if err != nil {
		return err
	}
	if f.jsonOut {
		if err := emitReport(cmd, results, 0); err != nil {
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
		return fmt.Errorf("%d of %d item(s) failed", failed, len(results))
	}

	if f.saveAs != "" && manual.BaseAppID == 0 {
		// Enrich the just-saved profile with what the run resolved (base
		// game id from workshop metadata) — no re-confirmation for our own
		// seconds-old profile.
		if link, ok := store.AppLink(strconv.Itoa(manual.AppID)); ok {
			if base, _ := strconv.Atoi(link.BaseAppID); base != 0 {
				manual.BaseAppID = base
				yes := func(string) (bool, error) { return true, nil }
				if err := saveProfile(cfgPath, f.saveAs, manual, yes, cmd.ErrOrStderr()); err != nil {
					return fmt.Errorf("updating profile %q: %w", f.saveAs, err)
				}
			}
		}
	}
	return nil
}
