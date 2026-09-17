package ui

import (
	"context"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/san-est/cx/internal/cloud"
	"github.com/san-est/cx/internal/shellcfg"
)

// loadedMsg carries the result of the disk-only scan, which is fast enough to
// render immediately.
type loadedMsg struct {
	aws      []cloud.Target
	gcp      []cloud.Target
	gcpState cloud.GCPState
	adc      cloud.ADC
	err      error
}

// execDoneMsg reports that a suspended external command has finished.
type execDoneMsg struct{ err error }

// mode is which key map is active.
type mode int

const (
	// modeNormal is the browsing key map.
	modeNormal mode = iota
	// modeAdd shows the menu of ways to create a new target.
	modeAdd
	// modeForm shows a modal form collecting a new target's details.
	modeForm
	// modeConfirm asks before doing something irreversible.
	modeConfirm
)

// probedMsg carries the result of the network probes.
type probedMsg struct {
	aws []cloud.Target
	gcp []cloud.Target
	adc cloud.ADC
}

// Model is the dashboard state.
type Model struct {
	aws      []cloud.Target
	gcp      []cloud.Target
	gcpState cloud.GCPState
	adc      cloud.ADC

	cursor  int
	probing bool
	loaded  bool
	err     error
	mode    mode
	// active is the form on screen in modeForm.
	active *form
	// addCursor is the highlighted entry in the add menu.
	addCursor int
	// pending confirmation, set while mode is modeConfirm.
	confirm *confirmation
	// notice reports the outcome of the last action.
	notice string

	// canSwitch reports whether the shell wrapper is installed. Without it
	// there is nowhere to hand environment changes back to, so the UI says so
	// instead of offering an action that would do nothing.
	canSwitch bool
	// pending holds the environment changes chosen before quitting.
	pending *shellcfg.Script

	width  int
	height int
}

// New returns an empty dashboard. shellOut is the path the wrapper function
// gave us to write environment changes into; an empty string means cx was run
// without the wrapper.
func New(shellOut string) Model {
	return Model{canSwitch: shellOut != ""}
}

// PendingScript returns the environment changes the user selected, or nil if
// they quit without switching. The caller applies it after the program exits.
func (m Model) PendingScript() *shellcfg.Script { return m.pending }

func (m Model) Init() tea.Cmd { return loadCmd }

// loadCmd reads every provider's configuration from disk. No network, so this
// returns in single-digit milliseconds and the user sees their profiles
// immediately rather than waiting on STS.
func loadCmd() tea.Msg {
	var msg loadedMsg
	a, err := cloud.LoadAWS()
	if err != nil {
		msg.err = err
	}
	msg.aws = a

	g, st, err := cloud.LoadGCP()
	if err != nil && msg.err == nil {
		msg.err = err
	}
	msg.gcp, msg.gcpState = g, st
	msg.adc = cloud.LoadADC()

	prod, err := cloud.LoadProduction()
	if err != nil && msg.err == nil {
		// Surfaced rather than swallowed: a configuration cx cannot read must
		// not quietly mean "nothing here is production".
		msg.err = err
	}
	cloud.MarkProduction(msg.aws, prod, "aws")
	cloud.MarkProduction(msg.gcp, prod, "gcp")
	return msg
}

