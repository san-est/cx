package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/vboyadzhiev/cx/internal/cloud"
)

// resizeModel is a populated dashboard with content long enough to overflow a
// narrow terminal if any part of the layout ignores the width.
func resizeModel(w int) Model {
	return Model{
		loaded: true, canSwitch: true, width: w, cursor: 1,
		notice: "added AWS profile client-prod — press l on its row to sign in",
		aws: []cloud.Target{
			{Name: "appex", Kind: cloud.KindStatic, Account: "749929395228",
				Scope: "eu-west-1", Health: cloud.Valid,
				Identity: "user/vasil.boyadzhiev@limechain.tech"},
			{Name: "client-prod", Kind: cloud.KindSSOSession, Account: "999988887777",
				Scope: "eu-west-1", Active: true, Health: cloud.Valid},
		},
		gcp: []cloud.Target{
			{Name: "devops-platform", Kind: cloud.KindUser,
				Account: "vasil.boyadzhiev@limechain.tech",
				Scope:   "devops-platform-1234", Active: true, Health: cloud.Valid},
			{Name: "mirror-node-backup", Kind: cloud.KindUser, Health: cloud.Expired,
				Detail: "not logged in — run: gcloud auth login"},
		},
		gcpState: cloud.GCPState{Active: "devops-platform", Global: "default",
			Source: cloud.SourceGlobal},
		adc: cloud.ADC{Present: true, Identity: "vasil.boyadzhiev@limechain.tech",
			QuotaProject: "devops-platform-1234", Health: cloud.Valid},
	}
}

// TestNoLineOverflowsAtAnyWidth is the regression test for the dashboard
// breaking up when a terminal is resized smaller. Every screen is rendered at
// every width in the range and no line may exceed it.
func TestNoLineOverflowsAtAnyWidth(t *testing.T) {
	modes := map[string]mode{"dashboard": modeNormal, "add menu": modeAdd, "form": modeForm}

	for w := 20; w <= 220; w++ {
		for name, md := range modes {
			m := resizeModel(w)
			m.mode = md
			if md == modeForm {
				m.active = awsSSOForm()
			}
			for i, line := range strings.Split(m.View(), "\n") {
				if got := lipgloss.Width(line); got > w {
					t.Fatalf("width %d, %s: line %d overflows by %d cells:\n%q",
						w, name, i, got-w, stripANSI(line))
				}
			}
		}
	}
}

// TestHeaderStacksWhenNarrow covers the two header panels sitting side by side
// only while there is room, and stacking rather than overflowing below that.
func TestHeaderStacksWhenNarrow(t *testing.T) {
	// Two borders plus five body rows, all three panels the same height.
	wide := strings.Split(resizeModel(120).renderHeader(120), "\n")
	if len(wide) != 7 {
		t.Errorf("at 120 columns the header should be 7 lines side by side, got %d", len(wide))
	}

	narrow := strings.Split(resizeModel(60).renderHeader(60), "\n")
	if len(narrow) <= 7 {
		t.Errorf("at 60 columns the header should stack into more lines, got %d", len(narrow))
	}
	// Stacking must not lose anything.
	out := stripANSI(resizeModel(60).renderHeader(60))
	for _, want := range []string{"AWS Profile", "ADC", "navigate", "quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("stacked header lost %q:\n%s", want, out)
		}
	}
}

// TestVeryNarrowTerminalSaysSoRatherThanBreaking covers the floor: below the
// minimum width a broken table is worse than an honest message.
func TestVeryNarrowTerminalSaysSoRatherThanBreaking(t *testing.T) {
	for _, w := range []int{20, 30, 43} {
		out := resizeModel(w).View()
		if !strings.Contains(out, "too narrow") {
			t.Errorf("width %d rendered a layout instead of reporting the size:\n%s", w, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("width %d: even the warning overflows: %q", w, stripANSI(line))
			}
		}
	}
	if strings.Contains(resizeModel(minWidth).View(), "too narrow") {
		t.Errorf("width %d is the minimum and should render normally", minWidth)
	}
}

