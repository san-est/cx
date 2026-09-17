package cloud

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ConfigPath returns cx's own configuration file, which is separate from any
// vendor's. CX_CONFIG overrides it, chiefly so tests never read the file
// belonging to whoever is running them.
func ConfigPath() string {
	if p := os.Getenv("CX_CONFIG"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "cx", "config")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cx", "config")
}

// Production holds the name patterns a user has flagged as production:
//
//	[production]
//	aws = client-prod, *-prod
//	gcp = acme-prod, prod-*
//
// Matching is by name rather than by account or project id, because the
// prompt segment and `cx status --no-probe` must answer without touching the
// network, and for several credential kinds the account is known only after a
// probe. A name is what the user types, so it is also what a confirmation can
// meaningfully quote back at them.
type Production struct {
	AWS []string
	GCP []string
}

// LoadProduction reads the flagged patterns. A missing configuration file
// means nothing is flagged, which is not an error.
func LoadProduction() (Production, error) {
	cfg, err := parseINI(ConfigPath())
	if err != nil {
		return Production{}, err
	}
	p := Production{
		AWS: splitPatterns(cfg.get("production", "aws")),
		GCP: splitPatterns(cfg.get("production", "gcp")),
	}
	// A typo in a pattern silently protects nothing, which is the most
	// dangerous way for this feature to fail. Report it rather than ignoring
	// the line.
	for _, pat := range append(append([]string{}, p.AWS...), p.GCP...) {
		if _, err := path.Match(pat, ""); err != nil {
			return p, fmt.Errorf("production pattern %q in %s: %w", pat, ConfigPath(), err)
		}
	}
	return p, nil
}

// splitPatterns turns a comma-separated list into trimmed entries, dropping
// empties so a trailing comma is harmless.
func splitPatterns(v string) []string {
	var out []string
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Patterns returns the patterns for a provider, "aws" or "gcp".
func (p Production) Patterns(provider string) []string {
	if provider == "gcp" {
		return p.GCP
	}
	return p.AWS
}

// Matches reports whether a target name is flagged as production.
//
// path.Match rather than filepath.Match: the subject is a target name, not a
// path, and filepath.Match gives the separator a meaning on some platforms
// that has no bearing here.
func (p Production) Matches(provider, name string) bool {
	for _, pat := range p.Patterns(provider) {
		if ok, err := path.Match(pat, name); ok && err == nil {
			return true
		}
	}
	return false
}

// MarkProduction sets Sensitive on every target whose name is flagged.
//
// It is applied after loading rather than inside it, so the loaders stay
// concerned only with what the vendor's files say.
func MarkProduction(targets []Target, p Production, provider string) {
	for i := range targets {
		targets[i].Sensitive = p.Matches(provider, targets[i].Name)
	}
}
