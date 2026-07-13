package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// absent returns a path that is guaranteed not to exist, to pin a layer
// to "not present" instead of its real default location.
func absent(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "absent.yaml")
}

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// noEnv pins the environment layer to empty so the real environment
// cannot leak into tests.
var noEnv = envFrom(nil)

func mustLoad(t *testing.T, opts Options) *Config {
	t.Helper()
	if opts.Getenv == nil {
		opts.Getenv = noEnv
	}
	cfg, err := Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func wantErrContaining(t *testing.T, err error, substrings ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", substrings)
	}
	for _, s := range substrings {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("error %q does not contain %q", err, s)
		}
	}
}

// TestLayerPrecedence peels layers off the top of the stack one by one and
// checks that the winner for the contested key moves down accordingly.
func TestLayerPrecedence(t *testing.T) {
	dir := t.TempDir()
	system := write(t, filepath.Join(dir, "system", "config.yaml"),
		"parallelism: 1\ndownload_rate_limit: 1M\n")
	user := write(t, filepath.Join(dir, "user", "config.yaml"),
		"parallelism: 2\nsteamcmd_path: /steamcmd\n")
	local := write(t, filepath.Join(dir, "local", "gamefetcher.yaml"),
		"parallelism: 3\nstate_path: /state.json\n")
	env := envFrom(map[string]string{
		"GAMEFETCHER_PARALLELISM":           "5",
		"GAMEFETCHER_AUTO_INSTALL_STEAMCMD": "true",
	})
	overrides := map[string]any{"parallelism": 6}

	cases := []struct {
		name string
		opts Options
		want int
	}{
		{"flags beat env", Options{SystemPath: system, UserPath: user, LocalPath: local, Getenv: env, Overrides: overrides}, 6},
		{"env beats local", Options{SystemPath: system, UserPath: user, LocalPath: local, Getenv: env}, 5},
		{"local beats user", Options{SystemPath: system, UserPath: user, LocalPath: local}, 3},
		{"user beats system", Options{SystemPath: system, UserPath: user, LocalPath: absent(t)}, 2},
		{"system beats defaults", Options{SystemPath: system, UserPath: absent(t), LocalPath: absent(t)}, 1},
		{"built-in default", Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: absent(t)}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mustLoad(t, tc.opts)
			if cfg.Parallelism != tc.want {
				t.Errorf("Parallelism = %d, want %d", cfg.Parallelism, tc.want)
			}
		})
	}

	// The full stack must still see values that only lower layers set:
	// overriding is per-key, not per-file.
	cfg := mustLoad(t, cases[0].opts)
	if cfg.DownloadRateLimit != "1M" {
		t.Errorf("DownloadRateLimit = %q, want %q (from system layer)", cfg.DownloadRateLimit, "1M")
	}
	if cfg.SteamcmdPath != "/steamcmd" {
		t.Errorf("SteamcmdPath = %q, want %q (from user layer)", cfg.SteamcmdPath, "/steamcmd")
	}
	if cfg.StatePath != "/state.json" {
		t.Errorf("StatePath = %q, want %q (from local layer)", cfg.StatePath, "/state.json")
	}
	if !cfg.AutoInstallSteamcmd {
		t.Error("AutoInstallSteamcmd = false, want true (from env layer)")
	}
}

// TestProfileDeepMerge checks that a later layer overrides individual profile
// fields instead of replacing the whole profile.
func TestProfileDeepMerge(t *testing.T) {
	dir := t.TempDir()
	system := write(t, filepath.Join(dir, "system.yaml"), `
profiles:
  rust:
    app_id: 258550
    install_dir: /srv/rust
`)
	local := write(t, filepath.Join(dir, "local.yaml"), `
profiles:
  rust:
    install_dir: /opt/rust
    mods: [111, 222]
`)
	cfg := mustLoad(t, Options{SystemPath: system, UserPath: absent(t), LocalPath: local})
	rust := cfg.Profiles["rust"]
	if rust.AppID != 258550 {
		t.Errorf("AppID = %d, want 258550 (must survive from the system layer)", rust.AppID)
	}
	if rust.InstallDir != "/opt/rust" {
		t.Errorf("InstallDir = %q, want /opt/rust (local override)", rust.InstallDir)
	}
	if len(rust.Mods) != 2 || rust.Mods[0] != 111 || rust.Mods[1] != 222 {
		t.Errorf("Mods = %v, want [111 222]", rust.Mods)
	}
}

func TestIncludeOrder(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "gamefetcher.yaml"), `
include: [inc/a.yaml, inc/b.yaml]
parallelism: 1
state_path: /main
`)
	write(t, filepath.Join(dir, "conf.d", "10-first.yaml"), "parallelism: 2\nsteamcmd_path: /confd\n")
	write(t, filepath.Join(dir, "conf.d", "20-second.yaml"), "parallelism: 3\n")
	write(t, filepath.Join(dir, "inc", "a.yaml"), "parallelism: 4\ndownload_rate_limit: 9M\n")
	write(t, filepath.Join(dir, "inc", "b.yaml"), "parallelism: 5\n")

	cfg := mustLoad(t, Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: main})
	// Own values < conf.d drop-ins (sorted) < explicit includes (listed order).
	if cfg.Parallelism != 5 {
		t.Errorf("Parallelism = %d, want 5 (last explicit include wins)", cfg.Parallelism)
	}
	if cfg.StatePath != "/main" {
		t.Errorf("StatePath = %q, want /main (uncontested key from the main file)", cfg.StatePath)
	}
	if cfg.SteamcmdPath != "/confd" {
		t.Errorf("SteamcmdPath = %q, want /confd (uncontested key from conf.d)", cfg.SteamcmdPath)
	}
	if cfg.DownloadRateLimit != "9M" {
		t.Errorf("DownloadRateLimit = %q, want 9M (uncontested key from include)", cfg.DownloadRateLimit)
	}
}

