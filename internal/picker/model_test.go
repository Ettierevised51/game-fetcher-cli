package picker

import (
	"testing"
)

func opts(labels ...string) []Option {
	out := make([]Option, len(labels))
	for i, l := range labels {
		out[i] = Option{Label: l, Value: l}
	}
	return out
}

func TestModelNavigationBounds(t *testing.T) {
	m := newModel(opts("a", "b", "c"), false, 12)
	m.handle(keyUp) // already at the top
	if m.cursor != 0 {
		t.Errorf("cursor moved above the top: %d", m.cursor)
	}
	m.handle(keyDown)
	m.handle(keyDown)
	m.handle(keyDown) // already at the bottom
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want clamped to 2", m.cursor)
	}
	done, canceled := m.handle(keyEnter)
	if !done || canceled {
		t.Fatalf("enter: done=%v canceled=%v", done, canceled)
	}
	if got := m.chosen(); len(got) != 1 || got[0].Value != "c" {
		t.Errorf("chosen = %v, want the hovered row", got)
	}
}

func TestModelMultiSelect(t *testing.T) {
	m := newModel(opts("a", "b", "c"), true, 12)
	m.handle(keySpace) // select a
	m.handle(keyDown)
	m.handle(keyDown)
	m.handle(keySpace) // select c
	m.handle(keySpace) // deselect c
	m.handle(keySpace) // select c again
	m.handle(keyEnter)
	got := m.chosen()
	if len(got) != 2 || got[0].Value != "a" || got[1].Value != "c" {
		t.Errorf("chosen = %v, want [a c] in list order", got)
	}
}

func TestModelMultiEnterWithNothingMarked(t *testing.T) {
	m := newModel(opts("a", "b"), true, 12)
	m.handle(keyDown)
	m.handle(keyEnter)
	if got := m.chosen(); len(got) != 1 || got[0].Value != "b" {
		t.Errorf("chosen = %v, want the hovered row when nothing is marked", got)
	}
}

func TestModelCancel(t *testing.T) {
	m := newModel(opts("a"), false, 12)
	done, canceled := m.handle(keyCancel)
	if !done || !canceled {
		t.Fatalf("cancel: done=%v canceled=%v", done, canceled)
	}
}

func TestModelScrollWindow(t *testing.T) {
	m := newModel(opts("a", "b", "c", "d", "e"), false, 2)
	for range 4 {
		m.handle(keyDown)
	}
	if m.cursor != 4 || m.offset != 3 {
		t.Errorf("cursor=%d offset=%d, want the window to follow the cursor", m.cursor, m.offset)
	}
	for range 4 {
		m.handle(keyUp)
	}
	if m.cursor != 0 || m.offset != 0 {
		t.Errorf("cursor=%d offset=%d after scrolling back up", m.cursor, m.offset)
	}
}
