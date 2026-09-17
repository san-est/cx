// Command cx shows and switches the cloud context a shell is pointed at:
// AWS profiles, gcloud configurations, and Application Default Credentials.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/san-est/cx/internal/cloud"
	"github.com/san-est/cx/internal/shellcfg"
	"github.com/san-est/cx/internal/ui"
)

const usage = `cx - cloud context

  cx                      dashboard
  cx status [--no-probe]  one-shot report; exits 2 if the shell can be misdirected
  cx use aws <profile>    point this shell at an AWS profile
  cx use gcp <config>     point this shell at a gcloud configuration
                          --yes confirms a target marked production
  cx clear [aws|gcp|all]  drop this shell's overrides
  cx prompt [--warn]      compact status for a shell prompt (no network)
                          --warn prints only hazards, nothing when clean
  cx shell-init [shell]   print the shell wrapper (zsh or bash); add to your rc file
  cx version              print the version, revision, and platform

Switching requires the shell wrapper. Add this to ~/.zshrc:

  eval "$(cx shell-init zsh)"

or to ~/.bashrc:

  eval "$(cx shell-init bash)"
`

func main() {
	args := os.Args[1:]

	// Accept the flag spellings as aliases so old habits keep working.
	if len(args) > 0 && (args[0] == "--plain" || args[0] == "-plain") {
		args[0] = "status"
	}

	if len(args) == 0 {
		os.Exit(runDashboard())
	}

	switch args[0] {
	case "status":
		probe := true
		for _, a := range args[1:] {
			if a == "--no-probe" || a == "-no-probe" {
				probe = false
			}
		}
		os.Exit(runStatus(probe))
	case "use":
		os.Exit(runUse(args[1:]))
	case "clear":
		os.Exit(runClear(args[1:]))
	case "prompt":
		warnOnly := false
		for _, a := range args[1:] {
			if a == "--warn" || a == "-warn" {
				warnOnly = true
			}
		}
		os.Exit(runPrompt(warnOnly))
	case "shell-init":
		os.Exit(runShellInit(args[1:]))
	case "version", "--version", "-version":
		os.Exit(runVersion())
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "cx: unknown command %q\n\n%s", args[0], usage)
		os.Exit(1)
	}
}

// shellOutPath is where the wrapper function expects environment changes to be
// written. Its absence means cx was run directly, without the wrapper, and so
// cannot affect the calling shell.
func shellOutPath() string { return os.Getenv("CX_SHELL_OUT") }

// applyScript hands environment changes back to the calling shell. Without the
// wrapper installed there is nowhere to write them, so say so plainly rather
// than appearing to succeed.
func applyScript(s *shellcfg.Script) int {
	if s.Empty() {
		return 0
	}
	out := shellOutPath()
	if out == "" {
		fmt.Fprintln(os.Stderr,
			"cx: the shell wrapper is not installed, so this shell cannot be changed.\n"+
				"    Add this to ~/.zshrc and open a new shell:\n\n"+
				"      eval \"$(cx shell-init zsh)\"")
		return 1
	}
	if err := os.WriteFile(out, []byte(s.String()), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "cx: writing shell output:", err)
		return 1
	}
	return 0
}

func runDashboard() int {
	m, err := tea.NewProgram(ui.New(shellOutPath()), tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cx:", err)
		return 1
	}
	if fm, ok := m.(ui.Model); ok {
		if s := fm.PendingScript(); s != nil {
			return applyScript(s)
		}
	}
	return 0
}

func runUse(args []string) int {
	args, assumeYes := takeYesFlag(args)
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "cx: usage: cx use <aws|gcp> <name> [--yes]")
		return 1
	}
	provider, name := args[0], args[1]

	switch provider {
	case "aws":
		targets, err := cloud.LoadAWS()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cx:", err)
			return 1
		}
		if !hasTarget(targets, name) {
			fmt.Fprintf(os.Stderr, "cx: no AWS profile %q\n", name)
			listNames(targets)
			return 1
		}
		if code, ok := guardProduction(provider, name, assumeYes); !ok {
			return code
		}
		return applyScript(cloud.SwitchAWS(name))

	case "gcp":
		targets, _, err := cloud.LoadGCP()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cx:", err)
			return 1
		}
		if !hasTarget(targets, name) {
			fmt.Fprintf(os.Stderr, "cx: no gcloud configuration %q\n", name)
			listNames(targets)
			return 1
		}
		if code, ok := guardProduction(provider, name, assumeYes); !ok {
			return code
		}
		return applyScript(cloud.SwitchGCP(name))

	default:
		fmt.Fprintf(os.Stderr, "cx: unknown provider %q (want aws or gcp)\n", provider)
		return 1
	}
}

func runClear(args []string) int {
	what := "all"
	if len(args) > 0 {
		what = args[0]
	}
	switch what {
	case "aws", "gcp", "all":
		return applyScript(cloud.Clear(what))
	default:
		fmt.Fprintf(os.Stderr, "cx: unknown scope %q (want aws, gcp, or all)\n", what)
		return 1
	}
}

