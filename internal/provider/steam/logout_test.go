package steam

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLogoutRemovesSessionArtifacts(t *testing.T) {
	// Logout scans $HOME for Steam data; point it at a sandbox so the test
	// can never touch the real Steam installation.
	fakeHome := t.TempDir()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return fakeHome, nil }
	t.Cleanup(func() { userHomeDir = orig })

	root := t.TempDir()
	bin := filepath.Join(root, "steamcmd.sh")
	for _, f := range []string{bin, filepath.Join(root, "ssfn1234567890"), filepath.Join(root, "config", "config.vdf")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := New(Options{SteamcmdPath: bin})
	removed, err := p.Logout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("want config.vdf and the ssfn file removed, got %v", removed)
	}
	for _, f := range []string{filepath.Join(root, "config", "config.vdf"), filepath.Join(root, "ssfn1234567890")} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s must be gone", f)
		}
	}
	if _, err := os.Stat(bin); err != nil {
		t.Errorf("the steamcmd binary itself must survive: %v", err)
	}
}
