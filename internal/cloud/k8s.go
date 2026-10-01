package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/san-est/cx/internal/shellcfg"
)

// K8sState describes how this shell resolves its Kubernetes context, and
// whether that resolution is one another terminal can change.
type K8sState struct {
	// Active is the context in effect for this shell.
	Active string
	// Source is how Active was chosen.
	Source ActiveSource
	// Shared is the context selected in the kubeconfig every terminal reads.
	Shared string
	// Available reports whether the context could be read at all.
	Available bool
	// Detail explains a false Available, for the pane to show instead of rows.
	Detail string
}

// Unsafe reports whether this shell's context comes from a file another
// terminal can rewrite. Identical in spirit to GCPState.Unsafe: the hazard is
// shared mutable selection, not anything specific to gcloud.
func (s K8sState) Unsafe() bool { return s.Active != "" && s.Source == SourceGlobal }

// kubeOverlayDir is where cx keeps per-shell kubeconfig overlays.
func kubeOverlayDir() string {
	if p := os.Getenv("CX_KUBE_DIR"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "cx", "kube")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "cx", "kube")
}

// kubeconfigFiles returns the kubeconfig search path, in precedence order.
func kubeconfigFiles() []string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		var out []string
		for _, p := range filepath.SplitList(v) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	home, _ := os.UserHomeDir()
	return []string{filepath.Join(home, ".kube", "config")}
}

// kubeConfigView is the subset of `kubectl config view -o json` cx reads.
// Credentials are deliberately absent: kubectl redacts them, and cx has no use
// for them.
type kubeConfigView struct {
	CurrentContext string `json:"current-context"`
	Contexts       []struct {
		Name    string `json:"name"`
		Context struct {
			Cluster   string `json:"cluster"`
			User      string `json:"user"`
			Namespace string `json:"namespace"`
		} `json:"context"`
	} `json:"contexts"`
	Clusters []struct {
		Name string `json:"name"`
	} `json:"clusters"`
	Users []struct {
		Name string `json:"name"`
	} `json:"users"`
}

// LoadK8s reads the Kubernetes contexts available to this shell.
//
// Unlike the AWS and gcloud loaders this shells out, which is a deliberate
// exception to the rule that reading configuration does not. kubeconfig is
// YAML, and the alternatives are worse than the subprocess: a YAML dependency
// would be a third-party parser running against a file that holds client
// certificates and bearer tokens, and a hand-rolled one would be a YAML subset
// parser, which is a bug factory. `kubectl config view -o json` costs about
// 28ms here against gcloud's 400ms, it is a Go binary rather than a Python
// one, and it redacts every credential before cx ever sees the bytes. The
// prompt segment does not call this; see PromptK8s.
func LoadK8s() ([]Target, K8sState, error) {
	state := K8sState{Source: SourceNone}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "config", "view", "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		// Not having kubectl is an ordinary state, not a failure: most shells
		// on most machines have no Kubernetes at all.
		if errors.Is(err, exec.ErrNotFound) {
			state.Detail = "kubectl not found"
			return nil, state, nil
		}
		if ctx.Err() != nil {
			state.Detail = "kubectl timed out"
			return nil, state, nil
		}
		state.Detail = "kubectl config view failed"
		return nil, state, nil
	}

	var view kubeConfigView
	if err := json.Unmarshal(out, &view); err != nil {
		state.Detail = "could not parse kubectl output"
		return nil, state, nil
	}

	state.Available = true
	state.Active = view.CurrentContext
	state.Shared = sharedKubeContext()
	if state.Active != "" {
		state.Source = SourceGlobal
		if shellPinnedKubeconfig() {
			state.Source = SourceShell
		}
	}

	known := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}
	var clusters, users []string
	for _, c := range view.Clusters {
		clusters = append(clusters, c.Name)
	}
	for _, u := range view.Users {
		users = append(users, u.Name)
	}

	var targets []Target
	for _, c := range view.Contexts {
		namespace := c.Context.Namespace
		if namespace == "" {
			// kubectl's own default when a context omits it.
			namespace = "default"
		}
		t := Target{
			Name:    c.Name,
			Kind:    KindContext,
			Account: c.Context.Cluster,
			Scope:   namespace,
			Active:  c.Name == view.CurrentContext,
			Health:  Valid,
		}
		// A context naming a cluster or user that no longer exists fails only
		// when a command is run, which is the wrong moment to find out.
		switch {
		case c.Context.Cluster == "" || !known(clusters, c.Context.Cluster):
			t.Health, t.Detail = Missing, "cluster not defined in kubeconfig"
		case c.Context.User == "" || !known(users, c.Context.User):
			t.Health, t.Detail = Missing, "user not defined in kubeconfig"
		}
		targets = append(targets, t)
	}
	return targets, state, nil
}

// shellPinnedKubeconfig reports whether the highest-precedence kubeconfig is
// an overlay cx wrote for this shell. That file is what makes the selection
// shell-local, so its presence at the front is the whole test.
func shellPinnedKubeconfig() bool {
	files := kubeconfigFiles()
	return len(files) > 0 && insideOverlayDir(files[0])
}

