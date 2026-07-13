// Package cli wires up the gamefetcher command tree.
package cli

import (
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
)

// version is stamped by the release build via
// -ldflags "-X .../internal/cli.version=v1.2.3".
var version = "dev"

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "gamefetcher",
		Version: version,
		Short:   "Install and update game servers and mods via Steam (steamcmd under the hood)",
		// Runtime errors (download failures, bad profiles) should not drag
		// the usage text along with them.
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			switch level, _ := cmd.Flags().GetString("log-level"); level {
			case "quiet", "normal", "verbose":
				return nil
			default:
				return errors.New("--log-level must be quiet, normal or verbose")
			}
		},
	}

	cmd.PersistentFlags().StringP("config", "c", "", "explicit config file (replaces ./gamefetcher.yaml)")
	cmd.PersistentFlags().Bool("allow-install-steamcmd", false, "allow downloading steamcmd from the Valve CDN when it is not installed")
	cmd.PersistentFlags().StringP("log-level", "L", "normal", "quiet (errors and prompts only) | normal (filtered steamcmd output) | verbose (everything, raw)")
	cmd.PersistentFlags().StringP("steam-user", "U", "", "Steam account for non-anonymous downloads (env: GAMEFETCHER_STEAM_USERNAME)")
	cmd.PersistentFlags().StringP("steam-password", "P", "", "Steam password; omit to be asked interactively (env: GAMEFETCHER_STEAM_PASSWORD)")

	cmd.AddCommand(
		newRunCmd(),
		newSyncCmd(),
		newSearchCmd(),
		newHealthCheckCmd(),
		newStatusCmd(),
		newSystemdGenCmd(),
		newLogoutCmd(),
	)

	return cmd
}

// Execute runs the root command and returns its error, if any.
func Execute() error {
	return newRootCmd().Execute()
}

// resolveCreds resolves the Steam login for a command: --steam-user /
// --steam-password flags, then the environment, then the config's
// steam_user key, then an interactive hidden prompt. "anonymous" as the
// username forces the anonymous login past env and config.
func resolveCreds(cmd *cobra.Command, cfg *config.Config) (steam.Credentials, error) {
	user, _ := cmd.Flags().GetString("steam-user")
	pass, _ := cmd.Flags().GetString("steam-password")
	source := ""
	if user == "" && os.Getenv("GAMEFETCHER_STEAM_USERNAME") == "" && cfg != nil {
		user = cfg.SteamUser
		source = "config steam_user"
	}
	return steam.ResolveCredentials(user, pass, source, nil, nil)
}

// progressOut receives the tool's own status lines (steamcmd auto-install
// progress, Steam Guard notices); silenced by --log-level quiet.
func progressOut(cmd *cobra.Command) io.Writer {
	if outputMode(cmd) == steam.OutputQuiet {
		return io.Discard
	}
	return cmd.ErrOrStderr()
}

// outputMode maps --log-level onto how much steamcmd output the user sees;
// "normal" filters it down to the meaningful lines.
func outputMode(cmd *cobra.Command) steam.OutputMode {
	switch level, _ := cmd.Flags().GetString("log-level"); level {
	case "verbose":
		return steam.OutputRaw
	case "quiet":
		return steam.OutputQuiet
	default:
		return steam.OutputFiltered
	}
}
