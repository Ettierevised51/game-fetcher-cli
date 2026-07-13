package steam

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

const linuxAppInfo = `"896660"
{
	"common"
	{
		"oslist"		"windows,linux"
	}
}
`

// Right after a steamcmd self-update the first app_info_print returns no
// appinfo block; that must trigger a second attempt — not read as
// "Windows-only" (the bug: a bogus Proton prompt for a Linux-native app).
func TestAppInfoEmptyFirstOutputRetries(t *testing.T) {
	runs := 0
	p := New(Options{SteamcmdPath: fakeBin(t), Run: func(context.Context, string, ...string) (string, error) {
		runs++
		if runs == 1 {
			return "[100%] Download Complete.\n[----] Download complete.\n", nil
		}
		return linuxAppInfo, nil
	}})
	info, err := p.AppInfo(context.Background(), "896660")
	if err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Errorf("want exactly 2 attempts, got %d", runs)
	}
	if !slices.Contains(info.OSes, "linux") {
		t.Errorf("OSes = %v, want linux included", info.OSes)
	}
}

func TestAppInfoEmptyTwiceIsRetryableError(t *testing.T) {
	p := New(Options{SteamcmdPath: fakeBin(t), Run: func(context.Context, string, ...string) (string, error) {
		return "still nothing", nil
	}})
	_, err := p.AppInfo(context.Background(), "896660")
	if err == nil || retry.ClassOf(err) != retry.Retryable || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want a retryable empty-appinfo error, got %v", err)
	}
}

func TestParseAppInfoMissingBlockIsNil(t *testing.T) {
	if info := parseAppInfo(parseVDF("no appinfo here"), "1"); info != nil {
		t.Errorf("no appinfo block must be nil (no data), got %+v", info)
	}
}
