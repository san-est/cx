package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ActiveSource records how the current gcloud configuration was selected. The
// distinction is the whole reason this tool exists: an env-var selection is
// private to one shell, while the on-disk selection is shared by every shell on
// the machine and changes under you when another terminal switches.
type ActiveSource int

const (
	// SourceNone means no configuration is selected at all.
	SourceNone ActiveSource = iota
	// SourceShell means CLOUDSDK_ACTIVE_CONFIG_NAME is set: safe, shell-local.
	SourceShell
	// SourceGlobal means the selection comes from ~/.config/gcloud/active_config,
	// which is machine-wide mutable state.
	SourceGlobal
)

func (s ActiveSource) String() string {
	switch s {
	case SourceShell:
		return "shell-local"
	case SourceGlobal:
		return "MACHINE-GLOBAL"
	default:
		return "none"
	}
}

// GCPState describes how the current shell resolves gcloud configuration, and
// where that resolution disagrees with the machine-wide default.
type GCPState struct {
	// Active is the configuration name in effect for this shell.
	Active string
	// Source is how Active was chosen.
	Source ActiveSource
	// Global is the machine-wide selection from the active_config file.
	Global string
	// GlobalMissing reports that active_config names a configuration that does
	// not exist, which makes gcloud silently fall back to built-in defaults.
	GlobalMissing bool
	// ProjectOverride and AccountOverride hold CLOUDSDK_CORE_* values, which
	// beat the configuration's own properties and are easy to forget about.
	ProjectOverride string
	AccountOverride string
}

// Unsafe reports whether the current shell's project is determined by state
// that another terminal can change without warning.
func (s GCPState) Unsafe() bool { return s.Source == SourceGlobal }

func gcloudDir() string {
	if p := os.Getenv("CLOUDSDK_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gcloud")
}

// LoadGCP reads gcloud configurations straight from disk. It deliberately does
// not invoke the gcloud CLI: gcloud is a Python program that takes on the order
// of a second to start, which makes a per-configuration shell loop painfully
// slow, and its config files are a stable documented format.
func LoadGCP() ([]Target, GCPState, error) {
	dir := gcloudDir()
	state := GCPState{
		ProjectOverride: os.Getenv("CLOUDSDK_CORE_PROJECT"),
		AccountOverride: os.Getenv("CLOUDSDK_CORE_ACCOUNT"),
	}

	// Machine-wide selection.
	if b, err := os.ReadFile(filepath.Join(dir, "active_config")); err == nil {
		state.Global = strings.TrimSpace(string(b))
	}

	// Shell-local selection wins, and is the safe one.
	if v := strings.TrimSpace(os.Getenv("CLOUDSDK_ACTIVE_CONFIG_NAME")); v != "" {
		state.Active, state.Source = v, SourceShell
	} else if state.Global != "" {
		state.Active, state.Source = state.Global, SourceGlobal
	}

	entries, err := os.ReadDir(filepath.Join(dir, "configurations"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, state, nil
		}
		return nil, state, err
	}

	known := map[string]bool{}
	var out []Target
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "config_") {
			continue
		}
		name := strings.TrimPrefix(e.Name(), "config_")
		known[name] = true

		props, err := parseINI(filepath.Join(dir, "configurations", e.Name()))
		if err != nil {
			continue
		}
		account := props.get("core", "account")
		project := props.get("core", "project")

		t := Target{
			Name:            name,
			Kind:            KindUser,
			Account:         account,
			Scope:           project,
			ConfiguredScope: project,
			Active:          name == state.Active,
			Health:          Unknown,
		}
		if account == "" {
			t.Kind = KindNone
		}
		// Env overrides apply only to whichever configuration is active, and
		// silently replace its stored properties.
		if t.Active {
			if state.ProjectOverride != "" {
				t.Scope = state.ProjectOverride
			}
			if state.AccountOverride != "" {
				t.Account = state.AccountOverride
			}
		}
		out = append(out, t)
	}

	if state.Global != "" && !known[state.Global] {
		state.GlobalMissing = true
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, state, nil
}

