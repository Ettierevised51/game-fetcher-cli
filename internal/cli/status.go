package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
)

type statusRow struct {
	Profile     string     `json:"profile"`
	AppID       int        `json:"app_id"`
	Platform    string     `json:"platform"`
	InstallDir  string     `json:"install_dir"`
	Installed   bool       `json:"installed"`
	Mods        int        `json:"mods"`
	Collections int        `json:"collections"`
	Branch      string     `json:"branch,omitempty"`
	LastSync    *time.Time `json:"last_sync,omitempty"`
	LastSuccess *bool      `json:"last_success,omitempty"`
}

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status [profile|group ...]",
		Short: "Show per-profile install and sync state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, args, jsonOut)
		},
	}
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "machine-readable output")
	return cmd
}

func runStatus(cmd *cobra.Command, args []string, jsonOut bool) error {
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

	var rows []statusRow
	for _, name := range slices.Sorted(maps.Keys(profiles)) {
		p := profiles[name]
		appItem := provider.Item{ID: strconv.Itoa(p.AppID), Kind: provider.KindApp, InstallDir: p.InstallDir}
		row := statusRow{
			Profile:     name,
			AppID:       p.AppID,
			InstallDir:  p.InstallDir,
			Mods:        len(p.Mods),
			Collections: len(p.Collections),
			Branch:      p.Branch,
			Platform:    "native",
		}
		if p.Platform != "" {
			row.Platform = p.Platform
		} else if cached, ok := store.Platform(appItem.ID); ok && cached != "native" {
			row.Platform = cached + " (remembered)"
		}
		if _, err := os.Stat(steam.ManifestPath(appItem)); err == nil {
			row.Installed = true
		}
		if st, ok := store.Item(appItem.Key()); ok {
			if !st.LastDownloadedAt.IsZero() {
				t := st.LastDownloadedAt
				row.LastSync = &t
			}
			ok := st.LastSuccess
			row.LastSuccess = &ok
		}
		rows = append(rows, row)
	}

	out := cmd.OutOrStdout()
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PROFILE\tAPP\tPLATFORM\tINSTALLED\tMODS\tLAST SYNC\tDIR")
	for _, r := range rows {
		installed := "no"
		if r.Installed {
			installed = "yes"
		}
		lastSync := "-"
		if r.LastSync != nil {
			lastSync = r.LastSync.Format("2006-01-02 15:04")
			if r.LastSuccess != nil && !*r.LastSuccess {
				lastSync += " (FAILED)"
			}
		}
		mods := strconv.Itoa(r.Mods)
		if r.Collections > 0 {
			mods += fmt.Sprintf("+%dcoll", r.Collections)
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			r.Profile, r.AppID, r.Platform, installed, mods, lastSync, r.InstallDir)
	}
	return w.Flush()
}
