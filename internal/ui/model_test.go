package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vboyadzhiev/cx/internal/cloud"
)

// TestViewRendersWithoutPanic exercises the layout against a populated model.
// The renderer does a lot of width arithmetic on styled strings, which is the
// kind of code that panics on an edge case long after it was written.
func TestViewRendersWithoutPanic(t *testing.T) {
	m := Model{
		loaded:    true,
		canSwitch: true,
		width:     120,
		aws: []cloud.Target{
			{Name: "default", Kind: cloud.KindStatic, Account: "111122223333",
				Scope: "us-east-1", Active: true, Health: cloud.Valid, Identity: "user/vasil"},
			{Name: "oldclient", Kind: cloud.KindStaticTemp, Health: cloud.Expired,
				Detail: "session token expired"},
			{Name: "a-very-long-profile-name-that-overflows-its-column",
				Kind: cloud.KindSSOSession, Health: cloud.Probing},
		},
		gcp: []cloud.Target{
			{Name: "prod", Kind: cloud.KindUser, Account: "me@example.com",
				Scope: "acme-prod", Active: true, Health: cloud.Valid},
		},
		gcpState: cloud.GCPState{Active: "prod", Global: "prod", Source: cloud.SourceGlobal},
		adc:      cloud.ADC{Present: true, QuotaProject: "acme-dev", Identity: "me@example.com", Health: cloud.Valid},
	}

	out := m.View()
	for _, want := range []string{"AWS", "GCP", "ADC", "default", "oldclient", "prod", "session token expired"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q", want)
		}
	}
	// The cross-cutting hazards must reach the screen, not just the audit.
	if !strings.Contains(out, "machine-global") {
		t.Error("View() did not surface the machine-global alert")
	}
	if !strings.Contains(out, "bills API calls to") {
		t.Error("View() did not surface the ADC drift alert")
	}
}

func TestViewBeforeLoad(t *testing.T) {
	if out := New("").View(); !strings.Contains(out, "scanning") {
		t.Errorf("pre-load view = %q", out)
	}
}

// TestNarrowTerminalDoesNotPanic guards the width fallback.
func TestNarrowTerminalDoesNotPanic(t *testing.T) {
	m := Model{loaded: true, width: 20, aws: []cloud.Target{{Name: "x", Kind: cloud.KindStatic}}}
	_ = m.View()
}

func TestEmptyStateOffersTheAddAction(t *testing.T) {
	// A dashboard with nothing in it must say what to do, not just report the
	// absence -- that state is exactly when a new user first opens it.
	m := Model{loaded: true, canSwitch: true, width: 120}
	out := m.View()

	if !strings.Contains(out, "no profiles yet") || !strings.Contains(out, "to add one") {
		t.Errorf("empty AWS section gives no next step:\n%s", out)
	}
	// The key map moved into the header block, k9s style.
	if !strings.Contains(out, "<a>") || !strings.Contains(out, "add") {
		t.Errorf("header key map does not advertise the add key:\n%s", out)
	}
}

func TestAddMenuListsEveryAction(t *testing.T) {
	m := Model{loaded: true, canSwitch: true, width: 120, mode: modeAdd}
	out := m.View()

	for _, e := range addEntries() {
		if !strings.Contains(out, e.label) {
			t.Errorf("add menu missing %q", e.label)
		}
	}
	if !strings.Contains(out, "esc cancel") {
		t.Error("add menu gives no way out")
	}
}

func TestRowIsClippedToTerminalWidth(t *testing.T) {
	// A long explanation must not run off the edge, which is what a fixed-width
	// layout does on a narrow terminal.
	long := cloud.Target{
		Name: "x", Kind: cloud.KindStatic, Health: cloud.Expired,
		Detail: strings.Repeat("very long failure explanation ", 10),
	}
	for _, w := range []int{60, 72, 80, 100, 120, 160, 200} {
		line := Model{width: w}.renderRow(long, 0, w)
		if got := lipgloss.Width(strings.TrimRight(line, "\n")); got > w {
			t.Errorf("at width %d the row rendered %d cells wide", w, got)
		}
	}
}

// stripANSI removes escape sequences so a rendered line can be measured and
// searched as plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// columnOf returns the display column a token starts at, rather than its byte
// offset. Glyphs such as ● and ▪ are three bytes but one cell wide, so byte
// offsets would report a misalignment that is not there.
func columnOf(line, token string) int {
	i := strings.Index(line, token)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(line[:i])
}

// TestHeaderAlignsWithRows is the regression test for the column headers
// sitting three cells left of the data they label. Header and rows derive their
// widths from the same place, and this proves they stay in step.
func TestHeaderAlignsWithRows(t *testing.T) {
	for _, w := range []int{80, 100, 120, 160} {
		m := Model{loaded: true, width: w, cursor: -1,
			aws: []cloud.Target{{Name: "profilename", Kind: cloud.KindStatic,
				Account: "111122223333", Scope: "eu-west-1", Health: cloud.Valid}}}

		row := 0
		panel := stripANSI(m.renderPanel("AWS Profiles", m.aws, &row, w, "none", 99))
		lines := strings.Split(panel, "\n")
		if len(lines) < 3 {
			t.Fatalf("panel too short at width %d", w)
		}

		hdr, data := lines[1], lines[2]
		hdrAt := columnOf(hdr, "NAME")
		dataAt := columnOf(data, "profilename")
		if hdrAt < 0 || dataAt < 0 {
			t.Fatalf("width %d: could not locate columns\nhdr:  %q\ndata: %q", w, hdr, data)
		}
		if hdrAt != dataAt {
			t.Errorf("width %d: NAME header at column %d but names start at %d\nhdr:  %q\ndata: %q",
				w, hdrAt, dataAt, hdr, data)
		}
	}
}

