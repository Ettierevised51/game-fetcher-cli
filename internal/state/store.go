// Package state persists per-item sync state between runs (spec.md
// section 6). The MVP backend is a single JSON file; SQLite and Redis may
// follow post-MVP behind the same surface.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Item is what the store remembers about one downloadable unit.
type Item struct {
	ID               string    `json:"id"`
	Name             string    `json:"name,omitempty"`
	LastTimeUpdated  int64     `json:"last_time_updated,omitempty"` // unix, from the Steam Web API
	LastDownloadedAt time.Time `json:"last_downloaded_at,omitzero"`
	LastSuccess      bool      `json:"last_success"`
}

// AppLink maps a dedicated-server app id to its base/client app id and a
// human-readable name (spec section 6).
type AppLink struct {
	ServerAppID string `json:"server_app_id"`
	BaseAppID   string `json:"base_app_id"`
	Name        string `json:"name"`
}

type fileFormat struct {
	Items map[string]Item `json:"items"`
	Apps  []AppLink       `json:"apps,omitempty"`
	// Platforms remembers per app id which build gets downloaded:
	// "native", or a forced platform like "windows" (chosen when the app
	// has no build for the host OS).
	Platforms map[string]string `json:"platforms,omitempty"`
}

// Store is the JSON-file state store. Safe for concurrent use.
type Store struct {
	path string

	mu   sync.Mutex
	data fileFormat
}

// Open loads the store at path; a missing file yields an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: fileFormat{Items: map[string]Item{}}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("state file %s: %w", path, err)
	}
	if s.data.Items == nil {
		s.data.Items = map[string]Item{}
	}
	return s, nil
}

func (s *Store) Item(key string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.data.Items[key]
	return it, ok
}

func (s *Store) SetItem(key string, it Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Items[key] = it
}

// SetAppLink upserts a mapping by its server app id.
func (s *Store) SetAppLink(link AppLink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, l := range s.data.Apps {
		if l.ServerAppID == link.ServerAppID {
			s.data.Apps[i] = link
			return
		}
	}
	s.data.Apps = append(s.data.Apps, link)
}

// Platform returns the remembered build choice for an app id.
func (s *Store) Platform(appID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.data.Platforms[appID]
	return p, ok
}

func (s *Store) SetPlatform(appID, platform string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Platforms == nil {
		s.data.Platforms = map[string]string{}
	}
	s.data.Platforms[appID] = platform
}

// ForcedPlatforms returns the app ids that download a non-native build.
func (s *Store) ForcedPlatforms() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for id, p := range s.data.Platforms {
		if p != "native" {
			out[id] = p
		}
	}
	return out
}

func (s *Store) AppLink(serverAppID string) (AppLink, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.data.Apps {
		if l.ServerAppID == serverAppID {
			return l, true
		}
	}
	return AppLink{}, false
}

// Save writes the store atomically: temp file in the same directory, then
// rename over the target.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	// Flush to disk before the rename: without it a power loss right after
	// renaming can leave an empty state.json on ext4/xfs.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
