package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
)

func TestModBatches(t *testing.T) {
	mod := func(id, dir string) provider.Item {
		return provider.Item{ID: id, Kind: provider.KindMod, AppID: "730", InstallDir: dir}
	}
	mods := []provider.Item{
		mod("1", "/a"), mod("2", "/a"), mod("3", "/a"), mod("4", "/a"), mod("5", "/a"),
		mod("6", "/b"),
	}
	batches := modBatches(mods, 2)
	// /a splits into ceil(5/2)-sized batches (3+2), /b keeps its own batch.
	if len(batches) != 3 {
		t.Fatalf("want 3 batches, got %d: %v", len(batches), batches)
	}
	if len(batches[0]) != 3 || len(batches[1]) != 2 || len(batches[2]) != 1 {
		t.Errorf("unexpected batch sizes: %d/%d/%d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
	for _, b := range batches {
		for _, it := range b {
			if it.InstallDir != b[0].InstallDir {
				t.Errorf("batch mixes install dirs: %v", b)
			}
		}
	}
	if got := modBatches(nil, 4); len(got) != 0 {
		t.Errorf("no mods must mean no batches, got %v", got)
	}
}

// A mod repeated in the plain mods: list (or the same mod from two profiles
// into one dir) must produce a single download item.
func TestItemsFromProfilesDedup(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[string]config.Profile{
		"a": {AppID: 2394010, BaseAppID: 1623730, InstallDir: "/opt/pal", Mods: []int64{111, 222, 111}},
		"b": {AppID: 2394010, BaseAppID: 1623730, InstallDir: "/opt/pal", Mods: []int64{222, 333}},
	}
	items, err := itemsFromProfiles(context.Background(), store, steam.New(steam.Options{}), retry.Policy{MaxAttempts: 1}, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mods, apps int
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.Key()] {
			t.Errorf("duplicate item %s", it.Key())
		}
		seen[it.Key()] = true
		if it.Kind == provider.KindMod {
			mods++
		} else {
			apps++
		}
	}
	// The same app into the same dir collapses too; mods 111/222/333 once each.
	if apps != 1 || mods != 3 {
		t.Errorf("want 1 app and 3 mods, got %d app(s) and %d mod(s): %v", apps, mods, items)
	}
}

// Re-saving a profile must be additive: fields the new flags do not set
// (a previously resolved base_app_id) survive instead of being diffed away.
func TestSaveProfileAdditiveUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	seed := "profiles:\n  pal:\n    app_id: 1\n    install_dir: /d\n    base_app_id: 5\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	p := config.Profile{AppID: 1, InstallDir: "/d", Collections: []int64{9}}
	confirm := func(string) (bool, error) { return true, nil }
	var out strings.Builder
	if err := saveProfile(path, "pal", p, confirm, &out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "base_app_id: 5") {
		t.Errorf("base_app_id must survive an additive update:\n%s", raw)
	}
	if !strings.Contains(string(raw), "collections") {
		t.Errorf("collections must be added:\n%s", raw)
	}
}

// TestDownloadModBatchRetriesOnlyFailures: attempt 1 fails one item of two
// retryably; attempt 2 must re-run just that item and succeed.
func TestDownloadModBatchRetriesOnlyFailures(t *testing.T) {
	dir := t.TempDir()
	items := []provider.Item{
		{ID: "111", Kind: provider.KindMod, AppID: "730", InstallDir: dir},
		{ID: "222", Kind: provider.KindMod, AppID: "730", InstallDir: dir},
	}
	land := func(id string) error {
		content := filepath.Join(dir, "steamapps", "workshop", "content", "730", id)
		if err := os.MkdirAll(content, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(content, "mod.pak"), []byte("x"), 0o644)
	}

	bin := filepath.Join(t.TempDir(), "steamcmd.sh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var scripts []string
	prov := steam.New(steam.Options{
		SteamcmdPath: bin,
		Run: func(_ context.Context, _ string, args ...string) (string, error) {
			raw, err := os.ReadFile(args[len(args)-1])
			if err != nil {
				return "", err
			}
			scripts = append(scripts, string(raw))
			if len(scripts) == 1 {
				if err := land("111"); err != nil {
					return "", err
				}
				return "Success. Downloaded item 111 to \"...\" (1 bytes)\nERROR! Download item 222 failed (Failure).", nil
			}
			if err := land("222"); err != nil {
				return "", err
			}
			return "Success. Downloaded item 222 to \"...\" (1 bytes)", nil
		},
	})
	policy := retry.Policy{MaxAttempts: 3, Sleep: func(context.Context, time.Duration) error { return nil }}
	results := downloadModBatch(context.Background(), prov, policy, items, provider.DownloadOptions{})

	if len(scripts) != 2 {
		t.Fatalf("want 2 steamcmd runs, got %d", len(scripts))
	}
	if strings.Contains(scripts[1], "workshop_download_item 730 111") {
		t.Errorf("second attempt must not re-download the succeeded item:\n%s", scripts[1])
	}
	if !strings.Contains(scripts[1], "workshop_download_item 730 222") {
		t.Errorf("second attempt must retry the failed item:\n%s", scripts[1])
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error after retry: %v", r.Item.ID, r.Err)
		}
	}
}
