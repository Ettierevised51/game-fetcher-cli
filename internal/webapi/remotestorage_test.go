package webapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublishedFileDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.PostFormValue("itemcount") != "2" || r.PostFormValue("publishedfileids[1]") != "222" {
			t.Errorf("bad form: %v", r.PostForm)
		}
		// Shape verified against the live keyless endpoint (2026-07).
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":2,"publishedfiledetails":[
			{"publishedfileid":"111","result":1,"title":"Some Mod","consumer_app_id":252490,"time_updated":1700000000},
			{"publishedfileid":"222","result":9}
		]}}`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	mods, err := c.PublishedFileDetails(context.Background(), []string{"111", "222"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].ID != "111" || mods[0].Title != "Some Mod" || mods[0].ConsumerAppID != 252490 {
		t.Fatalf("mods = %+v (result!=1 must be dropped, consumer_app_id accepted)", mods)
	}
}

func TestGetCollectionDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.PostFormValue("collectioncount") != "1" || r.PostFormValue("publishedfileids[0]") != "555" {
			t.Errorf("bad form: %v", r.PostForm)
		}
		// Envelope verified live (2026-07); children per the documented shape.
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":1,"collectiondetails":[
			{"publishedfileid":"555","result":1,"children":[
				{"publishedfileid":"20","sortorder":2,"filetype":0},
				{"publishedfileid":"10","sortorder":1,"filetype":0}
			]}
		]}}`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	ids, err := c.GetCollectionDetails(context.Background(), "555")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "10" || ids[1] != "20" {
		t.Fatalf("ids = %v, want sorted by sortorder", ids)
	}
}

func TestGetCollectionDetailsNotACollection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Real response for a non-collection id (verified live, 2026-07).
		fmt.Fprint(w, `{"response":{"result":1,"resultcount":0,"collectiondetails":[{"publishedfileid":"3043951128","result":9}]}}`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.GetCollectionDetails(context.Background(), "3043951128")
	if err == nil {
		t.Fatal("result=9 must be an explicit error")
	}
}
