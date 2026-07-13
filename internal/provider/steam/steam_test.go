package steam

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

func appItem(dir string) provider.Item {
	return provider.Item{ID: "730", Kind: provider.KindApp, InstallDir: dir}
}

func modItem(dir string) provider.Item {
	return provider.Item{ID: "987654", Kind: provider.KindMod, AppID: "730", InstallDir: dir}
}

// fakeBin returns an existing file usable as Options.SteamcmdPath.
func fakeBin(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "steamcmd.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func lineIndex(t *testing.T, lines []string, prefix string) int {
	t.Helper()
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	t.Fatalf("no line with prefix %q in script:\n%s", prefix, strings.Join(lines, "\n"))
	return -1
}

func TestRunScriptCommandOrder(t *testing.T) {
	script, err := buildRunScript(appItem("/srv/cs2"), Credentials{Username: "user", Password: "pa ss"}, provider.DownloadOptions{Validate: true})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(script), "\n")

	forceIdx := lineIndex(t, lines, "force_install_dir")
	loginIdx := lineIndex(t, lines, "login")
	updateIdx := lineIndex(t, lines, "app_update")
	if !(forceIdx < loginIdx && loginIdx < updateIdx) {
		t.Fatalf("wrong command order (force_install_dir=%d, login=%d, app_update=%d):\n%s",
			forceIdx, loginIdx, updateIdx, script)
	}
	if lines[len(lines)-1] != "quit" {
		t.Errorf("script must end with quit, got %q", lines[len(lines)-1])
	}
	if !strings.Contains(script, `app_update 730 validate`) {
		t.Errorf("missing validate: %s", script)
	}
	if !strings.Contains(script, `login "user" "pa ss"`) {
		t.Errorf("credentials must be quoted: %s", script)
	}
}

func TestRunScriptAnonymousMod(t *testing.T) {
	script, err := buildRunScript(modItem("/srv/game"), Credentials{}, provider.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "login anonymous\n") {
		t.Errorf("expected anonymous login: %s", script)
	}
	// Mods are fetched via the base game app id, then the workshop item id.
	if !strings.Contains(script, "workshop_download_item 730 987654\n") {
		t.Errorf("expected workshop_download_item 730 987654: %s", script)
	}
}

func TestValidateItemRejectsNonNumericID(t *testing.T) {
	item := appItem(t.TempDir())
	item.ID = "730; quit"
	if err := validateItem(item); err == nil {
		t.Fatal("expected an error for a non-numeric id")
	}
}

func TestPrepareInstallDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "srv", "game")
	if err := prepareInstallDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "steamapps")); err != nil {
		t.Fatalf("steamapps was not pre-created: %v", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("write-permission check is meaningless as root")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	err := prepareInstallDir(filepath.Join(ro, "game"))
	if err == nil {
		t.Fatal("expected an error for a read-only parent directory")
	}
	// Permission problems don't heal by retrying — they must be fatal and
	// carry the fix recipe (issues.txt #2).
	if retry.ClassOf(err) != retry.Fatal {
		t.Errorf("permission error must be Fatal, got class %v: %v", retry.ClassOf(err), err)
	}
	if !strings.Contains(err.Error(), "sudo mkdir -p") || !strings.Contains(err.Error(), "sudo chown") {
		t.Errorf("permission error must include the sudo hint, got: %v", err)
	}
}

// TestDownloadVerifiesResult: a zero exit code without an appmanifest on disk
// must be treated as a failure, and vice versa.
func TestDownloadVerifiesResult(t *testing.T) {
	ctx := context.Background()

	t.Run("exit 0 without manifest is an error", func(t *testing.T) {
		item := appItem(filepath.Join(t.TempDir(), "game"))
		p := New(Options{SteamcmdPath: fakeBin(t), Run: func(context.Context, string, ...string) (string, error) {
			return "", nil // steamcmd "succeeded" but wrote nothing
		}})
		err := p.Download(ctx, item, provider.DownloadOptions{})
		if err == nil || !strings.Contains(err.Error(), "appmanifest_730.acf") {
			t.Fatalf("expected a verification error naming the manifest, got: %v", err)
		}
	})

	t.Run("manifest present means success", func(t *testing.T) {
		item := appItem(filepath.Join(t.TempDir(), "game"))
		var script string
		p := New(Options{SteamcmdPath: fakeBin(t), Run: func(_ context.Context, _ string, args ...string) (string, error) {
			raw, err := os.ReadFile(args[len(args)-1])
			if err != nil {
				return "", err
			}
			script = string(raw)
			return "", os.WriteFile(manifestPath(item), []byte(`"appid" "730"`), 0o644)
		}})
		if err := p.Download(ctx, item, provider.DownloadOptions{}); err != nil {
			t.Fatal(err)
		}
		// The runscript handed to steamcmd must have had the right order.
		if f, l := strings.Index(script, "force_install_dir"), strings.Index(script, "login"); f == -1 || l == -1 || f > l {
			t.Errorf("force_install_dir must precede login in the executed script:\n%s", script)
		}
	})
}

func TestIsInstalledMod(t *testing.T) {
	ctx := context.Background()
	item := modItem(t.TempDir())
	p := New(Options{SteamcmdPath: "unused"})

	ok, err := p.IsInstalled(ctx, item)
	if err != nil || ok {
		t.Fatalf("empty install dir: got ok=%v err=%v, want false nil", ok, err)
	}

	content := workshopDir(item)
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "mod.pak"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err = p.IsInstalled(ctx, item)
	if err != nil || !ok {
		t.Fatalf("populated workshop dir: got ok=%v err=%v, want true nil", ok, err)
	}
}

func TestResolveCredentials(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	devNull, err := os.Open(os.DevNull) // not a terminal
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	t.Run("nothing set means anonymous", func(t *testing.T) {
		creds, err := ResolveCredentials("", "", "", env(nil), devNull)
		if err != nil || !creds.Anonymous() {
			t.Fatalf("got %+v, %v; want anonymous", creds, err)
		}
	})
	t.Run("env supplies both", func(t *testing.T) {
		creds, err := ResolveCredentials("", "", "", env(map[string]string{
			envUsername: "gabe", envPassword: "s3cret",
		}), devNull)
		if err != nil || creds.Username != "gabe" || creds.Password != "s3cret" {
			t.Fatalf("got %+v, %v", creds, err)
		}
	})
	t.Run("explicit value beats env", func(t *testing.T) {
		creds, err := ResolveCredentials("flaguser", "flagpass", "", env(map[string]string{
			envUsername: "envuser", envPassword: "envpass",
		}), devNull)
		if err != nil || creds.Username != "flaguser" || creds.Password != "flagpass" {
			t.Fatalf("got %+v, %v", creds, err)
		}
	})
	t.Run("no password and no terminal is an error", func(t *testing.T) {
		_, err := ResolveCredentials("gabe", "", "", env(nil), devNull)
		if err == nil || !strings.Contains(err.Error(), envPassword) {
			t.Fatalf("expected an error pointing at %s, got: %v", envPassword, err)
		}
	})
}
