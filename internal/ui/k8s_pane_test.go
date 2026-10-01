package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/san-est/cx/internal/cloud"
)

// dashboardWithK8s returns a loaded dashboard carrying Kubernetes contexts.
func dashboardWithK8s(contexts ...cloud.Target) Model {
	return Model{
		loaded:    true,
		canSwitch: true,
		width:     120,
		height:    40,
		aws:       []cloud.Target{{Name: "staging"}},
		k8s:       contexts,
		k8sState:  cloud.K8sState{Available: true, Active: "prod", Source: cloud.SourceShell},
	}
}

func TestPaneIsHiddenWithoutKubernetes(t *testing.T) {
	// Most shells have no kubectl at all. An empty pane would cost height on
	// every one of them.
	m := Model{loaded: true, width: 120, height: 40, aws: []cloud.Target{{Name: "staging"}}}

	if out := m.View(); strings.Contains(out, "Kubernetes") {
		t.Error("the Kubernetes pane was drawn on a machine with no kubernetes")
	}
}

func TestPaneShowsContextsWithClusterAndNamespace(t *testing.T) {
	m := dashboardWithK8s(
		cloud.Target{Name: "prod", Kind: cloud.KindContext, Account: "prod-cluster",
			Scope: "payments", Active: true, Health: cloud.Valid},
		cloud.Target{Name: "dev", Kind: cloud.KindContext, Account: "dev-cluster",
			Scope: "default", Health: cloud.Valid},
	)

	out := m.View()
	for _, want := range []string{"Kubernetes Contexts", "prod", "prod-cluster", "payments", "dev-cluster"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q", want)
		}
	}
}

func TestContextsAreReachableWithTheCursor(t *testing.T) {
	m := dashboardWithK8s(cloud.Target{Name: "prod"}, cloud.Target{Name: "dev"})

	if got := m.rowCount(); got != 3 {
		t.Fatalf("rowCount = %d, want the AWS profile plus two contexts", got)
	}

	// One AWS profile, so the contexts are rows 1 and 2.
	m.cursor = 1
	tg, provider, ok := m.targetAtCursor()
	if !ok || provider != "k8s" || tg.Name != "prod" {
		t.Errorf("row 1 = %q/%q, want the first context", provider, tg.Name)
	}
	m.cursor = 2
	tg, provider, _ = m.targetAtCursor()
	if provider != "k8s" || tg.Name != "dev" {
		t.Errorf("row 2 = %q/%q, want the second context", provider, tg.Name)
	}
}

func TestEnterOnAContextStagesAKubeconfigOverlay(t *testing.T) {
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	t.Setenv("CX_SHELL_ID", "1234")
	t.Setenv("KUBECONFIG", "")

	m := dashboardWithK8s(cloud.Target{Name: "prod"})
	m.cursor = 1

	var mm tea.Model = m
	mm, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := mm.(Model)

	if got.pending == nil {
		t.Fatal("the switch was not staged")
	}
	if !strings.Contains(got.pending.String(), "KUBECONFIG") {
		t.Errorf("staged script = %q, want a KUBECONFIG export", got.pending.String())
	}
	if cmd == nil {
		t.Error("the dashboard should quit so the wrapper can apply the switch")
	}
}

func TestAProductionContextIsConfirmedLikeAnyOther(t *testing.T) {
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	t.Setenv("CX_SHELL_ID", "1234")

	m := dashboardWithK8s(cloud.Target{Name: "prod", Sensitive: true})
	m.cursor = 1

	var mm tea.Model = m
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := mm.(Model)

	if got.mode != modeConfirm {
		t.Fatalf("mode = %v, want a confirmation before switching to production", got.mode)
	}
	if got.pending != nil {
		t.Error("the switch was staged before the question was answered")
	}
}

func TestKeysThatDoNotApplyToAContextSaySo(t *testing.T) {
	// A key that silently does nothing reads as a broken key map.
	for _, k := range []string{"d", "e", "l"} {
		m := dashboardWithK8s(cloud.Target{Name: "prod"})
		m.cursor = 1

		var mm tea.Model = m
		mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		got := mm.(Model)

		if got.notice == "" {
			t.Errorf("key %q on a context produced no explanation", k)
		}
		if got.mode != modeNormal {
			t.Errorf("key %q on a context changed mode to %v", k, got.mode)
		}
		if !strings.Contains(got.notice, "kubectl") && !strings.Contains(got.notice, "kubeconfig") {
			t.Errorf("key %q notice = %q, want it to point at kubectl", k, got.notice)
		}
	}
}

