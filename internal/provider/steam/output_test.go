package steam

import (
	"strings"
	"testing"
)

// TestLineFilter feeds a realistic chunk of steamcmd noise (log_example.txt)
// and expects only the meaningful lines to pass, labeled and whole.
func TestLineFilter(t *testing.T) {
	var out strings.Builder
	f := newLineFilter(&out, "mods")

	noise := []string{
		"steamcmd.sh[378686]: Starting  /srv/.local/share/Steam/steamcmd/linux32/steamcmd",
		"Redirecting stderr to '/srv/.local/share/Steam/logs/stderr.txt'",
		"[  0%] Checking for available updates...",
		"[----] Verifying installation...",
		"Looks like steam didn't shutdown cleanly, scheduling immediate update check",
		"UpdateUI: skip show logo",
		"Steam Console Client (c) Valve Corporation - version 1782532820",
		"-- type 'quit' to exit --",
		"Loading Steam API...OK",
		"@ShutdownOnFailedCommand 1",
		`"@ShutdownOnFailedCommand" = "1"`,
		`force_install_dir "/opt/palworld/"`,
		"Connecting anonymously to Steam Public...OK",
		"Waiting for client config...OK",
		"IPC function call IClientUtils::GetSteamRealm took too long: 67 msec",
		"workshop_download_item 1623730 3638142192",
		"quit",
		"Unloading Steam API...OK",
	}
	interesting := []string{
		"Downloading item 3638142192 ...",
		"Success! App '2394010' already up to date.",
		"Update state (0x61) downloading, progress: 12.34 (123 / 456)",
		"ERROR! Download item 3661193331 failed (Failure).workshop_download_item 1623730 42",
	}
	// Progress lines are reformatted, glued command echoes cut off.
	want := []string{
		"[mods] Downloading item 3638142192 ...",
		"[mods] Success! App '2394010' already up to date.",
		"[mods] downloading [==                  ]  12.3%  123 B / 456 B",
		"[mods] ERROR! Download item 3661193331 failed (Failure).",
	}
	for _, l := range noise {
		f.Write([]byte(l + "\n"))
	}
	for _, l := range interesting {
		f.Write([]byte(l + "\n"))
	}
	f.Flush()

	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != len(want) {
		t.Fatalf("want %d lines, got %d:\n%s", len(want), len(got), out.String())
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d: want %q, got %q", i, w, got[i])
		}
	}
}

// Partial writes and \r progress updates must still come out as whole lines.
func TestLineFilterReassemblesChunks(t *testing.T) {
	var out strings.Builder
	f := newLineFilter(&out, "")
	for _, chunk := range []string{"ERROR! Download item 42 fai", "led (Failure).\rUpdate state (0x61) downloading", ", progress: 5\n"} {
		f.Write([]byte(chunk))
	}
	f.Flush()
	want := "ERROR! Download item 42 failed (Failure).\nUpdate state (0x61) downloading, progress: 5\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}
