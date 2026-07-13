// Package webapi is a minimal client for the keyless part of the public
// Steam Web API: ISteamRemoteStorage/GetPublishedFileDetails and
// GetCollectionDetails (both verified empirically, 2026-07). Everything the
// tool needs works without API keys by design.
package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

const defaultBaseURL = "https://api.steampowered.com"

// Client queries the Steam Web API. The zero value uses the public API and
// a client with a sane timeout.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// defaultHTTPClient bounds every request: a blackholed connection must fail
// the attempt (and reach the retry policy) instead of hanging sync forever —
// command contexts carry no deadline.
var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return defaultHTTPClient
}

// do runs the request with the shared status classification: 429/5xx and
// transport errors are retryable, other non-200s fatal (spec section 5).
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return retry.Mark(fmt.Errorf("steam web api: %w", err), retry.Retryable)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return retry.Mark(fmt.Errorf("steam web api: %s", resp.Status), retry.Retryable)
	default:
		return retry.Mark(fmt.Errorf("steam web api: %s", resp.Status), retry.Fatal)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return retry.Mark(fmt.Errorf("steam web api: decoding response: %w", err), retry.Retryable)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

// Ping verifies Steam Web API reachability via the keyless
// ISteamWebAPIUtil/GetServerInfo (verified live, 2026-07).
func (c *Client) Ping(ctx context.Context) error {
	var out struct {
		ServerTime int64 `json:"servertime"`
	}
	if err := c.getJSON(ctx, c.baseURL()+"/ISteamWebAPIUtil/GetServerInfo/v1/", &out); err != nil {
		return err
	}
	if out.ServerTime == 0 {
		return fmt.Errorf("steam web api: unexpected GetServerInfo response")
	}
	return nil
}

func (c *Client) postForm(ctx context.Context, rawURL string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, out)
}
