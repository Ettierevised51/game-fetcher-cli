package steam

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/Austrum-lab/game-fetcher-cli/internal/provider"
	"github.com/Austrum-lab/game-fetcher-cli/internal/retry"
)

// DownloadBatch installs or updates several workshop items in one steamcmd
// run — one process per item hammers the shared Steam bootstrap and floods
// the log. All items must be mods sharing one install dir (force_install_dir
// is per process). Returns one error per item, nil on success; process-level
// failures are folded into every unresolved item's error.
func (p *Provider) DownloadBatch(ctx context.Context, items []provider.Item, opts provider.DownloadOptions) []error {
	errs := make([]error, len(items))
	fail := func(err error) []error {
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	if len(items) == 0 {
		return nil
	}
	for _, it := range items {
		if err := validateItem(it); err != nil {
			return fail(err)
		}
		if it.Kind != provider.KindMod {
			return fail(fmt.Errorf("batch downloads are for workshop items, got %s", itemRef(it)))
		}
		if it.InstallDir != items[0].InstallDir {
			return fail(errors.New("all items of one batch must share the install dir"))
		}
	}
	if err := prepareInstallDir(items[0].InstallDir); err != nil {
		return fail(err)
	}
	bin, err := p.steamcmdPath(ctx)
	if err != nil {
		return fail(err)
	}
	scriptPath, cleanup, err := writeRunScript(buildBatchRunScript(items, p.opts.Credentials, opts))
	if err != nil {
		return fail(err)
	}
	defer cleanup()

	out, runErr := p.run(withRunLabel(ctx, "mods"), bin, "+runscript", scriptPath)
	// A failed login means nothing after it ran; without this check an item
	// already on disk from an earlier run would pass IsInstalled and be
	// reported as updated when it wasn't.
	if loginFailed(out) {
		return fail(retry.Mark(fmt.Errorf("steamcmd login failed, batch of %d workshop item(s) skipped", len(items)), classifyOutput(out)))
	}
	verdicts := parseBatchOutput(out)
	for i, it := range items {
		errs[i] = p.batchItemResult(ctx, it, verdicts[it.ID], out, runErr)
	}
	return errs
}

// batchItemResult decides one item's outcome: an explicit per-item ERROR
// line wins; a dead process fails every item without a Success line (stale
// content on disk from an earlier run must not pass for an update that never
// ran); then the on-disk check (spec section 4: never trust the exit code).
func (p *Provider) batchItemResult(ctx context.Context, item provider.Item, v *itemVerdict, out string, runErr error) error {
	if v != nil && !v.ok {
		return retry.Mark(fmt.Errorf("steamcmd: downloading %s failed (%s)", itemRef(item), v.reason), classifyOutput(v.reason))
	}
	if runErr != nil && (v == nil || !v.ok) {
		return retry.Mark(fmt.Errorf("steamcmd failed for %s: %w", itemRef(item), runErr), classifyOutput(out))
	}
	installed, err := p.IsInstalled(ctx, item)
	if err != nil {
		return err
	}
	if installed {
		return nil
	}
	return retry.Mark(fmt.Errorf("steamcmd reported success for %s, but %s is missing — the download did not land in %s",
		itemRef(item), verificationTarget(item), item.InstallDir), classifyOutput(out))
}

var (
	batchSuccessRe = regexp.MustCompile(`Success\. Downloaded item (\d+)`)
	batchErrorRe   = regexp.MustCompile(`ERROR! Download item (\d+) failed \(([^)]*)\)`)
)

type itemVerdict struct {
	ok     bool
	reason string
}

// parseBatchOutput extracts per-item verdicts from the combined output of a
// batch run. An ERROR line overrides a Success line for the same id.
func parseBatchOutput(out string) map[string]*itemVerdict {
	verdicts := map[string]*itemVerdict{}
	for _, m := range batchSuccessRe.FindAllStringSubmatch(out, -1) {
		verdicts[m[1]] = &itemVerdict{ok: true}
	}
	for _, m := range batchErrorRe.FindAllStringSubmatch(out, -1) {
		verdicts[m[1]] = &itemVerdict{ok: false, reason: m[2]}
	}
	return verdicts
}
