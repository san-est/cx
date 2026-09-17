package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/san-est/cx/internal/cloud"
)

// gate is what the production check decides about a switch.
type gate int

const (
	// gateAllow: the target is not flagged, or consent was given with --yes.
	gateAllow gate = iota
	// gateAsk: flagged, and there is a terminal to put the question to.
	gateAsk
	// gateRefuse: flagged, no consent given, and nobody to ask.
	gateRefuse
)

// productionGate decides whether a switch to a flagged target may proceed.
//
// The policy is separate from the asking so it can be tested without a
// terminal. Feeding a reply through a pipe would not exercise the case that
// actually matters, which is having no terminal at all.
func productionGate(p cloud.Production, provider, name string, assumeYes, interactive bool) gate {
	if !p.Matches(provider, name) {
		return gateAllow
	}
	if assumeYes {
		return gateAllow
	}
	if !interactive {
		// The only safe answer: there is nobody to ask, and proceeding would
		// point a script at production on an assumption.
		return gateRefuse
	}
	return gateAsk
}

// interactiveStdin reports whether stdin is a terminal a question can be put to.
func interactiveStdin() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// askToSwitch puts the question and reports whether the answer was yes.
// Anything that is not an explicit yes is a no, including end of input.
func askToSwitch(in io.Reader, out io.Writer, what, name string) bool {
	fmt.Fprintf(out, "%s %s is marked production. Point this shell at it? [y/N] ", what, name)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// takeYesFlag removes the consent flag from args and reports whether it was
// given, so it can be written either side of the positional arguments.
func takeYesFlag(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	yes := false
	for _, a := range args {
		switch a {
		case "--yes", "-yes", "-y":
			yes = true
		default:
			out = append(out, a)
		}
	}
	return out, yes
}

// providerLabel names a provider the way the user would.
func providerLabel(provider string) string {
	if provider == "gcp" {
		return "gcloud configuration"
	}
	return "AWS profile"
}

// guardProduction applies the gate, and reports the exit code to use when the
// switch must not go ahead.
func guardProduction(provider, name string, assumeYes bool) (int, bool) {
	prod, err := cloud.LoadProduction()
	if err != nil {
		// A configuration cx cannot read must not silently disable the guard:
		// that would be the one failure mode this feature exists to prevent.
		fmt.Fprintln(os.Stderr, "cx:", err)
		return 1, false
	}

	what := providerLabel(provider)
	switch productionGate(prod, provider, name, assumeYes, interactiveStdin()) {
	case gateAllow:
		return 0, true
	case gateRefuse:
		fmt.Fprintf(os.Stderr,
			"cx: %s %s is marked production, and there is no terminal to confirm on.\n"+
				"    Pass --yes if this is deliberate.\n", what, name)
		return 1, false
	default:
		if askToSwitch(os.Stdin, os.Stderr, what, name) {
			return 0, true
		}
		fmt.Fprintln(os.Stderr, "cx: cancelled")
		return 1, false
	}
}
