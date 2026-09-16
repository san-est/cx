package cloud

import (
	"reflect"
	"testing"
)

func TestShortWarnings(t *testing.T) {
	// Two configurations: only then can a shared selection actually switch to
	// something else, which is what makes it worth warning about.
	active := []Target{
		{Name: "prod", Scope: "acme-prod", Active: true},
		{Name: "dev", Scope: "acme-dev"},
	}

	cases := []struct {
		name  string
		gcp   []Target
		state GCPState
		adc   ADC
		want  []string
	}{
		{
			name:  "shell-pinned and aligned is silent",
			gcp:   active,
			state: GCPState{Active: "prod", Source: SourceShell},
			adc:   ADC{Present: true, QuotaProject: "acme-prod"},
			want:  nil,
		},
		{
			name:  "machine-global selection",
			gcp:   active,
			state: GCPState{Active: "prod", Global: "prod", Source: SourceGlobal},
			adc:   ADC{Present: true, QuotaProject: "acme-prod"},
			want:  []string{"gcp:shared"},
		},
		{
			name:  "dangling config outranks shared",
			gcp:   active,
			state: GCPState{Active: "gone", Global: "gone", Source: SourceGlobal, GlobalMissing: true},
			adc:   ADC{},
			want:  []string{"gcp:no-config"},
		},
		{
			name:  "adc drift names the project adc will hit",
			gcp:   active,
			state: GCPState{Active: "prod", Source: SourceShell},
			adc:   ADC{Present: true, QuotaProject: "acme-dev"},
			want:  []string{"adc:acme-dev"},
		},
		{
			name:  "both hazards at once",
			gcp:   active,
			state: GCPState{Active: "prod", Global: "prod", Source: SourceGlobal},
			adc:   ADC{Present: true, QuotaProject: "acme-dev"},
			want:  []string{"gcp:shared", "adc:acme-dev"},
		},
		{
			name:  "a lone configuration is never reported as shared",
			gcp:   []Target{{Name: "default", Scope: "acme-dev", Active: true}},
			state: GCPState{Active: "default", Global: "default", Source: SourceGlobal},
			adc:   ADC{Present: true, QuotaProject: "acme-dev"},
			// Nothing else exists to switch to, so the warning would be
			// permanently true and therefore useless.
			want: nil,
		},
		{
			name:  "absent adc is not a prompt-level hazard",
			gcp:   active,
			state: GCPState{Active: "prod", Source: SourceShell},
			adc:   ADC{Present: false},
			want:  nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ShortWarnings(c.gcp, c.state, c.adc)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ShortWarnings = %v, want %v", got, c.want)
			}
		})
	}
}
