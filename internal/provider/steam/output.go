package steam

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/term"
)

// OutputMode controls how much of steamcmd's output reaches the user. The
// full output is always captured internally for error classification.
type OutputMode int

const (
	// OutputFiltered (default) forwards only meaningful lines — download
	// starts, successes, errors, progress — and drops steamcmd's bootstrap
	// chatter (self-update, IPC noise, script command echoes).
	OutputFiltered OutputMode = iota
	// OutputRaw passes everything through (--log-level verbose).
	OutputRaw
	// OutputQuiet forwards nothing (--log-level quiet); failures still
	// surface as errors on the command's result.
	OutputQuiet
)

type runLabelKey struct{}

// withRunLabel tags ctx with a short marker ("app 2394010", "mods") so
// filtered lines from parallel steamcmd processes stay attributable.
func withRunLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, runLabelKey{}, label)
}

func runLabel(ctx context.Context) string {
	s, _ := ctx.Value(runLabelKey{}).(string)
	return s
}

// lineFilter assembles steamcmd output into whole lines and forwards only
// the interesting ones, prefixed with the run label. Emitting complete lines
// in single Write calls also stops parallel processes from interleaving
// mid-line at the terminal.
type lineFilter struct {
	mu    sync.Mutex
	dst   io.Writer
	label string
	buf   bytes.Buffer
	// tty enables in-place progress-bar rewriting (\r); in logs every
	// progress report stays its own line.
	tty bool
	// progLen is the length of the progress line currently on screen;
	// 0 means the cursor is at a fresh line.
	progLen int
	// lastVerb/lastTotal remember the latest real progress report so a
	// Success line can complete the bar to 100% (steamcmd's own final
	// report is the bogus "unknown (0 / 0)").
	lastVerb  string
	lastTotal int64
}

func newLineFilter(dst io.Writer, label string) *lineFilter {
	tty := false
	if f, ok := dst.(*os.File); ok {
		tty = term.IsTerminal(int(f.Fd()))
	}
	return &lineFilter{dst: dst, label: label, tty: tty}
}

func (f *lineFilter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf.Write(p)
	for {
		raw := f.buf.Bytes()
		// steamcmd uses \r for in-place progress updates; treat it as a
		// line break so progress still comes through line by line.
		i := bytes.IndexAny(raw, "\r\n")
		if i < 0 {
			break
		}
		line := string(raw[:i])
		f.buf.Next(i + 1)
		f.emit(line)
	}
	return len(p), nil
}

// Flush emits whatever is buffered after the process exited (output not
// ending in a newline) and terminates a pending progress bar.
func (f *lineFilter) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buf.Len() > 0 {
		f.emit(f.buf.String())
		f.buf.Reset()
	}
	f.endProgress()
}

var (
	// steamcmd glues the echo of the next script command onto result lines
	// ("...failed (Failure).workshop_download_item 1623730 42", "....quit").
	gluedEchoRe = regexp.MustCompile(`(workshop_download_item\s.*|app_update\s.*|quit)$`)
	// Update state (0x61) downloading, progress: 12.34 (123 / 456)
	progressRe = regexp.MustCompile(`Update state \(0x[0-9a-fA-F]+\) ([a-z][a-z ]*), progress: ([0-9.]+) \((\d+) / (\d+)\)`)
)

func (f *lineFilter) emit(line string) {
	line = strings.TrimSpace(gluedEchoRe.ReplaceAllString(strings.TrimSpace(line), ""))
	if m := progressRe.FindStringSubmatch(line); m != nil {
		f.emitProgress(m)
		return
	}
	if !interestingLine(line) {
		return
	}
	if f.progLen > 0 && strings.Contains(strings.ToLower(line), "success") {
		f.completeProgress()
	}
	f.endProgress()
	if f.label != "" {
		fmt.Fprintf(f.dst, "[%s] %s\n", f.label, line)
		return
	}
	fmt.Fprintln(f.dst, line)
}

// emitProgress renders steamcmd's raw update-state reports as a progress
// bar: rewritten in place on a terminal, one compact line per report in logs.
func (f *lineFilter) emitProgress(m []string) {
	pct, _ := strconv.ParseFloat(m[2], 64)
	done, _ := strconv.ParseInt(m[3], 10, 64)
	total, _ := strconv.ParseInt(m[4], 10, 64)
	// steamcmd finishes with a bogus "unknown, progress: 0.00 (0 / 0)"
	// report; showing it would reset a 99% bar to zero right before the
	// Success line.
	if total <= 0 {
		return
	}
	f.lastVerb, f.lastTotal = m[1], total
	text := fmt.Sprintf("%s %s %5.1f%%  %s / %s", m[1], progressBar(pct), pct, humanSize(done), humanSize(total))
	if f.label != "" {
		text = "[" + f.label + "] " + text
	}
	if !f.tty {
		fmt.Fprintln(f.dst, text)
		return
	}
	pad := ""
	if n := f.progLen - len(text); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	fmt.Fprintf(f.dst, "\r%s%s", text, pad)
	f.progLen = len(text)
}

// completeProgress fills the on-screen bar to 100% — used right before a
// Success line, since steamcmd never reports the final 100%.
func (f *lineFilter) completeProgress() {
	if f.lastTotal <= 0 {
		return
	}
	total := strconv.FormatInt(f.lastTotal, 10)
	f.emitProgress([]string{"", f.lastVerb, "100", total, total})
}

// endProgress moves off an in-place progress bar before printing anything else.
func (f *lineFilter) endProgress() {
	if f.progLen > 0 {
		fmt.Fprintln(f.dst)
		f.progLen = 0
	}
}

func progressBar(pct float64) string {
	const width = 20
	filled := min(width, max(0, int(pct/100*width+0.5)))
	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", width-filled) + "]"
}

func humanSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// interestingKeywords whitelists what a user needs to see from steamcmd:
// results, failures, download starts and progress. Everything else (client
// bootstrap, "Waiting for ...", appinfo dumps) is noise.
var interestingKeywords = []string{
	"error",
	"failed",
	"failure",
	"success",
	"downloading item",
	"downloading update",
	"download complete",
	"update state (",
	"steam guard",
	"timeout",
	"rate limit",
}

// noiseFragments force-drop lines that would otherwise slip through the
// keyword whitelist: directive echoes contain "failed"
// (@ShutdownOnFailedCommand) and "password" (@NoPromptForPassword), and the
// set_steam_guard_code echo would leak the code. Contains-based on purpose —
// prefix checks miss lines glued to console control sequences.
var noiseFragments = []string{
	"shutdownonfailedcommand",
	"nopromptforpassword",
	"set_steam_guard_code",
}

func interestingLine(line string) bool {
	if line == "" {
		return false
	}
	// Runscript command echoes: `@ShutdownOnFailedCommand 1` and their
	// `"@..." = "1"` confirmations.
	if strings.HasPrefix(line, "@") || strings.HasPrefix(line, "\"@") {
		return false
	}
	l := strings.ToLower(line)
	// steamcmd.sh wrapper chatter: `steamcmd.sh[12345]: Starting ...`.
	if strings.HasPrefix(l, "steamcmd.sh[") {
		return false
	}
	for _, noise := range noiseFragments {
		if strings.Contains(l, noise) {
			return false
		}
	}
	for _, kw := range interestingKeywords {
		if strings.Contains(l, kw) {
			return true
		}
	}
	return false
}