// TestWidthZeroUsesAFallback covers the first frame, which is painted before
// any size message has arrived.
func TestWidthZeroUsesAFallback(t *testing.T) {
	m := resizeModel(0)
	if got := m.layoutWidth(); got != 100 {
		t.Errorf("layoutWidth with no size message = %d, want the 100 fallback", got)
	}
	if strings.Contains(m.View(), "too narrow") {
		t.Error("the first frame reported the terminal as too narrow")
	}
}

// TestWidthIsCappedOnVeryWideTerminals keeps lines readable rather than
// stretching a five-column table across an ultrawide display.
func TestWidthIsCappedOnVeryWideTerminals(t *testing.T) {
	if got := resizeModel(400).layoutWidth(); got != maxWidth {
		t.Errorf("layoutWidth at 400 columns = %d, want %d", got, maxWidth)
	}
}

// TestStatusColumnStaysReadable covers the status never being squeezed to a
// couple of characters: a status clipped to "val…" conveys nothing, so the name
// and scope columns give way first.
func TestStatusColumnStaysReadable(t *testing.T) {
	for w := minWidth; w <= 200; w++ {
		inner := w - 2
		nameW, kindW, acctW, scopeW, showKind, showAcct := columns(inner)

		used := nameW + scopeW + 4
		if showKind {
			used += kindW + 1
		}
		if showAcct {
			used += acctW + 1
		}
		if got := inner - used - 1; got < 10 {
			t.Errorf("width %d leaves only %d cells for STATUS", w, got)
		}
	}
}

// manyTargets builds a dashboard with more rows than a small terminal can show.
func manyTargets(w, h int) Model {
	m := resizeModel(w)
	m.height = h
	for i := 0; i < 12; i++ {
		m.aws = append(m.aws, cloud.Target{
			Name: "aws-profile-" + itoa(i), Kind: cloud.KindStatic,
			Scope: "eu-west-1", Health: cloud.Valid, Identity: "someone"})
		m.gcp = append(m.gcp, cloud.Target{
			Name: "gcp-config-" + itoa(i), Kind: cloud.KindUser,
			Scope: "project-" + itoa(i), Health: cloud.Valid})
	}
	return m
}

// TestOutputNeverExceedsTerminalHeight is the regression test for torn,
// duplicated frames after shrinking a terminal. Drawing more rows than the
// terminal has is what leaves the previous frame on screen, so no screen may
// ever emit more lines than it was given.
func TestOutputNeverExceedsTerminalHeight(t *testing.T) {
	for h := 12; h <= 60; h++ {
		for _, w := range []int{60, 100, 160} {
			m := manyTargets(w, h)
			if got := countLines(m.View()); got > h {
				t.Fatalf("terminal %dx%d: view emitted %d lines, %d too many",
					w, h, got, got-h)
			}
		}
	}
}

// TestHiddenRowsAreReported covers the user being told rows exist off-screen,
// rather than silently seeing a partial list and believing it complete.
func TestHiddenRowsAreReported(t *testing.T) {
	out := stripANSI(manyTargets(120, 20).View())
	if !strings.Contains(out, "more, ↑↓ to scroll") {
		t.Errorf("truncated tables did not say rows were hidden:\n%s", out)
	}
}

// TestSelectedRowStaysVisibleWhenScrolled covers the window following the
// cursor: a selection you cannot see is worse than no selection.
func TestSelectedRowStaysVisibleWhenScrolled(t *testing.T) {
	m := manyTargets(120, 20)
	for _, cursor := range []int{0, 3, 7, len(m.aws) - 1} {
		m.cursor = cursor
		name := m.aws[cursor].Name
		if !strings.Contains(stripANSI(m.View()), name) {
			t.Errorf("cursor %d: selected row %q is not on screen", cursor, name)
		}
	}
}

