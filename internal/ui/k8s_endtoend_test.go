package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/san-est/cx/internal/cloud"
)

// TestLoadedDashboardFlagsAProductionContext drives the real load path, not a
// hand-built Model: loadCmd reads the config, marks the contexts, and the view
// renders them. A unit test on either half alone would miss a break in the
// seam between them.
func TestLoadedDashboardFlagsAProductionContext(t *testing.T) {
	dir := t.TempDir()

	cfg := filepath.Join(dir, "cxconfig")
	if err := os.WriteFile(cfg, []byte("[production]\nk8s = gke_acme-prod_*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CX_CONFIG", cfg)
	t.Setenv("CX_KUBE_DIR", filepath.Join(dir, "state"))

	// A stand-in kubectl, so this does not depend on the machine.
	payload := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(payload, []byte(`{
	  "current-context": "gke_acme-prod_europe-west1_main",
	  "clusters": [{"name": "gke-main"}],
	  "users": [{"name": "gke-user"}],
	  "contexts": [
	    {"name": "gke_acme-prod_europe-west1_main", "context": {"cluster": "gke-main", "user": "gke-user"}},
	    {"name": "staging", "context": {"cluster": "gke-main", "user": "gke-user"}}
	  ]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "kubectl"),
		[]byte("#!/bin/sh\n/bin/cat "+payload+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	// No AWS or gcloud, so the panes below are empty and the contexts are all
	// that is on screen.
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "no-aws"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "no-aws-creds"))
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "no-gcloud"))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "no-adc"))

	msg, ok := loadCmd().(loadedMsg)
	if !ok {
		t.Fatal("loadCmd did not return a loadedMsg")
	}
	if !msg.k8sState.Available {
		t.Fatalf("kubernetes unavailable: %q", msg.k8sState.Detail)
	}
	if len(msg.k8s) != 2 {
		t.Fatalf("got %d contexts, want 2", len(msg.k8s))
	}

	var flagged, plain cloud.Target
	for _, c := range msg.k8s {
		if c.Name == "staging" {
			plain = c
		} else {
			flagged = c
		}
	}
	if !flagged.Sensitive {
		t.Errorf("%q should be marked production by the pattern", flagged.Name)
	}
	if plain.Sensitive {
		t.Errorf("%q should not be marked production", plain.Name)
	}

	m := Model{loaded: true, canSwitch: true, width: 140, height: 44}
	m.k8s, m.k8sState = msg.k8s, msg.k8sState

	out := stripANSI(m.View())
	if !strings.Contains(out, "[prod]") {
		t.Errorf("the rendered dashboard does not flag the production context:\n%s", out)
	}
	if !strings.Contains(out, "Kubernetes Contexts") {
		t.Error("the Kubernetes pane was not drawn")
	}
}
