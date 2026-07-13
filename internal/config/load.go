package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const (
	defaultSystemPath = "/etc/gamefetcher/config.yaml"
	// DefaultLocalPath is the local config layer; also where
	// --save-as-profile writes when no --config is given.
	DefaultLocalPath = "gamefetcher.yaml"
)

// Options controls where Load looks for each configuration layer.
// The zero value means "use the default locations".
type Options struct {
	// SystemPath overrides the system layer (/etc/gamefetcher/config.yaml).
	SystemPath string
	// UserPath overrides the user layer
	// ($XDG_CONFIG_HOME/gamefetcher/config.yaml).
	UserPath string
	// LocalPath overrides the local layer (./gamefetcher.yaml).
	LocalPath string
	// ConfigPath is the --config flag value. When set it replaces the local
	// layer and, unlike the default-location layers, must exist.
	ConfigPath string
	// Getenv reads the environment layer; defaults to os.Getenv.
	Getenv func(string) string
	// Overrides is the CLI-flag layer, deep-merged over everything else.
	// Keys use the same names as the YAML schema.
	Overrides map[string]any
}

// envBindings maps environment variables to top-level config keys.
var envBindings = []struct {
	env  string
	key  string
	kind string // "string", "int" or "bool"
}{
	{"GAMEFETCHER_PARALLELISM", "parallelism", "int"},
	{"GAMEFETCHER_AUTO_INSTALL_STEAMCMD", "auto_install_steamcmd", "bool"},
	{"GAMEFETCHER_DOWNLOAD_RATE_LIMIT", "download_rate_limit", "string"},
	{"GAMEFETCHER_STEAMCMD_PATH", "steamcmd_path", "string"},
	{"GAMEFETCHER_STATE_PATH", "state_path", "string"},
}

// Load merges all configuration layers and returns the resolved Config.
func Load(opts Options) (*Config, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	localPath := cmp.Or(opts.ConfigPath, opts.LocalPath, DefaultLocalPath)
	layers := []struct {
		path     string
		required bool
	}{
		{cmp.Or(opts.SystemPath, defaultSystemPath), false},
		{opts.userPath(), false},
		{localPath, opts.ConfigPath != ""},
	}

	merged := defaults()
	for _, layer := range layers {
		if layer.path == "" {
			continue
		}
		if _, err := os.Stat(layer.path); err != nil {
			if os.IsNotExist(err) && !layer.required {
				continue
			}
			return nil, fmt.Errorf("config file %s: %w", layer.path, err)
		}
		data, err := loadLayer(layer.path)
		if err != nil {
			return nil, err
		}
		merged = mergeMaps(merged, data)
	}

	envLayer, err := envOverrides(getenv)
	if err != nil {
		return nil, err
	}
	merged = mergeMaps(merged, envLayer)

	if len(opts.Overrides) > 0 {
		merged = mergeMaps(merged, opts.Overrides)
	}

	if err := resolveExtends(merged); err != nil {
		return nil, err
	}
	cfg, err := decode(merged)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (o Options) userPath() string {
	if o.UserPath != "" {
		return o.UserPath
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gamefetcher", "config.yaml")
}

func envOverrides(getenv func(string) string) (map[string]any, error) {
	out := map[string]any{}
	for _, b := range envBindings {
		raw := getenv(b.env)
		if raw == "" {
			continue
		}
		switch b.kind {
		case "int":
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: expected an integer, got %q", b.env, raw)
			}
			out[b.key] = n
		case "bool":
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: expected a boolean, got %q", b.env, raw)
			}
			out[b.key] = v
		default:
			out[b.key] = raw
		}
	}
	return out, nil
}