// probeCmd runs every credential check concurrently. It operates on copies so
// that the model is only ever mutated on the Bubble Tea update loop.
func probeCmd(aws, gcp []cloud.Target, adc cloud.ADC) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		a := append([]cloud.Target(nil), aws...)
		g := append([]cloud.Target(nil), gcp...)
		d := adc

		done := make(chan struct{}, 3)
		go func() { cloud.ProbeAWS(ctx, a, 8); done <- struct{}{} }()
		go func() { cloud.ProbeGCP(ctx, g, 6); done <- struct{}{} }()
		go func() { cloud.ProbeADC(ctx, &d); done <- struct{}{} }()
		for i := 0; i < 3; i++ {
			<-done
		}
		return probedMsg{aws: a, gcp: g, adc: d}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Shrinking the terminal leaves the previous, larger frame behind:
		// the renderer only repaints lines it believes changed. Clearing forces
		// a clean repaint at the new size.
		return m, tea.ClearScreen

	case tea.KeyMsg:
		// A confirmation is answered before anything else can happen.
		if m.mode == modeConfirm && m.confirm != nil {
			switch msg.String() {
			case "y", "enter":
				if s := m.confirm.script; s != nil {
					m.pending = s
					m.mode, m.confirm = modeNormal, nil
					return m, tea.Quit
				}
				note, err := m.confirm.act()
				if err != nil {
					m.notice = err.Error()
				} else {
					m.notice = note
				}
				m.mode, m.confirm = modeNormal, nil
				// Keep the cursor inside the shortened list.
				if m.cursor > 0 && m.cursor >= m.rowCount()-1 {
					m.cursor--
				}
				return m, loadCmd
			case "n", "esc", "q":
				m.mode, m.confirm = modeNormal, nil
				return m, nil
			}
			return m, nil
		}

		// A form takes every key, including ones that are shortcuts elsewhere:
		// the user is typing a profile name, not driving the dashboard.
		if m.mode == modeForm && m.active != nil {
			res, cmd := m.active.update(msg)
			switch res {
			case formCancelled:
				m.mode, m.active = modeNormal, nil
				return m, nil
			case formSubmitted:
				m.notice = m.active.err
				m.mode, m.active = modeNormal, nil
				// The files on disk changed, so reload rather than patching
				// what is already on screen.
				return m, loadCmd
			}
			return m, cmd
		}

		// The add menu is navigated the same way as the tables, rather than
		// with mnemonic letters that have to be learned.
		if m.mode == modeAdd {
			entries := addEntries()
			switch msg.String() {
			case "esc", "q":
				m.mode = modeNormal
				return m, nil
			case "up", "k":
				if m.addCursor > 0 {
					m.addCursor--
				}
				return m, nil
			case "down", "j":
				if m.addCursor < len(entries)-1 {
					m.addCursor++
				}
				return m, nil
			case "enter":
				return m.openAddEntry(entries[m.addCursor])
			}
			// A number key jumps straight to an entry, for when the list is
			// familiar enough to skip the arrows.
			if n := int(msg.String()[0]) - '1'; len(msg.String()) == 1 && n >= 0 && n < len(entries) {
				m.addCursor = n
				return m.openAddEntry(entries[n])
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit

		case "a":
			m.mode, m.addCursor, m.notice = modeAdd, 0, ""
			return m, nil

		case "l":
			if a, ok := m.loginActionForCursor(); ok {
				return m, runAction(a)
			}
			return m, nil

		case "e":
			t, provider, ok := m.targetAtCursor()
			if !ok {
				return m, nil
			}
			f, err := editFormFor(t, provider)
			if err != nil {
				m.notice = err.Error()
				return m, nil
			}
			m.mode, m.active, m.notice = modeForm, f, ""
			return m, textinput.Blink

		case "d":
			c, ok := m.deleteConfirmation()
			if !ok {
				return m, nil
			}
			m.mode, m.confirm, m.notice = modeConfirm, c, ""
			return m, nil

		case "enter":
			if !m.canSwitch {
				return m, nil
			}
			s := m.scriptForCursor()
			if s == nil {
				return m, nil
			}
			if t, _, ok := m.targetAtCursor(); ok && t.Sensitive {
				m.mode, m.confirm, m.notice = modeConfirm, switchConfirmation(t, s), ""
				return m, nil
			}
			m.pending = s
			return m, tea.Quit

		case "x":
			if !m.canSwitch {
				return m, nil
			}
			m.pending = cloud.Clear("all")
			return m, tea.Quit

		case "r":
			if m.probing {
				return m, nil
			}
			m.probing = true
			m = m.markProbing()
			return m, probeCmd(m.aws, m.gcp, m.adc)

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case "down", "j":
			if m.cursor < m.rowCount()-1 {
				m.cursor++
			}
			return m, nil
		}
		return m, nil

	case loadedMsg:
		m.aws, m.gcp, m.gcpState, m.adc = msg.aws, msg.gcp, msg.gcpState, msg.adc
		m.err = msg.err
		m.loaded = true
		m.probing = true
		m = m.markProbing()
		return m, probeCmd(m.aws, m.gcp, m.adc)

	case probedMsg:
		m.aws, m.gcp, m.adc = msg.aws, msg.gcp, msg.adc
		m.probing = false
		return m, nil

	case execDoneMsg:
		// Whatever the command did, the configuration on disk may have changed,
		// so reload rather than trusting what is on screen.
		if msg.err != nil {
			m.notice = "command failed: " + msg.err.Error()
		} else {
			m.notice = ""
		}
		return m, loadCmd
	}
	return m, nil
}

// openAddEntry starts whichever kind of add the entry describes.
func (m Model) openAddEntry(e addEntry) (tea.Model, tea.Cmd) {
	if e.form != nil {
		m.mode, m.active, m.notice = modeForm, e.form(), ""
		return m, textinput.Blink
	}
	m.mode = modeNormal
	return m, runAction(*e.action)
}

