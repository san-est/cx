package ui

import (
	"fmt"

	"github.com/vboyadzhiev/cx/internal/cloud"
)

// addEntry is one option in the add menu. It either opens a form that cx fills
// in itself, or runs an external command.
//
// The split is deliberate: anything that is only a matter of writing
// configuration is a form, because handing the terminal to another program's
// prompts is jarring and loses the dashboard. Anything that needs a browser
// sign-in has to be the vendor CLI, because only it can complete that flow.
type addEntry struct {
	key    string
	label  string
	hint   string
	form   func() *form
	action *cloud.Action
}

// addEntries is the add menu.
func addEntries() []addEntry {
	adc := cloud.Action{
		Label: "gcloud auth application-default login",
		Cmd:   "gcloud", Args: []string{"auth", "application-default", "login"},
	}
	return []addEntry{
		{
			key: "s", label: "AWS profile — SSO",
			hint: "recommended: tokens refresh themselves and cannot go stale",
			form: awsSSOForm,
		},
		{
			key: "k", label: "AWS profile — access keys",
			hint: "for clients who hand you a key and secret",
			form: awsKeysForm,
		},
		{
			key: "g", label: "gcloud configuration",
			hint: "a named account plus project to switch to",
			form: gcpConfigForm,
		},
		{
			key: "d", label: "Application Default Credentials",
			hint:   "opens a browser — what terraform actually uses",
			action: &adc,
		},
	}
}

