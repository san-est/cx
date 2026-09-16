package cloud

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyAWS(t *testing.T) {
	// Precedence matters: a profile can carry both a role_arn and stale static
	// keys, and the refreshing mechanism is the one actually in use.
	cases := []struct {
		name string
		kv   map[string]string
		want CredKind
	}{
		{"empty", map[string]string{}, KindNone},
		{"static", map[string]string{"aws_access_key_id": "AKIA"}, KindStatic},
		{"pasted session token", map[string]string{
			"aws_access_key_id": "ASIA", "aws_session_token": "tok"}, KindStaticTemp},
		{"modern sso", map[string]string{"sso_session": "corp"}, KindSSOSession},
		{"legacy sso", map[string]string{"sso_start_url": "https://x"}, KindSSO},
		{"assume role", map[string]string{"role_arn": "arn:aws:iam::1:role/r"}, KindAssumeRole},
		{"process beats static", map[string]string{
			"credential_process": "/bin/helper", "aws_access_key_id": "AKIA"}, KindProcess},
		{"sso beats static", map[string]string{
			"sso_session": "corp", "aws_access_key_id": "AKIA"}, KindSSOSession},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyAWS(c.kv); got != c.want {
				t.Errorf("classifyAWS = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRotsOnlyFlagsPastedTokens(t *testing.T) {
	// SSO and credential_process refresh themselves; a pasted session token is
	// the one that silently dies mid-afternoon.
	if !KindStaticTemp.Rots() {
		t.Error("static+token should be flagged as rotting")
	}
	for _, k := range []CredKind{KindSSO, KindSSOSession, KindProcess, KindStatic, KindAssumeRole} {
		if k.Rots() {
			t.Errorf("%v should not be flagged as rotting", k)
		}
	}
}

func TestLoadAWSMergesConfigAndCredentials(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	credPath := filepath.Join(dir, "credentials")

	// ~/.aws/config prefixes profiles with "profile "; ~/.aws/credentials does
	// not. Both must land under the same bare name.
	if err := os.WriteFile(cfgPath, []byte(`
[default]
region = us-east-1

[profile prod]
sso_session = corp
sso_account_id = 999988887777
region = eu-west-1

[sso-session corp]
sso_start_url = https://example.awsapps.com/start

[services thing]
region = ignored
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, []byte(`
[default]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret

[legacy]
aws_access_key_id = ASIAEXAMPLE
aws_secret_access_key = secret
aws_session_token = pasted
`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AWS_CONFIG_FILE", cfgPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credPath)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")

	got, err := LoadAWS()
	if err != nil {
		t.Fatal(err)
	}

	by := map[string]Target{}
	for _, tg := range got {
		by[tg.Name] = tg
	}

	if _, ok := by["sso-session corp"]; ok {
		t.Error("sso-session block was treated as a profile")
	}
	if _, ok := by["services thing"]; ok {
		t.Error("non-profile section was treated as a profile")
	}
	if len(by) != 3 {
		t.Errorf("got %d profiles (%v), want 3", len(by), by)
	}

	// default draws its region from config and its keys from credentials.
	if d := by["default"]; d.Kind != KindStatic || d.Scope != "us-east-1" {
		t.Errorf("default = %+v, want static/us-east-1", d)
	}
	// With no AWS_PROFILE set, the default profile is what commands hit.
	if !by["default"].Active {
		t.Error("default should be marked active when AWS_PROFILE is unset")
	}
	if p := by["prod"]; p.Kind != KindSSOSession || p.Account != "999988887777" {
		t.Errorf("prod = %+v, want sso-session with account from sso_account_id", p)
	}
	if l := by["legacy"]; l.Kind != KindStaticTemp {
		t.Errorf("legacy = %+v, want static+token", l)
	}
}

func TestSummarizeAWSError(t *testing.T) {
	cases := map[string]string{
		"An error occurred (ExpiredToken) when calling...":              "session token expired",
		"Error loading SSO Token: Token has expired and refresh failed": "SSO session expired - run: aws sso login",
		"An error occurred (InvalidClientTokenId) ...":                  "access key not recognized",
		"Unable to locate credentials. You can configure...":            "no credentials resolved",
	}
	for in, want := range cases {
		if got := summarizeAWSError(in, nil); got != want {
			t.Errorf("summarizeAWSError(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanEnvRemovesAmbientCredentials(t *testing.T) {
	// A probe must reflect the profile on disk, not whatever the parent shell
	// exported, or every row reports the same ambient identity.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAAMBIENT")
	t.Setenv("AWS_PROFILE", "something")
	t.Setenv("PATH", "/usr/bin")

	for _, kv := range cleanEnv(awsEnvOverrides) {
		if len(kv) > 18 && kv[:18] == "AWS_ACCESS_KEY_ID=" {
			t.Error("ambient AWS_ACCESS_KEY_ID survived cleanEnv")
		}
		if len(kv) > 12 && kv[:12] == "AWS_PROFILE=" {
			t.Error("ambient AWS_PROFILE survived cleanEnv")
		}
	}
}
