package steam

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

// HostOS returns Steam's oslist token for the platform we run on.
func HostOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

// validPlatforms are the tokens @sSteamCmdForcePlatformType accepts.
var validPlatforms = map[string]bool{"windows": true, "linux": true, "macos": true}

// LaunchEntry is one entry of appinfo's config.launch — Valve's own record
// of what to execute (used by the systemd generator).
type LaunchEntry struct {
	Executable string
	Arguments  string
	Type       string   // "server", "default", ...
	OSes       []string // empty means any platform
}

// AppInfo is the part of `app_info_print` output the tool cares about.
type AppInfo struct {
	// OSes is common.oslist — the authoritative supported-platform list.
	// Depot-level oslist entries are decoys (a Windows-only app still
	// carries linux redistributable depots; verified on Space Engineers DS).
	OSes []string
	// Sizes is the approximate install size per platform, summed over
	// depot manifests (shared depots count for every platform).
	Sizes map[string]int64
	// Launch lists config.launch entries in their numeric order.
	Launch []LaunchEntry
}

// AppInfo queries and parses appinfo via `steamcmd +app_info_print`
// (anonymous, keyless). A missing common.oslist means Windows-only by Steam
// convention.
func (p *Provider) AppInfo(ctx context.Context, appID string) (*AppInfo, error) {
	if !numericID.MatchString(appID) {
		return nil, fmt.Errorf("app id %q: must be a numeric Steam id", appID)
	}
	bin, err := p.steamcmdPath(ctx)
	if err != nil {
		return nil, err
	}
	script := "@ShutdownOnFailedCommand 1\n@NoPromptForPassword 1\n" +
		loginCommand(p.opts.Credentials) +
		"app_info_update 1\napp_info_print " + appID + "\nquit\n"
	scriptPath, cleanup, err := writeRunScript(script)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	// Right after a fresh install/self-update the first app_info_print
	// often returns nothing; without the extra attempt the missing block
	// would read as "Windows-only" and trigger a bogus Proton prompt.
	for attempt := 1; ; attempt++ {
		out, err := p.run(withRunLabel(ctx, "appinfo "+appID), bin, "+runscript", scriptPath)
		if err != nil {
			return nil, retry.Mark(fmt.Errorf("querying appinfo for %s: %w", appID, err), classifyOutput(out))
		}
		if info := parseAppInfo(parseVDF(out), appID); info != nil {
			return info, nil
		}
		if attempt == 2 {
			return nil, retry.Mark(fmt.Errorf("appinfo for %s came back empty (steamcmd just self-updated?); please retry", appID), retry.Retryable)
		}
	}
}

// parseAppInfo returns nil when the output carries no appinfo block for the
// app at all — that is "no data", not "Windows-only".
func parseAppInfo(root map[string]any, appID string) *AppInfo {
	app := vdfMap(root[appID])
	if len(app) == 0 {
		return nil
	}
	info := &AppInfo{Sizes: map[string]int64{}}

	if osl := vdfStr(vdfMap(app["common"])["oslist"]); osl != "" {
		info.OSes = strings.Split(osl, ",")
	} else {
		info.OSes = []string{"windows"}
	}

	for _, dv := range vdfMap(app["depots"]) {
		depot := vdfMap(dv)
		if depot == nil {
			continue
		}
		size, err := strconv.ParseInt(vdfStr(vdfMap(vdfMap(depot["manifests"])["public"])["size"]), 10, 64)
		if err != nil || size <= 0 {
			continue
		}
		osl := vdfStr(vdfMap(depot["config"])["oslist"])
		for plat := range validPlatforms {
			if osl == "" || strings.Contains(osl, plat) {
				info.Sizes[plat] += size
			}
		}
	}

	launch := vdfMap(vdfMap(app["config"])["launch"])
	keys := make([]string, 0, len(launch))
	for k := range launch {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	for _, k := range keys {
		entry := vdfMap(launch[k])
		if entry == nil {
			continue
		}
		le := LaunchEntry{
			Executable: vdfStr(entry["executable"]),
			Arguments:  vdfStr(entry["arguments"]),
			Type:       strings.ToLower(vdfStr(entry["type"])),
		}
		if osl := vdfStr(vdfMap(entry["config"])["oslist"]); osl != "" {
			le.OSes = strings.Split(osl, ",")
		}
		if le.Executable != "" {
			info.Launch = append(info.Launch, le)
		}
	}
	return info
}
