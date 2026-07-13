package webapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

func TestTimesUpdated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if got := r.PostFormValue("itemcount"); got != "2" {
			t.Errorf("itemcount = %q, want 2", got)
		}
		if got := r.PostFormValue("publishedfileids[0]"); got != "111" {
			t.Errorf("publishedfileids[0] = %q, want 111", got)
		}
		w.Write([]byte(`{"response":{"publishedfiledetails":[
			{"publishedfileid":"111","result":1,"time_updated":1700000000},
			{"publishedfileid":"222","result":9}
		]}}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	times, err := c.TimesUpdated(context.Background(), []string{"111", "222"})
	if err != nil {
		t.Fatal(err)
	}
	if times["111"] != 1700000000 {
		t.Errorf("times[111] = %d, want 1700000000", times["111"])
	}
	// result != 1 (hidden/removed) must be omitted, not zero-valued.
	if _, ok := times["222"]; ok {
		t.Error("item with result=9 must be omitted")
	}
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Shape verified against the live endpoint (2026-07).
		w.Write([]byte(`{"servertime":1783850597,"servertimestring":"Sun Jul 12 03:03:17 2026"}`))
	}))
	defer srv.Close()
	if err := (&Client{BaseURL: srv.URL}).Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTimesUpdatedServerErrorIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.TimesUpdated(context.Background(), []string{"111"})
	if err == nil || retry.ClassOf(err) != retry.Retryable {
		t.Fatalf("5xx must be retryable, got err=%v class=%v", err, retry.ClassOf(err))
	}
}
