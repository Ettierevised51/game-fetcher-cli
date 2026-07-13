package steam

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// cdnBase is the official Valve CDN; package managers are deliberately not
// used (spec section 8).
const cdnBase = "https://steamcdn-a.akamaihd.net/client/installer/"

// steamcmdPath locates the steamcmd binary: explicit steamcmd_path, then
// $PATH, then a previous auto-install. Downloading it is gated behind an
// explicit opt-in (--allow-install-steamcmd / auto_install_steamcmd).
// steamcmd self-updates on its first run, so a freshly extracted archive
// needs no further bootstrapping here.
func (p *Provider) steamcmdPath(ctx context.Context) (string, error) {
	if p.opts.SteamcmdPath != "" {
		if _, err := os.Stat(p.opts.SteamcmdPath); err != nil {
			return "", fmt.Errorf("steamcmd_path: %w", err)
		}
		return p.opts.SteamcmdPath, nil
	}
	if path, err := exec.LookPath("steamcmd"); err == nil {
		return path, nil
	}
	root, err := p.installRoot()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(root, binaryName())
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	if !p.opts.AllowInstall {
		return "", errors.New("steamcmd not found; pass --allow-install-steamcmd (or set auto_install_steamcmd: true) to download it from the Valve CDN")
	}
	if err := p.installSteamcmd(ctx, root); err != nil {
		return "", err
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("steamcmd archive did not contain %s: %w", binaryName(), err)
	}
	return bin, nil
}

func (p *Provider) installRoot() (string, error) {
	if p.opts.InstallRoot != "" {
		return p.opts.InstallRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "gamefetcher", "steamcmd"), nil
}

func installerName() (string, error) {
	switch runtime.GOOS {
	case "linux":
		return "steamcmd_linux.tar.gz", nil
	case "darwin":
		return "steamcmd_osx.tar.gz", nil
	case "windows":
		return "steamcmd.zip", nil
	default:
		return "", fmt.Errorf("steamcmd is not available for %s", runtime.GOOS)
	}
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "steamcmd.exe"
	}
	return "steamcmd.sh"
}

func (p *Provider) installSteamcmd(ctx context.Context, root string) error {
	name, err := installerName()
	if err != nil {
		return err
	}
	url := p.opts.InstallerURL
	if url == "" {
		url = cdnBase + name
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading steamcmd: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading steamcmd from %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	// This runs on first use and takes a while — say so instead of hanging
	// the terminal silently, and note the self-update that follows.
	w := orWriter(p.opts.Progress, os.Stderr)
	fmt.Fprintf(w, "steamcmd not found — downloading %s\n", url)
	body := &progressReader{r: resp.Body, total: resp.ContentLength, w: w}
	if strings.HasSuffix(name, ".zip") {
		data, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		if err := extractZip(data, root); err != nil {
			return err
		}
	} else if err := extractTarGz(body, root); err != nil {
		return err
	}
	body.finish()
	fmt.Fprintf(w, "steamcmd installed to %s (it will self-update on its first run — that can take another minute)\n", root)
	return nil
}

// progressReader reports download progress on w while the archive streams
// through it: percentages when the size is known, plain MiB otherwise.
type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	last  int64 // last reported percent, or bytes at the last report
	w     io.Writer
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	switch {
	case p.total > 0:
		if pct := p.read * 100 / p.total; pct != p.last {
			p.last = pct
			fmt.Fprintf(p.w, "\rdownloading steamcmd... %d%%", pct)
		}
	case p.read-p.last >= 1<<20:
		p.last = p.read
		fmt.Fprintf(p.w, "\rdownloading steamcmd... %.1f MiB", float64(p.read)/(1<<20))
	}
	return n, err
}

func (p *progressReader) finish() {
	fmt.Fprintf(p.w, "\rdownloading steamcmd... done (%.1f MiB)\n", float64(p.read)/(1<<20))
}

func extractTarGz(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dst, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		default:
			// Symlinks and specials are not expected in the steamcmd archive.
		}
	}
}

func extractZip(data []byte, dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		target, err := safeJoin(dst, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, rc, f.Mode()&0o777)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(target string, src io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// safeJoin guards archive extraction against path traversal.
func safeJoin(dst, name string) (string, error) {
	target := filepath.Join(dst, name)
	if target != dst && !strings.HasPrefix(target, dst+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return target, nil
}