func TestSharedContextWarningReachesTheDashboard(t *testing.T) {
	m := dashboardWithK8s(cloud.Target{Name: "prod", Active: true})
	m.k8sState = cloud.K8sState{Available: true, Active: "prod", Source: cloud.SourceGlobal}

	if out := m.View(); !strings.Contains(out, "shared kubeconfig") {
		t.Error("the shared-context hazard was not shown in the warnings panel")
	}
}

func TestShareRowsNeverStarvesOrOverfeedsATable(t *testing.T) {
	cases := []struct {
		spare int
		want  []int
	}{
		{spare: 30, want: []int{2, 3, 4}},   // everything fits
		{spare: 6, want: []int{10, 10, 10}}, // nothing fits; split evenly
		{spare: 5, want: []int{1, 20, 1}},   // one table dominates
		{spare: 2, want: []int{5, 5, 5}},    // less spare than tables
		{spare: 7, want: []int{0, 9, 0}},    // two tables are empty
		{spare: 9, want: []int{3, 3}},       // the two-table case still works
	}
	for _, c := range cases {
		got := shareRows(c.spare, c.want)
		if len(got) != len(c.want) {
			t.Fatalf("shareRows(%d, %v) returned %d budgets", c.spare, c.want, len(got))
		}
		sum := 0
		for i, n := range got {
			if n > c.want[i] && n > 1 {
				t.Errorf("shareRows(%d, %v)[%d] = %d, more rows than the table has", c.spare, c.want, i, n)
			}
			if n < 1 {
				t.Errorf("shareRows(%d, %v)[%d] = %d, a table must keep at least one row", c.spare, c.want, i, n)
			}
			sum += n
		}
		// Overflowing the budget is what tears the frame on redraw.
		if sum > c.spare && c.spare >= len(c.want) {
			t.Errorf("shareRows(%d, %v) = %v, totalling %d rows", c.spare, c.want, got, sum)
		}
	}
}

func TestContextHeaderKeepsTheProductionFlagWhenCramped(t *testing.T) {
	// The table already trims the name rather than the flag. The header must
	// agree, or the one line the user reads first is the one that drops the
	// warning.
	long := "gke_acme-prod_europe-west1_main"

	if got := truncateKeepingFlag(long+" [prod]", 20); !strings.HasSuffix(got, "[prod]") {
		t.Errorf("truncateKeepingFlag = %q, want the flag kept", got)
	}
	if got := truncateKeepingFlag(long+" [prod]", 20); len([]rune(got)) > 20 {
		t.Errorf("truncateKeepingFlag = %q (%d runes), want at most 20", got, len([]rune(got)))
	}
	// An ordinary value is untouched by the special case.
	if got := truncateKeepingFlag("staging", 20); got != "staging" {
		t.Errorf("truncateKeepingFlag = %q, want the value unchanged", got)
	}

	m := dashboardWithK8s(cloud.Target{Name: long, Active: true, Sensitive: true})
	m.width = 100
	if out := stripANSI(m.View()); !strings.Contains(out, "[prod]") {
		t.Errorf("the context header dropped the production flag:\n%s", out)
	}
}

func TestEachPaneNamesItsOwnColumns(t *testing.T) {
	// ACCOUNT/SCOPE for everything is accurate and says nothing. A reader
	// should be able to tell a region from a namespace by the header.
	m := dashboardWithK8s(cloud.Target{Name: "prod", Account: "c", Scope: "ns"})
	m.gcp = []cloud.Target{{Name: "dev", Account: "me@example.com", Scope: "acme-dev"}}

	out := stripANSI(m.View())
	for _, want := range []string{"REGION", "PROJECT", "CLUSTER", "NAMESPACE"} {
		if !strings.Contains(out, want) {
			t.Errorf("no pane is headed %q:\n%s", want, out)
		}
	}
}

func TestProductionFlagShowsOnRowsThatAreNotSelected(t *testing.T) {
	// The selected row is styled as a whole from the measured line, while every
	// other row is rebuilt cell by cell. Rendering the name differently in the
	// second path dropped the flag from every row but the one under the cursor
	// -- in all three panes.
	m := dashboardWithK8s(
		cloud.Target{Name: "staging-ctx", Kind: cloud.KindContext, Health: cloud.Valid},
		cloud.Target{Name: "prod-ctx", Kind: cloud.KindContext, Health: cloud.Valid, Sensitive: true},
	)
	m.cursor = 0 // the AWS row, so neither context is selected

	out := stripANSI(m.View())
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "prod-ctx") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("the context row was not drawn:\n%s", out)
	}
	if !strings.Contains(row, "[prod]") {
		t.Errorf("an unselected production row lost its flag:\n%s", row)
	}
}
