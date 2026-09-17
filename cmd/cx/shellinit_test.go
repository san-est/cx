package main

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// controlledVars are set by shell-init itself. A developer running these tests
// from a shell that has cx installed inherits them, which would otherwise mask
// the very behaviour under test.
var controlledVars = []string{"CLOUDSDK_ACTIVE_CONFIG_NAME", "CX_NO_AUTOPIN"}

// cleanEnviron is the caller's environment minus the variables these tests set
// themselves, so the result does not depend on the machine running them.
func cleanEnviron() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(controlledVars, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runShellSnippet executes shell code and reports the resulting value of one
// variable, so the emitted script is checked by running it rather than by
// eyeballing the text.
func runShellSnippet(t *testing.T, snippet, env, variable string) string {
	t.Helper()
	script := snippet + "\nprintf '%s' \"$" + variable + "\"\n"
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = cleanEnviron()
	if env != "" {
		cmd.Env = append(cmd.Env, env)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running snippet: %v", err)
	}
	return string(out)
}

func TestAutoPinAssignsABareValue(t *testing.T) {
	// Regression: an earlier version used "${VAR:=value}" inside double quotes,
	// which made the surrounding quote characters part of the value and pinned
	// the shell to a configuration name that could not exist.
	snippet := strings.Replace(autoPinTemplate, configPlaceholder, "'default'", 1)

	got := runShellSnippet(t, snippet, "", "CLOUDSDK_ACTIVE_CONFIG_NAME")
	if got != "default" {
		t.Errorf("pinned value = %q, want %q (quote characters leaked into it)", got, "default")
	}
}

func TestAutoPinDoesNotOverrideAnInheritedValue(t *testing.T) {
	// An explicit `cx use gcp`, or a value inherited by a subshell, must win.
	snippet := strings.Replace(autoPinTemplate, configPlaceholder, "'default'", 1)

	got := runShellSnippet(t, snippet, "CLOUDSDK_ACTIVE_CONFIG_NAME=chosen", "CLOUDSDK_ACTIVE_CONFIG_NAME")
	if got != "chosen" {
		t.Errorf("pinned value = %q, want the inherited %q", got, "chosen")
	}
}

func TestAutoPinRespectsOptOut(t *testing.T) {
	snippet := strings.Replace(autoPinTemplate, configPlaceholder, "'default'", 1)

	got := runShellSnippet(t, snippet, "CX_NO_AUTOPIN=1", "CLOUDSDK_ACTIVE_CONFIG_NAME")
	if got != "" {
		t.Errorf("pinned value = %q, want empty when opted out", got)
	}
}

func TestAutoPinQuotesAHostileName(t *testing.T) {
	// Configuration names come from directory entries on disk.
	snippet := strings.Replace(autoPinTemplate, configPlaceholder, `'a b; touch /tmp/cx-pwned'`, 1)

	got := runShellSnippet(t, snippet, "", "CLOUDSDK_ACTIVE_CONFIG_NAME")
	if got != "a b; touch /tmp/cx-pwned" {
		t.Errorf("value = %q; the name must survive intact and unexecuted", got)
	}
}