func TestIncludeMissingFile(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "gamefetcher.yaml"), "include: nope.yaml\n")
	_, err := Load(Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: main, Getenv: noEnv})
	wantErrContaining(t, err, "nope.yaml")
}

func TestIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "gamefetcher.yaml"), "include: other.yaml\n")
	write(t, filepath.Join(dir, "other.yaml"), "include: gamefetcher.yaml\n")
	_, err := Load(Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: main, Getenv: noEnv})
	wantErrContaining(t, err, "include cycle")
}

func TestExtendsChain(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "gamefetcher.yaml"), `
profiles:
  base:
    app_id: 100
    install_dir: /base
  child:
    extends: base
    install_dir: /child
  grand:
    extends: child
    mods: [1, 2]
`)
	cfg := mustLoad(t, Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: main})

	grand := cfg.Profiles["grand"]
	if grand.AppID != 100 || grand.InstallDir != "/child" || len(grand.Mods) != 2 {
		t.Errorf("grand = %+v, want app_id 100, install_dir /child, 2 mods", grand)
	}
	child := cfg.Profiles["child"]
	if child.AppID != 100 || child.InstallDir != "/child" || child.Mods != nil {
		t.Errorf("child = %+v, want app_id 100, install_dir /child, no mods", child)
	}
	base := cfg.Profiles["base"]
	if base.InstallDir != "/base" || base.Mods != nil {
		t.Errorf("base = %+v must not inherit anything from children", base)
	}
}

// TestExtendsAcrossLayers: extends resolves after the full tree is merged,
// so a local profile may extend one defined in the system layer.
func TestExtendsAcrossLayers(t *testing.T) {
	dir := t.TempDir()
	system := write(t, filepath.Join(dir, "system.yaml"), `
profiles:
  base:
    app_id: 730
    install_dir: /srv/base
`)
	local := write(t, filepath.Join(dir, "local.yaml"), `
profiles:
  cs2:
    extends: base
    install_dir: /srv/cs2
`)
	cfg := mustLoad(t, Options{SystemPath: system, UserPath: absent(t), LocalPath: local})
	cs2 := cfg.Profiles["cs2"]
	if cs2.AppID != 730 || cs2.InstallDir != "/srv/cs2" {
		t.Errorf("cs2 = %+v, want app_id 730 from system base, local install_dir", cs2)
	}
}

func TestExtendsUnknownProfile(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "gamefetcher.yaml"), `
profiles:
  web:
    extends: ghost
`)
	_, err := Load(Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: main, Getenv: noEnv})
	wantErrContaining(t, err, `profile "web" extends unknown profile "ghost"`)
}

func TestExtendsCycle(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "gamefetcher.yaml"), `
profiles:
  a:
    extends: b
  b:
    extends: a
`)
	_, err := Load(Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: main, Getenv: noEnv})
	wantErrContaining(t, err, "extends cycle")
}

func TestGroups(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "gamefetcher.yaml"), `
profiles:
  a: {app_id: 1}
  b: {app_id: 2}
groups:
  all: [a, b]
`)
	cfg := mustLoad(t, Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: main})
	if got := cfg.Groups["all"]; len(got) != 2 {
		t.Errorf("group all = %v, want [a b]", got)
	}

	bad := write(t, filepath.Join(dir, "bad.yaml"), `
groups:
  all: [ghost]
`)
	_, err := Load(Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: bad, Getenv: noEnv})
	wantErrContaining(t, err, `group "all" references unknown profile "ghost"`)
}

func TestExplicitConfigPathMustExist(t *testing.T) {
	_, err := Load(Options{
		SystemPath: absent(t),
		UserPath:   absent(t),
		ConfigPath: filepath.Join(t.TempDir(), "nope.yaml"),
		Getenv:     noEnv,
	})
	wantErrContaining(t, err, "nope.yaml")
}

func TestUnknownKeyRejected(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "gamefetcher.yaml"), "paralelism: 3\n") // typo
	_, err := Load(Options{SystemPath: absent(t), UserPath: absent(t), LocalPath: main, Getenv: noEnv})
	wantErrContaining(t, err, "paralelism")
}

func TestEnvInvalidValue(t *testing.T) {
	_, err := Load(Options{
		SystemPath: absent(t),
		UserPath:   absent(t),
		LocalPath:  absent(t),
		Getenv:     envFrom(map[string]string{"GAMEFETCHER_PARALLELISM": "many"}),
	})
	wantErrContaining(t, err, "GAMEFETCHER_PARALLELISM")
}
