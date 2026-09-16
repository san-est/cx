package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vboyadzhiev/cx/internal/cloud"
)

// The palette follows tokyonight, which is what this terminal is already themed
// with. Light variants keep the dashboard legible on a pale background.
var (
	colDim    = lipgloss.AdaptiveColor{Light: "#8189a8", Dark: "#565f89"}
	colText   = lipgloss.AdaptiveColor{Light: "#343b58", Dark: "#c0caf5"}
	colBlue   = lipgloss.AdaptiveColor{Light: "#2959aa", Dark: "#7aa2f7"}
	colCyan   = lipgloss.AdaptiveColor{Light: "#0f4b6e", Dark: "#7dcfff"}
	colGreen  = lipgloss.AdaptiveColor{Light: "#385f0d", Dark: "#9ece6a"}
	colYellow = lipgloss.AdaptiveColor{Light: "#8f5e15", Dark: "#e0af68"}
	colRed    = lipgloss.AdaptiveColor{Light: "#8c4351", Dark: "#f7768e"}
	colPurple = lipgloss.AdaptiveColor{Light: "#5a3e8e", Dark: "#bb9af7"}

	// The selection background. k9s highlights the whole row rather than
	// marking it, which is far easier to track while scanning a table.
	colSelBg = lipgloss.AdaptiveColor{Light: "#c5d1f2", Dark: "#2e3c64"}
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colBlue)
	hintStyle  = lipgloss.NewStyle().Foreground(colDim)
	dimStyle   = lipgloss.NewStyle().Foreground(colDim)
	textStyle  = lipgloss.NewStyle().Foreground(colText)
	okStyle    = lipgloss.NewStyle().Foreground(colGreen)
	warnStyle  = lipgloss.NewStyle().Foreground(colYellow)
	badStyle   = lipgloss.NewStyle().Foreground(colRed)

	// Column headers, as k9s renders them: bold, and a colour used nowhere else
	// so the eye separates structure from data instantly.
	colHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colPurple)

	// selectedRowStyle paints an entire row. Any inner colouring is dropped for
	// the selected row, because a nested reset code would punch a hole in the
	// background partway along the line.
	selectedRowStyle = lipgloss.NewStyle().Background(colSelBg).Foreground(colText).Bold(true)

	// keyStyle renders the <x> shortcut chips in the header.
	keyStyle   = lipgloss.NewStyle().Foreground(colBlue)
	keyDescSty = lipgloss.NewStyle().Foreground(colDim)

	// infoKeyStyle labels the context block at the top left.
	infoKeyStyle = lipgloss.NewStyle().Foreground(colCyan)

	alertStyle    = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(colBlue)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBlue).
			Padding(1, 2)
)

// healthMark renders the status glyph for a target.
func healthMark(h cloud.Health) string {
	switch h {
	case cloud.Valid:
		return "●"
	case cloud.Expired:
		return "✗"
	case cloud.Missing:
		return "○"
	case cloud.Probing:
		return "◌"
	default:
		return "·"
	}
}

// healthStyle is the colour a status glyph or word takes.
func healthStyle(h cloud.Health) lipgloss.Style {
	switch h {
	case cloud.Valid:
		return okStyle
	case cloud.Expired:
		return badStyle
	case cloud.Probing:
		return warnStyle
	default:
		return dimStyle
	}
}

// panelTitle draws a k9s-style top border with the title set into it:
//
//	╭─ AWS Profiles(2) ──────────────────────────╮
func panelTitle(title string, count, width int) string {
	border := lipgloss.NewStyle().Foreground(colBlue)

	// An untitled panel gets a plain top border, rather than a gap where the
	// label would have been.
	if title == "" && count < 0 {
		inner := width - 2
		if inner < 0 {
			inner = 0
		}
		return border.Render("╭" + strings.Repeat("─", inner) + "╮")
	}

	label := title
	if count >= 0 {
		label = strings.Join([]string{title, "(", itoa(count), ")"}, "")
	}
	rendered := " " + colHeaderStyle.Render(label) + " "

	// Two corners plus the leading dash.
	used := lipgloss.Width(rendered) + 3
	fill := width - used
	if fill < 0 {
		fill = 0
	}
	return border.Render("╭─") + rendered + border.Render(strings.Repeat("─", fill)+"╮")
}

// box assembles a complete bordered panel from pre-rendered body lines. Rows
// are padded and clipped to the panel width, so a caller cannot break the right
// border by handing over a line of the wrong length.
func box(title string, count, width int, rows []string) string {
	var b strings.Builder
	b.WriteString(panelTitle(title, count, width) + "\n")
	for _, r := range rows {
		b.WriteString(panelRow(r, width) + "\n")
	}
	b.WriteString(panelBottom(width))
	return b.String()
}

// panelBottom closes a panel opened by panelTitle.
func panelBottom(width int) string {
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	return lipgloss.NewStyle().Foreground(colBlue).Render("╰" + strings.Repeat("─", inner) + "╯")
}

// panelRow wraps one line of body text in the panel's side borders, padding it
// so the right edge lines up regardless of the content's width.
func panelRow(content string, width int) string {
	border := lipgloss.NewStyle().Foreground(colBlue)
	inner := width - 2
	body := content
	if w := lipgloss.Width(body); w < inner {
		body += strings.Repeat(" ", inner-w)
	} else if w > inner {
		body = truncate(body, inner)
	}
	return border.Render("│") + body + border.Render("│")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// pad right-pads s to width w, measuring display cells rather than bytes so
// that styled and multi-byte content still lines up.
func pad(s string, w int) string {
	d := w - lipgloss.Width(s)
	if d <= 0 {
		return s
	}
	return s + strings.Repeat(" ", d)
}

// truncate shortens s to at most w display cells, with an ellipsis.
func truncate(s string, w int) string {
	if w <= 1 || lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if len(r) > w-1 {
		r = r[:w-1]
	}
	return string(r) + "…"
}

// logoStyle colours the wordmark. It is the accent used for panel borders, so
// the logo reads as part of the frame rather than as data.
var logoStyle = lipgloss.NewStyle().Foreground(colBlue).Bold(true)
