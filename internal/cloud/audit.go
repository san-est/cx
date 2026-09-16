package cloud

import "fmt"

// Severity ranks an audit finding.
type Severity int

const (
	// Warn marks state that is merely surprising.
	Warn Severity = iota
	// Danger marks state that can send a command to the wrong account or
	// project without the user noticing.
	Danger
)

// Alert is a hazard that spans more than one target, and so cannot be expressed
// by any single row of the dashboard.
type Alert struct {
	Severity Severity
	// Title states what is wrong.
	Title string
	// Fix states what to do about it.
	Fix string
}

// Audit inspects the whole resolved picture for the ways a shell can end up
// pointed somewhere other than where its operator believes.
//
// This lives in the domain layer rather than the UI because both the dashboard
// and the scriptable --plain mode need it, and because a non-zero exit from the
// latter is the thing that can gate a `terraform apply`.
func Audit(gcp []Target, state GCPState, adc ADC) []Alert {
	var out []Alert

	// A selection that lives in a shared file is one another terminal can
	// change underneath this one.
	if state.Unsafe() {
		out = append(out, Alert{
			Severity: Danger,
			Title:    fmt.Sprintf("gcloud target %q comes from machine-global state, not this shell", state.Active),
			// The shell-init auto-pin fixes this for every new shell, so the
			// usual cause is a terminal that was open before cx was installed.
			// Say that, rather than handing over an export line to paste.
			Fix: "another terminal running `gcloud config configurations activate` retargets this shell too — press enter on a row to pin it, or just open a new terminal",
		})
	}
	if state.GlobalMissing {
		out = append(out, Alert{
			Severity: Danger,
			Title:    fmt.Sprintf("active_config names %q, which does not exist", state.Global),
			Fix:      "gcloud falls back to built-in defaults without complaining — commands may target no project at all",
		})
	}

	var active *Target
	for i := range gcp {
		if gcp[i].Active {
			active = &gcp[i]
			break
		}
	}

	// ADC is a different credential store from the active gcloud
	// configuration, and nothing in the standard tooling shows both at once.
	if active != nil && adc.Present {
		if q := adc.QuotaProject; q != "" && active.Scope != "" && q != active.Scope {
			out = append(out, Alert{
				Severity: Warn,
				Title:    fmt.Sprintf("ADC bills API calls to %s, but gcloud is set to %s", q, active.Scope),
				Fix:      "the Google client libraries charge quota to the ADC project — terraform uses its own billing_project instead and ignores this",
			})
		}
		if adc.Identity != "" && active.Account != "" && adc.Identity != active.Account {
			out = append(out, Alert{
				Severity: Danger,
				Title:    fmt.Sprintf("ADC authenticates as %s but gcloud is %s", adc.Identity, active.Account),
				Fix:      "re-sync with: gcloud auth application-default login",
			})
		}
	}
	if active != nil && !adc.Present {
		out = append(out, Alert{
			Severity: Warn,
			Title:    "a gcloud configuration is active but ADC is not configured",
			Fix:      "terraform and the client libraries will fail to authenticate — run: gcloud auth application-default login",
		})
	}

	// Env overrides beat the configuration's stored properties and are easy to
	// set once and forget.
	if state.ProjectOverride != "" {
		// This is the one piece of gcloud state that reaches Terraform: the
		// google provider reads CLOUDSDK_CORE_PROJECT when no project is set in
		// the provider block, while ignoring gcloud's configuration files
		// entirely. So a forgotten export here really can retarget an apply.
		sev := Warn
		title := fmt.Sprintf("CLOUDSDK_CORE_PROJECT=%s overrides the active configuration's project", state.ProjectOverride)
		if active != nil && active.Scope != "" && state.ProjectOverride != active.Scope {
			sev = Danger
			title = fmt.Sprintf("CLOUDSDK_CORE_PROJECT=%s overrides the gcloud project (%s)", state.ProjectOverride, active.Scope)
		}
		out = append(out, Alert{
			Severity: sev,
			Title:    title,
			Fix:      "terraform's google provider reads this variable — unset it to fall back to the configuration's own project",
		})
	}
	if state.AccountOverride != "" {
		out = append(out, Alert{
			Severity: Warn,
			Title:    fmt.Sprintf("CLOUDSDK_CORE_ACCOUNT=%s overrides the active configuration's account", state.AccountOverride),
			Fix:      "unset it to fall back to the configuration's own setting",
		})
	}
	return out
}

// HasDanger reports whether any alert describes state that can misdirect a
// command. Callers use it to decide an exit code.
func HasDanger(alerts []Alert) bool {
	for _, a := range alerts {
		if a.Severity == Danger {
			return true
		}
	}
	return false
}
