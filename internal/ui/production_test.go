package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/san-est/cx/internal/cloud"
)

// dashboardWith returns a loaded dashboard whose first AWS row is the one
// under the cursor.
func dashboardWith(targets ...cloud.Target) Model {
	return Model{loaded: true, canSwitch: true, width: 120, height: 40, aws: targets}
}

func TestEnterOnAProductionTargetAsksFirst(t *testing.T) {
	var mm tea.Model = dashboardWith(cloud.Target{Name: "client-prod", Sensitive: true})

	mm, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := mm.(Model)

	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want a confirmation before switching to production", m.mode)
	}
	if m.pending != nil {
		t.Error("the switch was staged before the question was answered")
	}
	if cmd != nil {
		t.Error("the dashboard quit instead of asking")
	}
	if m.confirm == nil || !strings.Contains(m.confirm.title, "client-prod") {
		t.Errorf("confirmation = %+v, want the target named in the question", m.confirm)
	}
}

func TestEnterOnAnOrdinaryTargetSwitchesWithoutAsking(t *testing.T) {
	// The common case must stay a single keystroke, or the confirmation on the
	// rare one stops being noticed.
	var mm tea.Model = dashboardWith(cloud.Target{Name: "staging"})

	mm, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := mm.(Model)

	if m.mode == modeConfirm {
		t.Error("an unflagged target should not be confirmed")
	}
	if m.pending == nil {
		t.Error("the switch was not staged")
	}
	if cmd == nil {
		t.Error("the dashboard should quit so the wrapper can apply the switch")
	}
}

func TestConfirmingAProductionSwitchStagesItAndQuits(t *testing.T) {
	var mm tea.Model = dashboardWith(cloud.Target{Name: "client-prod", Sensitive: true})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	mm, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m := mm.(Model)

	if m.pending == nil {
		t.Fatal("confirming did not stage the switch")
	}
	if !strings.Contains(m.pending.String(), "client-prod") {
		t.Errorf("staged script = %q, want the chosen profile", m.pending.String())
	}
	if cmd == nil {
		t.Error("confirming should quit: the wrapper applies the change after cx exits")
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want the confirmation cleared", m.mode)
	}
}

func TestDecliningAProductionSwitchChangesNothing(t *testing.T) {
	var mm tea.Model = dashboardWith(cloud.Target{Name: "client-prod", Sensitive: true})
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	mm, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m := mm.(Model)

	if m.pending != nil {
		t.Error("declining still staged the switch")
	}
	if cmd != nil {
		t.Error("declining should return to the dashboard, not quit")
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want the dashboard back", m.mode)
	}
}

func TestProductionFlagSurvivesANarrowNameColumn(t *testing.T) {
	// Losing the name to truncation is cosmetic. Losing the flag would remove
	// the warning, which is worse than the row being unreadable.
	t.Run("wide", func(t *testing.T) {
		got := nameCell(cloud.Target{Name: "client-prod", Sensitive: true}, 24)
		if !strings.Contains(got, "client-prod") || !strings.Contains(got, "[prod]") {
			t.Errorf("cell = %q, want both the name and the flag", got)
		}
	})
	t.Run("narrow", func(t *testing.T) {
		got := nameCell(cloud.Target{Name: "a-very-long-production-profile", Sensitive: true}, 14)
		if !strings.Contains(got, "[prod]") {
			t.Errorf("cell = %q, want the flag kept when space runs short", got)
		}
		if len([]rune(got)) != 14 {
			t.Errorf("cell = %q (%d runes), want exactly 14 to keep the columns aligned", got, len([]rune(got)))
		}
	})
	t.Run("unflagged is unchanged", func(t *testing.T) {
		got := nameCell(cloud.Target{Name: "staging"}, 12)
		if strings.Contains(got, "prod") {
			t.Errorf("cell = %q, want no flag on an ordinary target", got)
		}
	})
}

func TestProductionFlagAppearsInTheContextHeader(t *testing.T) {
	m := dashboardWith(cloud.Target{Name: "client-prod", Sensitive: true, Active: true})

	if got := m.activeName(m.aws); !strings.Contains(got, "[prod]") {
		t.Errorf("active name = %q, want the production flag where the user looks first", got)
	}
}
