package cloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertPreservesNestedSubProperties(t *testing.T) {
	// The whole reason this edits lines instead of reparsing: a round trip
	// through the parser would drop the indented block and silently change the
	// user's S3 configuration.
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	original := `[profile app]
region = eu-central-1
s3 =
    max_concurrent_requests = 20
    max_queue_size = 10000

[profile other]
region = us-east-1
`
	if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := upsertINISection(p, "profile app", map[string]string{"output": "json"}); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(p)
	s := string(got)
	for _, want := range []string{
		"    max_concurrent_requests = 20",
		"    max_queue_size = 10000",
		"output = json",
		"[profile other]",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("result lost %q:\n%s", want, s)
		}
	}
}

func TestUpsertUpdatesInPlaceWithoutDuplicating(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte("[profile a]\nregion = us-east-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := upsertINISection(p, "profile a", map[string]string{"region": "eu-west-1"}); err != nil {
		t.Fatal(err)
	}

	s, _ := os.ReadFile(p)
	if strings.Count(string(s), "region") != 1 {
		t.Errorf("region was duplicated rather than updated:\n%s", s)
	}
	if !strings.Contains(string(s), "eu-west-1") {
		t.Errorf("region not updated:\n%s", s)
	}
}

func TestUpsertDoesNotLeakIntoTheNextSection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p,
		[]byte("[profile a]\nregion = us-east-1\n\n[profile b]\nregion = eu-west-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := upsertINISection(p, "profile a", map[string]string{"output": "json"}); err != nil {
		t.Fatal(err)
	}

	f, err := parseINI(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.get("profile a", "output") != "json" {
		t.Error("key not written to the target section")
	}
	if f.get("profile b", "output") != "" {
		t.Error("key leaked into the following section")
	}
	if f.get("profile b", "region") != "eu-west-1" {
		t.Error("following section was damaged")
	}
}

func TestWriteAWSStaticSplitsAcrossBothFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	creds := filepath.Join(dir, "credentials")
	t.Setenv("AWS_CONFIG_FILE", cfg)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", creds)

	err := WriteAWSStatic(AWSStaticSpec{
		Name: "client", Region: "eu-west-1",
		AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Keys belong in the credentials file, settings in the config file, which
	// is where the AWS CLI itself puts them.
	c, _ := parseINI(creds)
	if c.get("client", "aws_access_key_id") != "AKIAEXAMPLE" {
		t.Errorf("key not in credentials file: %v", c)
	}
	g, _ := parseINI(cfg)
	if g.get("profile client", "region") != "eu-west-1" {
		t.Errorf("region not in config file under a profile section: %v", g)
	}

	// And the round trip must classify it the way the dashboard will show it.
	t.Setenv("AWS_PROFILE", "")
	targets, err := LoadAWS()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Kind != KindStatic {
		t.Errorf("reload gave %+v", targets)
	}
}

func TestWriteAWSSSOWritesProfileAndSession(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	t.Setenv("AWS_CONFIG_FILE", cfg)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")

	err := WriteAWSSSO(AWSSSOSpec{
		Name: "prod", SessionName: "corp",
		StartURL: "https://example.awsapps.com/start", SSORegion: "eu-west-1",
		AccountID: "999988887777", RoleName: "Admin", Region: "eu-west-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	f, _ := parseINI(cfg)
	if f.get("sso-session corp", "sso_start_url") != "https://example.awsapps.com/start" {
		t.Errorf("sso-session block missing: %v", f)
	}
	if f.get("profile prod", "sso_session") != "corp" {
		t.Errorf("profile does not reference the session: %v", f)
	}

	targets, _ := LoadAWS()
	if len(targets) != 1 || targets[0].Kind != KindSSOSession {
		t.Errorf("reload gave %+v, want one sso-session profile", targets)
	}
	// The sso-session block must not show up as a profile of its own.
	for _, tg := range targets {
		if strings.HasPrefix(tg.Name, "sso-session") {
			t.Errorf("sso-session block surfaced as a profile: %v", tg.Name)
		}
	}
}

func TestWriteGCPConfigRoundTrips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOUDSDK_CONFIG", dir)
	t.Setenv("CLOUDSDK_ACTIVE_CONFIG_NAME", "")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "")
	t.Setenv("CLOUDSDK_CORE_ACCOUNT", "")

	err := WriteGCPConfig(GCPConfigSpec{
		Name: "dev", Account: "me@example.com", Project: "acme-dev", Region: "europe-west1",
	})
	if err != nil {
		t.Fatal(err)
	}

	targets, _, err := LoadGCP()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d configurations, want 1", len(targets))
	}
	if targets[0].Name != "dev" || targets[0].Account != "me@example.com" || targets[0].Scope != "acme-dev" {
		t.Errorf("round trip gave %+v", targets[0])
	}
}

func TestCredentialsFileIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	creds := filepath.Join(dir, "credentials")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", creds)

	if err := WriteAWSStatic(AWSStaticSpec{
		Name: "x", AccessKeyID: "AKIA", SecretAccessKey: "s",
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(creds)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials file mode = %o, want 600", perm)
	}
}

func TestValidateName(t *testing.T) {
	bad := []string{"", "1leading", "has space", "has[bracket", "has]bracket",
		"semi;colon", "quote'mark", strings.Repeat("a", 65), "-dash-first"}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) accepted a bad name", n)
		}
	}
	for _, n := range []string{"default", "client-prod", "a.b_c", "A1"} {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) rejected a good name: %v", n, err)
		}
	}
}

func TestDeleteAWSProfileRemovesFromBothFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	creds := filepath.Join(dir, "credentials")
	t.Setenv("AWS_CONFIG_FILE", cfg)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", creds)
	t.Setenv("AWS_PROFILE", "")

	for _, n := range []string{"keep", "goner"} {
		if err := WriteAWSStatic(AWSStaticSpec{
			Name: n, Region: "eu-west-1", AccessKeyID: "AKIA" + n, SecretAccessKey: "s",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := DeleteAWSProfile("goner"); err != nil {
		t.Fatal(err)
	}

	targets, err := LoadAWS()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "keep" {
		t.Errorf("after delete, profiles = %+v", targets)
	}

	// Neither file may keep a trace of it.
	for _, p := range []string{cfg, creds} {
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "goner") {
			t.Errorf("%s still mentions the deleted profile:\n%s", p, b)
		}
		if !strings.Contains(string(b), "keep") {
			t.Errorf("%s lost the surviving profile:\n%s", p, b)
		}
	}
}

func TestDeleteAWSProfileLeavesSharedSSOSessionAlone(t *testing.T) {
	// Two profiles can share one sso-session block. Removing it with the first
	// profile would break the second.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	t.Setenv("AWS_CONFIG_FILE", cfg)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")

	for _, n := range []string{"one", "two"} {
		if err := WriteAWSSSO(AWSSSOSpec{
			Name: n, SessionName: "corp",
			StartURL: "https://example.awsapps.com/start", SSORegion: "eu-west-1",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := DeleteAWSProfile("one"); err != nil {
		t.Fatal(err)
	}

	f, _ := parseINI(cfg)
	if f.get("sso-session corp", "sso_start_url") == "" {
		t.Error("the shared sso-session block was removed with the profile")
	}
	if f.get("profile two", "sso_session") != "corp" {
		t.Error("the surviving profile lost its session reference")
	}
}

func TestDeleteUnknownIsAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("CLOUDSDK_CONFIG", dir)

	if err := DeleteAWSProfile("nope"); err == nil {
		t.Error("deleting a missing AWS profile should report it")
	}
	if err := DeleteGCPConfig("nope"); err == nil {
		t.Error("deleting a missing gcloud configuration should report it")
	}
}

func TestDeleteGCPConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLOUDSDK_CONFIG", dir)
	t.Setenv("CLOUDSDK_ACTIVE_CONFIG_NAME", "")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "")
	t.Setenv("CLOUDSDK_CORE_ACCOUNT", "")

	for _, n := range []string{"keep", "goner"} {
		if err := WriteGCPConfig(GCPConfigSpec{Name: n, Account: "me@x.com", Project: n}); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteGCPConfig("goner"); err != nil {
		t.Fatal(err)
	}

	targets, _, err := LoadGCP()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "keep" {
		t.Errorf("after delete, configurations = %+v", targets)
	}
}

func TestFieldsRoundTripForEditing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("CLOUDSDK_CONFIG", dir)

	if err := WriteAWSSSO(AWSSSOSpec{
		Name: "prod", SessionName: "corp",
		StartURL: "https://example.awsapps.com/start", SSORegion: "eu-west-1",
		AccountID: "999988887777", RoleName: "Admin", Region: "eu-central-1",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := AWSProfileFields("prod")
	if err != nil {
		t.Fatal(err)
	}
	// Details from the shared session block must come through too, or an edit
	// form would show them blank and then wipe them on save.
	for k, want := range map[string]string{
		"sso_session":    "corp",
		"sso_start_url":  "https://example.awsapps.com/start",
		"sso_region":     "eu-west-1",
		"sso_account_id": "999988887777",
		"sso_role_name":  "Admin",
		"region":         "eu-central-1",
	} {
		if got[k] != want {
			t.Errorf("field %s = %q, want %q", k, got[k], want)
		}
	}

	if err := WriteGCPConfig(GCPConfigSpec{
		Name: "dev", Account: "me@x.com", Project: "acme-dev", Region: "europe-west1",
	}); err != nil {
		t.Fatal(err)
	}
	g, err := GCPConfigFields("dev")
	if err != nil {
		t.Fatal(err)
	}
	if g["account"] != "me@x.com" || g["project"] != "acme-dev" || g["region"] != "europe-west1" {
		t.Errorf("gcloud fields = %+v", g)
	}
}