func awsSSOForm() *form {
	return newForm(
		"Add AWS profile (SSO)",
		"Written to ~/.aws/config. Afterwards, press l on the row to sign in.",
		[]field{
			{key: "name", label: "Profile name", hint: "client-prod", required: true},
			{key: "session", label: "SSO session", hint: "corp", required: true},
			{key: "start_url", label: "Start URL", hint: "https://acme.awsapps.com/start", required: true},
			{key: "sso_region", label: "SSO region", hint: "eu-west-1", required: true},
			{key: "account_id", label: "Account ID", hint: "123456789012"},
			{key: "role", label: "Role name", hint: "AdministratorAccess"},
			{key: "region", label: "Default region", hint: "eu-west-1"},
		},
		func(v map[string]string) (string, error) {
			err := cloud.WriteAWSSSO(cloud.AWSSSOSpec{
				Name:        v["name"],
				SessionName: v["session"],
				StartURL:    v["start_url"],
				SSORegion:   v["sso_region"],
				AccountID:   v["account_id"],
				RoleName:    v["role"],
				Region:      v["region"],
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("added AWS profile %s — press l on its row to sign in", v["name"]), nil
		},
	)
}

func awsKeysForm() *form {
	return newForm(
		"Add AWS profile (access keys)",
		"Keys go to ~/.aws/credentials, mode 600. A session token makes the profile expire.",
		[]field{
			{key: "name", label: "Profile name", hint: "client-dev", required: true},
			{key: "access_key", label: "Access key ID", hint: "AKIA...", required: true},
			{key: "secret_key", label: "Secret access key", required: true, secret: true},
			{key: "session_token", label: "Session token", hint: "optional — only for temporary credentials", secret: true},
			{key: "region", label: "Default region", hint: "eu-west-1"},
		},
		func(v map[string]string) (string, error) {
			err := cloud.WriteAWSStatic(cloud.AWSStaticSpec{
				Name:            v["name"],
				AccessKeyID:     v["access_key"],
				SecretAccessKey: v["secret_key"],
				SessionToken:    v["session_token"],
				Region:          v["region"],
			})
			if err != nil {
				return "", err
			}
			if v["session_token"] != "" {
				return fmt.Sprintf("added %s — note this profile will expire", v["name"]), nil
			}
			return fmt.Sprintf("added AWS profile %s", v["name"]), nil
		},
	)
}

func gcpConfigForm() *form {
	return newForm(
		"Add gcloud configuration",
		"Written to ~/.config/gcloud. Afterwards, press l on the row to sign the account in.",
		[]field{
			{key: "name", label: "Configuration name", hint: "acme-dev", required: true},
			{key: "account", label: "Account email", hint: "you@example.com"},
			{key: "project", label: "Project ID", hint: "acme-dev-1234"},
			{key: "region", label: "Default region", hint: "europe-west1"},
		},
		func(v map[string]string) (string, error) {
			err := cloud.WriteGCPConfig(cloud.GCPConfigSpec{
				Name:    v["name"],
				Account: v["account"],
				Project: v["project"],
				Region:  v["region"],
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("added gcloud configuration %s — press l on its row to sign in", v["name"]), nil
		},
	)
}

// editFormFor builds a form pre-filled with a target's current settings.
//
// The name is not editable. Renaming a profile means deleting one section and
// creating another, and a half-completed rename would leave credentials behind
// under the old name -- delete and re-add is the honest way to do that.
func editFormFor(t cloud.Target, provider string) (*form, error) {
	if provider == "gcp" {
		f, err := cloud.GCPConfigFields(t.Name)
		if err != nil {
			return nil, err
		}
		return editGCPForm(t.Name, f), nil
	}

	f, err := cloud.AWSProfileFields(t.Name)
	if err != nil {
		return nil, err
	}
	if t.Kind == cloud.KindSSO || t.Kind == cloud.KindSSOSession {
		return editAWSSSOForm(t.Name, f), nil
	}
	return editAWSKeysForm(t.Name, f), nil
}

// withValues pre-fills a form's inputs from stored settings.
func withValues(f *form, values map[string]string) *form {
	for i := range f.fields {
		if v, ok := values[f.fields[i].key]; ok && v != "" {
			f.fields[i].input.SetValue(v)
		}
	}
	return f
}

func editAWSKeysForm(name string, cur map[string]string) *form {
	f := newForm(
		"Edit AWS profile — "+name,
		"Leave the secret blank to keep the one already stored.",
		[]field{
			{key: "access_key", label: "Access key ID", hint: "AKIA...", required: true},
			{key: "secret_key", label: "Secret access key", hint: "unchanged if blank", secret: true},
			{key: "session_token", label: "Session token", hint: "optional — temporary credentials", secret: true},
			{key: "region", label: "Default region", hint: "eu-west-1"},
		},
		func(v map[string]string) (string, error) {
			secret := v["secret_key"]
			if secret == "" {
				// Writing an empty secret would break the profile, so an
				// untouched field means "keep what is there".
				secret = cur["aws_secret_access_key"]
			}
			if secret == "" {
				return "", fmt.Errorf("no secret access key stored; enter one")
			}
			err := cloud.WriteAWSStatic(cloud.AWSStaticSpec{
				Name:            name,
				AccessKeyID:     v["access_key"],
				SecretAccessKey: secret,
				SessionToken:    v["session_token"],
				Region:          v["region"],
			})
			if err != nil {
				return "", err
			}
			return "updated AWS profile " + name, nil
		},
	)
	return withValues(f, map[string]string{
		"access_key":    cur["aws_access_key_id"],
		"session_token": cur["aws_session_token"],
		"region":        cur["region"],
	})
}

func editAWSSSOForm(name string, cur map[string]string) *form {
	f := newForm(
		"Edit AWS profile — "+name,
		"Changes to the SSO session apply to every profile sharing it.",
		[]field{
			{key: "session", label: "SSO session", hint: "corp", required: true},
			{key: "start_url", label: "Start URL", hint: "https://acme.awsapps.com/start", required: true},
			{key: "sso_region", label: "SSO region", hint: "eu-west-1", required: true},
			{key: "account_id", label: "Account ID", hint: "123456789012"},
			{key: "role", label: "Role name", hint: "AdministratorAccess"},
			{key: "region", label: "Default region", hint: "eu-west-1"},
		},
		func(v map[string]string) (string, error) {
			err := cloud.WriteAWSSSO(cloud.AWSSSOSpec{
				Name:        name,
				SessionName: v["session"],
				StartURL:    v["start_url"],
				SSORegion:   v["sso_region"],
				AccountID:   v["account_id"],
				RoleName:    v["role"],
				Region:      v["region"],
			})
			if err != nil {
				return "", err
			}
			return "updated AWS profile " + name, nil
		},
	)
	return withValues(f, map[string]string{
		"session":    cur["sso_session"],
		"start_url":  cur["sso_start_url"],
		"sso_region": cur["sso_region"],
		"account_id": cur["sso_account_id"],
		"role":       cur["sso_role_name"],
		"region":     cur["region"],
	})
}

func editGCPForm(name string, cur map[string]string) *form {
	f := newForm(
		"Edit gcloud configuration — "+name,
		"Written to ~/.config/gcloud.",
		[]field{
			{key: "account", label: "Account email", hint: "you@example.com"},
			{key: "project", label: "Project ID", hint: "acme-dev-1234"},
			{key: "region", label: "Default region", hint: "europe-west1"},
		},
		func(v map[string]string) (string, error) {
			err := cloud.WriteGCPConfig(cloud.GCPConfigSpec{
				Name:    name,
				Account: v["account"],
				Project: v["project"],
				Region:  v["region"],
			})
			if err != nil {
				return "", err
			}
			return "updated gcloud configuration " + name, nil
		},
	)
	return withValues(f, cur)
}
