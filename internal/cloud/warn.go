package cloud

// ShortWarnings returns terse codes for hazards worth putting in a shell
// prompt. It is deliberately narrow: a prompt has room for a nudge, not an
// explanation, and `cx status` is where the full reasoning lives.
//
// Only locally-determinable hazards appear here. Anything needing a network
// round trip is excluded, because this runs before every prompt.
func ShortWarnings(gcp []Target, state GCPState, adc ADC) []string {
	var out []string

	if state.GlobalMissing {
		// gcloud silently falls back to built-in defaults, so commands may
		// target no project at all.
		out = append(out, "gcp:no-config")
	} else if state.Unsafe() && len(gcp) > 1 {
		// The target comes from a file every shell shares. With only one
		// configuration on the machine there is nothing it could be switched
		// to, so saying so every prompt would be pure noise.
		out = append(out, "gcp:shared")
	}

	var active *Target
	for i := range gcp {
		if gcp[i].Active {
			active = &gcp[i]
			break
		}
	}

	// Terraform and the client libraries follow ADC, not `gcloud config`. When
	// the two disagree, name the project ADC will actually bill and target.
	if active != nil && adc.Present && adc.QuotaProject != "" &&
		active.Scope != "" && adc.QuotaProject != active.Scope {
		out = append(out, "adc:"+adc.QuotaProject)
	}

	return out
}
