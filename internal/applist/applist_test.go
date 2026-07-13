package applist

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestMatchRanking(t *testing.T) {
	apps := []App{
		{AppID: 1, Name: "Trust the Process"},
		{AppID: 2, Name: "Rust Dedicated Server"},
		{AppID: 3, Name: "Rust"},
		{AppID: 4, Name: "Unrelated Game"},
	}
	got := Match(apps, "rust", 10)
	if len(got) != 3 {
		t.Fatalf("got %d matches: %v", len(got), got)
	}
	// exact > prefix > substring; no-match excluded
	if got[0].AppID != 3 || got[1].AppID != 2 || got[2].AppID != 1 {
		t.Errorf("wrong ranking: %v", got)
	}

	if got := Match(apps, "rust server", 10); len(got) != 1 || got[0].AppID != 2 {
		t.Errorf("all-words query: %v, want only the dedicated server", got)
	}
	if got := Match(apps, "rust", 1); len(got) != 1 || got[0].AppID != 3 {
		t.Errorf("limit: %v", got)
	}
}

func TestEnsureCacheLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "applist.json")
	calls := 0
	fetch := func(context.Context) ([]App, error) {
		calls++
		return []App{{AppID: 10, Name: "Counter-Strike"}}, nil
	}

	// No key: no fetch, empty index.
	idx, err := Ensure(ctx, path, DefaultMaxAge, nil)
	if err != nil || len(idx.Apps) != 0 || calls != 0 {
		t.Fatalf("keyless: idx=%v calls=%d err=%v", idx, calls, err)
	}

	// First fetch populates the cache.
	idx, err = Ensure(ctx, path, DefaultMaxAge, fetch)
	if err != nil || len(idx.Apps) != 1 || calls != 1 {
		t.Fatalf("first fetch: apps=%d calls=%d err=%v", len(idx.Apps), calls, err)
	}

	// Fresh cache: no second fetch.
	idx, err = Ensure(ctx, path, DefaultMaxAge, fetch)
	if err != nil || calls != 1 {
		t.Fatalf("fresh cache: calls=%d err=%v", calls, err)
	}

	// Stale cache: refetched.
	if _, err := Ensure(ctx, path, time.Nanosecond, fetch); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("stale cache: calls=%d, want 2", calls)
	}

	// Failing refresh with an existing cache returns the stale data + error.
	idx, err = Ensure(ctx, path, time.Nanosecond, func(context.Context) ([]App, error) {
		return nil, errors.New("api down")
	})
	if err == nil || idx == nil || len(idx.Apps) != 1 {
		t.Fatalf("failed refresh must fall back to stale cache: idx=%v err=%v", idx, err)
	}
}

func TestFetchOpenMergesFormats(t *testing.T) {
	// Source 1: classic wrapped GetAppList shape (has the dedicated server).
	src1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"applist":{"apps":[
			{"appid":252490,"name":"Rust (old name)"},
			{"appid":258550,"name":"Rust Dedicated Server"}
		]}}`)
	}))
	defer src1.Close()
	// Source 2: flat-array shape (fresher, wins on name).
	src2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"appid":252490,"name":"Rust","last_modified":1},{"appid":730,"name":"Counter-Strike 2"}]`)
	}))
	defer src2.Close()

	apps, err := FetchOpen(context.Background(), nil, []string{src1.URL, src2.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int]string{}
	for _, a := range apps {
		byID[a.AppID] = a.Name
	}
	if len(apps) != 3 || byID[252490] != "Rust" || byID[258550] != "Rust Dedicated Server" || byID[730] == "" {
		t.Fatalf("merged apps = %v", apps)
	}
}

func TestFetchOpenSurvivesOneDeadSource(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"appid":10,"name":"Counter-Strike"}]`)
	}))
	defer alive.Close()

	apps, err := FetchOpen(context.Background(), nil, []string{dead.URL, alive.URL}, nil)
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps=%v err=%v; one dead source must not be fatal", apps, err)
	}
	if _, err := FetchOpen(context.Background(), nil, []string{dead.URL}, nil); err == nil {
		t.Fatal("all sources dead must be an error")
	}
}
