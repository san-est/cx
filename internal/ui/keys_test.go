package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vboyadzhiev/cx/internal/cloud"
)

// TestArrowKeysMoveCursor pins down the navigation keys. The cursor silently
// doing nothing is indistinguishable from a broken key map, so this asserts the
// movement rather than relying on manual checking.
func TestArrowKeysMoveCursor(t *testing.T) {
	m := Model{
		loaded:    true,
		canSwitch: true,
		width:     120,
		aws:       []cloud.Target{{Name: "a1"}, {Name: "a2"}},
		gcp:       []cloud.Target{{Name: "g1"}},
	}

	var mm tea.Model = m
	press := func(k tea.KeyType) int {
		mm, _ = mm.Update(tea.KeyMsg{Type: k})
		return mm.(Model).cursor
	}

	if got := press(tea.KeyDown); got != 1 {
		t.Errorf("after one down, cursor = %d, want 1", got)
	}
	if got := press(tea.KeyDown); got != 2 {
		t.Errorf("after two downs, cursor = %d, want 2", got)
	}
	// The cursor must stop at the last row rather than running past the rows.
	if got := press(tea.KeyDown); got != 2 {
		t.Errorf("cursor ran past the last row to %d", got)
	}
	if got := press(tea.KeyUp); got != 1 {
		t.Errorf("after up, cursor = %d, want 1", got)
	}
	press(tea.KeyUp)
	if got := press(tea.KeyUp); got != 0 {
		t.Errorf("cursor ran above the first row to %d", got)
	}
}

func TestVimKeysMatchArrowKeys(t *testing.T) {
	m := Model{loaded: true, width: 120,
		aws: []cloud.Target{{Name: "a1"}, {Name: "a2"}}}

	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := mm.(Model).cursor; got != 1 {
		t.Errorf("j moved cursor to %d, want 1", got)
	}
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if got := mm.(Model).cursor; got != 0 {
		t.Errorf("k moved cursor to %d, want 0", got)
	}
}

// TestSingleRowSaysWhyNothingMoves covers the configuration a new user actually
// has: one gcloud configuration and no AWS profiles. Arrow keys genuinely have
// nowhere to go, so the dashboard has to say that rather than look broken.
func TestSingleRowSaysWhyNothingMoves(t *testing.T) {
	m := Model{loaded: true, canSwitch: true, width: 120,
		gcp: []cloud.Target{{Name: "default"}}}

	out := m.View()
	if !strings.Contains(out, "only one target") {
		t.Errorf("single-row dashboard does not explain itself:\n%s", out)
	}

	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := mm.(Model).cursor; got != 0 {
		t.Errorf("cursor = %d with one row, want 0", got)
	}
}

func TestAddMenuSwallowsNavigationKeys(t *testing.T) {
	// While the menu is open, arrows move the menu's own cursor and must not
	// move the table selection underneath it.
	m := Model{loaded: true, width: 120, mode: modeAdd,
		aws: []cloud.Target{{Name: "a1"}, {Name: "a2"}}}

	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := mm.(Model).cursor; got != 0 {
		t.Errorf("table cursor moved to %d while the add menu was open", got)
	}
	if got := mm.(Model).addCursor; got != 1 {
		t.Errorf("menu cursor = %d, want 1", got)
	}
	// esc closes it.
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).mode != modeNormal {
		t.Error("esc did not close the add menu")
	}
}
