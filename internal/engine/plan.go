package engine

import (
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
)

// Plan applies the sync diff (spec section 6): an item goes to steamcmd when
// it is new, failed on the previous run (automatic retry), or the Web API
// reports a newer time_updated than the cache. Items with no known remote
// time — apps, hidden workshop entries — are always included: their freshness
// can't be proven cheaply, and steamcmd app updates are incremental anyway.
// force (--force-all/--recheck) includes everything.
func Plan(items []provider.Item, remoteTimes map[string]int64, store *state.Store, force bool) (run, skip []provider.Item) {
	for _, item := range items {
		if force {
			run = append(run, item)
			continue
		}
		cached, seen := store.Item(item.Key())
		remote, hasRemote := remoteTimes[item.Key()]
		switch {
		case !seen, // new item
			!cached.LastSuccess,             // failed last run — auto-retry
			!hasRemote,                      // freshness unknown
			remote > cached.LastTimeUpdated: // updated upstream
			run = append(run, item)
		default:
			skip = append(skip, item)
		}
	}
	return run, skip
}
