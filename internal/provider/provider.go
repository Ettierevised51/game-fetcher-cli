// Package provider defines the backend abstraction (spec.md, section 2).
// The core (config, retry, parallelism, rate-limit, state cache, CLI) works
// only against Provider and must not know whether the backend is Steam or EGS.
package provider

import "context"

// ItemKind distinguishes what an Item refers to.
type ItemKind string

const (
	// KindApp is a game or dedicated-server application.
	KindApp ItemKind = "app"
	// KindMod is a workshop item (mod).
	KindMod ItemKind = "mod"
)

// Item is a single downloadable unit resolved from a Source.
type Item struct {
	// ID is the Steam app id for apps, or the workshop item id for mods.
	ID   string
	Name string
	Kind ItemKind
	// AppID is the base/client app id a mod belongs to (its consumer_app_id;
	// never the dedicated-server app id — spec.md section 7). Empty for apps.
	AppID string
	// InstallDir is the target installation directory, normally taken from
	// the profile by whoever resolves items.
	InstallDir string
	// Platform forces a non-native build ("windows", "linux", "macos");
	// empty means the platform steamcmd runs on. Windows-only dedicated
	// servers on Linux are the reason this exists.
	Platform string
	// Branch selects a beta branch of the same app (steamcmd
	// `app_update -beta`); BranchPassword unlocks a protected one.
	Branch         string
	BranchPassword string
}

// Key identifies an item and its destination in the state store. The
// install dir is part of the key so that the same app or mod deployed into
// two directories is tracked separately.
func (i Item) Key() string {
	if i.Kind == KindMod {
		return "mod:" + i.AppID + ":" + i.ID + "@" + i.InstallDir
	}
	return "app:" + i.ID + "@" + i.InstallDir
}

// Source describes where items come from (spec.md section 2): workshop
// collections and/or an explicit list of workshop item ids.
type Source struct {
	// Collections are workshop collection ids, each expanded into its items.
	Collections []string
	// ModIDs are explicit workshop item ids.
	ModIDs []string
}

// DownloadOptions carries per-download settings.
type DownloadOptions struct {
	// Validate makes the backend verify installed files
	// (steamcmd: `app_update ... validate`).
	Validate bool
}

// Provider is implemented by each backend (MVP: Steam via steamcmd;
// post-MVP: EGS via Legendary).
type Provider interface {
	// ResolveItems expands a source (collection, search, id list) into
	// a concrete list of items.
	ResolveItems(ctx context.Context, source Source) ([]Item, error)

	// Download fetches an app or mod.
	Download(ctx context.Context, item Item, opts DownloadOptions) error

	// DownloadBatch fetches several mods sharing one InstallDir in a single
	// backend operation (steamcmd: many workshop_download_item commands in
	// one process). Returns one error slot per item, nil on success; an
	// implementation without batch support may simply loop Download.
	DownloadBatch(ctx context.Context, items []Item, opts DownloadOptions) []error

	// IsInstalled reports whether the item is already present on disk.
	IsInstalled(ctx context.Context, item Item) (bool, error)
}
