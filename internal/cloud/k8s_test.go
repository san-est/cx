package cloud

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeKubectl puts a stand-in kubectl at the front of PATH.
//
// Shelling out to the real binary would make these tests depend on whichever
// kubectl and kubeconfig the machine happens to have, which is the failure the
// shell-init tests already taught us to avoid.
func fakeKubectl(t *testing.T, stdout string, exitCode int) {
	t.Helper()
	dir := t.TempDir()

	// The payload goes in its own file and the stub uses absolute paths for
	// everything: PATH is replaced wholesale below, so the stub cannot rely on
	// finding any command itself.
	payload := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(payload, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n/bin/cat " + payload + "\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// kubeconfigOnDisk writes a shared kubeconfig and points KUBECONFIG at it.
func kubeconfigOnDisk(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", p)
	return p
}

const twoContexts = `{
  "current-context": "prod",
  "clusters": [{"name": "dev-cluster"}, {"name": "prod-cluster"}],
  "users": [{"name": "dev-user"}, {"name": "prod-user"}],
  "contexts": [
    {"name": "dev",  "context": {"cluster": "dev-cluster",  "user": "dev-user",  "namespace": "apps"}},
    {"name": "prod", "context": {"cluster": "prod-cluster", "user": "prod-user"}}
  ]
}`

func TestLoadK8sReadsContextsAndMarksTheActiveOne(t *testing.T) {
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	kubeconfigOnDisk(t, "current-context: prod\n")
	fakeKubectl(t, twoContexts, 0)

	targets, state, err := LoadK8s()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Available {
		t.Fatalf("state = %+v, want available", state)
	}
	if state.Active != "prod" {
		t.Errorf("Active = %q, want prod", state.Active)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d contexts, want 2", len(targets))
	}
	for _, tg := range targets {
		switch tg.Name {
		case "prod":
			if !tg.Active {
				t.Error("prod should be the active context")
			}
			if tg.Account != "prod-cluster" {
				t.Errorf("cluster = %q, want prod-cluster", tg.Account)
			}
			// kubectl's own default when a context omits the namespace. Showing
			// it blank would read as "no namespace", which is a different and
			// much safer thing than "default".
			if tg.Scope != "default" {
				t.Errorf("namespace = %q, want the implied default", tg.Scope)
			}
		case "dev":
			if tg.Active {
				t.Error("dev should not be active")
			}
			if tg.Scope != "apps" {
				t.Errorf("namespace = %q, want apps", tg.Scope)
			}
		}
	}
}

func TestLoadK8sFlagsAContextPointingAtNothing(t *testing.T) {
	// A context naming a cluster that is gone fails only when a command runs,
	// which is the wrong moment to discover it.
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	kubeconfigOnDisk(t, "current-context: broken\n")
	fakeKubectl(t, `{
	  "current-context": "broken",
	  "clusters": [{"name": "real"}],
	  "users": [{"name": "real-user"}],
	  "contexts": [
	    {"name": "broken",   "context": {"cluster": "deleted", "user": "real-user"}},
	    {"name": "no-user",  "context": {"cluster": "real",    "user": "deleted"}}
	  ]
	}`, 0)

	targets, _, err := LoadK8s()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d contexts, want 2", len(targets))
	}
	if targets[0].Health != Missing || !strings.Contains(targets[0].Detail, "cluster") {
		t.Errorf("broken context = %+v, want a missing-cluster detail", targets[0])
	}
	if targets[1].Health != Missing || !strings.Contains(targets[1].Detail, "user") {
		t.Errorf("no-user context = %+v, want a missing-user detail", targets[1])
	}
}

func TestLoadK8sWithoutKubectlIsNotAnError(t *testing.T) {
	// Most shells on most machines have no Kubernetes at all. That is an
	// ordinary state, and the dashboard must not report it as a failure.
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	targets, state, err := LoadK8s()
	if err != nil {
		t.Fatalf("a missing kubectl must not be an error: %v", err)
	}
	if state.Available {
		t.Error("state should report kubernetes as unavailable")
	}
	if !strings.Contains(state.Detail, "kubectl") {
		t.Errorf("detail = %q, want it to name kubectl", state.Detail)
	}
	if targets != nil {
		t.Errorf("targets = %+v, want none", targets)
	}
}

func TestLoadK8sSurvivesUnparseableOutput(t *testing.T) {
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	fakeKubectl(t, "not json at all", 0)

	if _, state, err := LoadK8s(); err != nil || state.Available {
		t.Errorf("state = %+v, err = %v; want unavailable and no error", state, err)
	}
}

func TestLoadK8sCallsASharedContextUnsafe(t *testing.T) {
	// The hazard this pane exists for: the selection lives in a file every
	// terminal reads, so another one can retarget this shell.
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	kubeconfigOnDisk(t, "current-context: prod\n")
	fakeKubectl(t, twoContexts, 0)

	_, state, err := LoadK8s()
	if err != nil {
		t.Fatal(err)
	}
	if state.Source != SourceGlobal {
		t.Errorf("Source = %v, want SourceGlobal for a shared kubeconfig", state.Source)
	}
	if !state.Unsafe() {
		t.Error("a context from the shared kubeconfig must be reported unsafe")
	}
}

func TestLoadK8sCallsAnOverlaidContextSafe(t *testing.T) {
	// With cx's own overlay at the front of KUBECONFIG the selection is this
	// shell's alone, so there is nothing to warn about.
	overlayDir := t.TempDir()
	t.Setenv("CX_KUBE_DIR", overlayDir)

	overlay := filepath.Join(overlayDir, "42.yaml")
	if err := os.WriteFile(overlay, []byte("current-context: prod\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(shared, []byte("current-context: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", overlay+string(os.PathListSeparator)+shared)
	fakeKubectl(t, twoContexts, 0)

	_, state, err := LoadK8s()
	if err != nil {
		t.Fatal(err)
	}
	if state.Source != SourceShell {
		t.Errorf("Source = %v, want SourceShell when cx's overlay leads KUBECONFIG", state.Source)
	}
	if state.Unsafe() {
		t.Error("a shell-local context must not be reported unsafe")
	}
	// What the other terminals see, which is the thing worth telling the user.
	if state.Shared != "dev" {
		t.Errorf("Shared = %q, want dev from the shared file", state.Shared)
	}
}

func TestSharedKubeContextReadsTheValueNotTheDecoration(t *testing.T) {
	for body, want := range map[string]string{
		"current-context: prod\n":                                             "prod",
		"current-context: \"prod\"\n":                                         "prod",
		"current-context: 'prod'\n":                                           "prod",
		"current-context: prod # set by the installer":                        "prod",
		"apiVersion: v1\nkind: Config\ncurrent-context: prod\nclusters: []\n": "prod",
		"kind: Config\n":                                                      "",
	} {
		t.Setenv("KUBECONFIG", "")
		p := filepath.Join(t.TempDir(), "config")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KUBECONFIG", p)

		if got := sharedKubeContext(); got != want {
			t.Errorf("sharedKubeContext() for %q = %q, want %q", body, got, want)
		}
	}
}

func TestParseYAMLScalarHandlesQuotingAndComments(t *testing.T) {
	// Context names are not always plain words: an EKS context is a full ARN,
	// and tools quote values that begin with punctuation.
	cases := map[string]string{
		"prod":                        "prod",
		"  prod  ":                    "prod",
		`"prod"`:                      "prod",
		"'prod'":                      "prod",
		"prod # set by the installer": "prod",
		"'needs # a hash'":            "needs # a hash",
		"'it''s'":                     "it's",
		`"say \"hi\""`:                `say "hi"`,
		"arn:aws:eks:eu-west-1:1:c/x": "arn:aws:eks:eu-west-1:1:c/x",
		"":                            "",
	}
	for in, want := range cases {
		if got := parseYAMLScalar(in); got != want {
			t.Errorf("parseYAMLScalar(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPromptK8sFollowsKubectlsFirstWinsRule(t *testing.T) {
	overlayDir := t.TempDir()
	t.Setenv("CX_KUBE_DIR", overlayDir)

	overlay := filepath.Join(overlayDir, "1234.yaml")
	if err := os.WriteFile(overlay, []byte("current-context: 'prod'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(shared, []byte("current-context: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KUBECONFIG", overlay+string(os.PathListSeparator)+shared)
	name, unsafe := PromptK8s()
	if name != "prod" {
		t.Errorf("context = %q, want prod from the overlay that leads the path", name)
	}
	if unsafe {
		t.Error("a context decided by cx's own overlay is not shared")
	}

	// Without the overlay the shared file decides, and that is worth flagging.
	t.Setenv("KUBECONFIG", shared)
	name, unsafe = PromptK8s()
	if name != "dev" {
		t.Errorf("context = %q, want dev", name)
	}
	if !unsafe {
		t.Error("a context decided by the shared kubeconfig must be flagged")
	}
}

func TestPromptK8sSkipsFilesThatSayNothing(t *testing.T) {
	// A kubeconfig fragment with no current-context must not stop the search,
	// or a shell would appear to have no context at all.
	t.Setenv("CX_KUBE_DIR", t.TempDir())
	quiet := filepath.Join(t.TempDir(), "fragment")
	if err := os.WriteFile(quiet, []byte("kind: Config\nclusters: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(real, []byte("current-context: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", quiet+string(os.PathListSeparator)+real)

	if name, _ := PromptK8s(); name != "dev" {
		t.Errorf("context = %q, want dev from the second file", name)
	}
}
