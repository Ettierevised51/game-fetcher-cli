package picker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

const viewportRows = 12

// Pick runs a single-select picker. The UI is drawn on out (use stderr so
// stdout stays clean for the result); in must be a terminal.
func Pick(in *os.File, out io.Writer, title string, options []Option) (Option, error) {
	chosen, err := run(in, out, title, options, false)
	if err != nil {
		return Option{}, err
	}
	return chosen[0], nil
}

// PickMulti runs a multi-select picker (space toggles, Enter confirms;
// Enter with nothing marked picks the hovered row).
func PickMulti(in *os.File, out io.Writer, title string, options []Option) ([]Option, error) {
	return run(in, out, title, options, true)
}

func run(in *os.File, out io.Writer, title string, options []Option, multi bool) ([]Option, error) {
	if len(options) == 0 {
		return nil, errors.New("nothing to pick from")
	}
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("interactive picker needs a terminal; use --json for scripted output")
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer term.Restore(fd, oldState)

	m := newModel(options, multi, viewportRows)

	hint := "↑/↓ move · enter confirm · q cancel"
	if multi {
		hint = "↑/↓ move · space select · enter confirm · q cancel"
	}
	fmt.Fprintf(out, "%s\r\n%s\r\n", title, hint)
	fmt.Fprint(out, "\x1b[?25l")       // hide cursor
	defer fmt.Fprint(out, "\x1b[?25h") // show cursor

	lines := m.rows
	render(out, m)
	for {
		k, err := readKey(in)
		if err != nil {
			fmt.Fprintf(out, "\x1b[%dB\r\n", lines)
			return nil, err
		}
		done, canceled := m.handle(k)
		if done {
			// Leave the final list on screen and park the cursor below it.
			fmt.Fprintf(out, "\x1b[%dB\r\n", lines)
			if canceled {
				return nil, ErrCanceled
			}
			return m.chosen(), nil
		}
		render(out, m)
	}
}

// render draws the visible window and moves the cursor back to its top, so
// the next render overdraws in place.
func render(out io.Writer, m *model) {
	var b strings.Builder
	for row := m.offset; row < m.offset+m.rows; row++ {
		opt := m.options[row]
		marker := ""
		if m.multi {
			if m.selected[row] {
				marker = "[x] "
			} else {
				marker = "[ ] "
			}
		}
		line := fmt.Sprintf("  %s%s", marker, opt.Label)
		if row == m.cursor {
			line = fmt.Sprintf("\x1b[7m> %s%s\x1b[0m", marker, opt.Label)
		}
		b.WriteString("\x1b[2K" + line + "\r\n")
	}
	fmt.Fprint(out, b.String())
	fmt.Fprintf(out, "\x1b[%dA", m.rows)
}

func readKey(in *os.File) (key, error) {
	var buf [4]byte
	n, err := in.Read(buf[:])
	if err != nil {
		return keyNone, err
	}
	switch {
	case n == 0:
		return keyNone, io.EOF
	case buf[0] == 3 || buf[0] == 'q': // ctrl-c / q
		return keyCancel, nil
	case buf[0] == '\r' || buf[0] == '\n':
		return keyEnter, nil
	case buf[0] == ' ':
		return keySpace, nil
	case buf[0] == 'k':
		return keyUp, nil
	case buf[0] == 'j':
		return keyDown, nil
	case buf[0] == 0x1b: // escape sequences
		if n >= 3 && buf[1] == '[' {
			switch buf[2] {
			case 'A':
				return keyUp, nil
			case 'B':
				return keyDown, nil
			}
			return keyNone, nil
		}
		return keyCancel, nil // bare Esc
	}
	return keyNone, nil
}
