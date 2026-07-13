package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
)

const fakeWindowsOnlyInfo = `"298740"
{
	"common"
	{
		"name"		"Space Engineers Dedicated Server"
		"oslist"		"windows"
	}
}
`

const fakeNativeInfo = `"896660"
{
	"common"
	{
		"name"		"Valheim Dedicated Server"
		"oslist"		"windows,macos,linux"
	}
}
`

func fakeSteamcmd(t *testing.T, appinfo string, calls *int) *steam.Provider {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "steamcmd.sh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return steam.New(steam.Options{
		SteamcmdPath: bin,
		Run: func(context.Context, string, ...string) (string, error) {
			*calls++
			return appinfo, nil
		},
	})
}

func TestResolvePlatformsNative(t *testing.T) {
	store, _ := state.Open(filepath.Join(t.TempDir(), "state.json"))
	calls := 0
	prov := fakeSteamcmd(t, fakeNativeInfo, &calls)
	items := []provider.Item{{ID: "896660", Kind: provider.KindApp, InstallDir: "/srv"}}

	err := resolvePlatforms(context.Background(), store, prov, map[string]*steam.AppInfo{}, items, confirmBoom, nil)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Platform != "" {
		t.Fatalf("native app must not get a forced platform: %+v", items[0])
	}
	// Second resolve: cached, steamcmd not asked again.
	if err := resolvePlatforms(context.Background(), store, prov, map[string]*steam.AppInfo{}, items, confirmBoom, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("steamcmd asked %d times, want 1 (cache)", calls)
	}
}

func TestResolvePlatformsWindowsOnly(t *testing.T) {
	store, _ := state.Open(filepath.Join(t.TempDir(), "state.json"))
	calls := 0
	prov := fakeSteamcmd(t, fakeWindowsOnlyInfo, &calls)
	items := []provider.Item{{ID: "298740", Kind: provider.KindApp, InstallDir: "/srv"}}

	// Declined → hard error, nothing cached as windows.
	err := resolvePlatforms(context.Background(), store, prov, map[string]*steam.AppInfo{}, items, confirmNo, nil)
	if err == nil {
		t.Fatal("declining the Windows build must be an error")
	}

	// Accepted → platform set and remembered.
	if err := resolvePlatforms(context.Background(), store, prov, map[string]*steam.AppInfo{}, items, confirmYes, nil); err != nil {
		t.Fatal(err)
	}
	if items[0].Platform != "windows" {
		t.Fatalf("platform = %q, want windows", items[0].Platform)
	}
	if cached, ok := store.Platform("298740"); !ok || cached != "windows" {
		t.Fatalf("choice not cached: %q ok=%v", cached, ok)
	}

	// Non-tty (confirm errors) with a fresh store → error advising the config key.
	store2, _ := state.Open(filepath.Join(t.TempDir(), "state.json"))
	items2 := []provider.Item{{ID: "298740", Kind: provider.KindApp, InstallDir: "/srv"}}
	err = resolvePlatforms(context.Background(), store2, prov, map[string]*steam.AppInfo{}, items2, confirmBoom, nil)
	if err == nil || !strings.Contains(err.Error(), "platform: windows") {
		t.Fatalf("expected advice to set platform: windows, got: %v", err)
	}
}

func TestResolvePlatformsPinnedSkipsDetection(t *testing.T) {
	store, _ := state.Open(filepath.Join(t.TempDir(), "state.json"))
	calls := 0
	prov := fakeSteamcmd(t, fakeWindowsOnlyInfo, &calls)
	items := []provider.Item{{ID: "298740", Kind: provider.KindApp, InstallDir: "/srv", Platform: "windows"}}
	if err := resolvePlatforms(context.Background(), store, prov, map[string]*steam.AppInfo{}, items, confirmBoom, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("pinned platform must skip detection, steamcmd asked %d times", calls)
	}
}
