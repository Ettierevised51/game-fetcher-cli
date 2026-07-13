package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
)

// appInfoMemo caches appinfo per app id for the duration of one command, so
// platform resolution and the disk preflight share a single steamcmd query.
func appInfoMemo(ctx context.Context, prov *steam.Provider, infos map[string]*steam.AppInfo, appID string) (*steam.AppInfo, error) {
	if info, ok := infos[appID]; ok {
		return info, nil
	}
	info, err := prov.AppInfo(ctx, appID)
	if err != nil {
		return nil, err
	}
	infos[appID] = info
	return info, nil
}

// resolvePlatforms fills Item.Platform for app items whose profile did not
// pin one: native build when it exists; otherwise — if a Windows build
// exists — offer to download that one (it will need Proton/Wine to run).
// Decisions are cached in the state store so steamcmd is asked once per app.
func resolvePlatforms(ctx context.Context, store *state.Store, prov *steam.Provider, infos map[string]*steam.AppInfo, items []provider.Item, confirm func(string) (bool, error), logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	native := steam.HostOS()
	dirty := false
	for i := range items {
		item := &items[i]
		if item.Kind != provider.KindApp || item.Platform != "" {
			continue
		}
		if cached, ok := store.Platform(item.ID); ok {
			if cached != "native" {
				item.Platform = cached
				logf("app %s: using the remembered %s build", item.ID, cached)
			}
			continue
		}
		info, err := appInfoMemo(ctx, prov, infos, item.ID)
		if err != nil {
			return fmt.Errorf("app %s: checking available platforms: %w", item.ID, err)
		}
		if slices.Contains(info.OSes, native) {
			store.SetPlatform(item.ID, "native")
			dirty = true
			continue
		}
		if !slices.Contains(info.OSes, "windows") {
			return fmt.Errorf("app %s has no %s build and no Windows build either (available: %v)", item.ID, native, info.OSes)
		}
		ok, err := confirm(fmt.Sprintf("app %s has no %s build; download the Windows build instead (running it will need Proton/Wine)?", item.ID, native))
		if err != nil {
			return fmt.Errorf("app %s has no %s build; set `platform: windows` in the profile to download the Windows build (%v)", item.ID, native, err)
		}
		if !ok {
			return fmt.Errorf("app %s: skipped — no %s build", item.ID, native)
		}
		item.Platform = "windows"
		store.SetPlatform(item.ID, "windows")
		dirty = true
		logf("app %s: Windows build selected (remembered in the state store; pin it with `platform: windows` in the profile if you prefer it explicit)", item.ID)
	}
	if dirty {
		return store.Save()
	}
	return nil
}
