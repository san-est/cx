package cloud

import (
	"os"
	"path/filepath"
	"testing"
)

// gcloudFixture builds a fake CLOUDSDK_CONFIG tree and points the package at it.
func gcloudFixture(t *testing.T, activeConfig string, configs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "configurations"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range configs {
		p := filepath.Join(dir, "configurations", "config_"+name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if activeConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, "active_config"), []byte(activeConfig), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CLOUDSDK_CONFIG", dir)
	t.Setenv("CLOUDSDK_ACTIVE_CONFIG_NAME", "")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "")
	t.Setenv("CLOUDSDK_CORE_ACCOUNT", "")
	return dir
}

const devConfig = `[core]
account = me@example.com
project = proj-dev
[compute]
region = europe-west1
`

const prodConfig = `[core]
account = me@example.com
project = proj-prod
`

func TestGCPActiveFromFileIsFlaggedGlobal(t *testing.T) {
	// The selection lives in a file every shell on the machine shares. This is
	// the bug the tool exists to catch, so it must be reported as unsafe.
	gcloudFixture(t, "dev", map[string]string{"dev": devConfig, "prod": prodConfig})

	targets, state, err := LoadGCP()
	if err != nil {
		t.Fatal(err)
	}
	if state.Active != "dev" {
		t.Errorf("Active = %q, want dev", state.Active)
	}
	if state.Source != SourceGlobal {
		t.Errorf("Source = %v, want SourceGlobal", state.Source)
	}
	if !state.Unsafe() {
		t.Error("a file-derived selection must report Unsafe")
	}
	if state.GlobalMissing {
		t.Error("dev exists; GlobalMissing should be false")
	}

	var active []string
	for _, tg := range targets {
		if tg.Active {
			active = append(active, tg.Name)
		}
	}
	if len(active) != 1 || active[0] != "dev" {
		t.Errorf("active targets = %v, want [dev]", active)
	}
}

func TestGCPEnvVarBeatsFileAndIsSafe(t *testing.T) {
	// A shell-local override is the safe way to pin a target: another terminal
	// cannot change it.
	gcloudFixture(t, "dev", map[string]string{"dev": devConfig, "prod": prodConfig})
	t.Setenv("CLOUDSDK_ACTIVE_CONFIG_NAME", "prod")

	_, state, err := LoadGCP()
	if err != nil {
		t.Fatal(err)
	}
	if state.Active != "prod" {
		t.Errorf("Active = %q, want prod (env must win over active_config)", state.Active)
	}
	if state.Global != "dev" {
		t.Errorf("Global = %q, want dev", state.Global)
	}
	if state.Source != SourceShell || state.Unsafe() {
		t.Errorf("Source = %v, Unsafe = %v; want shell-local and safe", state.Source, state.Unsafe())
	}
}

func TestGCPDanglingActiveConfigDetected(t *testing.T) {
	// gcloud does not complain when active_config names a configuration that
	// was deleted; it quietly falls back to defaults and commands may target
	// no project at all.
	gcloudFixture(t, "deleted", map[string]string{"dev": devConfig})

	_, state, err := LoadGCP()
	if err != nil {
		t.Fatal(err)
	}
	if !state.GlobalMissing {
		t.Error("dangling active_config was not detected")
	}
}

func TestGCPCoreEnvOverridesApplyToActiveOnly(t *testing.T) {
	// CLOUDSDK_CORE_PROJECT silently replaces the configuration's own project.
	// It must be reflected, and only on the target actually in effect.
	gcloudFixture(t, "dev", map[string]string{"dev": devConfig, "prod": prodConfig})
	t.Setenv("CLOUDSDK_CORE_PROJECT", "proj-override")

	targets, state, err := LoadGCP()
	if err != nil {
		t.Fatal(err)
	}
	if state.ProjectOverride != "proj-override" {
		t.Errorf("ProjectOverride = %q", state.ProjectOverride)
	}
	for _, tg := range targets {
		switch tg.Name {
		case "dev":
			if tg.Scope != "proj-override" {
				t.Errorf("active dev scope = %q, want proj-override", tg.Scope)
			}
		case "prod":
			if tg.Scope != "proj-prod" {
				t.Errorf("inactive prod scope = %q, want its own proj-prod", tg.Scope)
			}
		}
	}
}

func TestGCPKeepsTheConfiguredProjectAlongsideAnOverride(t *testing.T) {
	// Audit compares the override against what the file said, to tell a silent
	// retarget from an override that merely restates the configuration. Scope
	// no longer holds that value once the override is applied, so losing
	// ConfiguredScope here would make the Danger alert unreachable without any
	// test in the audit package noticing.
	gcloudFixture(t, "dev", map[string]string{"dev": devConfig})
	t.Setenv("CLOUDSDK_CORE_PROJECT", "proj-override")

	targets, _, err := LoadGCP()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(targets))
	}
	if got := targets[0].Scope; got != "proj-override" {
		t.Errorf("Scope = %q, want the effective proj-override", got)
	}
	if got := targets[0].ConfiguredScope; got != "proj-dev" {
		t.Errorf("ConfiguredScope = %q, want the configured proj-dev", got)
	}
}

func TestGCPConfigWithoutAccountIsKindNone(t *testing.T) {
	gcloudFixture(t, "bare", map[string]string{"bare": "[core]\nproject = p\n"})

	targets, _, err := LoadGCP()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Kind != KindNone {
		t.Errorf("targets = %+v, want one target of kind none", targets)
	}
}

func TestLoadADCServiceAccountNeedsNoProbe(t *testing.T) {
	// A service account key carries its identity inline, so it should resolve
	// without a network round trip.
	dir := t.TempDir()
	p := filepath.Join(dir, "sa.json")
	body := `{"type":"service_account","client_email":"robot@proj.iam.gserviceaccount.com","quota_project_id":"proj-x"}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", p)

	a := LoadADC()
	if !a.Present {
		t.Fatal("ADC should be present")
	}
	if a.Kind != KindServiceAcc {
		t.Errorf("Kind = %v, want service-account", a.Kind)
	}
	if a.Identity != "robot@proj.iam.gserviceaccount.com" {
		t.Errorf("Identity = %q", a.Identity)
	}
	if a.QuotaProject != "proj-x" {
		t.Errorf("QuotaProject = %q, want proj-x", a.QuotaProject)
	}
}

func TestLoadADCAbsent(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "nope.json"))
	a := LoadADC()
	if a.Present || a.Health != Missing {
		t.Errorf("absent ADC = %+v, want not present and Missing", a)
	}
	if a.Detail == "" {
		t.Error("absent ADC should explain how to fix it")
	}
}