// TestActiveMarkerDoesNotShiftTheNameColumn covers the marker being its own
// cell rather than a prefix glued onto the name.
func TestActiveMarkerDoesNotShiftTheNameColumn(t *testing.T) {
	const w = 120
	plainRow := Model{width: w, cursor: -1}.renderRow(
		cloud.Target{Name: "sameplace", Health: cloud.Valid}, 0, w)
	activeRow := Model{width: w, cursor: -1}.renderRow(
		cloud.Target{Name: "sameplace", Health: cloud.Valid, Active: true}, 0, w)

	a := columnOf(stripANSI(plainRow), "sameplace")
	b := columnOf(stripANSI(activeRow), "sameplace")
	if a != b {
		t.Errorf("name column moves when a row is active: %d vs %d", a, b)
	}
}

// TestEveryPanelLineIsTheSameWidth guards the right-hand border lining up.
func TestEveryPanelLineIsTheSameWidth(t *testing.T) {
	for _, w := range []int{72, 100, 140} {
		m := Model{loaded: true, width: w, cursor: 1,
			aws: []cloud.Target{
				{Name: "short", Kind: cloud.KindStatic, Health: cloud.Valid, Identity: "a"},
				{Name: "an-active-one", Kind: cloud.KindSSOSession, Active: true,
					Health: cloud.Expired, Detail: strings.Repeat("long detail ", 8)},
			}}
		row := 0
		for i, line := range strings.Split(m.renderPanel("AWS", m.aws, &row, w, "none", 99), "\n") {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("width %d: panel line %d is %d cells wide: %q", w, i, got, stripANSI(line))
			}
		}
	}
}

// TestSelectedRowIsPaintedEdgeToEdge covers the highlight actually covering the
// row, rather than only the text within it.
func TestSelectedRowIsPaintedEdgeToEdge(t *testing.T) {
	const w = 120
	m := Model{loaded: true, width: w, cursor: 0,
		aws: []cloud.Target{{Name: "picked", Kind: cloud.KindStatic, Health: cloud.Valid}}}

	// lipgloss emits no escape codes when it cannot detect a terminal, which is
	// the case under `go test`, so the profile is forced for this check.
	saved := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(saved)

	line := m.renderRow(m.aws[0], 0, w)
	if !strings.Contains(line, "\x1b[") {
		t.Fatal("selected row carries no styling at all")
	}
	// The painted span must reach the full inner width, not stop after the text.
	plain := stripANSI(line)
	if got := lipgloss.Width(plain); got != w {
		t.Errorf("selected row is %d cells wide, want %d", got, w)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(plain, "│"), "│")
	if strings.TrimRight(inner, " ") == inner {
		t.Error("selected row does not pad out to the right border")
	}
}

// TestHeaderPanelsSpanTheFullWidth covers the two header panels being joined
// without a gap or an overhang at any terminal size.
func TestHeaderPanelsSpanTheFullWidth(t *testing.T) {
	for _, w := range []int{80, 100, 112, 140, 160} {
		m := Model{loaded: true, width: w,
			gcpState: cloud.GCPState{Active: "dev", Source: cloud.SourceShell},
			adc:      cloud.ADC{Present: true, Identity: "someone@example.com"}}

		for i, line := range strings.Split(m.renderHeader(w), "\n") {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("width %d: header line %d is %d cells wide:\n%s",
					w, i, got, stripANSI(line))
			}
		}
	}
}

// TestHeaderPanelsAreTheSameHeight covers the key map being laid out in columns
// to match the context panel, rather than trailing blank lines beside it.
func TestHeaderPanelsAreTheSameHeight(t *testing.T) {
	m := Model{loaded: true, width: 120}
	lines := strings.Split(m.renderHeader(120), "\n")

	// Two borders plus five body rows.
	if len(lines) != 7 {
		t.Errorf("header is %d lines, want 7:\n%s", len(lines), stripANSI(m.renderHeader(120)))
	}
	// Every key must still be reachable after the wrap into columns.
	out := stripANSI(m.renderHeader(120))
	for _, k := range []string{"navigate", "switch", "add", "edit", "delete", "login", "clear", "refresh", "quit"} {
		if !strings.Contains(out, k) {
			t.Errorf("key %q was lost in the column layout", k)
		}
	}
}

// TestWarningsPanelAppearsOnlyWhenThereIsSomethingToSay guards against a panel
// that is always on screen, which is the same noise problem as a permanent
// prompt warning.
func TestWarningsPanelAppearsOnlyWhenThereIsSomethingToSay(t *testing.T) {
	clean := Model{loaded: true, width: 120,
		gcp:      []cloud.Target{{Name: "dev", Scope: "acme-dev", Account: "me@x.com", Active: true}},
		gcpState: cloud.GCPState{Active: "dev", Source: cloud.SourceShell},
		adc:      cloud.ADC{Present: true, QuotaProject: "acme-dev", Identity: "me@x.com"}}
	if got := clean.renderAlerts(120); got != "" {
		t.Errorf("warnings panel rendered with nothing to report:\n%s", stripANSI(got))
	}

	drifted := clean
	drifted.adc.QuotaProject = "acme-prod"
	out := drifted.renderAlerts(120)
	if !strings.Contains(stripANSI(out), "Warnings(1)") {
		t.Errorf("warnings panel missing its count:\n%s", stripANSI(out))
	}
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if got := lipgloss.Width(line); got != 120 {
			t.Errorf("warnings line %d is %d cells wide", i, got)
		}
	}
}
