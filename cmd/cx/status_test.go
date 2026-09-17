package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// cloudFixture builds a self-contained AWS and gcloud configuration tree and
// points cx at it through the environment.
//
// Every variable cx consults is given an explicit value, not just the ones a
// given test cares about. cx exports several of them into the shell it is
// installed in, so a test that inherits the ambient environment passes in CI
// and fails on the machine of anyone who actually uses the tool.
type cloudFixture struct {
	t      *testing.T
	gcloud string
}

func newCloudFixture(t *testing.T) *cloudFixture {
	t.Helper()
	dir := t.TempDir()

	// Empty AWS files, so no profile from the developer's own ~/.aws can reach
	// the audit. They must exist and be named explicitly: an empty
	// AWS_CONFIG_FILE falls back to the home directory.
	awsConfig := filepath.Join(dir, "aws-config")
	awsCredentials := filepath.Join(dir, "aws-credentials")
	for _, p := range []string{awsConfig, awsCredentials} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}
	t.Setenv("AWS_CONFIG_FILE", awsConfig)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", awsCredentials)

	gcloud := filepath.Join(dir, "gcloud")
	if err := os.MkdirAll(filepath.Join(gcloud, "configurations"), 0o755); err != nil {
		t.Fatalf("creating gcloud tree: %v", err)
	}
	t.Setenv("CLOUDSDK_CONFIG", gcloud)

	for _, key := range []string{
		"CLOUDSDK_ACTIVE_CONFIG_NAME", "CLOUDSDK_CORE_PROJECT", "CLOUDSDK_CORE_ACCOUNT",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN", "AWS_SECURITY_TOKEN", "AWS_CREDENTIAL_EXPIRATION",
	} {
		t.Setenv(key, "")
	}

	return &cloudFixture{t: t, gcloud: gcloud}
}

// configuration writes a gcloud configuration file.
func (f *cloudFixture) configuration(name, account, project string) {
	f.t.Helper()
	body := "[core]\naccount = " + account + "\nproject = " + project + "\n"
	p := filepath.Join(f.gcloud, "configurations", "config_"+name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		f.t.Fatalf("writing configuration %q: %v", name, err)
	}
}

// machineWide writes active_config, the file every terminal on the machine
// shares.
func (f *cloudFixture) machineWide(name string) {
	f.t.Helper()
	p := filepath.Join(f.gcloud, "active_config")
	if err := os.WriteFile(p, []byte(name+"\n"), 0o600); err != nil {
		f.t.Fatalf("writing active_config: %v", err)
	}
}

// pinShell sets the per-shell selection, which is the safe one.
func (f *cloudFixture) pinShell(name string) {
	f.t.Helper()
	f.t.Setenv("CLOUDSDK_ACTIVE_CONFIG_NAME", name)
}

// serviceAccountADC writes Application Default Credentials that carry their own
// identity, so the audit can compare principals without a network probe.
func (f *cloudFixture) serviceAccountADC(email, quotaProject string) {
	f.t.Helper()
	b, err := json.Marshal(map[string]string{
		"type":             "service_account",
		"client_email":     email,
		"quota_project_id": quotaProject,
	})
	if err != nil {
		f.t.Fatalf("encoding ADC: %v", err)
	}
	p := filepath.Join(f.gcloud, "application_default_credentials.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		f.t.Fatalf("writing ADC: %v", err)
	}
}

// statusExitCode runs the report with its output discarded. Probing is off, so
// the test never touches the network or the vendor CLIs.
func statusExitCode(t *testing.T) int {
	t.Helper()
	silenceOutput(t)
	return runStatus(false)
}

// The exit code is a documented interface: the README offers
//
//	cx status --no-probe >/dev/null || { echo "refusing to apply"; exit 1; }
//
// so anything that changes it silently breaks a script that gates a deploy.