func TestVisibleWindow(t *testing.T) {
	cases := []struct{ total, cursor, max, wantStart, wantEnd int }{
		{total: 3, cursor: 0, max: 10, wantStart: 0, wantEnd: 3},  // all fit
		{total: 10, cursor: 0, max: 4, wantStart: 0, wantEnd: 4},  // at the top
		{total: 10, cursor: 9, max: 4, wantStart: 6, wantEnd: 10}, // at the bottom
		{total: 10, cursor: 5, max: 4, wantStart: 3, wantEnd: 7},  // centred
		{total: 10, cursor: -1, max: 4, wantStart: 0, wantEnd: 4}, // cursor elsewhere
	}
	for _, c := range cases {
		start, end := visibleWindow(c.total, c.cursor, c.max)
		if start != c.wantStart || end != c.wantEnd {
			t.Errorf("visibleWindow(%d,%d,%d) = %d,%d want %d,%d",
				c.total, c.cursor, c.max, start, end, c.wantStart, c.wantEnd)
		}
	}
}

// keysPanelOf pulls the Keys column out of a rendered header, so its shape can
// be compared across terminal widths.
func keysPanelOf(header string) string {
	var out []string
	for _, line := range strings.Split(stripANSI(header), "\n") {
		i := strings.Index(line, "╮╭")
		j := strings.LastIndex(line, "╮╭")
		if i < 0 || i == j {
			continue
		}
		out = append(out, line[i+len("╮╭"):j])
	}
	return strings.Join(out, "\n")
}

// TestKeyMapDoesNotReflowOnWideTerminals is the regression test for the key map
// spreading into sparse columns as the terminal grows. It is a fixed width, and
// the logo panel takes the extra space instead.
func TestKeyMapDoesNotReflowOnWideTerminals(t *testing.T) {
	reference := keysPanelOf(resizeModel(110).renderHeader(110))
	if reference == "" {
		t.Fatal("could not isolate the Keys panel")
	}
	for _, w := range []int{112, 120, 140, 150, maxWidth} {
		if got := keysPanelOf(resizeModel(w).renderHeader(w)); got != reference {
			t.Errorf("Keys panel changed shape at width %d:\n--- %d ---\n%s\n--- 110 ---\n%s",
				w, w, got, reference)
		}
	}
}

// TestLogoAppearsOnlyWhenThereIsRoom covers the logo being the first thing
// dropped as the terminal narrows, since it carries no information.
func TestLogoAppearsOnlyWhenThereIsRoom(t *testing.T) {
	wide := stripANSI(resizeModel(140).renderHeader(140))
	if !strings.Contains(wide, logoArt[0]) {
		t.Errorf("logo missing on a wide terminal:\n%s", wide)
	}

	for _, w := range []int{70, 95} {
		narrow := stripANSI(resizeModel(w).renderHeader(w))
		if strings.Contains(narrow, logoArt[0]) {
			t.Errorf("logo drawn at width %d, where the space is needed:\n%s", w, narrow)
		}
		// Dropping it must not cost any actual information.
		for _, want := range []string{"AWS Profile", "ADC", "navigate", "quit"} {
			if !strings.Contains(narrow, want) {
				t.Errorf("width %d lost %q when the logo was dropped", w, want)
			}
		}
	}
}

// TestLogoArtIsRectangular guards the wordmark itself: a ragged block would
// break the panel's right border.
func TestLogoArtIsRectangular(t *testing.T) {
	want := lipgloss.Width(logoArt[0])
	for i, line := range logoArt {
		if got := lipgloss.Width(line); got != want {
			t.Errorf("logo line %d is %d cells wide, want %d", i, got, want)
		}
	}
	if want+2 > logoPanelW {
		t.Errorf("logo is %d cells wide but the panel is only %d", want, logoPanelW)
	}
}
