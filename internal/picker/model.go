// Package picker implements the interactive terminal selector from spec.md
// section 7: arrow keys + Enter, space for multi-select.
package picker

import (
	"errors"
)

// Option is one selectable row.
type Option struct {
	Label string // shown in the list
	Value string // returned to the caller
}

// ErrCanceled is returned when the user aborts the selection.
var ErrCanceled = errors.New("selection canceled")

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keySpace
	keyEnter
	keyCancel
)

// model is the pure selection state machine, separated from terminal IO so
// it can be tested without a TTY.
type model struct {
	options  []Option
	cursor   int
	offset   int // top of the visible window
	rows     int // visible window height
	multi    bool
	selected map[int]bool
}

func newModel(options []Option, multi bool, rows int) *model {
	if rows > len(options) {
		rows = len(options)
	}
	return &model{options: options, multi: multi, rows: rows, selected: map[int]bool{}}
}

// handle applies one keypress; done means the interaction finished.
func (m *model) handle(k key) (done, canceled bool) {
	switch k {
	case keyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case keyDown:
		if m.cursor < len(m.options)-1 {
			m.cursor++
		}
	case keySpace:
		if m.multi {
			if m.selected[m.cursor] {
				delete(m.selected, m.cursor)
			} else {
				m.selected[m.cursor] = true
			}
		}
	case keyEnter:
		return true, false
	case keyCancel:
		return true, true
	}
	m.scroll()
	return false, false
}

func (m *model) scroll() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.rows {
		m.offset = m.cursor - m.rows + 1
	}
}

// chosen returns the outcome: marked rows for multi-select (Enter with
// nothing marked picks the hovered row), the hovered row otherwise.
func (m *model) chosen() []Option {
	if !m.multi {
		return []Option{m.options[m.cursor]}
	}
	var out []Option
	for i, opt := range m.options {
		if m.selected[i] {
			out = append(out, opt)
		}
	}
	if len(out) == 0 {
		out = []Option{m.options[m.cursor]}
	}
	return out
}