// confirmation is a yes/no question about something that cannot be undone.
type confirmation struct {
	title  string
	detail string
	// act performs the change and returns the message to show afterwards.
	act func() (string, error)
	// script, when set, is handed to the calling shell instead of running act.
	// A switch can only take effect once the dashboard exits and the wrapper
	// sources what it wrote, so this answer quits rather than redrawing.
	script *shellcfg.Script
}

// targetAtCursor returns the selected target and which provider it belongs to.
func (m Model) targetAtCursor() (cloud.Target, string, bool) {
	if m.cursor < len(m.aws) {
		return m.aws[m.cursor], "aws", true
	}
	if i := m.cursor - len(m.aws); i >= 0 && i < len(m.gcp) {
		return m.gcp[i], "gcp", true
	}
	return cloud.Target{}, "", false
}

// switchConfirmation asks before pointing the shell at a target the user has
// flagged as production. Unlike a delete this is reversible, but it is the
// moment at which a later command silently acquires a blast radius.
func switchConfirmation(t cloud.Target, s *shellcfg.Script) *confirmation {
	return &confirmation{
		title:  "Point this shell at " + t.Name + "?",
		detail: "marked production in " + cloud.ConfigPath(),
		script: s,
	}
}

// deleteConfirmation describes removing the selected target.
func (m Model) deleteConfirmation() (*confirmation, bool) {
	t, provider, ok := m.targetAtCursor()
	if !ok {
		return nil, false
	}

	what := "AWS profile"
	detail := "removes it from ~/.aws/config and ~/.aws/credentials"
	act := func() (string, error) {
		if err := cloud.DeleteAWSProfile(t.Name); err != nil {
			return "", err
		}
		return "deleted AWS profile " + t.Name, nil
	}
	if provider == "gcp" {
		what = "gcloud configuration"
		detail = "removes it from ~/.config/gcloud"
		act = func() (string, error) {
			if err := cloud.DeleteGCPConfig(t.Name); err != nil {
				return "", err
			}
			return "deleted gcloud configuration " + t.Name, nil
		}
	}
	if t.Active {
		// Deleting what the shell is pointed at leaves it aimed at nothing.
		detail += " — this is what this shell is currently using"
	}

	return &confirmation{
		title:  "Delete " + what + " " + t.Name + "?",
		detail: detail,
		act:    act,
	}, true
}

// runAction suspends the dashboard and hands the terminal to an external CLI.
// These flows are interactive and vendor-owned; cx does not reimplement them.
func runAction(a cloud.Action) tea.Cmd {
	c := exec.Command(a.Cmd, a.Args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return execDoneMsg{err: err}
	})
}

// loginActionForCursor picks the right re-authentication command for the
// selected row, which depends on how that target gets its credentials.
func (m Model) loginActionForCursor() (cloud.Action, bool) {
	if m.cursor < len(m.aws) {
		return cloud.LoginAction(m.aws[m.cursor]), true
	}
	if i := m.cursor - len(m.aws); i < len(m.gcp) {
		return cloud.LoginActionGCP(m.gcp[i]), true
	}
	return cloud.Action{}, false
}

// scriptForCursor builds the environment changes for whichever row is selected.
func (m Model) scriptForCursor() *shellcfg.Script {
	if m.cursor < len(m.aws) {
		return cloud.SwitchAWS(m.aws[m.cursor].Name)
	}
	if i := m.cursor - len(m.aws); i < len(m.gcp) {
		return cloud.SwitchGCP(m.gcp[i].Name)
	}
	return nil
}

// markProbing flips every pending target to the probing state so the user sees
// the refresh take effect immediately.
func (m Model) markProbing() Model {
	for i := range m.aws {
		if m.aws[i].Kind != cloud.KindNone {
			m.aws[i].Health, m.aws[i].Detail = cloud.Probing, ""
		}
	}
	for i := range m.gcp {
		if m.gcp[i].Account != "" {
			m.gcp[i].Health, m.gcp[i].Detail = cloud.Probing, ""
		}
	}
	if m.adc.Present {
		m.adc.Health, m.adc.Detail = cloud.Probing, ""
	}
	return m
}

func (m Model) rowCount() int { return len(m.aws) + len(m.gcp) }

const (
	// minWidth is the narrowest terminal the dashboard will draw into.
	minWidth = 44
	// maxWidth stops the tables stretching into unreadable lines on a very
	// wide terminal.
	maxWidth = 160
	// sideBySideWidth is the point below which the two header panels stack
	// instead of sitting next to each other.
	sideBySideWidth = 84
)

