package steam

import (
	"context"
	"os"
	"path/filepath"
)

// userHomeDir is a test seam: Logout deletes real files, and a test must
// NEVER let it near the developer's actual home directory.
var userHomeDir = os.UserHomeDir

// Logout removes steamcmd's cached session artifacts: login tokens
// (config/config.vdf) and Steam Guard sentry files (ssfn*). steamcmd's own
// `logout` console command does not clear them (ValveSoftware/steam-for-linux
// #7120), so the files have to go. The next non-anonymous login asks for the
// password and a fresh Steam Guard code again. Returns the removed paths.
func (p *Provider) Logout(ctx context.Context) ([]string, error) {
	var roots []string
	if bin, err := p.steamcmdPath(ctx); err == nil {
		roots = append(roots, filepath.Dir(bin))
	}
	// steamcmd keeps its Steam data under the home dir regardless of where
	// the binary lives.
	if home, err := userHomeDir(); err == nil {
		roots = append(roots,
			filepath.Join(home, "Steam"),
			filepath.Join(home, ".steam", "steam"),
			filepath.Join(home, ".local", "share", "Steam"))
	}
	var removed []string
	seen := map[string]bool{}
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		targets := []string{filepath.Join(root, "config", "config.vdf")}
		if ssfn, err := filepath.Glob(filepath.Join(root, "ssfn*")); err == nil {
			targets = append(targets, ssfn...)
		}
		for _, t := range targets {
			if _, err := os.Stat(t); err != nil {
				continue
			}
			if err := os.Remove(t); err != nil {
				return removed, err
			}
			removed = append(removed, t)
		}
	}
	return removed, nil
}
