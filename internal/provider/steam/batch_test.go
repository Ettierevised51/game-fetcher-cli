package steam

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

func TestBuildBatchRunScript(t *testing.T) {
	items := []provider.Item{
		{ID: "111", Kind: provider.KindMod, AppID: "730", InstallDir: "/srv/game"},
		{ID: "222", Kind: provider.KindMod, AppID: "730", InstallDir: "/srv/game"},
	}
	script := buildBatchRunScript(items, Credentials{}, provider.DownloadOptions{Validate: true})
	lines := strings.Split(strings.TrimSpace(script), "\n")

	// One failed item must not abort the rest of the batch.
	if lines[0] != "@ShutdownOnFailedCommand 0" {
		t.Errorf("batch script must disable ShutdownOnFailedCommand, got %q", lines[0])
	}
	forceIdx := lineIndex(t, lines, "force_install_dir")
	loginIdx := lineIndex(t, lines, "login")
	if forceIdx > loginIdx {
		t.Errorf("force_install_dir must precede login:\n%s", script)
	}
	for _, want := range []string{"workshop_download_item 730 111 validate", "workshop_download_item 730 222 validate"} {
		if !strings.Contains(script, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, script)
		}
	}
	if lines[len(lines)-1] != "quit" {
		t.Errorf("script must end with quit, got %q", lines[len(lines)-1])
	}
}

func TestParseBatchOutput(t *testing.T) {
	out := `Downloading item 111 ...
Success. Downloaded item 111 to "/srv/game/steamapps/workshop/content/730/111" (52428800 bytes)
Downloading item 222 ...
ERROR! Download item 222 failed (Failure).
ERROR! Download item 333 failed (No subscription).`
	v := parseBatchOutput(out)
	if v["111"] == nil || !v["111"].ok {
		t.Errorf("111 should be a success, got %+v", v["111"])
	}
	if v["222"] == nil || v["222"].ok || v["222"].reason != "Failure" {
		t.Errorf("222 should have failed with Failure, got %+v", v["222"])
	}
	if v["333"] == nil || v["333"].reason != "No subscription" {
		t.Errorf("333 should have failed with No subscription, got %+v", v["333"])
	}
}

// TestDownloadBatch: one steamcmd run, three items — a real success, a
// retryable failure and a fatal one — must come back with per-item errors of
// the right retry class.
func TestDownloadBatch(t *testing.T) {
	dir := t.TempDir()
	items := []provider.Item{
		{ID: "111", Kind: provider.KindMod, AppID: "730", InstallDir: dir},
		{ID: "222", Kind: provider.KindMod, AppID: "730", InstallDir: dir},
		{ID: "333", Kind: provider.KindMod, AppID: "730", InstallDir: dir},
	}
	var runs int
	p := New(Options{SteamcmdPath: fakeBin(t), Run: func(_ context.Context, _ string, args ...string) (string, error) {
		runs++
		// The successful item lands on disk.
		content := filepath.Join(dir, "steamapps", "workshop", "content", "730", "111")
		if err := os.MkdirAll(content, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(content, "mod.pak"), []byte("x"), 0o644); err != nil {
			return "", err
		}
		return `Success. Downloaded item 111 to "..." (123 bytes)
ERROR! Download item 222 failed (Failure).
ERROR! Download item 333 failed (No subscription).`, nil
	}})

	errs := p.DownloadBatch(context.Background(), items, provider.DownloadOptions{})
	if runs != 1 {
		t.Fatalf("expected a single steamcmd run for the batch, got %d", runs)
	}
	if errs[0] != nil {
		t.Errorf("item 111: unexpected error: %v", errs[0])
	}
	if errs[1] == nil || retry.ClassOf(errs[1]) != retry.Retryable || !strings.Contains(errs[1].Error(), "Failure") {
		t.Errorf("item 222: want a retryable Failure, got %v", errs[1])
	}
	if errs[2] == nil || retry.ClassOf(errs[2]) != retry.Fatal {
		t.Errorf("item 333: want a fatal error, got %v", errs[2])
	}
}

// TestDownloadBatchLoginFailure: with @ShutdownOnFailedCommand 0 a failed
// login still exits 0 and prints no per-item ERROR lines. A mod already on
// disk from an earlier run must NOT be reported as updated then.
func TestDownloadBatchLoginFailure(t *testing.T) {
	dir := t.TempDir()
	item := provider.Item{ID: "111", Kind: provider.KindMod, AppID: "730", InstallDir: dir}
	// Old version present on disk.
	content := filepath.Join(dir, "steamapps", "workshop", "content", "730", "111")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "old.pak"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		out  string
		want retry.Class
	}{
		"invalid password": {"Logging in user 'gabe' to Steam Public...FAILED login with result code Invalid Password", retry.Fatal},
		"rate limited":     {"FAILED login with result code Rate Limit Exceeded", retry.RateLimited},
		"anonymous down":   {"Connecting anonymously to Steam Public...FAILED (No Connection)", retry.Retryable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := New(Options{SteamcmdPath: fakeBin(t), Run: func(context.Context, string, ...string) (string, error) {
				return tc.out, nil
			}})
			errs := p.DownloadBatch(context.Background(), []provider.Item{item}, provider.DownloadOptions{})
			if errs[0] == nil {
				t.Fatal("stale on-disk mod must not count as success after a failed login")
			}
			if got := retry.ClassOf(errs[0]); got != tc.want {
				t.Errorf("class = %v, want %v (%v)", got, tc.want, errs[0])
			}
		})
	}
}

// TestDownloadBatchProcessDied: the process dies mid-batch (timeout, OOM).
// Items finished before the crash (Success verdict) stay successes; items
// after it must fail even when stale content from an earlier run is on disk.
func TestDownloadBatchProcessDied(t *testing.T) {
	dir := t.TempDir()
	items := []provider.Item{
		{ID: "111", Kind: provider.KindMod, AppID: "730", InstallDir: dir},
		{ID: "222", Kind: provider.KindMod, AppID: "730", InstallDir: dir},
	}
	for _, id := range []string{"111", "222"} { // 222 = stale from an earlier run
		content := filepath.Join(dir, "steamapps", "workshop", "content", "730", id)
		if err := os.MkdirAll(content, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(content, "mod.pak"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := New(Options{SteamcmdPath: fakeBin(t), Run: func(context.Context, string, ...string) (string, error) {
		return `Success. Downloaded item 111 to "..." (1 bytes)`, errors.New("signal: killed")
	}})
	errs := p.DownloadBatch(context.Background(), items, provider.DownloadOptions{})
	if errs[0] != nil {
		t.Errorf("item finished before the crash must stay a success, got %v", errs[0])
	}
	if errs[1] == nil {
		t.Error("stale on-disk item must not pass for success when the process died before reaching it")
	}
}

func TestDownloadBatchRejectsMixedDirs(t *testing.T) {
	items := []provider.Item{
		{ID: "111", Kind: provider.KindMod, AppID: "730", InstallDir: t.TempDir()},
		{ID: "222", Kind: provider.KindMod, AppID: "730", InstallDir: t.TempDir()},
	}
	p := New(Options{SteamcmdPath: "unused"})
	errs := p.DownloadBatch(context.Background(), items, provider.DownloadOptions{})
	for i, err := range errs {
		if err == nil {
			t.Errorf("item %d: expected an error for mixed install dirs", i)
		}
	}
}
