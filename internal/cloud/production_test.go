package cloud

import (
	"os"
	"path/filepath"
	"testing"
)

// cxConfig writes a cx configuration file and points the package at it.
func cxConfig(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CX_CONFIG", p)
}

func TestProductionMatchesExactNamesAndGlobs(t *testing.T) {
	cxConfig(t, `[production]
aws = client-prod, *-production, prod-*
gcp = acme-prod
`)
	p, err := LoadProduction()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"client-prod", "acme-production", "prod-eu"} {
		if !p.Matches("aws", name) {
			t.Errorf("%q should be flagged as production", name)
		}
	}
	for _, name := range []string{"staging", "client-prod-2", "production", "acme-prod"} {
		if p.Matches("aws", name) {
			t.Errorf("%q should not be flagged as an AWS production target", name)
		}
	}

	// Providers are kept apart: an AWS pattern must not flag a gcloud
	// configuration that happens to share the name.
	if p.Matches("gcp", "client-prod") {
		t.Error("an AWS pattern leaked into gcloud matching")
	}
	if !p.Matches("gcp", "acme-prod") {
		t.Error("acme-prod should be flagged for gcloud")
	}
}

func TestProductionWithNoConfigurationFlagsNothing(t *testing.T) {
	// The common case: someone who has not opted in must not be prompted.
	t.Setenv("CX_CONFIG", filepath.Join(t.TempDir(), "absent"))

	p, err := LoadProduction()
	if err != nil {
		t.Fatalf("a missing configuration file must not be an error: %v", err)
	}
	if p.Matches("aws", "client-prod") || p.Matches("gcp", "acme-prod") {
		t.Error("nothing should be flagged without a configuration file")
	}
}

func TestProductionIgnoresBlankAndTrailingEntries(t *testing.T) {
	cxConfig(t, "[production]\naws =   client-prod ,, staging-prod ,\n")

	p, err := LoadProduction()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AWS) != 2 {
		t.Fatalf("patterns = %q, want the two real entries", p.AWS)
	}
	if !p.Matches("aws", "client-prod") || !p.Matches("aws", "staging-prod") {
		t.Errorf("patterns = %q, want both to match after trimming", p.AWS)
	}
	// An empty entry becoming a pattern would match the empty name only, but a
	// stray "*" from a mangled split would match everything.
	if p.Matches("aws", "dev") {
		t.Error("a blank entry must not widen the match")
	}
}

func TestProductionReportsAMalformedPattern(t *testing.T) {
	// Silently ignoring a typo protects nothing while appearing to, which is
	// the most dangerous way for this to fail.
	cxConfig(t, "[production]\naws = client-prod, [unclosed\n")

	if _, err := LoadProduction(); err == nil {
		t.Error("a malformed pattern should be reported, not ignored")
	}
}

func TestMarkProductionSetsSensitiveOnMatchingTargetsOnly(t *testing.T) {
	cxConfig(t, "[production]\naws = *-prod\n")
	p, err := LoadProduction()
	if err != nil {
		t.Fatal(err)
	}

	targets := []Target{{Name: "client-prod"}, {Name: "staging"}}
	MarkProduction(targets, p, "aws")

	if !targets[0].Sensitive {
		t.Error("client-prod should be marked sensitive")
	}
	if targets[1].Sensitive {
		t.Error("staging should not be marked sensitive")
	}
}

func TestMarkProductionClearsAStaleFlag(t *testing.T) {
	// Marking runs on every load, so a target that no longer matches must lose
	// the flag rather than keep one from a previous configuration.
	t.Setenv("CX_CONFIG", filepath.Join(t.TempDir(), "absent"))
	p, err := LoadProduction()
	if err != nil {
		t.Fatal(err)
	}

	targets := []Target{{Name: "client-prod", Sensitive: true}}
	MarkProduction(targets, p, "aws")

	if targets[0].Sensitive {
		t.Error("the flag should have been cleared once the pattern was removed")
	}
}

func TestConfigPathPrefersXDGOverHome(t *testing.T) {
	t.Setenv("CX_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	if got, want := ConfigPath(), filepath.Join("/xdg", "cx", "config"); got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}
