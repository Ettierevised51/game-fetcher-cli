package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
)

func poolItems(n int) []provider.Item {
	items := make([]provider.Item, n)
	for i := range items {
		items[i] = provider.Item{ID: strconv.Itoa(i + 1), Kind: provider.KindApp, InstallDir: "/srv"}
	}
	return items
}

// concurrencyProbe returns a do-func tracking peak concurrency.
func concurrencyProbe(peak *atomic.Int32) func(context.Context, provider.Item) error {
	var cur atomic.Int32
	return func(context.Context, provider.Item) error {
		c := cur.Add(1)
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		return nil
	}
}

func TestRunAnonymousParallel(t *testing.T) {
	var peak atomic.Int32
	results := Run(context.Background(), poolItems(8), 4, true, nil, concurrencyProbe(&peak))
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}
	if peak.Load() < 2 {
		t.Errorf("peak concurrency = %d, expected parallel execution with limit 4", peak.Load())
	}
	if peak.Load() > 4 {
		t.Errorf("peak concurrency = %d exceeds the limit of 4", peak.Load())
	}
}

func TestRunNonAnonymousDowngradesToOne(t *testing.T) {
	var warnings []string
	logf := func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) }

	var peak atomic.Int32
	Run(context.Background(), poolItems(6), 4, false, logf, concurrencyProbe(&peak))
	if peak.Load() != 1 {
		t.Errorf("peak concurrency = %d, want 1 for a non-anonymous login", peak.Load())
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "downgrading parallelism") {
		t.Errorf("expected a downgrade warning, got %v", warnings)
	}
}

func TestRunKeepsOrderAndErrors(t *testing.T) {
	boom := errors.New("boom")
	results := Run(context.Background(), poolItems(3), 2, true, nil, func(_ context.Context, it provider.Item) error {
		if it.ID == "2" {
			return boom
		}
		return nil
	})
	if len(results) != 3 {
		t.Fatalf("got %d results", len(results))
	}
	for i, r := range results {
		if r.Item.ID != strconv.Itoa(i+1) {
			t.Errorf("result %d is item %s: order not preserved", i, r.Item.ID)
		}
	}
	if results[1].Err != boom || results[0].Err != nil || results[2].Err != nil {
		t.Errorf("errors misplaced: %+v", results)
	}
}
