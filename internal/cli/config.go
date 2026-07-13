package cli

import (
	"github.com/spf13/cobra"

	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
)

// loadConfig assembles the final configuration for a command invocation.
// Only flags the user explicitly set become the topmost override layer, so
// flag defaults cannot shadow values from lower layers.
func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	flags := cmd.Flags()
	configPath, err := flags.GetString("config")
	if err != nil {
		return nil, err
	}
	overrides := map[string]any{}
	if flags.Changed("allow-install-steamcmd") {
		v, err := flags.GetBool("allow-install-steamcmd")
		if err != nil {
			return nil, err
		}
		overrides["auto_install_steamcmd"] = v
	}
	return config.Load(config.Options{ConfigPath: configPath, Overrides: overrides})
}
