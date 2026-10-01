package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/san-est/cx/internal/cloud"
)

func TestParseAddFlagsReadsValuesAndPositionals(t *testing.T) {
	f, err := parseAddFlags([]string{"--region", "eu-west-1", "client-prod", "--access-key", "AKIAEXAMPLE"})
	if err != nil {
		t.Fatal(err)
	}
	if f.region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", f.region)
	}
	if f.accessKey != "AKIAEXAMPLE" {
		t.Errorf("access key = %q, want AKIAEXAMPLE", f.accessKey)
	}
	if len(f.rest) != 1 || f.rest[0] != "client-prod" {
		t.Errorf("positionals = %q, want just the profile name", f.rest)
	}
	if f.withToken || f.force {
		t.Errorf("booleans set without being asked for: %+v", f)
	}
}

func TestParseAddFlagsHandlesTheBooleans(t *testing.T) {
	f, err := parseAddFlags([]string{"x", "--with-session-token", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.withToken || !f.force {
		t.Errorf("flags = %+v, want both set", f)
	}
}

func TestParseAddFlagsRefusesACredentialOnTheCommandLine(t *testing.T) {
	// The whole reason the secret is not a flag: process arguments are
	// readable by other processes and are kept in shell history. Being told
	// why beats the flag being silently ignored.
	for _, args := range [][]string{
		{"x", "--secret-key", "s3cret"},
		{"x", "--secret-access-key", "s3cret"},
		{"x", "--session-token", "tok"},
	} {
		_, err := parseAddFlags(args)
		if err == nil {
			t.Errorf("args %q were accepted; a credential must not be a flag", args)
			continue
		}
		if !strings.Contains(err.Error(), "shell history") {
			t.Errorf("args %q: error %q does not explain why", args, err)
		}
	}
}

func TestParseAddFlagsReportsAMissingValueAndUnknownFlags(t *testing.T) {
	if _, err := parseAddFlags([]string{"x", "--region"}); err == nil {
		t.Error("a flag with no value was accepted")
	}
	if _, err := parseAddFlags([]string{"x", "--colour"}); err == nil {
		t.Error("an unknown flag was accepted")
	}
}

// scriptedPrompter answers in order and records what it was asked, so the
// order of the questions is itself under test.
type scriptedPrompter struct {
	answers []string
	asked   []string
	secrets []string
}

func (p *scriptedPrompter) take(label string) (string, error) {
	p.asked = append(p.asked, label)
	if len(p.answers) == 0 {
		return "", nil
	}
	v := p.answers[0]
	p.answers = p.answers[1:]
	return v, nil
}

func (p *scriptedPrompter) Line(label string) (string, error) { return p.take(label) }
func (p *scriptedPrompter) Secret(label string) (string, error) {
	p.secrets = append(p.secrets, label)
	return p.take(label)
}

func TestCollectAsksOnlyForWhatTheFlagsDidNotSupply(t *testing.T) {
	p := &scriptedPrompter{answers: []string{"s3cret"}}
	spec, err := collectAWSStatic(p, addFlags{region: "eu-west-1", accessKey: "AKIAEXAMPLE"}, "client-prod")
	if err != nil {
		t.Fatal(err)
	}
	if spec.AccessKeyID != "AKIAEXAMPLE" || spec.Region != "eu-west-1" {
		t.Errorf("spec = %+v, want the flag values kept", spec)
	}
	if spec.SecretAccessKey != "s3cret" {
		t.Errorf("secret = %q, want the prompted value", spec.SecretAccessKey)
	}
	if len(p.asked) != 1 {
		t.Errorf("asked %q, want only the secret", p.asked)
	}
}

func TestCollectNeverEchoesACredential(t *testing.T) {
	// The secret and the session token must go through Secret; anything asked
	// as an ordinary Line is echoed to the terminal.
	p := &scriptedPrompter{answers: []string{"AKIAEXAMPLE", "s3cret", "tok", "eu-west-1"}}
	spec, err := collectAWSStatic(p, addFlags{withToken: true}, "client-prod")
	if err != nil {
		t.Fatal(err)
	}
	if spec.SessionToken != "tok" {
		t.Errorf("session token = %q", spec.SessionToken)
	}
	want := []string{"Secret access key", "Session token"}
	if len(p.secrets) != len(want) {
		t.Fatalf("hidden prompts = %q, want exactly %q", p.secrets, want)
	}
	for i := range want {
		if p.secrets[i] != want[i] {
			t.Errorf("hidden prompt %d = %q, want %q", i, p.secrets[i], want[i])
		}
	}
}

func TestCollectSkipsTheSessionTokenUnlessAskedFor(t *testing.T) {
	// A profile with a pasted token is the one kind here that expires with
	// nothing able to refresh it, so it is never created by accident.
	p := &scriptedPrompter{answers: []string{"AKIAEXAMPLE", "s3cret", "eu-west-1"}}
	spec, err := collectAWSStatic(p, addFlags{}, "client-prod")
	if err != nil {
		t.Fatal(err)
	}
	if spec.SessionToken != "" {
		t.Errorf("session token = %q, want none without --with-session-token", spec.SessionToken)
	}
	for _, label := range p.asked {
		if strings.Contains(label, "Session") {
			t.Errorf("asked for a session token anyway: %q", p.asked)
		}
	}
}

func TestPipePrompterReadsOneValuePerLine(t *testing.T) {
	p := newPipePrompter(strings.NewReader("AKIAEXAMPLE\ns3cret\neu-west-1\n"))
	spec, err := collectAWSStatic(p, addFlags{}, "client-prod")
	if err != nil {
		t.Fatal(err)
	}
	if spec.AccessKeyID != "AKIAEXAMPLE" || spec.SecretAccessKey != "s3cret" || spec.Region != "eu-west-1" {
		t.Errorf("spec = %+v, want the three piped values in order", spec)
	}
}

func TestPipePrompterSaysWhatItExpectedWhenInputRunsOut(t *testing.T) {
	// A script that pipes too little should be told what was missing rather
	// than writing a profile with an empty credential.
	p := newPipePrompter(strings.NewReader("AKIAEXAMPLE\n"))
	_, err := collectAWSStatic(p, addFlags{}, "client-prod")
	if err == nil {
		t.Fatal("running out of input was not reported")
	}
	if !strings.Contains(err.Error(), "Secret access key") {
		t.Errorf("error %q does not name what was missing", err)
	}
}

func TestCollectCarriesTheNameThrough(t *testing.T) {
	p := &scriptedPrompter{answers: []string{"AKIAEXAMPLE", "s3cret", ""}}
	spec, err := collectAWSStatic(p, addFlags{}, "client-prod")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "client-prod" {
		t.Errorf("name = %q", spec.Name)
	}
	var _ cloud.AWSStaticSpec = spec
}

// awsFiles points cx at empty AWS files of its own.
func awsFiles(t *testing.T) (config, creds string) {
	t.Helper()
	dir := t.TempDir()
	config = filepath.Join(dir, "config")
	creds = filepath.Join(dir, "credentials")
	for _, p := range []string{config, creds} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AWS_CONFIG_FILE", config)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", creds)
	for _, k := range []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ACCESS_KEY_ID"} {
		t.Setenv(k, "")
	}
	return config, creds
}

