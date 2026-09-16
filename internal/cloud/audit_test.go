package cloud

import (
	"strings"
	"testing"
)

func hasTitle(alerts []Alert, substr string) bool {
	for _, a := range alerts {
		if strings.Contains(a.Title, substr) {
			return true
		}
	}
	return false
}

func TestAuditFlagsMachineGlobalSelection(t *testing.T) {
	gcp := []Target{{Name: "prod", Scope: "acme-prod", Account: "me@x.com", Active: true}}
	state := GCPState{Active: "prod", Global: "prod", Source: SourceGlobal}
	adc := ADC{Present: true, QuotaProject: "acme-prod"}

	alerts := Audit(gcp, state, adc)
	if !hasTitle(alerts, "machine-global") {
		t.Errorf("expected a machine-global alert, got %+v", alerts)
	}
	if !HasDanger(alerts) {
		t.Error("machine-global selection must be Danger")
	}
}

func TestAuditSilentWhenShellPinnedAndAligned(t *testing.T) {
	// The healthy configuration: a shell-local pin, and ADC pointing at the
	// same project and principal. This must be quiet, or the warnings become
	// noise people learn to ignore.
	gcp := []Target{{Name: "prod", Scope: "acme-prod", Account: "me@x.com", Active: true}}
	state := GCPState{Active: "prod", Global: "dev", Source: SourceShell}
	adc := ADC{Present: true, QuotaProject: "acme-prod", Identity: "me@x.com"}

	if alerts := Audit(gcp, state, adc); len(alerts) != 0 {
		t.Errorf("expected no alerts for aligned state, got %+v", alerts)
	}
}

func TestAuditFlagsADCQuotaDriftAsWarnNotDanger(t *testing.T) {
	// The ADC quota project decides who is billed for client-library API calls.
	// Terraform ignores it entirely -- its google provider uses billing_project
	// -- so this is worth saying, but it does not misdirect an apply.
	gcp := []Target{{Name: "prod", Scope: "acme-prod", Account: "me@x.com", Active: true}}
	state := GCPState{Active: "prod", Source: SourceShell}
	adc := ADC{Present: true, QuotaProject: "acme-dev", Identity: "me@x.com"}

	alerts := Audit(gcp, state, adc)
	if !hasTitle(alerts, "bills API calls to acme-dev") {
		t.Errorf("expected an ADC billing alert, got %+v", alerts)
	}
	if HasDanger(alerts) {
		t.Error("ADC quota drift is not a danger: terraform does not read it")
	}
}

func TestAuditFlagsCoreProjectOverrideAsDanger(t *testing.T) {
	// This one genuinely can retarget an apply: the google provider reads
	// CLOUDSDK_CORE_PROJECT while ignoring gcloud's configuration files.
	gcp := []Target{{Name: "dev", Scope: "acme-dev", Account: "me@x.com", Active: true}}
	state := GCPState{Active: "dev", Source: SourceShell, ProjectOverride: "acme-prod"}

	alerts := Audit(gcp, state, ADC{Present: true, QuotaProject: "acme-dev"})
	if !hasTitle(alerts, "CLOUDSDK_CORE_PROJECT=acme-prod") {
		t.Errorf("expected a core-project override alert, got %+v", alerts)
	}
	if !HasDanger(alerts) {
		t.Error("an override that disagrees with the gcloud project must be Danger")
	}

	// Agreeing with the configuration is merely worth noting.
	agreeing := GCPState{Active: "dev", Source: SourceShell, ProjectOverride: "acme-dev"}
	if HasDanger(Audit(gcp, agreeing, ADC{Present: true, QuotaProject: "acme-dev"})) {
		t.Error("an override matching the configuration should not be Danger")
	}
}

func TestAuditFlagsADCIdentityDrift(t *testing.T) {
	gcp := []Target{{Name: "prod", Scope: "acme-prod", Account: "me@x.com", Active: true}}
	state := GCPState{Active: "prod", Source: SourceShell}
	adc := ADC{Present: true, QuotaProject: "acme-prod", Identity: "someone-else@x.com"}

	if !hasTitle(Audit(gcp, state, adc), "ADC authenticates as") {
		t.Error("expected an ADC identity drift alert")
	}
}

func TestAuditMissingADCIsWarnNotDanger(t *testing.T) {
	// Absent ADC fails loudly at apply time rather than silently targeting the
	// wrong place, so it does not warrant a non-zero exit.
	gcp := []Target{{Name: "dev", Scope: "acme-dev", Account: "me@x.com", Active: true}}
	state := GCPState{Active: "dev", Source: SourceShell}

	alerts := Audit(gcp, state, ADC{Present: false})
	if !hasTitle(alerts, "ADC is not configured") {
		t.Errorf("expected a missing-ADC alert, got %+v", alerts)
	}
	if HasDanger(alerts) {
		t.Error("missing ADC should be Warn, not Danger")
	}
}

func TestAuditFlagsDanglingActiveConfig(t *testing.T) {
	state := GCPState{Active: "gone", Global: "gone", Source: SourceGlobal, GlobalMissing: true}
	if !hasTitle(Audit(nil, state, ADC{}), "does not exist") {
		t.Error("expected a dangling active_config alert")
	}
}

func TestAuditFlagsCoreEnvOverrides(t *testing.T) {
	state := GCPState{Active: "dev", Source: SourceShell, ProjectOverride: "other-proj"}
	alerts := Audit(nil, state, ADC{Present: true})
	if !hasTitle(alerts, "CLOUDSDK_CORE_PROJECT") {
		t.Errorf("expected a CLOUDSDK_CORE_PROJECT alert, got %+v", alerts)
	}
}
