package shellcfg

import (
	"strings"
	"testing"
)

func TestExportAndUnsetAreOrdered(t *testing.T) {
	// Unsets must come first: clearing a variable after setting it would undo
	// the switch.
	got := New().Export("AWS_PROFILE", "prod").Unset("AWS_ACCESS_KEY_ID").String()
	unsetAt := strings.Index(got, "unset AWS_ACCESS_KEY_ID")
	exportAt := strings.Index(got, "export AWS_PROFILE")
	if unsetAt < 0 || exportAt < 0 {
		t.Fatalf("missing lines in %q", got)
	}
	if unsetAt > exportAt {
		t.Errorf("unset must precede export, got:\n%s", got)
	}
}

func TestQuotingResistsInjection(t *testing.T) {
	// Names come from config files on disk. A name containing shell syntax must
	// not be able to run anything when the wrapper evaluates the script.
	evil := `x'; touch /tmp/pwned; echo '`
	got := New().Export("AWS_PROFILE", evil).String()

	if strings.Contains(got, "touch /tmp/pwned;") && !strings.Contains(got, `'\''`) {
		t.Errorf("value was not neutralised: %s", got)
	}
	if !strings.HasPrefix(got, "export AWS_PROFILE='") {
		t.Errorf("value not single-quoted: %s", got)
	}
	// Every embedded quote must be closed-escaped-reopened, leaving no
	// unbalanced quote for the shell to resume parsing from.
	if strings.Count(got, "'")%2 != 0 {
		t.Errorf("unbalanced quotes: %s", got)
	}
}

func TestExportOverridesPriorUnset(t *testing.T) {
	got := New().Unset("AWS_PROFILE").Export("AWS_PROFILE", "dev").String()
	if strings.Contains(got, "unset AWS_PROFILE") {
		t.Errorf("export should cancel the earlier unset, got:\n%s", got)
	}
}

func TestUnsetOverridesPriorExport(t *testing.T) {
	got := New().Export("AWS_PROFILE", "dev").Unset("AWS_PROFILE").String()
	if strings.Contains(got, "export AWS_PROFILE") {
		t.Errorf("unset should cancel the earlier export, got:\n%s", got)
	}
}

func TestEmpty(t *testing.T) {
	if !New().Empty() {
		t.Error("a new script should be empty")
	}
	if New().Export("A", "b").Empty() {
		t.Error("a script with an export is not empty")
	}
}
