package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
)

func confirmYes(string) (bool, error)  { return true, nil }
func confirmNo(string) (bool, error)   { return false, nil }
func confirmBoom(string) (bool, error) { return false, errors.New("no terminal") }

func readProfiles(t *testing.T, path string) map[string]any {
	t.Helper()
	raw := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	profiles, _ := raw["profiles"].(map[string]any)
	return profiles
}

func TestSaveProfileCreatesFileAndProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gamefetcher.yaml")
	var out strings.Builder
	p := config.Profile{AppID: 258550, InstallDir: "/srv/rust", Mods: []int64{111}}
	if err := saveProfile(path, "rust", p, confirmBoom, &out); err != nil {
		t.Fatal(err) // no confirmation needed for a new profile
	}
	profiles := readProfiles(t, path)
	block, _ := profiles["rust"].(map[string]any)
	if block["app_id"] != 258550 || block["install_dir"] != "/srv/rust" {
		t.Fatalf("saved block = %v", block)
	}
	// The config layer must accept the written file.
	cfg, err := config.Load(config.Options{
		SystemPath: filepath.Join(t.TempDir(), "absent.yaml"),
		UserPath:   filepath.Join(t.TempDir(), "absent.yaml"),
		LocalPath:  path,
		Getenv:     func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("written config does not load back: %v", err)
	}
	if cfg.Profiles["rust"].AppID != 258550 {
		t.Fatalf("roundtrip lost data: %+v", cfg.Profiles["rust"])
	}
}

func TestSaveProfilePreservesOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gamefetcher.yaml")
	os.WriteFile(path, []byte("parallelism: 7\nprofiles:\n  other:\n    app_id: 1\n    install_dir: /x\n"), 0o644)
	var out strings.Builder
	err := saveProfile(path, "rust", config.Profile{AppID: 258550, InstallDir: "/srv/rust"}, confirmBoom, &out)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "parallelism: 7") {
		t.Errorf("unrelated key lost:\n%s", data)
	}
	if profiles := readProfiles(t, path); profiles["other"] == nil {
		t.Errorf("existing profile lost:\n%s", data)
	}
}

func TestSaveProfileOverwriteNeedsConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gamefetcher.yaml")
	var out strings.Builder
	old := config.Profile{AppID: 258550, InstallDir: "/srv/old"}
	if err := saveProfile(path, "rust", old, confirmBoom, &out); err != nil {
		t.Fatal(err)
	}
	updated := config.Profile{AppID: 258550, InstallDir: "/srv/new"}

	// Declined → error, file untouched.
	if err := saveProfile(path, "rust", updated, confirmNo, &out); err == nil {
		t.Fatal("declined overwrite must be an error")
	}
	if block, _ := readProfiles(t, path)["rust"].(map[string]any); block["install_dir"] != "/srv/old" {
		t.Fatalf("declined overwrite still changed the file: %v", block)
	}

	// The prompt must have been preceded by a diff.
	out.Reset()
	if err := saveProfile(path, "rust", updated, confirmYes, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "- install_dir: /srv/old") || !strings.Contains(out.String(), "+ install_dir: /srv/new") {
		t.Errorf("no diff shown:\n%s", out.String())
	}
	if block, _ := readProfiles(t, path)["rust"].(map[string]any); block["install_dir"] != "/srv/new" {
		t.Fatalf("confirmed overwrite not applied: %v", block)
	}
}

func TestSaveProfileIdenticalIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gamefetcher.yaml")
	var out strings.Builder
	p := config.Profile{AppID: 1007, InstallDir: "/srv/sdk"}
	if err := saveProfile(path, "sdk", p, confirmBoom, &out); err != nil {
		t.Fatal(err)
	}
	// Same content again: no confirmation, no error, message says up to date.
	out.Reset()
	if err := saveProfile(path, "sdk", p, confirmBoom, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Errorf("unexpected output: %s", out.String())
	}
}
