package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/provider/steam"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
	"github.com/Austrum-lab/game-fetcher-cli/internal/state"
	"github.com/Austrum-lab/game-fetcher-cli/internal/webapi"
)

func modOf(items []provider.Item, id string) (provider.Item, bool) {
	for _, it := range items {
		if it.Kind == provider.KindMod && it.ID == id {
			return it, true
		}
	}
	return provider.Item{}, false
}

// TestItemsFromProfilesResolvesBase: a profile holds the dedicated-server
// app id; the base game id for its mods must come from the mods' own
// consumer_app_id (keyless workshop metadata), be cached as an AppLink, and
// never be silently defaulted to the server id.
func TestItemsFromProfilesResolvesBase(t *testing.T) {
	ctx := context.Background()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":1,"publishedfiledetails":[
			{"publishedfileid":"111","result":1,"title":"Some Rust Mod","consumer_app_id":252490,"time_updated":1700000000}
		]}}`)
	}))
	defer srv.Close()

	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	resolver := steam.New(steam.Options{WebAPI: &webapi.Client{BaseURL: srv.URL}})
	policy := retry.Policy{MaxAttempts: 1}
	profiles := map[string]config.Profile{
		"rust": {AppID: 258550, InstallDir: "/srv/rust", Mods: []int64{111}},
	}

	items, err := itemsFromProfiles(ctx, store, resolver, policy, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	mod, ok := modOf(items, "111")
	if !ok || mod.AppID != "252490" {
		t.Fatalf("mod item = %+v, want AppID 252490 from consumer_app_id", mod)
	}
	link, ok := store.AppLink("258550")
	if !ok || link.BaseAppID != "252490" {
		t.Fatalf("AppLink not cached: %+v ok=%v", link, ok)
	}
	if calls != 1 {
		t.Fatalf("workshop asked %d times, want 1", calls)
	}

	// Second expansion must hit the cached AppLink, not the network.
	srv.Close()
	items, err = itemsFromProfiles(ctx, store, resolver, policy, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mod, _ := modOf(items, "111"); mod.AppID != "252490" {
		t.Fatalf("cached resolution broken: %+v", mod)
	}
}

func TestItemsFromProfilesExplicitBaseWins(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Unreachable client: an explicit base_app_id must need no network.
	resolver := steam.New(steam.Options{WebAPI: &webapi.Client{BaseURL: "http://127.0.0.1:1"}})
	profiles := map[string]config.Profile{
		"gmod": {AppID: 4020, BaseAppID: 4000, InstallDir: "/srv/gmod", Mods: []int64{222}},
	}
	items, err := itemsFromProfiles(context.Background(), store, resolver, retry.Policy{MaxAttempts: 1}, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mod, _ := modOf(items, "222"); mod.AppID != "4000" {
		t.Fatalf("mod item = %+v, want explicit base_app_id 4000", mod)
	}
}

func TestItemsFromProfilesUnresolvableBaseFails(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Workshop knows nothing about this mod (result != 1) — the tool must
	// error out instead of guessing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":1,"publishedfiledetails":[{"publishedfileid":"333","result":9}]}}`)
	}))
	defer srv.Close()
	profiles := map[string]config.Profile{
		"broken": {AppID: 258550, InstallDir: "/srv/x", Mods: []int64{333}},
	}
	resolver := steam.New(steam.Options{WebAPI: &webapi.Client{BaseURL: srv.URL}})
	_, err = itemsFromProfiles(context.Background(), store, resolver, retry.Policy{MaxAttempts: 1}, profiles, nil)
	if err == nil {
		t.Fatal("expected an error for an unresolvable base app id")
	}
}

// TestItemsFromProfilesExpandsCollections: a profile may reference a workshop
// collection; it is re-expanded on every run and each mod carries its own
// consumer app id.
func TestItemsFromProfilesExpandsCollections(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ISteamRemoteStorage/GetCollectionDetails/v1/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":1,"collectiondetails":[
			{"publishedfileid":"555","result":1,"children":[
				{"publishedfileid":"20","sortorder":2},
				{"publishedfileid":"10","sortorder":1}
			]}
		]}}`)
	})
	mux.HandleFunc("/ISteamRemoteStorage/GetPublishedFileDetails/v1/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":2,"publishedfiledetails":[
			{"publishedfileid":"10","result":1,"title":"First","consumer_app_id":252490},
			{"publishedfileid":"20","result":1,"title":"Second","consumer_app_id":252490}
		]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	resolver := steam.New(steam.Options{WebAPI: &webapi.Client{BaseURL: srv.URL}})
	profiles := map[string]config.Profile{
		"rust": {AppID: 258550, InstallDir: "/srv/rust", Collections: []int64{555}},
	}
	items, err := itemsFromProfiles(context.Background(), store, resolver, retry.Policy{MaxAttempts: 1}, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, ok1 := modOf(items, "10")
	second, ok2 := modOf(items, "20")
	if !ok1 || !ok2 {
		t.Fatalf("collection children missing from items: %+v", items)
	}
	if first.AppID != "252490" || second.AppID != "252490" || first.InstallDir != "/srv/rust" {
		t.Fatalf("collection mods misresolved: %+v %+v", first, second)
	}
	if link, ok := store.AppLink("258550"); !ok || link.BaseAppID != "252490" {
		t.Fatalf("AppLink not cached from collection resolution: %+v ok=%v", link, ok)
	}
}