// insideOverlayDir reports whether a kubeconfig path is one of cx's overlays.
func insideOverlayDir(p string) bool {
	rel, err := filepath.Rel(kubeOverlayDir(), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// kubeOverlayPath is this shell's overlay file. The shell's own process id
// names it, so every terminal gets its own and none inherits another's pin.
func kubeOverlayPath() string {
	id := os.Getenv("CX_SHELL_ID")
	if id == "" {
		// cx is run by the wrapper function, so the parent is the shell.
		id = strconv.Itoa(os.Getppid())
	}
	return filepath.Join(kubeOverlayDir(), id+".yaml")
}

// SwitchK8s points this shell, and only this shell, at a Kubernetes context.
//
// It writes a kubeconfig carrying nothing but current-context and puts it at
// the front of KUBECONFIG. kubectl merges the files on the search path and
// takes current-context from the first that sets one, so this shell is
// retargeted while the shared kubeconfig is never written to.
//
// Copying the whole kubeconfig is the obvious alternative and a worse one: the
// copy goes stale the moment a cluster is added, and it duplicates every bearer
// token and client certificate into a second file on disk. The overlay holds
// one context name and no credentials at all.
//
// It also makes `kubectl config use-context` shell-local as a side effect,
// because kubectl writes current-context to the first file on the search path,
// which is now this one. The habit cx is trying to make safe stops being
// dangerous, rather than being reported forever.
func SwitchK8s(contextName string) (*shellcfg.Script, error) {
	path, err := writeKubeOverlay(contextName)
	if err != nil {
		return nil, err
	}
	s := shellcfg.New()
	s.Export("KUBECONFIG", kubeconfigWith(path))
	s.Note("cx: kubernetes context -> %s (this shell only)", contextName)
	return s, nil
}

// writeKubeOverlay writes this shell's overlay and returns its path.
func writeKubeOverlay(contextName string) (string, error) {
	path := kubeOverlayPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := writeLines(path, kubeOverlayLines(contextName)); err != nil {
		return "", err
	}
	return path, nil
}

// kubeOverlayLines renders the overlay document.
func kubeOverlayLines(contextName string) []string {
	return []string{
		"# Written by cx. Points this shell, and only this shell, at one context.",
		"apiVersion: v1",
		"kind: Config",
		"current-context: " + yamlQuote(contextName),
	}
}

// yamlQuote renders a scalar as a single-quoted YAML string, whose only escape
// is a doubled quote. Context names come from a file on disk, and one holding a
// colon, a leading asterisk or a '#' would otherwise change what the document
// means.
func yamlQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// kubeconfigWith puts overlay at the front of the search path, dropping any
// overlay cx put there before so repeated switches do not accumulate entries.
func kubeconfigWith(overlay string) string {
	out := []string{overlay}
	for _, p := range kubeconfigFiles() {
		if insideOverlayDir(p) {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// sharedKubeContext reads current-context from the last kubeconfig in the
// search path, which is the shared file every terminal sees.
//
// It scans for the key rather than parsing YAML. The value is a plain scalar at
// the top level of a machine-generated file, and this is only ever used to say
// what the other terminals are pointed at -- never to decide what this shell
// resolves to, which kubectl itself reports.
func sharedKubeContext() string {
	files := kubeconfigFiles()
	if len(files) == 0 {
		return ""
	}
	b, err := os.ReadFile(files[len(files)-1])
	if err != nil {
		return ""
	}
	return sharedKubeContextFromBytes(b)
}

// sharedKubeContextFromBytes is the scan itself, separated so it can be tested
// against documents without writing each one to disk.
func sharedKubeContextFromBytes(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "current-context:")
		if !ok {
			continue
		}
		return parseYAMLScalar(rest)
	}
	return ""
}

// parseYAMLScalar reads a scalar the way YAML does: a single-quoted string
// escapes a quote by doubling it, a double-quoted one by backslash, and '#'
// only begins a comment outside quotes.
//
// Naively trimming quotes and cutting at the first '#' gets both wrong, and
// context names are not always plain: an EKS context is a full ARN, and tools
// quote values that start with punctuation.
func parseYAMLScalar(v string) string {
	v = strings.TrimSpace(v)

	switch {
	case strings.HasPrefix(v, "'"):
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			if v[i] == '\'' {
				if i+1 < len(v) && v[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				break
			}
			b.WriteByte(v[i])
		}
		return b.String()

	case strings.HasPrefix(v, `"`):
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			if v[i] == '\\' && i+1 < len(v) {
				i++
				b.WriteByte(v[i])
				continue
			}
			if v[i] == '"' {
				break
			}
			b.WriteByte(v[i])
		}
		return b.String()
	}

	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// PromptK8s resolves this shell's context cheaply enough to run before every
// prompt: one small file read and no subprocess.
//
// LoadK8s costs a kubectl invocation, which is fine for the dashboard and far
// too slow here. The merge rule it reproduces is kubectl's own: current-context
// comes from the first file on the search path that sets one. unsafe reports
// that the file which decided it is shared with every other terminal.
func PromptK8s() (name string, unsafe bool) {
	for _, f := range kubeconfigFiles() {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if v := sharedKubeContextFromBytes(b); v != "" {
			return v, !insideOverlayDir(f)
		}
	}
	return "", false
}
