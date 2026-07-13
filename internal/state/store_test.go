package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "state.json")

	s, err := Open(path) // missing file → empty store
	if err != nil {
		t.Fatal(err)
	}
	s.SetItem("app:730@/srv/cs2", Item{
		ID: "730", Name: "cs2", LastTimeUpdated: 42,
		LastDownloadedAt: time.Now().UTC(), LastSuccess: true,
	})
	s.SetItem("mod:730:111@/srv/cs2", Item{ID: "111", LastSuccess: false})
	s.SetAppLink(AppLink{ServerAppID: "258550", BaseAppID: "252490", Name: "Rust"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	app, ok := s2.Item("app:730@/srv/cs2")
	if !ok || !app.LastSuccess || app.LastTimeUpdated != 42 || app.Name != "cs2" {
		t.Errorf("app state lost in roundtrip: %+v (ok=%v)", app, ok)
	}
	if m, ok := s2.Item("mod:730:111@/srv/cs2"); !ok || m.LastSuccess {
		t.Errorf("failed-item state lost: %+v (ok=%v)", m, ok)
	}
	link, ok := s2.AppLink("258550")
	if !ok || link.BaseAppID != "252490" {
		t.Errorf("app link lost: %+v (ok=%v)", link, ok)
	}

	// Upsert must replace, not append.
	s2.SetAppLink(AppLink{ServerAppID: "258550", BaseAppID: "252490", Name: "Rust Dedicated"})
	if err := s2.Save(); err != nil {
		t.Fatal(err)
	}
	s3, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	link, _ = s3.AppLink("258550")
	if link.Name != "Rust Dedicated" {
		t.Errorf("app link not upserted: %+v", link)
	}
}
