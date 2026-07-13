package steam

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
)

// Shaped like real app_info_print output (verified live on Space Engineers
// DS): the depot section carries a decoy linux oslist that must NOT count.
const windowsOnlyAppInfo = `AppID : 298740, change number : 123
"298740"
{
	"common"
	{
		"name"		"Space Engineers Dedicated Server"
		"type"		"Tool"
		"oslist"		"windows"
	}
	"config"
	{
		"installdir"		"SpaceEngineersDedicatedServer"
	}
	"depots"
	{
		"1006"
		{
			"config"
			{
				"oslist"		"linux"
			}
		}
	}
}
`

func TestParseAppInfoOSList(t *testing.T) {
	if got := parseAppInfo(parseVDF(windowsOnlyAppInfo), "298740").OSes; len(got) != 1 || got[0] != "windows" {
		t.Fatalf("windows-only app: %v (depot oslist must not leak in)", got)
	}
	multi := strings.Replace(windowsOnlyAppInfo, `"oslist"		"windows"`, `"oslist"		"windows,macos,linux"`, 1)
	if got := parseAppInfo(parseVDF(multi), "298740").OSes; len(got) != 3 || got[2] != "linux" {
		t.Fatalf("multi-os app: %v", got)
	}
	// No oslist in common at all → Windows-only by Steam convention.
	none := strings.Replace(windowsOnlyAppInfo, `"oslist"		"windows"`, "", 1)
	if got := parseAppInfo(parseVDF(none), "298740").OSes; len(got) != 1 || got[0] != "windows" {
		t.Fatalf("missing oslist: %v, want the windows default", got)
	}
}

func TestAppInfoQuery(t *testing.T) {
	var script string
	p := New(Options{SteamcmdPath: fakeBin(t), Run: func(_ context.Context, _ string, args ...string) (string, error) {
		raw, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return "", err
		}
		script = string(raw)
		return windowsOnlyAppInfo, nil
	}})
	info, err := p.AppInfo(context.Background(), "298740")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.OSes) != 1 || info.OSes[0] != "windows" {
		t.Fatalf("oses = %v", info.OSes)
	}
	if !strings.Contains(script, "app_info_update 1") || !strings.Contains(script, "app_info_print 298740") {
		t.Fatalf("runscript lacks appinfo commands:\n%s", script)
	}
	if _, err := p.AppInfo(context.Background(), "298740; quit"); err == nil {
		t.Fatal("non-numeric id must be rejected")
	}
}

func TestRunScriptPlatformDirective(t *testing.T) {
	item := provider.Item{ID: "298740", Kind: provider.KindApp, InstallDir: "/srv/se", Platform: "windows"}
	script, err := buildRunScript(item, Credentials{}, provider.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(script), "\n")
	platIdx := lineIndex(t, lines, "@sSteamCmdForcePlatformType windows")
	forceIdx := lineIndex(t, lines, "force_install_dir")
	loginIdx := lineIndex(t, lines, "login")
	if !(platIdx < forceIdx && forceIdx < loginIdx) {
		t.Fatalf("directive order wrong:\n%s", script)
	}

	item.Platform = "amiga"
	if err := validateItem(item); err == nil {
		t.Fatal("bogus platform must be rejected")
	}
}
