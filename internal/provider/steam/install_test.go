package steam

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func steamcmdArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
		mode       int64
	}{
		{"steamcmd.sh", "#!/bin/sh\nexit 0\n", 0o755},
		{"linux32/steamcmd", "not really a binary", 0o755},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSteamcmdRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("PATH", "") // hide any real steamcmd
	p := New(Options{InstallRoot: t.TempDir()})
	_, err := p.steamcmdPath(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--allow-install-steamcmd") {
		t.Fatalf("expected an opt-in error mentioning the flag, got: %v", err)
	}
}

func TestSteamcmdAutoInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test archive is a tar.gz")
	}
	t.Setenv("PATH", "")
	archive := steamcmdArchive(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	root := filepath.Join(t.TempDir(), "steamcmd")
	p := New(Options{
		AllowInstall: true,
		InstallRoot:  root,
		InstallerURL: srv.URL + "/steamcmd_linux.tar.gz",
	})
	bin, err := p.steamcmdPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "steamcmd.sh"); bin != want {
		t.Errorf("bin = %q, want %q", bin, want)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("steamcmd.sh is not executable: %v", info.Mode())
	}

	// A second lookup must reuse the existing install, not download again.
	srv.Close()
	if _, err := p.steamcmdPath(context.Background()); err != nil {
		t.Fatalf("cached install not reused: %v", err)
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	if _, err := safeJoin("/dst", "../evil"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	if _, err := safeJoin("/dst", "ok/file"); err != nil {
		t.Fatalf("legit path rejected: %v", err)
	}
}
