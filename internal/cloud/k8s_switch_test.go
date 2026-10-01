package cloud

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// exportedKUBECONFIG pulls the value the shell would end up with out of a
// generated script, rather than recomputing it, so the test checks what the
// user's shell actually receives.
func exportedKUBECONFIG(t *testing.T, script string) string {
	t.Helper()
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, "export KUBECONFIG=") {
			continue
		}
		v := strings.TrimPrefix(line, "export KUBECONFIG=")
		return strings.ReplaceAll(strings.Trim(v, "'"), `'\''`, "'")
	}
	t.Fatalf("no KUBECONFIG export in script:\n%s", script)
	return ""
}

func TestSwitchK8sWritesAnOverlayCarryingNoCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CX_KUBE_DIR", dir)
	t.Setenv("CX_SHELL_ID", "1234")
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "config"))

	if _, err := SwitchK8s("prod"); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "1234.yaml"))
	if err != nil {
		t.Fatalf("overlay not written: %v", err)
	}
	body := string(b)
	if !strings.Contains(body, "current-context: 'prod'") {
		t.Errorf("overlay = %q, want the context set", body)
	}
	// The whole argument for an overlay over a copy: no secret material moves.
	for _, forbidden := range []string{"token", "client-certificate", "client-key", "password", "users:"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("overlay contains %q; it must carry nothing but a context name:\n%s", forbidden, body)
		}
	}
}

func TestSwitchK8sPutsItsOverlayFirstAndKeepsTheRest(t *testing.T) {
	overlayDir := t.TempDir()
	t.Setenv("CX_KUBE_DIR", overlayDir)
	t.Setenv("CX_SHELL_ID", "1234")

	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	t.Setenv("KUBECONFIG", a+string(os.PathListSeparator)+b)

	s, err := SwitchK8s("prod")
	if err != nil {
		t.Fatal(err)
	}
	got := filepath.SplitList(exportedKUBECONFIG(t, s.String()))

	want := []string{filepath.Join(overlayDir, "1234.yaml"), a, b}
	if len(got) != len(want) {
		t.Fatalf("KUBECONFIG = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("KUBECONFIG[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSwitchK8sDoesNotAccumulateOverlays(t *testing.T) {
	// Switching repeatedly must not grow KUBECONFIG without bound, and an
	// older overlay left in front would keep winning over the new one.
	overlayDir := t.TempDir()
	t.Setenv("CX_KUBE_DIR", overlayDir)
	t.Setenv("CX_SHELL_ID", "1234")
	base := filepath.Join(t.TempDir(), "config")
	t.Setenv("KUBECONFIG", base)

	for i := 0; i < 3; i++ {
		s, err := SwitchK8s("prod")
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("KUBECONFIG", exportedKUBECONFIG(t, s.String()))
	}

	got := filepath.SplitList(os.Getenv("KUBECONFIG"))
	if len(got) != 2 {
		t.Errorf("KUBECONFIG = %q, want the overlay and the original only", got)
	}
}

func TestKubeOverlayQuotesAHostileContextName(t *testing.T) {
	// Context names come off disk. One carrying a quote, a colon or a comment
	// marker would otherwise change what the document means.
	for _, name := range []string{"prod", "it's", "a: b", "x # y", "*star"} {
		lines := kubeOverlayLines(name)
		doc := strings.Join(lines, "\n")

		got := sharedKubeContextFromBytes([]byte(doc))
		if got != name {
			t.Errorf("context %q did not round-trip through the overlay: got %q\n%s", name, got, doc)
		}
	}
}

func TestClearK8sRepinsInsteadOfFollowingTheSharedFile(t *testing.T) {
	// Unsetting KUBECONFIG would hand the shell back to the file every
	// terminal shares, so clearing would recreate the hazard it is meant to
	// remove.
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	t.Setenv("CX_SHELL_ID", "1234")
	shared := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(shared, []byte("current-context: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", shared)

	out := Clear("k8s").String()
	if strings.Contains(out, "unset KUBECONFIG") {
		t.Errorf("clear unset KUBECONFIG, putting the shell back under shared state:\n%s", out)
	}
	if !strings.Contains(out, "export KUBECONFIG=") {
		t.Errorf("clear did not re-pin the shell:\n%s", out)
	}
	if !strings.Contains(out, "dev") {
		t.Errorf("clear did not adopt the shared context by value:\n%s", out)
	}
}

func TestClearK8sWithNoSharedSelectionJustUnsets(t *testing.T) {
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	t.Setenv("CX_SHELL_ID", "1234")
	empty := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(empty, []byte("kind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", empty)

	if out := Clear("k8s").String(); !strings.Contains(out, "unset KUBECONFIG") {
		t.Errorf("want KUBECONFIG unset when there is nothing to pin to:\n%s", out)
	}
}

// TestSwitchK8sRetargetsRealKubectl is the claim the whole design rests on:
// an overlay at the front of KUBECONFIG changes this shell's context while the
// shared kubeconfig is left byte for byte alone.
func TestSwitchK8sRetargetsRealKubectl(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not installed")
	}

	t.Setenv("CX_KUBE_DIR", t.TempDir())
	t.Setenv("CX_SHELL_ID", "1234")

	shared := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(shared, []byte(`apiVersion: v1
kind: Config
current-context: dev
clusters:
- {name: dev-cluster, cluster: {server: https://dev.example.com}}
- {name: prod-cluster, cluster: {server: https://prod.example.com}}
contexts:
- {name: dev, context: {cluster: dev-cluster, user: dev-user}}
- {name: prod, context: {cluster: prod-cluster, user: prod-user}}
users:
- {name: dev-user, user: {token: dev-token}}
- {name: prod-user, user: {token: prod-token}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", shared)

	before, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}

	s, err := SwitchK8s("prod")
	if err != nil {
		t.Fatal(err)
	}
	merged := exportedKUBECONFIG(t, s.String())

	cmd := exec.Command("kubectl", "config", "current-context")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+merged)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("kubectl: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "prod" {
		t.Errorf("kubectl current-context = %q, want prod", got)
	}

	after, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the shared kubeconfig was modified; the point of the overlay is that it is not")
	}
}
