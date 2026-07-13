package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/proxy"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
	"github.com/Austrum-lab/game-fetcher-cli/internal/webapi"
)

type checkResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func newHealthCheckCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "health-check",
		Short: "Check the environment: config, network, steamcmd, Steam login, state dir, windows runner",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHealthCheck(cmd, jsonOut)
		},
	}
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "machine-readable output")
	return cmd
}

func runHealthCheck(cmd *cobra.Command, jsonOut bool) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()
	var results []checkResult
	// Checks take seconds (network, steamcmd); each verdict prints as soon
	// as it is known so the command never looks hung. JSON stays one
	// document at the end.
	add := func(name string, ok bool, detail string) {
		results = append(results, checkResult{Name: name, OK: ok, Detail: detail})
		if !jsonOut {
			status := " ok "
			if !ok {
				status = "FAIL"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[%s] %-14s %s\n", status, name, detail)
		}
	}

	// 1. Config loads and is consistent.
	cfg, err := loadConfig(cmd)
	switch {
	case err != nil:
		add("config", false, err.Error())
	case cfg.DownloadRateLimit != "":
		// sync/run would only trip over a bad rate limit at download time;
		// surface it here.
		if _, err := proxy.ParseRate(cfg.DownloadRateLimit); err != nil {
			add("config", false, fmt.Sprintf("download_rate_limit: %v", err))
		} else {
			add("config", true, fmt.Sprintf("%d profile(s), %d group(s), rate limit %s/s", len(cfg.Profiles), len(cfg.Groups), cfg.DownloadRateLimit))
		}
	default:
		add("config", true, fmt.Sprintf("%d profile(s), %d group(s)", len(cfg.Profiles), len(cfg.Groups)))
	}

	// 2. Steam Web API reachable (keyless GetServerInfo).
	if err := (&webapi.Client{}).Ping(ctx); err != nil {
		add("steam web api", false, err.Error())
	} else {
		add("steam web api", true, "reachable")
	}

	if cfg != nil {
		creds, err := resolveCreds(cmd, cfg)
		if err != nil {
			add("steam login", false, err.Error())
		} else {
			prov := steam.New(steam.Options{
				SteamcmdPath: cfg.SteamcmdPath,
				AllowInstall: cfg.AutoInstallSteamcmd,
				Credentials:  creds,
				Stdout:       cmd.ErrOrStderr(),
				OutputMode:   outputMode(cmd),
				Progress:     progressOut(cmd),
			})
			// 3. steamcmd binary resolvable.
			bin, err := prov.LocateSteamcmd(ctx)
			if err != nil {
				add("steamcmd", false, err.Error())
			} else {
				add("steamcmd", true, bin)
				// 4. Login works: validates steamcmd, connectivity and the
				// account in one bare login/quit run.
				// Say where a non-anonymous login came from — an exported
				// GAMEFETCHER_STEAM_USERNAME is easy to forget about.
				account := "anonymous"
				if !creds.Anonymous() {
					account = "user " + creds.Username
					if u, _ := cmd.Flags().GetString("steam-user"); u == "" {
						if os.Getenv("GAMEFETCHER_STEAM_USERNAME") != "" {
							account += " (username from GAMEFETCHER_STEAM_USERNAME)"
						} else {
							account += " (username from config steam_user)"
						}
					}
				}
				if err := prov.EnsureLogin(ctx); err != nil {
					add("steam login", false, err.Error())
				} else {
					add("steam login", true, account)
				}
			}
		}

		// 5. State directory writable.
		statePth, err := statePath(cfg)
		if err != nil {
			add("state", false, err.Error())
		} else if err := probeDir(filepath.Dir(statePth)); err != nil {
			add("state", false, err.Error())
		} else {
			add("state", true, statePth)
		}

		// 6. Windows-build profiles need a runner (umu/wine) on this host.
		if steam.HostOS() != "windows" {
			needRunner := false
			for _, p := range cfg.Profiles {
				if p.Platform == "windows" {
					needRunner = true
				}
			}
			if !needRunner && statePth != "" {
				if store, err := state.Open(statePth); err == nil && len(store.ForcedPlatforms()) > 0 {
					needRunner = true
				}
			}
			if needRunner {
				if path, err := exec.LookPath("umu-run"); err == nil {
					add("windows runner", true, path)
				} else if path, err := exec.LookPath("wine"); err == nil {
					add("windows runner", true, path+" (umu-run recommended: auto-manages Proton)")
				} else {
					add("windows runner", false, "profiles use Windows builds but neither umu-run nor wine is installed (e.g. pipx install umu-launcher)")
				}
			}
		}
	}

	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			OK     bool          `json:"ok"`
			Checks []checkResult `json:"checks"`
		}{failed == 0, results}); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d check(s) failed", failed, len(results))
	}
	return nil
}

func probeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".gamefetcher-health-*")
	if err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return nil
}
