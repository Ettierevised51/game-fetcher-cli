package applist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DefaultSources are the open app-id databases the index is built from,
// merged in order (later sources win on name). By the owner's decision the
// default is exactly one source — the project's own database; extra or
// replacement URLs go through the app_list_urls config key. SteamDB has no
// public API and forbids scraping.
var DefaultSources = []string{
	// The project's own PICS database (a separate repo: dumper, workflow and
	// data live there): the full steamcmd-installable set — games AND every
	// dedicated-server tool — bootstrapped from a complete id-space scan and
	// refreshed every 7 hours via the PICS changes feed.
	"https://raw.githubusercontent.com/Austrum-lab/steam-appdb/master/data/applist.json",
}

const maxDumpSize = 64 << 20

// FetchOpen downloads and merges the open app-id databases. Both dump
// formats are handled: the classic {"applist":{"apps":[…]}} shape and a
// flat array of {appid,name}. A failing source is skipped with a logf
// warning; an error is returned only when every source fails.
func FetchOpen(ctx context.Context, client *http.Client, urls []string, logf func(string, ...any)) ([]App, error) {
	if client == nil {
		// Bounded so a stalled source fails the attempt instead of hanging
		// search forever; the dump is a few MB, minutes are plenty.
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	merged := map[int]string{}
	var errs []error
	for _, u := range urls {
		apps, err := fetchDump(ctx, client, u)
		if err != nil {
			logf("warning: app list source %s: %v", u, err)
			errs = append(errs, fmt.Errorf("%s: %w", u, err))
			continue
		}
		for _, a := range apps {
			if a.Name != "" {
				merged[a.AppID] = a.Name
			}
		}
	}
	if len(merged) == 0 {
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return nil, errors.New("no app list sources configured")
	}
	out := make([]App, 0, len(merged))
	for id, name := range merged {
		out = append(out, App{AppID: id, Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	return out, nil
}

func fetchDump(ctx context.Context, client *http.Client, rawURL string) ([]App, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDumpSize))
	if err != nil {
		return nil, err
	}
	return parseDump(raw)
}

// parseDump sniffs the dump format by its first JSON token.
func parseDump(raw []byte) ([]App, error) {
	trimmed := strings.TrimLeft(string(raw), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var apps []App
		if err := json.Unmarshal(raw, &apps); err != nil {
			return nil, fmt.Errorf("flat app list: %w", err)
		}
		return apps, nil
	}
	var wrapped struct {
		AppList struct {
			Apps []App `json:"apps"`
		} `json:"applist"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("wrapped app list: %w", err)
	}
	if wrapped.AppList.Apps == nil {
		return nil, errors.New("unrecognized app list format")
	}
	return wrapped.AppList.Apps, nil
}
