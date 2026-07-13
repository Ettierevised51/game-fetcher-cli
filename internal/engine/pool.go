// Package engine orchestrates parallel steamcmd runs and the sync diff
// (spec.md sections 4-6).
package engine

import (
	"context"
	"sync"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
)

// Result pairs an item with its download outcome.
type Result struct {
	Item provider.Item
	Err  error
}

// Run executes do for every item with at most limit concurrent workers
// (each worker is a separate steamcmd process — steamcmd itself is strictly
// sequential). A non-anonymous login is hard-downgraded to one worker with a
// warning: a second login with the same account replaces the first session
// ("Session Replaced", spec section 4). Results keep the input order.
func Run(ctx context.Context, items []provider.Item, limit int, anonymous bool, logf func(format string, args ...any), do func(context.Context, provider.Item) error) []Result {
	batches := make([][]provider.Item, len(items))
	for i, item := range items {
		batches[i] = []provider.Item{item}
	}
	return RunBatches(ctx, batches, limit, anonymous, logf, func(ctx context.Context, batch []provider.Item) []Result {
		return []Result{{Item: batch[0], Err: do(ctx, batch[0])}}
	})
}

// RunBatches is Run for item batches: each batch is served by one steamcmd
// process handling its items sequentially, batches run concurrently under
// the same limit and non-anonymous downgrade. do returns one Result per
// batch item; the flattened results keep batch order.
func RunBatches(ctx context.Context, batches [][]provider.Item, limit int, anonymous bool, logf func(format string, args ...any), do func(context.Context, []provider.Item) []Result) []Result {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if limit < 1 {
		limit = 1
	}
	if !anonymous && limit > 1 {
		logf("warning: non-anonymous Steam login — downgrading parallelism from %d to 1 (a second login would replace the session)", limit)
		limit = 1
	}

	sem := make(chan struct{}, limit)
	results := make([][]Result, len(batches))
	var wg sync.WaitGroup
	for i, batch := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				out := make([]Result, len(batch))
				for j, item := range batch {
					out[j] = Result{Item: item, Err: ctx.Err()}
				}
				results[i] = out
				return
			}
			results[i] = do(ctx, batch)
		}()
	}
	wg.Wait()
	var flat []Result
	for _, r := range results {
		flat = append(flat, r...)
	}
	return flat
}