// note renders a loose line beneath the panels, clipped to the terminal width.
// These lines sit outside any box, so nothing else constrains them.
func note(style lipgloss.Style, w int, text string) string {
	return " " + style.Render(truncate(text, w-1)) + "\n"
}

// layoutHeight is the number of rows the dashboard may draw into. A zero
// height means no size message has arrived yet.
//
// Exceeding the terminal height is what leaves torn, duplicated frames on
// screen, so every screen is budgeted against this.
func (m Model) layoutHeight() int {
	if m.height == 0 {
		return 40
	}
	return m.height
}

// countLines reports how many terminal rows a rendered block occupies.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}

// layoutWidth is the width the dashboard draws into. A zero width means no
// size message has arrived yet, which happens for the first frame.
func (m Model) layoutWidth() int {
	w := m.width
	if w == 0 {
		return 100
	}
	if w > maxWidth {
		return maxWidth
	}
	return w
}

func (m Model) View() string {
	if !m.loaded {
		return "\n  scanning cloud configuration...\n"
	}

	w := m.layoutWidth()
	if w < minWidth {
		// Below this nothing can be laid out honestly, and a broken table is
		// worse than saying so.
		// Even this message has to fit, or the fallback overflows too.
		return "\n " + warnStyle.Render(truncate("terminal too narrow", w-1)) + "\n " +
			dimStyle.Render(truncate("widen to "+itoa(minWidth)+" columns", w-1)) + "\n"
	}

	// A form is modal: showing the dashboard behind it just competes for
	// attention while the user is typing.
	if m.mode == modeForm && m.active != nil {
		return "\n" + m.active.view(w) + "\n"
	}

	// A very short terminal cannot fit everything. Drop the optional sections
	// in order of how little they are missed, and clip as a last resort, rather
	// than drawing past the bottom and tearing the frame.
	h := m.layoutHeight()
	for _, showAlerts := range []bool{true, false} {
		for _, showADC := range []bool{true, false} {
			out := m.compose(w, showAlerts, showADC)
			if countLines(out) <= h {
				return out
			}
		}
	}
	return clipHeight(m.compose(w, false, false), h)
}

// clipHeight truncates a screen to at most h rows, marking that it was cut.
func clipHeight(s string, h int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= h {
		return s
	}
	if h < 1 {
		return ""
	}
	lines = lines[:h]
	lines[h-1] = dimStyle.Render("… terminal too short to show everything")
	return strings.Join(lines, "\n") + "\n"
}

