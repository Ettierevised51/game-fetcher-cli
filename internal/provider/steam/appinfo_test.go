package steam

import (
	"strings"
	"testing"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
)

// Shaped like real app_info_print output (verified live on Valheim DS):
// launch entries per OS, depot sizes, plus steamcmd console noise around it.
const richAppInfo = `Steam Console Client (c) Valve Corporation - version 123
AppID : 896660, change number : 456
"896660"
{
	"common"
	{
		"name"		"Valheim Dedicated Server"
		"type"		"Tool"
		"oslist"		"windows,macos,linux"
	}
	"config"
	{
		"installdir"		"Valheim dedicated server"
		"launch"
		{
			"1"
			{
				"executable"		"start_headless_server.bat"
				"type"		"server"
				"config"
				{
					"oslist"		"windows"
				}
			}
			"0"
			{
				"executable"		"start_server_xterm.sh"
				"type"		"server"
				"config"
				{
					"oslist"		"linux"
				}
			}
			"2"
			{
				"executable"		"tool.sh"
				"arguments"		"-editor"
				"type"		"tool"
			}
		}
	}
	"depots"
	{
		"896661"
		{
			"manifests"
			{
				"public"
				{
					"gid"		"111"
					"size"		"1000000"
				}
			}
		}
		"896662"
		{
			"config"
			{
				"oslist"		"windows"
			}
			"manifests"
			{
				"public"
				{
					"size"		"500000"
				}
			}
		}
		"branches"
		{
			"public"
			{
				"buildid"		"999"
			}
		}
	}
}
`

func TestParseAppInfo(t *testing.T) {
	info := parseAppInfo(parseVDF(richAppInfo), "896660")

	if len(info.OSes) != 3 || info.OSes[2] != "linux" {
		t.Fatalf("OSes = %v", info.OSes)
	}
	// Shared depot (1MB) counts everywhere; the windows depot adds 0.5MB.
	if info.Sizes["linux"] != 1000000 || info.Sizes["windows"] != 1500000 {
		t.Fatalf("Sizes = %v", info.Sizes)
	}
	if len(info.Launch) != 3 {
		t.Fatalf("Launch = %+v", info.Launch)
	}
	// Numeric ordering: entry "0" (linux) before "1" (windows).
	if info.Launch[0].Executable != "start_server_xterm.sh" || info.Launch[0].OSes[0] != "linux" {
		t.Errorf("launch[0] = %+v", info.Launch[0])
	}
	if info.Launch[2].Arguments != "-editor" || info.Launch[2].Type != "tool" || info.Launch[2].OSes != nil {
		t.Errorf("launch[2] = %+v", info.Launch[2])
	}
}

func TestRunScriptBranch(t *testing.T) {
	item := provider.Item{
		ID: "258550", Kind: provider.KindApp, InstallDir: "/srv/rust",
		Branch: "staging", BranchPassword: "sekret",
	}
	script, err := buildRunScript(item, Credentials{}, provider.DownloadOptions{Validate: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `app_update 258550 -beta staging -betapassword "sekret" validate`) {
		t.Fatalf("branch args missing:\n%s", script)
	}

	item.Branch = `st"aging`
	if err := validateItem(item); err == nil {
		t.Fatal("unsafe branch name must be rejected")
	}
}

// A branch without a password (most betas are open) plus a non-anonymous
// login — both must compose cleanly.
func TestRunScriptBranchNoPasswordWithLogin(t *testing.T) {
	item := provider.Item{ID: "258550", Kind: provider.KindApp, InstallDir: "/srv/rust", Branch: "staging"}
	script, err := buildRunScript(item, Credentials{Username: "gabe", Password: "pw"}, provider.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "app_update 258550 -beta staging\n") {
		t.Fatalf("open beta must not get -betapassword:\n%s", script)
	}
	if strings.Contains(script, "-betapassword") {
		t.Fatalf("-betapassword leaked in:\n%s", script)
	}
	if !strings.Contains(script, `login "gabe" "pw"`) {
		t.Fatalf("account login missing:\n%s", script)
	}
}
