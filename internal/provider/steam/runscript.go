package steam

import (
	"fmt"
	"os"
	"strings"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
)

// buildRunScript renders the steamcmd script for one item. Command order is
// load-bearing (spec section 4): force_install_dir must come before login,
// otherwise steamcmd may silently download into its default library.
func buildRunScript(item provider.Item, creds Credentials, opts provider.DownloadOptions) (string, error) {
	var b strings.Builder
	b.WriteString("@ShutdownOnFailedCommand 1\n")
	b.WriteString("@NoPromptForPassword 1\n")
	if item.Platform != "" {
		// Cross-platform download (e.g. Windows-only dedicated servers on
		// Linux). Verified live: without this directive steamcmd simply has
		// nothing to install; with it the foreign depots download fine.
		fmt.Fprintf(&b, "@sSteamCmdForcePlatformType %s\n", item.Platform)
	}
	fmt.Fprintf(&b, "force_install_dir %s\n", quote(item.InstallDir))
	b.WriteString(loginCommand(creds))
	switch item.Kind {
	case provider.KindApp:
		fmt.Fprintf(&b, "app_update %s", item.ID)
		if item.Branch != "" {
			fmt.Fprintf(&b, " -beta %s", item.Branch)
			if item.BranchPassword != "" {
				fmt.Fprintf(&b, " -betapassword %s", quote(item.BranchPassword))
			}
		}
		if opts.Validate {
			b.WriteString(" validate")
		}
		b.WriteString("\n")
	case provider.KindMod:
		fmt.Fprintf(&b, "workshop_download_item %s %s", item.AppID, item.ID)
		if opts.Validate {
			b.WriteString(" validate")
		}
		b.WriteString("\n")
	default:
		return "", fmt.Errorf("unsupported item kind %q", item.Kind)
	}
	b.WriteString("quit\n")
	return b.String(), nil
}

// buildBatchRunScript renders one runscript downloading several workshop
// items. @ShutdownOnFailedCommand stays off so a single failed item does not
// abort the rest of the batch; per-item outcomes are parsed from the output
// instead (parseBatchOutput).
func buildBatchRunScript(items []provider.Item, creds Credentials, opts provider.DownloadOptions) string {
	var b strings.Builder
	b.WriteString("@ShutdownOnFailedCommand 0\n")
	b.WriteString("@NoPromptForPassword 1\n")
	fmt.Fprintf(&b, "force_install_dir %s\n", quote(items[0].InstallDir))
	b.WriteString(loginCommand(creds))
	for _, it := range items {
		fmt.Fprintf(&b, "workshop_download_item %s %s", it.AppID, it.ID)
		if opts.Validate {
			b.WriteString(" validate")
		}
		b.WriteString("\n")
	}
	b.WriteString("quit\n")
	return b.String()
}

func loginCommand(creds Credentials) string {
	if creds.Anonymous() {
		return "login anonymous\n"
	}
	return fmt.Sprintf("login %s %s\n", quote(creds.Username), quote(creds.Password))
}

// quote wraps a value for the steamcmd script parser. Backslashes are left
// alone (Windows paths); only double quotes are escaped.
func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// writeRunScript stores the script in a private temp file: it may contain
// the account password, so 0600 (os.CreateTemp's default) and removed right
// after the run via cleanup.
func writeRunScript(content string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "gamefetcher-runscript-*.txt")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}
