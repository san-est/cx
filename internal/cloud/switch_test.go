package cloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwitchAWSClearsAmbientCredentials(t *testing.T) {
	// The AWS CLI reads AWS_ACCESS_KEY_ID before AWS_PROFILE. If a switch left
	// stale keys in place, the profile would appear to change while commands
	// kept hitting the old account -- the exact silent failure this tool exists
	// to prevent.
	got := SwitchAWS("client-prod").String()

	if !strings.Contains(got, "export AWS_PROFILE='client-prod'") {
		t.Errorf("profile not exported:\n%s", got)
	}
	for _, v := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if !strings.Contains(got, "unset "+v) {
			t.Errorf("%s not cleared:\n%s", v, got)
		}
	}
}

func TestSwitchGCPNeverTouchesGlobalState(t *testing.T) {
	// Setting the environment variable affects one shell. Running
	// `gcloud config configurations activate` would rewrite a file shared by
	// every shell on the machine.
	got := SwitchGCP("prod").String()

	if !strings.Contains(got, "export CLOUDSDK_ACTIVE_CONFIG_NAME='prod'") {
		t.Errorf("configuration not exported:\n%s", got)
	}
	// Only the executable lines matter here; the confirmation message names
	// gcloud legitimately.
	for _, line := range strings.Split(got, "\n") {
		if line == "" || strings.HasPrefix(line, "printf ") {
			continue
		}
		if !strings.HasPrefix(line, "export ") && !strings.HasPrefix(line, "unset ") {
			t.Errorf("switch emitted something other than an env change: %q", line)
		}
	}
	// Stale property overrides would beat the configuration we just selected.
	for _, v := range gcpAmbientOverrides {
		if !strings.Contains(got, "unset "+v) {
			t.Errorf("%s not cleared:\n%s", v, got)
		}
	}
}

func TestClearScopes(t *testing.T) {
	aws := Clear("aws").String()
	if !strings.Contains(aws, "unset AWS_PROFILE") {
		t.Errorf("aws scope did not clear AWS_PROFILE:\n%s", aws)
	}
	if strings.Contains(aws, "CLOUDSDK") {
		t.Errorf("aws scope should not touch gcloud:\n%s", aws)
	}

	gcp := Clear("gcp").String()
	if strings.Contains(gcp, "AWS_PROFILE") {
		t.Errorf("gcp scope should not touch AWS:\n%s", gcp)
	}

	all := Clear("all").String()
	if !strings.Contains(all, "unset AWS_PROFILE") {
		t.Errorf("all scope should clear AWS:\n%s", all)
	}
}

func TestSwitchQuotesHostileNames(t *testing.T) {
	// Names come from config files, which are not necessarily trustworthy.
	got := SwitchAWS(`a'; rm -rf /; echo '`).String()
	if strings.Count(got, "'")%2 != 0 {
		t.Errorf("unbalanced quoting would let the shell resume parsing:\n%s", got)
	}
}

func TestClearGCPRepinsRatherThanUnpinning(t *testing.T) {
	// Unsetting the variable would leave the shell following the shared
	// active_config file -- so "clear" would hand the user the very hazard the
	// tool warns about. It resets to that file's current value instead, pinned.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "configurations"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"default", "prod"} {
		if err := os.WriteFile(filepath.Join(dir, "configurations", "config_"+n),
			[]byte("[core]\naccount = me@example.com\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "active_config"), []byte("default"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDSDK_CONFIG", dir)
	t.Setenv("CLOUDSDK_ACTIVE_CONFIG_NAME", "prod")

	got := Clear("gcp").String()
	if !strings.Contains(got, "export CLOUDSDK_ACTIVE_CONFIG_NAME='default'") {
		t.Errorf("clear did not re-pin to the machine default:\n%s", got)
	}
	if strings.Contains(got, "unset CLOUDSDK_ACTIVE_CONFIG_NAME") {
		t.Errorf("clear left the shell unpinned and therefore shared:\n%s", got)
	}
	// Stale property overrides must still go.
	for _, v := range gcpAmbientOverrides {
		if !strings.Contains(got, "unset "+v) {
			t.Errorf("%s survived the clear:\n%s", v, got)
		}
	}
}

func TestClearGCPWithNoConfigsFallsBackToUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOUDSDK_CONFIG", dir)
	t.Setenv("CLOUDSDK_ACTIVE_CONFIG_NAME", "")

	got := Clear("gcp").String()
	if !strings.Contains(got, "unset CLOUDSDK_ACTIVE_CONFIG_NAME") {
		t.Errorf("with nothing to pin to, clear should unset:\n%s", got)
	}
}
