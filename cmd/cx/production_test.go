package main

import (
	"strings"
	"testing"

	"github.com/san-est/cx/internal/cloud"
)

func flagged() cloud.Production {
	return cloud.Production{AWS: []string{"*-prod"}, GCP: []string{"acme-prod"}}
}

func TestProductionGateLetsUnflaggedTargetsThrough(t *testing.T) {
	// Almost every switch is this one. It must not prompt, and it must not
	// depend on there being a terminal.
	if got := productionGate(flagged(), "aws", "staging", false, false); got != gateAllow {
		t.Errorf("gate = %v, want gateAllow for an unflagged target", got)
	}
}

func TestProductionGateAsksWhenThereIsATerminal(t *testing.T) {
	if got := productionGate(flagged(), "aws", "client-prod", false, true); got != gateAsk {
		t.Errorf("gate = %v, want gateAsk for a flagged target with a terminal", got)
	}
}

func TestProductionGateRefusesWithNoTerminalAndNoConsent(t *testing.T) {
	// The case the whole feature turns on: a script must not be pointed at
	// production because nobody was there to say no.
	if got := productionGate(flagged(), "aws", "client-prod", false, false); got != gateRefuse {
		t.Errorf("gate = %v, want gateRefuse with no terminal and no --yes", got)
	}
}

func TestProductionGateAcceptsExplicitConsentWithoutATerminal(t *testing.T) {
	// A deployment script that means it says so, and is not made to hang on a
	// prompt that cannot be answered.
	if got := productionGate(flagged(), "aws", "client-prod", true, false); got != gateAllow {
		t.Errorf("gate = %v, want gateAllow when --yes is given", got)
	}
}

func TestProductionGateKeepsProvidersApart(t *testing.T) {
	// An AWS pattern must not gate a gcloud configuration of the same name.
	if got := productionGate(flagged(), "gcp", "client-prod", false, true); got != gateAllow {
		t.Errorf("gate = %v, want gateAllow — the pattern belongs to AWS", got)
	}
	if got := productionGate(flagged(), "gcp", "acme-prod", false, true); got != gateAsk {
		t.Errorf("gate = %v, want gateAsk for the gcloud pattern", got)
	}
}

func TestAskToSwitchAcceptsOnlyAnExplicitYes(t *testing.T) {
	for _, reply := range []string{"y\n", "Y\n", "yes\n", "YES\n", " yes \n"} {
		var out strings.Builder
		if !askToSwitch(strings.NewReader(reply), &out, "AWS profile", "client-prod") {
			t.Errorf("reply %q should have been accepted", reply)
		}
	}
	for _, reply := range []string{"n\n", "no\n", "\n", "yep\n", "ye\n", "", "q\n"} {
		var out strings.Builder
		if askToSwitch(strings.NewReader(reply), &out, "AWS profile", "client-prod") {
			t.Errorf("reply %q should have been refused", reply)
		}
	}
}

func TestAskToSwitchNamesTheTargetInTheQuestion(t *testing.T) {
	// A prompt that does not say what it is about trains people to hit enter.
	var out strings.Builder
	askToSwitch(strings.NewReader("n\n"), &out, "AWS profile", "client-prod")

	if !strings.Contains(out.String(), "client-prod") {
		t.Errorf("prompt = %q, want the target named", out.String())
	}
	if !strings.Contains(out.String(), "production") {
		t.Errorf("prompt = %q, want it to say why it is asking", out.String())
	}
}

func TestTakeYesFlagAcceptsItEitherSideOfTheArguments(t *testing.T) {
	for _, args := range [][]string{
		{"aws", "client-prod", "--yes"},
		{"--yes", "aws", "client-prod"},
		{"aws", "-y", "client-prod"},
	} {
		rest, yes := takeYesFlag(args)
		if !yes {
			t.Errorf("args %q: consent flag not detected", args)
		}
		if len(rest) != 2 || rest[0] != "aws" || rest[1] != "client-prod" {
			t.Errorf("args %q: remaining = %q, want the positional arguments intact", args, rest)
		}
	}

	rest, yes := takeYesFlag([]string{"aws", "client-prod"})
	if yes {
		t.Error("consent reported without the flag")
	}
	if len(rest) != 2 {
		t.Errorf("remaining = %q, want both arguments", rest)
	}
}
