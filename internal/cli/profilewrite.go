package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/Austrum-lab/game-fetcher-cli/internal/config"
)

// writeConfigPath is where --save-as-profile persists profiles: the explicit
// --config file, then an already existing ./gamefetcher.yaml, then the user
// XDG config — a new config file must not appear in whatever directory the
// command happened to be run from.
func writeConfigPath(cmd *cobra.Command) string {
	if path, err := cmd.Flags().GetString("config"); err == nil && path != "" {
		return path
	}
	if _, err := os.Stat(config.DefaultLocalPath); err == nil {
		return config.DefaultLocalPath
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "gamefetcher", "config.yaml")
	}
	return config.DefaultLocalPath
}

// saveProfile upserts profiles.<name> in the YAML file at path, preserving
// everything else in the file. Overwriting an existing, different profile
// requires confirmation after showing a diff (spec section 3).
func saveProfile(path, name string, p config.Profile, confirm func(prompt string) (bool, error), out io.Writer) error {
	raw := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if raw == nil {
			raw = map[string]any{}
		}
	case errors.Is(err, fs.ErrNotExist):
		// new file
	default:
		return err
	}

	profiles, _ := raw["profiles"].(map[string]any)
	if profiles == nil {
		profiles = map[string]any{}
	}
	block := profileBlock(p)

	if old, exists := profiles[name]; exists {
		// Updates are additive: fields the new block does not set (a
		// previously resolved base_app_id, extends, hand-written extras)
		// survive instead of showing up in the diff as deletions.
		if oldMap, ok := old.(map[string]any); ok {
			merged := maps.Clone(oldMap)
			for k, v := range block {
				merged[k] = v
			}
			block = merged
		}
		oldYAML, err := yaml.Marshal(old)
		if err != nil {
			return err
		}
		newYAML, err := yaml.Marshal(block)
		if err != nil {
			return err
		}
		if bytes.Equal(oldYAML, newYAML) {
			fmt.Fprintf(out, "profile %q in %s is already up to date\n", name, path)
			return nil
		}
		fmt.Fprintf(out, "profile %q already exists in %s:\n%s", name, path, diffLines(oldYAML, newYAML))
		ok, err := confirm(fmt.Sprintf("Overwrite profile %q?", name))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("aborted: profile left unchanged, run cancelled")
		}
	}
	profiles[name] = block
	raw["profiles"] = profiles

	buf, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "saved profile %q to %s\n", name, path)
	return nil
}

func profileBlock(p config.Profile) map[string]any {
	block := map[string]any{
		"app_id":      p.AppID,
		"install_dir": p.InstallDir,
	}
	if p.BaseAppID != 0 {
		block["base_app_id"] = p.BaseAppID
	}
	if len(p.Mods) > 0 {
		block["mods"] = p.Mods
	}
	if len(p.Collections) > 0 {
		block["collections"] = p.Collections
	}
	if p.Platform != "" {
		block["platform"] = p.Platform
	}
	if p.Branch != "" {
		block["branch"] = p.Branch
	}
	if p.BranchPassword != "" {
		block["branch_password"] = p.BranchPassword
	}
	return block
}

// diffLines renders a small +/- diff of two YAML blocks.
func diffLines(oldYAML, newYAML []byte) string {
	oldLines := strings.Split(strings.TrimRight(string(oldYAML), "\n"), "\n")
	newLines := strings.Split(strings.TrimRight(string(newYAML), "\n"), "\n")
	inOld := map[string]bool{}
	for _, l := range oldLines {
		inOld[l] = true
	}
	inNew := map[string]bool{}
	for _, l := range newLines {
		inNew[l] = true
	}
	var b strings.Builder
	for _, l := range oldLines {
		if !inNew[l] {
			b.WriteString("  - " + l + "\n")
		}
	}
	for _, l := range newLines {
		if !inOld[l] {
			b.WriteString("  + " + l + "\n")
		}
	}
	return b.String()
}

// askConfirm returns an interactive y/N prompt bound to the command's
// stdin/stderr. Without a terminal it refuses instead of guessing.
func askConfirm(cmd *cobra.Command) func(string) (bool, error) {
	return func(prompt string) (bool, error) {
		stdin, ok := cmd.InOrStdin().(*os.File)
		if !ok || !term.IsTerminal(int(stdin.Fd())) {
			return false, errors.New("confirmation required but stdin is not a terminal")
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N]: ", prompt)
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil {
			return false, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
}
