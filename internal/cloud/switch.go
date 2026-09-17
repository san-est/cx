package cloud

import "github.com/san-est/cx/internal/shellcfg"

// awsAmbientCreds are credential variables that the AWS CLI consults *before*
// it looks at AWS_PROFILE. Leaving them set is how a profile switch appears to
// work and then silently keeps using the old account, so a switch clears them.
var awsAmbientCreds = []string{
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AWS_SECURITY_TOKEN",
	"AWS_CREDENTIAL_EXPIRATION",
	"AWS_DEFAULT_PROFILE",
}

// gcpAmbientOverrides are properties that beat whatever the active gcloud
// configuration says. A switch clears them so the chosen configuration is
// actually the thing in effect.
var gcpAmbientOverrides = []string{
	"CLOUDSDK_CORE_PROJECT",
	"CLOUDSDK_CORE_ACCOUNT",
}

// SwitchAWS builds the environment changes that point a shell at an AWS profile.
func SwitchAWS(profile string) *shellcfg.Script {
	s := shellcfg.New()
	for _, v := range awsAmbientCreds {
		s.Unset(v)
	}
	s.Export("AWS_PROFILE", profile)
	s.Note("cx: AWS profile -> %s", profile)
	return s
}

// SwitchGCP builds the environment changes that point a shell at a gcloud
// configuration.
//
// It deliberately does not run `gcloud config configurations activate`. That
// command writes a file shared by every shell on the machine, so it would
// retarget terminals the user is not looking at. Setting the environment
// variable affects this shell and only this shell.
func SwitchGCP(config string) *shellcfg.Script {
	s := shellcfg.New()
	for _, v := range gcpAmbientOverrides {
		s.Unset(v)
	}
	s.Export("CLOUDSDK_ACTIVE_CONFIG_NAME", config)
	s.Note("cx: gcloud configuration -> %s (this shell only)", config)
	return s
}

// Clear removes cx's overrides for one provider or all of them. Afterwards the
// shell falls back to whatever the machine-wide defaults are, which for gcloud
// means the shared active_config file again.
func Clear(scope string) *shellcfg.Script {
	s := shellcfg.New()
	if scope == "aws" || scope == "all" {
		s.Unset("AWS_PROFILE")
		for _, v := range awsAmbientCreds {
			s.Unset(v)
		}
		s.Note("cx: cleared AWS overrides for this shell")
	}
	if scope == "gcp" || scope == "all" {
		for _, v := range gcpAmbientOverrides {
			s.Unset(v)
		}
		// Reset to the machine-wide default by *value*, not by reference.
		//
		// Simply unsetting the variable would leave the shell following the
		// shared active_config file, which is precisely the hazard this tool
		// exists to prevent -- clearing would create the problem. Re-pinning to
		// whatever that file currently says gives the same target while keeping
		// the shell immune to another terminal switching.
		//
		// CX_NO_AUTOPIN=1 is the escape hatch for anyone who genuinely wants to
		// follow the file as it changes.
		if _, state, err := LoadGCP(); err == nil && state.Global != "" && !state.GlobalMissing {
			s.Export("CLOUDSDK_ACTIVE_CONFIG_NAME", state.Global)
			s.Note("cx: gcloud reset to the machine default (%s), pinned to this shell", state.Global)
		} else {
			s.Unset("CLOUDSDK_ACTIVE_CONFIG_NAME")
			s.Note("cx: cleared gcloud overrides")
		}
	}
	return s
}