// pipeStdin feeds one invocation's answers in, as a pipe would.
//
// Called once per invocation rather than once per test: the reader buffers
// ahead, so a second run sharing the same handle would find it already drained.
// Each real `cx add` is its own process with its own stdin.
func pipeStdin(t *testing.T, answers string) {
	t.Helper()
	in := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(in, []byte(answers), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

func TestAddWritesKeysAndSettingsWhereTheAWSCLILooks(t *testing.T) {
	silenceOutput(t)
	config, creds := awsFiles(t)
	pipeStdin(t, "AKIAEXAMPLEKEYID\ns3cret\neu-west-1\n")

	if code := runAddAWS([]string{"client-prod"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	c, err := os.ReadFile(creds)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c), "aws_secret_access_key = s3cret") {
		t.Errorf("credentials = %q, want the secret", c)
	}
	g, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(g), "region = eu-west-1") {
		t.Errorf("config = %q, want the region", g)
	}
	// Settings belong in the config file and keys in the credentials file.
	if strings.Contains(string(g), "s3cret") {
		t.Error("the secret was written to the config file")
	}
	if info, err := os.Stat(creds); err == nil && info.Mode().Perm() != 0o600 {
		t.Errorf("credentials mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestAddRefusesToReplaceAnExistingProfile(t *testing.T) {
	// Overwriting discards credentials that may be the only copy.
	silenceOutput(t)
	awsFiles(t)

	pipeStdin(t, "AKIAEXAMPLEKEYID\nfirst\n\n")
	if code := runAddAWS([]string{"client-prod"}); code != 0 {
		t.Fatalf("first write failed with %d", code)
	}
	pipeStdin(t, "AKIAOTHER\nsecond\n\n")
	if code := runAddAWS([]string{"client-prod"}); code == 0 {
		t.Error("a second write silently replaced the profile")
	}
}

func TestAddReplacesAnExistingProfileWithForce(t *testing.T) {
	silenceOutput(t)
	_, creds := awsFiles(t)

	pipeStdin(t, "AKIAEXAMPLEKEYID\nfirst\n\n")
	if code := runAddAWS([]string{"client-prod"}); code != 0 {
		t.Fatalf("first write failed with %d", code)
	}
	pipeStdin(t, "AKIAOTHER\nsecond\n\n")
	if code := runAddAWS([]string{"client-prod", "--force"}); code != 0 {
		t.Fatalf("--force was refused with %d", code)
	}

	c, err := os.ReadFile(creds)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c), "second") {
		t.Errorf("credentials = %q, want the replacement", c)
	}
}

func TestAddRejectsANameTheAWSCLICannotUse(t *testing.T) {
	silenceOutput(t)
	awsFiles(t)
	pipeStdin(t, "AKIAEXAMPLEKEYID\ns3cret\n\n")

	if code := runAddAWS([]string{"has spaces"}); code == 0 {
		t.Error("an invalid profile name was accepted")
	}
}

func TestAddWithNoCredentialWritesNothing(t *testing.T) {
	// Running out of piped input must not leave a profile with a blank key.
	silenceOutput(t)
	_, creds := awsFiles(t)
	pipeStdin(t, "AKIAEXAMPLEKEYID\n")

	if code := runAddAWS([]string{"client-prod"}); code == 0 {
		t.Error("a profile was written without a secret")
	}
	c, _ := os.ReadFile(creds)
	if strings.Contains(string(c), "client-prod") {
		t.Errorf("credentials = %q, want nothing written", c)
	}
}

func TestRunAddOnlyHandlesAWS(t *testing.T) {
	// The others are browser sign-ins only the vendor CLI can complete.
	silenceOutput(t)
	if code := runAdd([]string{"gcp", "whatever"}); code == 0 {
		t.Error("cx add gcp was accepted")
	}
	if code := runAdd(nil); code == 0 {
		t.Error("cx add with no provider was accepted")
	}
}