// runPrompt writes a compact one-line summary for a shell prompt. It touches
// only local files, because it runs before every prompt and must never stall a
// terminal on a slow network.
//
// In warn mode it prints only hazards, and nothing at all when everything is
// fine. That suits a prompt that already shows the current target by other
// means -- starship's own aws and gcloud modules, for instance -- where the
// only thing left to add is the warning.
func runPrompt(warnOnly bool) int {
	gcp, state, err := cloud.LoadGCP()
	if err != nil {
		return 0
	}

	if warnOnly {
		warnings := cloud.ShortWarnings(gcp, state, cloud.LoadADC())
		if len(warnings) == 0 {
			// No output and a non-zero exit, so a prompt can use this as its
			// own "should I render anything?" test.
			return 1
		}
		fmt.Println(strings.Join(warnings, " "))
		return 0
	}

	// An unreadable configuration leaves prod zero-valued, which flags nothing.
	// A prompt is the wrong place to report that, and cx status already does.
	prod, _ := cloud.LoadProduction()

	var parts []string
	if aws, err := cloud.LoadAWS(); err == nil {
		for _, t := range aws {
			if t.Active {
				parts = append(parts, "aws:"+t.Name+prodTag(prod.Matches("aws", t.Name)))
				break
			}
		}
	}
	if state.Active != "" {
		// The segment shows the project, since that is the blast radius, but
		// the production flag is matched on the configuration name the user
		// wrote in their cx configuration.
		seg := "gcp:" + state.Active
		for _, t := range gcp {
			if t.Active && t.Scope != "" {
				seg = "gcp:" + t.Scope
			}
		}
		seg += prodTag(prod.Matches("gcp", state.Active))
		// A trailing mark means this shell's target is machine-global: another
		// terminal can change it. That is the whole reason to show this.
		if state.Unsafe() || state.GlobalMissing {
			seg += "!"
		}
		parts = append(parts, seg)
	}

	if len(parts) > 0 {
		fmt.Println(strings.Join(parts, " "))
	}
	return 0
}

func runShellInit(args []string) int {
	shell := "zsh"
	if len(args) > 0 {
		shell = args[0]
	}
	switch shell {
	case "zsh", "bash":
		os.Stdout.WriteString(strings.Replace(shellInit, autoPinPlaceholder, autoPinBlock(), 1))
		return 0
	default:
		fmt.Fprintf(os.Stderr, "cx: unsupported shell %q (want zsh or bash)\n", shell)
		return 1
	}
}

// autoPinBlock returns shell code pinning this shell to the gcloud
// configuration selected right now, or nothing when there is none to pin to.
func autoPinBlock() string {
	_, state, err := cloud.LoadGCP()
	if err != nil || state.Global == "" || state.GlobalMissing {
		return ""
	}
	return strings.Replace(autoPinTemplate, configPlaceholder, shellcfg.Quote(state.Global), 1)
}

func runStatus(probe bool) int {
	ctx := context.Background()

	aws, err := cloud.LoadAWS()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cx: reading AWS config:", err)
	}
	gcp, state, err := cloud.LoadGCP()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cx: reading gcloud config:", err)
	}
	adc := cloud.LoadADC()

	// A configuration cx cannot read must be reported, not silently treated as
	// "nothing is production".
	prod, err := cloud.LoadProduction()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cx:", err)
	}
	cloud.MarkProduction(aws, prod, "aws")
	cloud.MarkProduction(gcp, prod, "gcp")

	if probe {
		cloud.ProbeAWS(ctx, aws, 8)
		cloud.ProbeGCP(ctx, gcp, 6)
		cloud.ProbeADC(ctx, &adc)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "AWS\tKIND\tACCOUNT\tREGION\tSTATUS")
	if len(aws) == 0 {
		fmt.Fprintln(w, "(none)\t\t\t\t")
	}
	for _, t := range aws {
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\t%s\n",
			activeMark(t.Active), targetName(t), t.Kind, dash(t.Account), dash(t.Scope), status(t.Health, t.Detail))
	}

	fmt.Fprintln(w, "\nGCP\tACCOUNT\tPROJECT\tSTATUS")
	if len(gcp) == 0 {
		fmt.Fprintln(w, "(none)\t\t\t")
	}
	for _, t := range gcp {
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\n",
			activeMark(t.Active), targetName(t), dash(t.Account), dash(t.Scope), status(t.Health, t.Detail))
	}

	fmt.Fprintln(w, "\nADC\tQUOTA PROJECT\tSTATUS")
	fmt.Fprintf(w, "%s\t%s\t%s\n", dash(adc.Identity), dash(adc.QuotaProject), status(adc.Health, adc.Detail))
	w.Flush()

	fmt.Printf("\ngcloud selection: %s", dash(state.Active))
	if state.Active != "" {
		fmt.Printf(" (%s)", state.Source)
	}
	fmt.Println()

	alerts := cloud.Audit(gcp, state, adc)
	for _, a := range alerts {
		mark := "WARN "
		if a.Severity == cloud.Danger {
			mark = "DANGER "
		}
		fmt.Fprintf(os.Stderr, "\n%s%s\n  %s\n", mark, a.Title, a.Fix)
	}

	// A non-zero exit lets this gate a script: refuse to apply while the shell
	// could be pointed somewhere other than where its operator believes.
	if cloud.HasDanger(alerts) {
		return 2
	}
	return 0
}

func hasTarget(targets []cloud.Target, name string) bool {
	for _, t := range targets {
		if t.Name == name {
			return true
		}
	}
	return false
}

func listNames(targets []cloud.Target) {
	if len(targets) == 0 {
		return
	}
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.Name)
	}
	fmt.Fprintln(os.Stderr, "available:", strings.Join(names, ", "))
}

// targetName renders a name with its production flag, for the plain report.
func targetName(t cloud.Target) string {
	return t.Name + prodTag(t.Sensitive)
}

// prodTag marks a target the user has flagged as production. Deliberately not
// the "!" used for a machine-global gcloud target: they are different problems
// and a reader should not have to work out which one a mark means.
func prodTag(sensitive bool) string {
	if sensitive {
		return "[prod]"
	}
	return ""
}

func activeMark(active bool) string {
	if active {
		return "* "
	}
	return "  "
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func status(h cloud.Health, detail string) string {
	if detail != "" {
		return detail
	}
	return h.String()
}
