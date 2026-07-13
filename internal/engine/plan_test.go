package engine

import (
	"path/filepath"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
)

func mod(id string) provider.Item {
	return provider.Item{ID: id, Kind: provider.KindMod, AppID: "730", InstallDir: "/srv/game"}
}

func keys(items []provider.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Key()
	}
	return out
}

func TestPlanDiff(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	upToDate := mod("1") // cached, success, remote time unchanged  → skip
	changed := mod("2")  // cached, success, remote time newer      → run
	fresh := mod("3")    // not in the cache                        → run
	failed := mod("4")   // cached, but the last run failed         → run
	app := provider.Item{ID: "730", Kind: provider.KindApp, InstallDir: "/srv/game"}
	// The app succeeded before, but apps have no remote time      → run

	store.SetItem(upToDate.Key(), state.Item{ID: "1", LastTimeUpdated: 100, LastSuccess: true})
	store.SetItem(changed.Key(), state.Item{ID: "2", LastTimeUpdated: 100, LastSuccess: true})
	store.SetItem(failed.Key(), state.Item{ID: "4", LastTimeUpdated: 100, LastSuccess: false})
	store.SetItem(app.Key(), state.Item{ID: "730", LastSuccess: true})

	remote := map[string]int64{
		upToDate.Key(): 100,
		changed.Key():  200,
		failed.Key():   100, // unchanged upstream, but failed items auto-retry
	}
	items := []provider.Item{app, upToDate, changed, fresh, failed}

	run, skip := Plan(items, remote, store, false)
	wantRun := []string{app.Key(), changed.Key(), fresh.Key(), failed.Key()}
	if got := keys(run); !equal(got, wantRun) {
		t.Errorf("run = %v, want %v", got, wantRun)
	}
	if got := keys(skip); !equal(got, []string{upToDate.Key()}) {
		t.Errorf("skip = %v, want only the up-to-date item", got)
	}

	run, skip = Plan(items, remote, store, true)
	if len(run) != len(items) || len(skip) != 0 {
		t.Errorf("force: run=%d skip=%d, want %d/0", len(run), len(skip), len(items))
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