func TestStatusExitsZeroWhenTheShellIsPinnedAndConsistent(t *testing.T) {
	f := newCloudFixture(t)
	f.configuration("staging", "dev@example.com", "acme-staging")
	f.machineWide("staging")
	f.pinShell("staging")
	f.serviceAccountADC("dev@example.com", "acme-staging")

	if got := statusExitCode(t); got != 0 {
		t.Errorf("exit code = %d, want 0 when nothing can misdirect a command", got)
	}
}

func TestStatusExitsTwoWhenTheTargetComesFromTheMachineWideFile(t *testing.T) {
	// The hazard the tool was built for: with no shell pin, the selection is
	// read from a file any other terminal can rewrite.
	f := newCloudFixture(t)
	f.configuration("staging", "dev@example.com", "acme-staging")
	f.machineWide("staging")
	f.serviceAccountADC("dev@example.com", "acme-staging")

	if got := statusExitCode(t); got != 2 {
		t.Errorf("exit code = %d, want 2 when another terminal can retarget this shell", got)
	}
}

func TestStatusExitsTwoWhenActiveConfigNamesAMissingConfiguration(t *testing.T) {
	// gcloud falls back to built-in defaults without saying so, which can mean
	// targeting no project at all. The shell is pinned to a real configuration
	// here, so this isolates the missing-configuration hazard.
	f := newCloudFixture(t)
	f.configuration("staging", "dev@example.com", "acme-staging")
	f.machineWide("deleted-last-week")
	f.pinShell("staging")
	f.serviceAccountADC("dev@example.com", "acme-staging")

	if got := statusExitCode(t); got != 2 {
		t.Errorf("exit code = %d, want 2 when active_config names a configuration that is gone", got)
	}
}

func TestStatusExitsTwoWhenADCAuthenticatesAsSomeoneElse(t *testing.T) {
	// ADC is a separate credential store, and it is what terraform and the
	// client libraries actually use.
	f := newCloudFixture(t)
	f.configuration("staging", "dev@example.com", "acme-staging")
	f.machineWide("staging")
	f.pinShell("staging")
	f.serviceAccountADC("robot@acme-prod.iam.gserviceaccount.com", "acme-staging")

	if got := statusExitCode(t); got != 2 {
		t.Errorf("exit code = %d, want 2 when ADC and gcloud disagree about who you are", got)
	}
}

func TestStatusExitsTwoWhenCoreProjectOverridesTheConfiguration(t *testing.T) {
	// terraform's google provider reads CLOUDSDK_CORE_PROJECT and ignores
	// gcloud's configuration files, so a forgotten export really can retarget
	// an apply.
	f := newCloudFixture(t)
	f.configuration("staging", "dev@example.com", "acme-staging")
	f.machineWide("staging")
	f.pinShell("staging")
	f.serviceAccountADC("dev@example.com", "acme-staging")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "acme-prod")

	if got := statusExitCode(t); got != 2 {
		t.Errorf("exit code = %d, want 2 when an env override silently replaces the project", got)
	}
}

func TestStatusDoesNotExitTwoForMerelySurprisingState(t *testing.T) {
	// A gate that fires on everything gets disabled. Missing ADC is a warning,
	// not a misdirection: nothing is pointed anywhere wrong, it simply will not
	// authenticate.
	f := newCloudFixture(t)
	f.configuration("staging", "dev@example.com", "acme-staging")
	f.machineWide("staging")
	f.pinShell("staging")

	if got := statusExitCode(t); got != 0 {
		t.Errorf("exit code = %d, want 0 — a warning must not fail a deploy gate", got)
	}
}

func TestStatusExitsZeroWithNoCloudConfigurationAtAll(t *testing.T) {
	// Nothing configured cannot misdirect anything, and a machine with no
	// gcloud install must not fail every gated script on it.
	newCloudFixture(t)

	if got := statusExitCode(t); got != 0 {
		t.Errorf("exit code = %d, want 0 when there is nothing configured", got)
	}
}
