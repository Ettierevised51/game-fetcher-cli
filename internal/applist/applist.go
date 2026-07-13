// Package applist maintains the local app id index for offline name search
// (spec.md section 7). The index is fetched from open app-id databases (see
// DefaultSources — the project's own steam-appdb; Valve removed the keyless
// GetAppList), cached on disk and refreshed every N days.
package applist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// App is one searchable entry.
type App struct {
	AppID int    `json:"appid"`
	Name  string `json:"name"`
}

// Index is the on-disk cache of the full app list.
type Index struct {
	FetchedAt time.Time `json:"fetched_at"`
	Apps      []App     `json:"apps"`
}

// DefaultMaxAge is how long a cached app list stays fresh
// (spec: refresh every N days).
const DefaultMaxAge = 7 * 24 * time.Hour

// Ensure returns the local index, refreshing it via fetch when older than
// maxAge. fetch == nil means offline mode: whatever is cached is returned.
// When a refresh fails but a stale cache exists, the stale cache is returned
// along with the error so the caller can warn and continue.
func Ensure(ctx context.Context, path string, maxAge time.Duration, fetch func(context.Context) ([]App, error)) (*Index, error) {
	idx, err := read(path)
	if err != nil {
		return nil, err
	}
	if fetch == nil {
		return idx, nil
	}
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	if len(idx.Apps) > 0 && time.Since(idx.FetchedAt) < maxAge {
		return idx, nil
	}
	apps, err := fetch(ctx)
	if err != nil {
		return idx, fmt.Errorf("refreshing app list: %w", err)
	}
	fresh := &Index{FetchedAt: time.Now(), Apps: apps}
	if err := write(path, fresh); err != nil {
		return fresh, err
	}
	return fresh, nil
}

func read(path string) (*Index, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Index{}, nil
	}
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		// A corrupt cache (external truncation — our own writes are atomic)
		// must not brick search until manually deleted; refetch instead.
		return &Index{}, nil
	}
	return &idx, nil
}

func write(path string, idx *Index) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	buf, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".applist-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
