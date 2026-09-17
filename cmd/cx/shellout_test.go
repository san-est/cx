package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/san-est/cx/internal/cloud"
	"github.com/san-est/cx/internal/shellcfg"
)

// silenceOutput redirects the process's stdout and stderr for the duration of a
// test, so exercising a command does not scribble over the test log.
func silenceOutput(t *testing.T) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() {
		os.Stdout, os.Stderr = stdout, stderr
		devnull.Close()
	})
}

func TestApplyScriptHandsEnvironmentChangesToTheWrapper(t *testing.T) {
	out := filepath.Join(t.TempDir(), "cx-out")
	t.Setenv("CX_SHELL_OUT", out)

	if code := applyScript(shellcfg.New().Export("AWS_PROFILE", "client-prod")); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the scratch file: %v", err)
	}
	if !strings.Contains(string(b), "AWS_PROFILE='client-prod'") {
		t.Errorf("scratch file = %q, want a quoted AWS_PROFILE assignment", b)
	}
}

func TestApplyScriptWritesTheScratchFilePrivately(t *testing.T) {
	// The file can carry a session token on its way to the shell, and it lives
	// in a world-readable temporary directory.
	out := filepath.Join(t.TempDir(), "cx-out")
	t.Setenv("CX_SHELL_OUT", out)

	if code := applyScript(shellcfg.New().Export("AWS_SESSION_TOKEN", "secret")); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("scratch file mode = %04o, want 0600", perm)
	}
}

func TestApplyScriptRefusesWhenTheWrapperIsNotInstalled(t *testing.T) {
	// Without the wrapper there is nowhere to write the changes, and a shell
	// that silently keeps its old credentials is the failure this tool exists
	// to prevent. It must fail loudly instead.
	silenceOutput(t)
	t.Setenv("CX_SHELL_OUT", "")

	if code := applyScript(shellcfg.New().Export("AWS_PROFILE", "client-prod")); code != 1 {
		t.Errorf("exit code = %d, want 1 when CX_SHELL_OUT is unset", code)
	}
}

func TestApplyScriptSucceedsWithNothingToApply(t *testing.T) {
	// An empty script must not require the wrapper: there is nothing to hand
	// over, so there is nothing to fail.
	t.Setenv("CX_SHELL_OUT", "")

	if code := applyScript(shellcfg.New()); code != 0 {
		t.Errorf("exit code = %d, want 0 for an empty script", code)
	}
}

func TestAutoPinQuotesAConfigurationNameReadFromDisk(t *testing.T) {
	// Configuration names are directory entries, so they are untrusted input to
	// shell code the user's rc file sources. The quoting is exercised
	// elsewhere, but only this path joins it to a name actually read off disk,
	// and a name reaches the template through Quote alone.
	hostile := `it's; echo PWNED`

	f := newCloudFixture(t)
	f.configuration(hostile, "dev@example.com", "acme-staging")
	f.machineWide(hostile)

	block := autoPinBlock()
	if block == "" {
		t.Fatal("no auto-pin block emitted; the fixture is not being read")
	}

	// Running it is the assertion: if the name escaped its quotes, the shell
	// would execute the trailing command and the value would not survive.
	got := runShellSnippet(t, block, "", "CLOUDSDK_ACTIVE_CONFIG_NAME")
	if got != hostile {
		t.Errorf("pinned value = %q, want %q intact and unexecuted", got, hostile)
	}
}

func TestAutoPinEmitsNothingWithoutAMachineWideSelection(t *testing.T) {
	// There is nothing to pin to, and emitting an empty assignment would pin
	// every new shell to the empty string.
	newCloudFixture(t)

	if got := autoPinBlock(); got != "" {
		t.Errorf("auto-pin block = %q, want nothing when no configuration is selected", got)
	}
}

func TestAutoPinEmitsNothingWhenTheSelectionIsDangling(t *testing.T) {
	// active_config naming a configuration that no longer exists is a hazard
	// cx reports; pinning every new shell to it would make it permanent.
	f := newCloudFixture(t)
	f.machineWide("deleted-last-week")

	if got := autoPinBlock(); got != "" {
		t.Errorf("auto-pin block = %q, want nothing for a dangling selection", got)
	}
}

func TestHasTargetMatchesByName(t *testing.T) {
	targets := []cloud.Target{{Name: "client-prod"}, {Name: "staging"}}

	if !hasTarget(targets, "staging") {
		t.Error("hasTarget did not find a name that is present")
	}
	if hasTarget(targets, "stag") {
		t.Error("hasTarget matched a prefix; it must match the whole name")
	}
	if hasTarget(nil, "staging") {
		t.Error("hasTarget matched against no targets at all")
	}
}

func TestStatusPrefersTheDetailOverTheBareHealth(t *testing.T) {
	// A status clipped to a bare health word tells the reader nothing about why.
	if got := status(cloud.Unknown, "expired"); got != "expired" {
		t.Errorf("status = %q, want the detail when there is one", got)
	}
	if got := status(cloud.Valid, ""); got != cloud.Valid.String() {
		t.Errorf("status = %q, want the health when there is no detail", got)
	}
}

func TestDashRendersAnEmptyFieldVisibly(t *testing.T) {
	if got := dash(""); got != "-" {
		t.Errorf("dash(\"\") = %q, want a dash so the column is not blank", got)
	}
	if got := dash("acme-dev"); got != "acme-dev" {
		t.Errorf("dash = %q, want the value unchanged", got)
	}
}
