package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vboyadzhiev/cx/internal/cloud"
)

// typeKeys feeds a string to a model one rune at a time, the way a person does.
func typeKeys(mm tea.Model, s string) tea.Model {
	for _, r := range s {
		mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return mm
}

func press(mm tea.Model, k tea.KeyType) tea.Model {
	mm, _ = mm.Update(tea.KeyMsg{Type: k})
	return mm
}

// TestAddProfileEndToEnd drives the whole path a user takes: open the menu,
// pick a form, fill it in, submit, and end up with a profile on disk that the
// loader recognises.
func TestAddProfileEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "gcloud"))

	var mm tea.Model = Model{loaded: true, canSwitch: true, width: 120}

	// a opens the menu; the access-keys form is the second entry.
	mm = typeKeys(mm, "a")
	if mm.(Model).mode != modeAdd {
		t.Fatal("a did not open the add menu")
	}
	mm = press(mm, tea.KeyDown)
	if got := mm.(Model).addCursor; got != 1 {
		t.Fatalf("add cursor = %d after one down, want 1", got)
	}
	mm = press(mm, tea.KeyEnter)
	if mm.(Model).mode != modeForm || mm.(Model).active == nil {
		t.Fatal("enter did not open the access-keys form")
	}

	// Typing must reach the form, not be read as dashboard shortcuts. "quit"
	// contains q, and "add" contains a.
	mm = typeKeys(mm, "quit-add-test")
	mm = press(mm, tea.KeyEnter)
	mm = typeKeys(mm, "AKIAEXAMPLE")
	mm = press(mm, tea.KeyEnter)
	mm = typeKeys(mm, "topsecret")
	mm = press(mm, tea.KeyEnter) // past the optional session token
	mm = press(mm, tea.KeyEnter) // onto region
	mm = typeKeys(mm, "eu-west-1")
	mm = press(mm, tea.KeyEnter) // last field: submit

	m := mm.(Model)
	if m.mode != modeNormal {
		t.Fatalf("form did not close after submit; err = %q", m.active.err)
	}
	if !strings.Contains(m.notice, "quit-add-test") {
		t.Errorf("notice = %q, want it to name the new profile", m.notice)
	}

	targets, err := cloud.LoadAWS()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d profiles, want 1", len(targets))
	}
	if targets[0].Name != "quit-add-test" {
		t.Errorf("profile name = %q — keystrokes were swallowed as shortcuts", targets[0].Name)
	}
	if targets[0].Kind != cloud.KindStatic {
		t.Errorf("kind = %v, want static", targets[0].Kind)
	}
	if targets[0].Scope != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", targets[0].Scope)
	}
}

func TestFormRejectsMissingRequiredField(t *testing.T) {
	f := awsKeysForm()
	// Jump straight to the last field and submit with everything empty.
	f.focusAt(len(f.fields) - 1)
	res, _ := f.update(tea.KeyMsg{Type: tea.KeyEnter})

	if res != formOpen {
		t.Error("form submitted with required fields empty")
	}
	if !strings.Contains(f.err, "required") {
		t.Errorf("err = %q, want it to name the missing field", f.err)
	}
}

func TestFormReportsWriteFailureWithoutClosing(t *testing.T) {
	// An invalid name is rejected by the writer, and the form must stay open so
	// the value can be corrected rather than retyped from scratch.
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))

	f := awsKeysForm()
	f.fields[0].input.SetValue("1-starts-with-a-digit")
	f.fields[1].input.SetValue("AKIA")
	f.fields[2].input.SetValue("secret")
	f.focusAt(len(f.fields) - 1)

	res, _ := f.update(tea.KeyMsg{Type: tea.KeyEnter})
	if res != formOpen {
		t.Error("form closed despite the write failing")
	}
	if f.err == "" {
		t.Error("write failure was not reported")
	}
}

func TestSecretFieldsAreMasked(t *testing.T) {
	f := awsKeysForm()
	for _, fl := range f.fields {
		if fl.key == "secret_key" || fl.key == "session_token" {
			if !fl.secret {
				t.Errorf("%s is not masked", fl.key)
			}
		}
	}
	// And the rendered view must not contain the typed secret.
	f.fields[2].input.SetValue("hunter2")
	if strings.Contains(f.view(100), "hunter2") {
		t.Error("secret value appeared in the rendered form")
	}
}

func TestEscapeCancelsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")

	var mm tea.Model = Model{loaded: true, width: 120}
	mm = typeKeys(mm, "a")
	mm = typeKeys(mm, "2") // access keys
	mm = typeKeys(mm, "throwaway")
	mm = press(mm, tea.KeyEsc)

	if mm.(Model).mode != modeNormal {
		t.Error("esc did not close the form")
	}
	targets, _ := cloud.LoadAWS()
	if len(targets) != 0 {
		t.Errorf("cancelling still wrote %d profile(s)", len(targets))
	}
}

// TestEditPreservesTheStoredSecret covers the trap in editing credentials: an
// untouched secret field must keep what is already on disk, not blank it.
func TestEditPreservesTheStoredSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")

	if err := cloud.WriteAWSStatic(cloud.AWSStaticSpec{
		Name: "client", Region: "eu-west-1",
		AccessKeyID: "AKIAORIGINAL", SecretAccessKey: "original-secret",
	}); err != nil {
		t.Fatal(err)
	}

	cur, err := cloud.AWSProfileFields("client")
	if err != nil {
		t.Fatal(err)
	}
	f := editAWSKeysForm("client", cur)

	// The form must arrive pre-filled, or saving would wipe the region.
	if got := f.fields[0].input.Value(); got != "AKIAORIGINAL" {
		t.Errorf("access key not pre-filled: %q", got)
	}
	if f.fields[1].input.Value() != "" {
		t.Error("the secret should start blank rather than being shown back")
	}

	// Change only the region and save.
	f.fields[3].input.SetValue("us-east-1")
	f.focusAt(len(f.fields) - 1)
	if res, _ := f.update(tea.KeyMsg{Type: tea.KeyEnter}); res != formSubmitted {
		t.Fatalf("save failed: %s", f.err)
	}

	after, err := cloud.AWSProfileFields("client")
	if err != nil {
		t.Fatal(err)
	}
	if after["aws_secret_access_key"] != "original-secret" {
		t.Errorf("secret = %q — an untouched field wiped the stored value", after["aws_secret_access_key"])
	}
	if after["region"] != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", after["region"])
	}
}

// TestDeleteAsksFirstAndThenRemoves covers the confirmation gate.
func TestDeleteAsksFirstAndThenRemoves(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "gcloud"))

	if err := cloud.WriteAWSStatic(cloud.AWSStaticSpec{
		Name: "doomed", AccessKeyID: "AKIA", SecretAccessKey: "s",
	}); err != nil {
		t.Fatal(err)
	}
	targets, _ := cloud.LoadAWS()

	var mm tea.Model = Model{loaded: true, width: 120, aws: targets}

	// d asks rather than acting.
	mm = typeKeys(mm, "d")
	if mm.(Model).mode != modeConfirm {
		t.Fatal("d did not ask for confirmation")
	}
	if got, _ := cloud.LoadAWS(); len(got) != 1 {
		t.Error("the profile was removed before the question was answered")
	}

	// n backs out.
	mm = typeKeys(mm, "n")
	if mm.(Model).mode != modeNormal {
		t.Error("n did not dismiss the confirmation")
	}
	if got, _ := cloud.LoadAWS(); len(got) != 1 {
		t.Error("declining still deleted the profile")
	}

	// y goes through with it.
	mm = typeKeys(mm, "d")
	mm = typeKeys(mm, "y")
	if got, _ := cloud.LoadAWS(); len(got) != 0 {
		t.Errorf("confirming did not delete: %+v", got)
	}
	if !strings.Contains(mm.(Model).notice, "deleted") {
		t.Errorf("notice = %q, want confirmation of the delete", mm.(Model).notice)
	}
}

// TestDeleteWarnsWhenRemovingTheActiveTarget covers the case worth pausing on.
func TestDeleteWarnsWhenRemovingTheActiveTarget(t *testing.T) {
	m := Model{loaded: true, width: 120,
		aws: []cloud.Target{{Name: "in-use", Active: true}}}

	c, ok := m.deleteConfirmation()
	if !ok {
		t.Fatal("no confirmation built")
	}
	if !strings.Contains(c.detail, "currently using") {
		t.Errorf("detail = %q, want a note that the shell is using it", c.detail)
	}
}

// TestEditKeysAreIgnoredWithNoRows guards against acting on an empty table.
func TestEditKeysAreIgnoredWithNoRows(t *testing.T) {
	var mm tea.Model = Model{loaded: true, width: 120}
	for _, k := range []string{"e", "d", "l"} {
		mm = typeKeys(mm, k)
		if got := mm.(Model).mode; got != modeNormal {
			t.Errorf("%q changed mode to %v with no rows to act on", k, got)
		}
	}
}
