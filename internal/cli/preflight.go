package cli

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
)

// preflightDisk warns when a first-time app install likely will not fit on
// the target filesystem. Sizes come from appinfo depot manifests and are
// approximate, so this is a warning, never a hard stop. Only fresh installs
// are checked (updates are incremental), and mods are not counted — their
// sizes are unknown upfront and workshop items are small next to depots.
func preflightDisk(ctx context.Context, prov *steam.Provider, infos map[string]*steam.AppInfo, items []provider.Item, logf func(string, ...any)) {
	for _, item := range items {
		if item.Kind != provider.KindApp {
			continue
		}
		if _, err := os.Stat(steam.ManifestPath(item)); err == nil {
			continue // already installed — incremental update
		}
		info, err := appInfoMemo(ctx, prov, infos, item.ID)
		if err != nil {
			continue // download will surface the real error
		}
		platform := item.Platform
		if platform == "" {
			platform = steam.HostOS()
		}
		need := info.Sizes[platform]
		if need <= 0 {
			continue
		}
		free, ok := diskFree(item.InstallDir)
		if !ok {
			continue
		}
		if free < need {
			logf("warning: app %s needs ~%.1f GiB but only %.1f GiB are free at %s",
				item.ID, gib(need), gib(free), item.InstallDir)
		}
	}
}

func gib(b int64) float64 { return float64(b) / (1 << 30) }

// diskFree reports the free bytes on the filesystem holding path, walking up
// to the nearest existing parent (the install dir may not exist yet).
func diskFree(path string) (int64, bool) {
	dir := path
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 0, false
		}
		dir = parent
	}
	return statfsFree(dir)
}
