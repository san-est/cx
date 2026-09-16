package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// field is one labelled input in a form.
type field struct {
	key   string
	label string
	hint  string
	// required fields block submission when empty.
	required bool
	// secret fields are masked as they are typed.
	secret bool
	input  textinput.Model
}

// form is a modal set of inputs. It exists so that creating a profile happens
// inside cx, rather than by handing the terminal to another program's
// question-and-answer prompt.
type form struct {
	title  string
	intro  string
	fields []field
	focus  int
	err    string
	// submit persists the collected values. It returns a message to show on
	// success, or an error to display without closing the form.
	submit func(vals map[string]string) (string, error)
}

// newForm builds a form and focuses its first input.
func newForm(title, intro string, fields []field, submit func(map[string]string) (string, error)) *form {
	f := &form{title: title, intro: intro, fields: fields, submit: submit}
	for i := range f.fields {
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = 256
		in.Width = 46
		in.Placeholder = f.fields[i].hint
		if f.fields[i].secret {
			in.EchoMode = textinput.EchoPassword
			in.EchoCharacter = '•'
		}
		f.fields[i].input = in
	}
	if len(f.fields) > 0 {
		f.fields[0].input.Focus()
	}
	return f
}

// values collects the current input contents.
func (f *form) values() map[string]string {
	out := make(map[string]string, len(f.fields))
	for _, fl := range f.fields {
		out[fl.key] = strings.TrimSpace(fl.input.Value())
	}
	return out
}

// focusAt moves the cursor to field i, wrapping at both ends.
func (f *form) focusAt(i int) {
	if len(f.fields) == 0 {
		return
	}
	if i < 0 {
		i = len(f.fields) - 1
	}
	if i >= len(f.fields) {
		i = 0
	}
	f.fields[f.focus].input.Blur()
	f.focus = i
	f.fields[f.focus].input.Focus()
}

// formResult reports what a key press did to the form.
type formResult int

const (
	// formOpen means the form is still collecting input.
	formOpen formResult = iota
	// formCancelled means the user backed out.
	formCancelled
	// formSubmitted means the values were persisted successfully.
	formSubmitted
)

// update handles one key press. It returns the outcome plus any command the
// focused input needs to run, such as the cursor blink.
func (f *form) update(msg tea.Msg) (formResult, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		f.fields[f.focus].input, cmd = f.fields[f.focus].input.Update(msg)
		return formOpen, cmd
	}

	switch key.String() {
	case "esc":
		return formCancelled, nil

	case "tab", "down":
		f.focusAt(f.focus + 1)
		return formOpen, nil

	case "shift+tab", "up":
		f.focusAt(f.focus - 1)
		return formOpen, nil

	case "enter":
		// Enter advances until the last field, so a form can be filled in
		// without reaching for tab, and only then submits.
		if f.focus < len(f.fields)-1 {
			f.focusAt(f.focus + 1)
			return formOpen, nil
		}
		if err := f.validate(); err != nil {
			f.err = err.Error()
			return formOpen, nil
		}
		msg, err := f.submit(f.values())
		if err != nil {
			f.err = err.Error()
			return formOpen, nil
		}
		f.err = msg
		return formSubmitted, nil
	}

	f.err = ""
	var cmd tea.Cmd
	f.fields[f.focus].input, cmd = f.fields[f.focus].input.Update(msg)
	return formOpen, cmd
}

// validate checks required fields before the submit function runs, so the
// common mistake is reported without touching any files.
func (f *form) validate() error {
	for _, fl := range f.fields {
		if fl.required && strings.TrimSpace(fl.input.Value()) == "" {
			return fmt.Errorf("%s is required", fl.label)
		}
	}
	return nil
}

// view renders the form as a bordered panel.
func (f *form) view(width int) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(f.title) + "\n")
	if f.intro != "" {
		b.WriteString(dimStyle.Render(f.intro) + "\n")
	}
	b.WriteString("\n")

	labelW := 0
	for _, fl := range f.fields {
		if n := len(fl.label); n > labelW {
			labelW = n
		}
	}

	for i, fl := range f.fields {
		marker := "  "
		label := dimStyle.Render(pad(fl.label, labelW))
		if i == f.focus {
			marker = selectedStyle.Render("▸ ")
			label = textStyle.Render(pad(fl.label, labelW))
		}
		req := " "
		if fl.required {
			req = warnStyle.Render("*")
		}
		b.WriteString(fmt.Sprintf("%s%s %s  %s\n", marker, label, req, fl.input.View()))
	}

	b.WriteString("\n")
	if f.err != "" {
		b.WriteString(badStyle.Render(f.err) + "\n")
	}
	b.WriteString(hintStyle.Render("tab/↑↓ move · enter next, or save on the last field · esc cancel"))

	return panelStyle.Width(min(width-4, 78)).Render(b.String())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
