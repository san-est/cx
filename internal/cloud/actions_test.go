package cloud

import (
	"strings"
	"testing"
)

func TestLoginActionMatchesCredentialKind(t *testing.T) {
	// An SSO profile is fixed by a new SSO session. A static profile has no
	// login flow at all -- offering one would just fail.
	sso := LoginAction(Target{Name: "prod", Kind: KindSSOSession})
	if sso.Cmd != "aws" || strings.Join(sso.Args, " ") != "sso login --profile prod" {
		t.Errorf("sso login = %s %v", sso.Cmd, sso.Args)
	}

	static := LoginAction(Target{Name: "legacy", Kind: KindStaticTemp})
	if strings.Join(static.Args, " ") != "configure --profile legacy" {
		t.Errorf("static login = %s %v", static.Cmd, static.Args)
	}
}

func TestLoginActionGCPWithoutAccountRunsInit(t *testing.T) {
	// There is no account to re-authenticate, so the configuration needs
	// setting up rather than signing in.
	got := LoginActionGCP(Target{Name: "default"})
	if strings.Join(got.Args, " ") != "init" {
		t.Errorf("got %s %v, want gcloud init", got.Cmd, got.Args)
	}

	withAcct := LoginActionGCP(Target{Name: "dev", Account: "me@example.com"})
	if strings.Join(withAcct.Args, " ") != "auth login --account me@example.com" {
		t.Errorf("got %s %v", withAcct.Cmd, withAcct.Args)
	}
}
