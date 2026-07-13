package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget the cached Steam session (login tokens + Steam Guard sentry) — the next login asks for the code again",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			// AllowInstall stays off: logging out must never install steamcmd.
			prov := steam.New(steam.Options{SteamcmdPath: cfg.SteamcmdPath})
			removed, err := prov.Logout(cmd.Context())
			for _, path := range removed {
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", path)
			}
			if err != nil {
				return err
			}
			if len(removed) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no cached Steam session found — nothing to do")
			}
			return nil
		},
	}
}