// compose builds the dashboard. showAlerts and showADC allow the caller to
// leave out the optional panels when the terminal is too short for them.
func (m Model) compose(w int, showAlerts, showADC bool) string {
	header := m.renderHeader(w)
	// Alerts come before the tables: they describe hazards that make the data
	// below untrustworthy.
	alerts := ""
	if showAlerts {
		alerts = m.renderAlerts(w)
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(header + "\n")
	b.WriteString(alerts)

	// Everything that is not a table costs a fixed number of rows; whatever is
	// left is shared between the two tables. Overflowing the terminal is what
	// tears the frame, so the budget is computed before anything is drawn.
	adc := ""
	if showADC {
		adc = m.renderADCPanel(w)
	}
	awsBudget, gcpBudget := m.tableBudgets(header, alerts, adc)

	row := 0
	b.WriteString(m.renderPanel("AWS Profiles", m.aws, &row, w,
		"no profiles yet — press a to add one", awsBudget))
	b.WriteString("\n")
	b.WriteString(m.renderPanel("GCP Configurations", m.gcp, &row, w,
		"no configurations yet — press a to add one", gcpBudget))
	b.WriteString("\n")
	if showADC {
		b.WriteString(adc)
		b.WriteString("\n")
	}

	if m.mode == modeConfirm && m.confirm != nil {
		b.WriteString(m.renderConfirm(w))
		return b.String()
	}
	if m.mode == modeAdd {
		b.WriteString(m.renderAddMenu(w))
		return b.String()
	}

	if m.notice != "" {
		b.WriteString(note(okStyle, w, m.notice))
	}
	if m.rowCount() == 1 {
		// With nothing to move between, the arrow keys do nothing, which reads
		// as a broken key map rather than an empty machine. Say which it is.
		b.WriteString(note(dimStyle, w,
			"only one target on this machine, so ↑/↓ have nowhere to go — press a to add another"))
	}
	if !m.canSwitch {
		b.WriteString(note(warnStyle, w,
			`switching needs the shell wrapper: add  eval "$(cx shell-init zsh)"  to ~/.zshrc`))
	}
	return b.String()
}

// logoArt is the wordmark shown in the top right, the way k9s carries its own.
//
// It is a fixed size on purpose. Giving the key map all the spare width was
// what made it reflow into four sparse columns on a wide terminal; a fixed
// logo absorbs the slack instead, so the key map keeps a stable shape and the
// context panel gets the growth, where a long account email can use it.
var logoArt = []string{
	" ▄████▄ ██   ██",
	"██       ▀█▄█▀ ",
	"██        ███  ",
	"██       ▄█▀█▄ ",
	" ▀████▀ ██   ██",
}

const (
	// logoPanelW fits logoArt with a space either side.
	logoPanelW = 19
	// keysPanelW fits the key map in two stable columns.
	keysPanelW = 44
	// contextPanelMinW is the narrowest the context panel is worth drawing.
	contextPanelMinW = 40
	// contextPanelMaxW stops the context panel stretching into mostly blank
	// space on a wide terminal; the logo takes the remainder instead.
	contextPanelMaxW = 58
)

// renderHeader draws the top block. It sheds panels as the terminal narrows:
// the logo goes first, then the panels stack rather than overflow.
func (m Model) renderHeader(w int) string {
	// The app name lives in the panel title rather than on a line of its own,
	// which keeps the whole screen inside boxes.
	title := "cx · context"
	if m.probing {
		title = "cx · context · checking…"
	}

	// Side by side needs enough room for both panels to stay readable;
	// below that they stack, which is the only honest option.
	if w < sideBySideWidth {
		return box(title, -1, w, m.contextRows(w-2)) + "\n" +
			box("Keys", -1, w, m.keyRows(w-2))
	}

	if ctxW := w - keysPanelW - logoPanelW; ctxW >= contextPanelMinW {
		if ctxW > contextPanelMaxW {
			ctxW = contextPanelMaxW
		}
		// Whatever is left over goes to the logo, which is why the key map
		// keeps the same shape at every width above this point.
		logoW := w - ctxW - keysPanelW
		return lipgloss.JoinHorizontal(lipgloss.Top,
			box(title, -1, ctxW, m.contextRows(ctxW-2)),
			box("Keys", -1, keysPanelW, m.keyRows(keysPanelW-2)),
			box("", -1, logoW, logoRows(logoW-2)),
		)
	}

	// Not enough room for the logo: the context panel takes what it needs and
	// the key map takes the rest.
	ctxW := 46
	if ctxW > w/2 {
		ctxW = w / 2
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		box(title, -1, ctxW, m.contextRows(ctxW-2)),
		box("Keys", -1, w-ctxW, m.keyRows(w-ctxW-2)),
	)
}

// logoRows centres the wordmark inside a panel of the given inner width.
func logoRows(inner int) []string {
	rows := make([]string, 0, len(logoArt))
	for _, line := range logoArt {
		gap := (inner - lipgloss.Width(line)) / 2
		if gap < 0 {
			gap = 0
		}
		rows = append(rows, strings.Repeat(" ", gap)+logoStyle.Render(line))
	}
	return rows
}

// contextRows lists what the current shell resolves to.
func (m Model) contextRows(inner int) []string {
	info := [][2]string{
		{"AWS Profile", m.activeName(m.aws)},
		{"GCP Config", m.gcpState.Active},
		{"GCP Project", m.activeScope(m.gcp)},
		{"ADC", m.adcSummary()},
		{"GCP Pin", m.gcpState.Source.String()},
	}

	labelW := 13
	if labelW > inner/2 {
		labelW = inner / 2
	}

	rows := make([]string, 0, len(info))
	for _, kv := range info {
		valW := inner - labelW - 1
		if valW < 1 {
			valW = 1
		}
		rendered := dimStyle.Render("—")
		if kv[1] != "" {
			rendered = textStyle.Render(truncate(kv[1], valW))
		}
		rows = append(rows, " "+pad(infoKeyStyle.Render(truncate(kv[0]+":", labelW)), labelW)+rendered)
	}
	return rows
}

// keyRows lays the key map out in two columns, so the panel is the same height
// as the context panel beside it rather than trailing empty lines under it.
func (m Model) keyRows(inner int) []string {
	keys := [][2]string{
		{"↑↓", "navigate"},
		{"enter", "switch"},
		{"a", "add"},
		{"e", "edit"},
		{"d", "delete"},
		{"l", "login"},
		{"x", "clear"},
		{"r", "refresh"},
		{"q", "quit"},
	}

	// A key plus its description needs about this much room; the column count
	// follows from how many of those fit, not the other way round.
	const cellW = 19

	cols := (inner - 1) / cellW
	if cols < 1 {
		cols = 1
	}
	if cols > len(keys) {
		cols = len(keys)
	}
	height := (len(keys) + cols - 1) / cols
	colW := (inner - 1) / cols

	rows := make([]string, 0, height)
	for r := 0; r < height; r++ {
		line := " "
		for c := 0; c < cols; c++ {
			i := c*height + r
			if i >= len(keys) {
				break
			}
			keyW := 9
			if keyW > colW-1 {
				keyW = colW - 1
			}
			if keyW < 3 {
				keyW = 3
			}
			cell := pad(keyStyle.Render("<"+keys[i][0]+">"), keyW) +
				keyDescSty.Render(truncate(keys[i][1], colW-keyW))
			line += pad(cell, colW)
		}
		rows = append(rows, strings.TrimRight(line, " "))
	}
	return rows
}

// renderAlerts draws the cross-cutting hazards as their own panel, so they read
// as findings rather than as stray lines above the tables.
func (m Model) renderAlerts(w int) string {
	alerts := cloud.Audit(m.gcp, m.gcpState, m.adc)
	if len(alerts) == 0 {
		return ""
	}

	rows := make([]string, 0, len(alerts)*2)
	for _, a := range alerts {
		mark := warnStyle.Render("▲")
		if a.Severity == cloud.Danger {
			mark = alertStyle.Render("⚠")
		}
		rows = append(rows, " "+mark+" "+textStyle.Render(truncate(a.Title, w-6)))
		rows = append(rows, "   "+dimStyle.Render(truncate(a.Fix, w-6)))
	}
	return box("Warnings", len(alerts), w, rows) + "\n"
}

// nameCell renders a name with its production flag, trimming the name rather
// than the flag. A narrow terminal dropping the name is a cosmetic loss;
// dropping the flag would quietly remove the warning this feature exists for.
func nameCell(t cloud.Target, w int) string {
	if !t.Sensitive {
		return pad(truncate(t.Name, w), w)
	}
	const tag = " [prod]"
	if w <= len(tag) {
		return pad(truncate(tag, w), w)
	}
	return pad(truncate(t.Name, w-len(tag))+tag, w)
}

func (m Model) activeName(ts []cloud.Target) string {
	for _, t := range ts {
		if t.Active {
			if t.Sensitive {
				return t.Name + " [prod]"
			}
			return t.Name
		}
	}
	return ""
}

func (m Model) activeScope(ts []cloud.Target) string {
	for _, t := range ts {
		if t.Active {
			return t.Scope
		}
	}
	return ""
}

func (m Model) adcSummary() string {
	if !m.adc.Present {
		return "not configured"
	}
	if m.adc.Identity != "" {
		return m.adc.Identity
	}
	return string(m.adc.Kind)
}

// rowPrefix is the width of the leading status glyph and active marker, with
// their separating spaces: " X M ". The header pads by the same amount, which
// is why it is a named constant rather than a literal in two places.
const rowPrefix = 5

// columns returns the widths used for a table at the given panel width.
//
// Two rules shape it. Narrower terminals get narrower name and scope columns
// rather than losing whole columns straight away, and the status column keeps a
// minimum share, since a status clipped to "val…" tells the reader nothing.
// Beyond that, columns are dropped in reverse order of usefulness: the account
// id first, then the credential kind. The name and the scope are never dropped
// -- they are the answer to the question the dashboard exists to answer.
func columns(inner int) (nameW, kindW, acctW, scopeW int, showKind, showAcct bool) {
	const statusMin = 14

	nameW, kindW, acctW, scopeW = 22, 14, 16, 20
	switch {
	case inner < 76:
		nameW, kindW, scopeW = 14, 11, 12
	case inner < 96:
		nameW, kindW, scopeW = 18, 12, 16
	}

	// Glyph, active marker and the single spaces between cells.
	fixed := 4

	if nameW+scopeW+fixed+statusMin > inner {
		spare := inner - fixed - statusMin
		if spare > 12 {
			nameW, scopeW = spare/2, spare-spare/2
		} else {
			nameW, scopeW = 8, 8
		}
		return nameW, kindW, acctW, scopeW, false, false
	}

	used := nameW + scopeW + fixed
	showKind = used+kindW+1+statusMin <= inner
	if showKind {
		used += kindW + 1
	}
	showAcct = showKind && used+acctW+1+statusMin <= inner
	return nameW, kindW, acctW, scopeW, showKind, showAcct
}

// renderPanel draws one bordered table. row is advanced past the rows it
// consumes so the caller can keep a single cursor index across panels.
//
// maxRows caps how many rows are drawn. When there are more, the window scrolls
// to keep the selected row visible and a final line says how many are hidden --
// drawing them all would overflow the terminal and tear the frame.
func (m Model) renderPanel(title string, targets []cloud.Target, row *int, w int, empty string, maxRows int) string {
	inner := w - 2
	nameW, kindW, acctW, scopeW, showKind, showAcct := columns(inner)

	// Header row, k9s style: uppercase and bold. It is indented by rowPrefix so
	// NAME sits exactly above the names below it.
	hdr := strings.Repeat(" ", rowPrefix) + pad("NAME", nameW)
	if showKind {
		hdr += " " + pad("KIND", kindW)
	}
	if showAcct {
		hdr += " " + pad("ACCOUNT", acctW)
	}
	hdr += " " + pad("SCOPE", scopeW) + " STATUS"

	rows := []string{colHeaderStyle.Render(truncate(hdr, inner))}
	if len(targets) == 0 {
		rows = append(rows, "  "+dimStyle.Render(empty))
	}

	base := *row
	start, end := visibleWindow(len(targets), m.cursor-base, maxRows)
	for i := start; i < end; i++ {
		rows = append(rows, m.rowBody(targets[i], base+i, w))
	}
	if hidden := len(targets) - (end - start); hidden > 0 {
		rows = append(rows, "  "+dimStyle.Render("… "+itoa(hidden)+" more, ↑↓ to scroll"))
	}
	*row = base + len(targets)

	return box(title, len(targets), w, rows)
}

// tableBudgets divides the rows left over after the fixed parts of the screen
// between the two tables, in proportion to how many entries each has.
func (m Model) tableBudgets(header, alerts, adc string) (awsRows, gcpRows int) {
	// Leading blank line, the two panel gaps, and a few lines of notes and
	// key hints below the tables.
	const chrome = 6
	// Each table spends three rows on its border and column header.
	const perTable = 3

	spare := m.layoutHeight() - countLines(header) - countLines(alerts) -
		countLines(adc) - chrome - 2*perTable

	// Always offer at least one row each, so a table never collapses to a
	// header with nothing under it.
	if spare < 2 {
		return 1, 1
	}
	if len(m.aws)+len(m.gcp) <= spare {
		return len(m.aws), len(m.gcp)
	}

	// Split proportionally, but never give a table more than it needs.
	total := len(m.aws) + len(m.gcp)
	awsRows = spare * len(m.aws) / total
	if awsRows < 1 {
		awsRows = 1
	}
	gcpRows = spare - awsRows
	if gcpRows < 1 {
		gcpRows, awsRows = 1, spare-1
	}
	if awsRows > len(m.aws) {
		gcpRows += awsRows - len(m.aws)
		awsRows = len(m.aws)
	}
	if gcpRows > len(m.gcp) {
		awsRows += gcpRows - len(m.gcp)
		gcpRows = len(m.gcp)
	}
	return awsRows, gcpRows
}

// visibleWindow returns the slice of rows to draw, scrolled so that the
// selected row stays on screen. A cursor outside this panel leaves the window
// at the top.
func visibleWindow(total, cursor, maxRows int) (start, end int) {
	if maxRows < 1 {
		maxRows = 1
	}
	if total <= maxRows {
		return 0, total
	}
	if cursor < 0 || cursor >= total {
		return 0, maxRows
	}
	start = cursor - maxRows/2
	if start < 0 {
		start = 0
	}
	if start+maxRows > total {
		start = total - maxRows
	}
	return start, start + maxRows
}

// renderRow lays out one target. The selected row is painted edge to edge, the
// way k9s highlights a selection: a marker alone is easy to lose in a table.
func (m Model) renderRow(t cloud.Target, row, w int) string {
	return panelRow(m.rowBody(t, row, w), w)
}

// rowBody renders a row's content without the surrounding border.
func (m Model) rowBody(t cloud.Target, row, w int) string {
	inner := w - 2
	nameW, kindW, acctW, scopeW, showKind, showAcct := columns(inner)
	selected := row == m.cursor

	// The marker is its own cell, not a prefix on the name, so the NAME header
	// lines up with the names whether or not a row is active.
	marker := " "
	if t.Active {
		// It answers "what will my next command hit?", which is the question
		// the whole dashboard exists to answer.
		marker = "▪"
	}

	status := t.Detail
	if status == "" {
		status = t.Identity
	}
	if status == "" {
		status = t.Health.String()
	}

	// Measure against the plain text, then style. Styling first would make the
	// widths depend on escape sequences.
	plain := " " + healthMark(t.Health) + " " + marker + " " + nameCell(t, nameW)
	if showKind {
		plain += " " + pad(truncate(string(t.Kind), kindW), kindW)
	}
	if showAcct {
		plain += " " + pad(truncate(t.Account, acctW), acctW)
	}
	plain += " " + pad(truncate(t.Scope, scopeW), scopeW)

	statusW := inner - lipgloss.Width(plain) - 1
	if statusW < 4 {
		statusW = 4
	}
	status = truncate(status, statusW)

	if selected {
		// A selected row is styled as a whole: a nested colour reset would
		// punch a hole in the background partway along the line.
		line := plain + " " + status
		if gap := inner - lipgloss.Width(line); gap > 0 {
			line += strings.Repeat(" ", gap)
		}
		return selectedRowStyle.Render(line)
	}

	nameSty := textStyle
	markerSty := dimStyle
	if t.Active {
		nameSty = lipgloss.NewStyle().Foreground(colGreen).Bold(true)
		markerSty = okStyle
	}
	kindSty := dimStyle
	if t.Kind.Rots() {
		// A hand-pasted session token is the only kind here that expires with
		// no way to refresh itself.
		kindSty = warnStyle
	}

	var b strings.Builder
	b.WriteString(" " + healthStyle(t.Health).Render(healthMark(t.Health)))
	b.WriteString(" " + markerSty.Render(marker))
	b.WriteString(" " + nameSty.Render(pad(truncate(t.Name, nameW), nameW)))
	if showKind {
		b.WriteString(" " + kindSty.Render(pad(truncate(string(t.Kind), kindW), kindW)))
	}
	if showAcct {
		b.WriteString(" " + dimStyle.Render(pad(truncate(t.Account, acctW), acctW)))
	}
	b.WriteString(" " + textStyle.Render(pad(truncate(t.Scope, scopeW), scopeW)))
	b.WriteString(" " + healthStyle(t.Health).Render(status))

	return b.String()
}

func (m Model) renderADCPanel(w int) string {
	var rows []string
	if !m.adc.Present {
		rows = append(rows, " "+dimStyle.Render("○ ")+dimStyle.Render(m.adc.Detail))
	} else {
		ident := m.adc.Identity
		if ident == "" {
			ident = "(identity unknown)"
		}
		quota := m.adc.QuotaProject
		if quota == "" {
			quota = "(no quota project)"
		}
		line := " " + healthStyle(m.adc.Health).Render(healthMark(m.adc.Health)) + " " +
			textStyle.Render(pad(truncate(ident, 34), 34)) + " " +
			textStyle.Render(pad(truncate(quota, 22), 22)) + " " +
			healthStyle(m.adc.Health).Render(m.adc.Detail)
		rows = append(rows, line)
	}
	return box("Application Default Credentials", -1, w, rows)
}

// renderConfirm asks before an irreversible change. It is drawn in the alert
// colour and defaults to nothing happening: the question has to be answered
// deliberately, not dismissed by habit.
func (m Model) renderConfirm(w int) string {
	c := m.confirm
	rows := []string{
		" " + alertStyle.Render(truncate(c.title, w-4)),
		" " + dimStyle.Render(truncate(c.detail, w-4)),
	}
	return box("Confirm", -1, w, rows) + "\n" +
		note(hintStyle, w, "y delete · n or esc cancel")
}

// renderAddMenu draws the add options as a navigable list rather than a set of
// mnemonic keys. The letters were arbitrary, and arrow keys plus enter are what
// the rest of the dashboard already uses.
func (m Model) renderAddMenu(w int) string {
	entries := addEntries()

	rows := make([]string, 0, len(entries))
	for i, e := range entries {
		num := itoa(i + 1)
		if i == m.addCursor {
			line := " ▸ " + num + "  " + pad(e.label, 34) + e.hint
			if gap := (w - 2) - lipgloss.Width(line); gap > 0 {
				line += strings.Repeat(" ", gap)
			}
			rows = append(rows, selectedRowStyle.Render(line))
			continue
		}
		rows = append(rows, "   "+dimStyle.Render(num)+"  "+
			textStyle.Render(pad(e.label, 34))+dimStyle.Render(e.hint))
	}
	return box("Add", len(entries), w, rows) + "\n" +
		note(hintStyle, w, "↑↓ choose · enter open · 1-"+itoa(len(entries))+" jump · esc cancel")
}