// ProbeGCP checks whether each configuration's account still holds a usable
// refresh token, concurrently.
func ProbeGCP(ctx context.Context, targets []Target, concurrency int) {
	if concurrency < 1 {
		concurrency = 6
	}
	if _, err := exec.LookPath("gcloud"); err != nil {
		for i := range targets {
			targets[i].Health = Missing
			targets[i].Detail = "gcloud CLI not found on PATH"
			targets[i].ProbedAt = time.Now()
		}
		return
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range targets {
		if targets[i].Account == "" {
			targets[i].Health = Missing
			targets[i].Detail = "no account set on this configuration"
			targets[i].ProbedAt = time.Now()
			continue
		}
		wg.Add(1)
		go func(t *Target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()

			cmd := exec.CommandContext(c, "gcloud", "auth", "print-access-token", "--account="+t.Account)
			cmd.Stdin = nil
			var stderr strings.Builder
			cmd.Stderr = &stderr

			t.ProbedAt = time.Now()
			if err := cmd.Run(); err != nil {
				t.Health = Expired
				t.Detail = summarizeGCPError(stderr.String(), c.Err())
				return
			}
			t.Health = Valid
			t.Identity = t.Account
			t.Detail = ""
		}(&targets[i])
	}
	wg.Wait()
}

func summarizeGCPError(stderr string, ctxErr error) string {
	if ctxErr != nil {
		return "timed out"
	}
	s := stderr
	switch {
	case strings.Contains(s, "does not have valid credentials"),
		strings.Contains(s, "Your current active account"),
		strings.Contains(s, "do not have valid credentials"):
		return "not logged in - run: gcloud auth login"
	case strings.Contains(s, "reauth"), strings.Contains(s, "invalid_grant"):
		return "refresh token rejected - re-auth required"
	case strings.Contains(s, "network"), strings.Contains(s, "Connection"):
		return "network unreachable"
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return "no valid token"
	}
	if len(last) > 70 {
		last = last[:70] + "..."
	}
	return last
}

// ADC describes Application Default Credentials: the credential set used by
// Terraform, the Google client libraries, and essentially everything that is
// not the gcloud binary itself.
//
// ADC is entirely independent of the active gcloud configuration. A shell can
// have `gcloud config` pointing at a staging project while ADC authenticates as
// a different principal entirely, and nothing in the standard tooling shows
// both at once.
//
// Note what ADC does and does not decide. It supplies the identity Terraform
// and the client libraries authenticate as. Its quota project decides who is
// billed for API calls made by the client libraries -- but not by Terraform,
// whose google provider has its own billing_project and does not read the ADC
// file's quota project at all.
type ADC struct {
	// Present reports whether an ADC file exists.
	Present bool
	// Path is where it was found.
	Path string
	// Kind is the credential type recorded in the file.
	Kind CredKind
	// QuotaProject is the project billed for API calls made with ADC. When it
	// disagrees with the active gcloud project, that is worth flagging.
	QuotaProject string
	// Identity is the resolved principal.
	Identity string
	// Health is the probe result.
	Health Health
	// Detail explains a non-Valid health.
	Detail string
}

type adcFile struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	QuotaProject string `json:"quota_project_id"`
}

func adcPath() string {
	if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		return p
	}
	return filepath.Join(gcloudDir(), "application_default_credentials.json")
}

// LoadADC reads the ADC file without contacting the network.
func LoadADC() ADC {
	p := adcPath()
	a := ADC{Path: p, Kind: KindNone, Health: Missing}

	b, err := os.ReadFile(p)
	if err != nil {
		a.Detail = "not configured - run: gcloud auth application-default login"
		return a
	}
	a.Present = true

	var f adcFile
	if err := json.Unmarshal(b, &f); err != nil {
		a.Detail = "ADC file is not valid JSON"
		return a
	}
	a.QuotaProject = f.QuotaProject
	switch f.Type {
	case "service_account":
		a.Kind = KindServiceAcc
		// A service account key carries its own identity, so no probe needed.
		a.Identity = f.ClientEmail
	case "authorized_user":
		a.Kind = KindUser
	case "external_account", "impersonated_service_account":
		a.Kind = KindProcess
	}
	a.Health = Unknown
	return a
}

// ProbeADC resolves who ADC actually authenticates as. For a user credential
// the file holds only a refresh token, so the identity can be discovered only
// by minting an access token and asking Google who it belongs to.
func ProbeADC(ctx context.Context, a *ADC) {
	if !a.Present {
		return
	}
	if a.Kind == KindServiceAcc && a.Identity != "" {
		a.Health = Valid
		return
	}
	if _, err := exec.LookPath("gcloud"); err != nil {
		a.Health = Missing
		a.Detail = "gcloud CLI not found on PATH"
		return
	}

	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(c, "gcloud", "auth", "application-default", "print-access-token")
	cmd.Stdin = nil
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		a.Health = Expired
		a.Detail = summarizeGCPError(stderr.String(), c.Err())
		return
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		a.Health = Expired
		a.Detail = "empty access token"
		return
	}

	email, err := userinfoEmail(c, token)
	if err != nil {
		// The token minted successfully, so ADC works even if we could not
		// attach a name to it. Report that honestly rather than as a failure.
		a.Health = Valid
		a.Detail = "token valid; identity lookup failed"
		return
	}
	a.Health = Valid
	a.Identity = email
	a.Detail = ""
}

// userinfoEmail asks Google which principal a token belongs to. The token is
// sent in an Authorization header rather than the tokeninfo query-string
// endpoint, so it does not end up in any URL log.
func userinfoEmail(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://www.googleapis.com/oauth2/v3/userinfo", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Email, nil
}
