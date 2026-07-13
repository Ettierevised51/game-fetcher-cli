// Package config implements the layered YAML configuration from spec.md,
// section 3.
//
// Layers, each overriding the previous one:
//
//  1. built-in defaults
//  2. /etc/gamefetcher/config.yaml (system)
//  3. $XDG_CONFIG_HOME/gamefetcher/config.yaml (user)
//  4. ./gamefetcher.yaml, or the --config path (which then must exist)
//  5. environment variables (GAMEFETCHER_*)
//  6. CLI flags (Options.Overrides)
//
// Each file layer also merges in conf.d/*.yaml drop-ins located next to it
// and files named by its `include` directive. Both override the file's own
// values: drop-ins first (lexicographic order), then explicit includes in
// the order they are listed.
//
// Profile inheritance (`extends`) is resolved in a separate pass after all
// layers are merged, so a profile may extend one defined in another file or
// layer. A dangling reference is a hard error naming the missing profile —
// never a silent fall back to defaults.
package config

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the fully merged and resolved configuration: all layers applied,
// includes expanded, profile inheritance flattened.
type Config struct {
	AutoInstallSteamcmd bool   `yaml:"auto_install_steamcmd"`
	Parallelism         int    `yaml:"parallelism"`
	DownloadRateLimit   string `yaml:"download_rate_limit"`
	SteamcmdPath        string `yaml:"steamcmd_path"`
	StatePath           string `yaml:"state_path"`
	// SteamUser is the account for non-anonymous downloads. Only the
	// username — passwords never live in YAML (spec section 10).
	SteamUser string `yaml:"steam_user"`

	// AppListMaxAge is how long the cached full app list stays fresh
	// (spec section 7: refresh every N days). Zero means the built-in
	// default of 7 days.
	AppListMaxAge Duration `yaml:"app_list_max_age"`
	// AppListURLs overrides the open app-id databases the index is built
	// from (default: applist.DefaultSources).
	AppListURLs []string `yaml:"app_list_urls"`

	Retry RetryConfig `yaml:"retry"`

	Profiles map[string]Profile  `yaml:"profiles"`
	Groups   map[string][]string `yaml:"groups"`
}

// Profile configures one game/server.
type Profile struct {
	AppID int `yaml:"app_id"`
	// BaseAppID is the base/client game app id used for workshop mods
	// (their consumer_app_id); a dedicated-server app id does not work for
	// mod downloads (spec section 7). Defaults to AppID when zero.
	BaseAppID  int     `yaml:"base_app_id"`
	InstallDir string  `yaml:"install_dir"`
	Mods       []int64 `yaml:"mods"`
	// Collections are workshop collection ids, expanded live on every
	// run/sync — items added to the collection flow in automatically.
	Collections []int64 `yaml:"collections"`
	// Platform pins the build to download ("windows", "linux", "macos").
	// Empty means: use the native build; when none exists but a Windows
	// build does, the tool offers to download that one and remembers the
	// answer in the state store.
	Platform string `yaml:"platform"`
	// Branch selects a beta branch of the app (e.g. Rust "staging");
	// BranchPassword unlocks a password-protected one. Branch passwords are
	// distribution passwords shared by the developer, not account secrets.
	Branch         string `yaml:"branch"`
	BranchPassword string `yaml:"branch_password"`
}

// RetryConfig tunes backoff separately per operation kind (spec section 5).
type RetryConfig struct {
	Login    Backoff `yaml:"login"`
	WebAPI   Backoff `yaml:"web_api"`
	Download Backoff `yaml:"download"`
}

// Backoff overrides parts of a retry policy; zero fields keep the built-in
// defaults.
type Backoff struct {
	MaxAttempts    int      `yaml:"max_attempts"`
	BaseDelay      Duration `yaml:"base_delay"`
	MaxDelay       Duration `yaml:"max_delay"`
	RateLimitDelay Duration `yaml:"rate_limit_delay"`
}

// Duration parses YAML strings like "30s", "5m" or "7d" (days are not in
// time.ParseDuration, but the spec talks in days for app_list_max_age).
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"30s\" or \"7d\"")
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return fmt.Errorf("duration %q: bad day count", s)
		}
		*d = Duration(time.Duration(n * 24 * float64(time.Hour)))
		return d.validate(s)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return d.validate(s)
}

func (d Duration) validate(s string) error {
	if d < 0 {
		return fmt.Errorf("duration %q must not be negative", s)
	}
	return nil
}

// defaults is layer 1: built-in defaults.
func defaults() map[string]any {
	return map[string]any{
		"parallelism":           4,
		"auto_install_steamcmd": false,
	}
}

// decode converts the merged tree into a typed Config, rejecting unknown keys.
func decode(m map[string]any) (*Config, error) {
	buf, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(buf))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	for _, group := range slices.Sorted(maps.Keys(c.Groups)) {
		for _, member := range c.Groups[group] {
			if _, ok := c.Profiles[member]; !ok {
				return fmt.Errorf("group %q references unknown profile %q", group, member)
			}
		}
	}
	return nil
}
