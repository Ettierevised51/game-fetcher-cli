package steam

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/webapi"
)

func TestResolveItems(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ISteamRemoteStorage/GetCollectionDetails/v1/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":1,"collectiondetails":[
			{"publishedfileid":"555","result":1,"children":[
				{"publishedfileid":"30","sortorder":2},
				{"publishedfileid":"10","sortorder":1}
			]}
		]}}`)
	})
	mux.HandleFunc("/ISteamRemoteStorage/GetPublishedFileDetails/v1/", func(w http.ResponseWriter, _ *http.Request) {
		// Item 30 has no metadata (hidden): must come back bare, not dropped.
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":3,"publishedfiledetails":[
			{"publishedfileid":"10","result":1,"title":"Ten","consumer_app_id":252490},
			{"publishedfileid":"20","result":1,"title":"Twenty","consumer_app_id":252490},
			{"publishedfileid":"30","result":9}
		]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(Options{WebAPI: &webapi.Client{BaseURL: srv.URL}})
	// Explicit mod 20 + collection 555 (children 10, 30); 10 appears once
	// even though sortorder puts it first in the collection.
	items, err := p.ResolveItems(context.Background(), provider.Source{
		ModIDs:      []string{"20", "10"},
		Collections: []string{"555"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v, want 3 (deduped)", items)
	}
	if items[0].ID != "20" || items[0].Name != "Twenty" || items[0].AppID != "252490" {
		t.Errorf("explicit mod misresolved: %+v", items[0])
	}
	if items[1].ID != "10" || items[1].Name != "Ten" {
		t.Errorf("deduped mod misresolved: %+v", items[1])
	}
	if items[2].ID != "30" || items[2].Name != "" || items[2].AppID != "" {
		t.Errorf("hidden item must come back bare: %+v", items[2])
	}
	for _, it := range items {
		if it.Kind != provider.KindMod {
			t.Errorf("item %s kind = %q", it.ID, it.Kind)
		}
	}
}

func TestResolveItemsEmptySource(t *testing.T) {
	p := New(Options{})
	if _, err := p.ResolveItems(context.Background(), provider.Source{}); err == nil {
		t.Fatal("empty source must be an error")
	}
}
