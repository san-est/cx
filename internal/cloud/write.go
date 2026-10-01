package cloud

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// validName is what both providers accept for a profile or configuration name,
// and is deliberately stricter than either: these names end up in file section
// headers, in shell assignments, and in command arguments.
var validName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)

// ValidateName reports why a name is unusable, or nil if it is fine.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("use letters, digits, dot, dash or underscore, starting with a letter")
	}
	return nil
}

// upsertINISection sets keys within one section of an INI file, creating the
// file or the section as needed.
//
// It edits lines rather than reparsing and rewriting the whole file, because
// the AWS config format carries indented sub-properties that a round trip
// through a parser would silently discard. Everything outside the target
// section is preserved byte for byte.
func upsertINISection(path, section string, kv map[string]string) error {
	// A line break in anything written would start a new line of the file,
	// letting a pasted value add keys or whole sections of its own.
	if strings.ContainsAny(section, "\r\n") {
		return fmt.Errorf("section name contains a line break")
	}
	for k, v := range kv {
		if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%s contains a line break", k)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	lines, err := readLines(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	nl := lineEnding(lines)

	start, end := findINISection(lines, section)

	// Keys are written in a stable order so repeated edits produce no spurious
	// diffs.
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if start < 0 {
		if n := len(lines); n > 0 {
			terminate(lines, n, nl)
			if strings.TrimSpace(lines[n-1]) != "" {
				lines = append(lines, nl)
			}
		}
		lines = append(lines, "["+section+"]"+nl)
		for _, k := range keys {
			lines = append(lines, k+" = "+kv[k]+nl)
		}
		return writeFile(path, strings.Join(lines, ""))
	}

	remaining := map[string]string{}
	for k, v := range kv {
		remaining[k] = v
	}
	// Update keys already present, in place. Every occurrence of a key is
	// rewritten, not just the first, since the last is the one that is read.
	for i := start + 1; i < end; i++ {
		raw := lines[i]
		if strings.TrimSpace(raw) == "" || raw[0] == ' ' || raw[0] == '\t' {
			continue
		}
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(raw[:eq])
		if v, ok := kv[key]; ok {
			lines[i] = key + " = " + v + terminator(raw)
			delete(remaining, key)
		}
	}

	// Append whatever was not already there, at the end of the section.
	var added []string
	for _, k := range keys {
		if v, ok := remaining[k]; ok {
			added = append(added, k+" = "+v+nl)
		}
	}
	if len(added) > 0 {
		terminate(lines, end, nl)
		tail := append([]string{}, lines[end:]...)
		lines = append(lines[:end], append(added, tail...)...)
	}
	return writeFile(path, strings.Join(lines, ""))
}

// findINISection returns the line span [start, end) of the last section named
// section, header included, or a negative start if there is none.
//
// The last, because a name that appears twice is read with the later
// occurrence winning, and editing an earlier one would write a value nothing
// then reads.
func findINISection(lines []string, section string) (start, end int) {
	for i := len(lines) - 1; i >= 0; i-- {
		if name, ok := iniHeader(lines[i]); ok && name == section {
			return i, nextINIHeader(lines, i+1)
		}
	}
	return -1, -1
}

// nextINIHeader returns the index of the first section header at or after
// from, or len(lines) if there is none.
func nextINIHeader(lines []string, from int) int {
	for i := from; i < len(lines); i++ {
		if _, ok := iniHeader(lines[i]); ok {
			return i
		}
	}
	return len(lines)
}

// readLines returns a file's lines with their terminators still attached, so
// that joining them reproduces the file exactly. Editing lines without their
// endings would quietly convert a CRLF file to LF, rewriting every section in
// it rather than only the one being changed.
func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(b), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// lineEnding returns the terminator new lines should use: whatever the file's
// first line ends with, so additions match what is already there.
func lineEnding(lines []string) string {
	if len(lines) > 0 && strings.HasSuffix(lines[0], "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// terminator returns the line ending a line from readLines carries, which is
// empty only for an unterminated last line.
func terminator(line string) string {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(line, "\n"):
		return "\n"
	}
	return ""
}

// terminate ends the line before index i with nl if it has no ending, so that
// a line inserted at i does not run onto it.
func terminate(lines []string, i int, nl string) {
	if i > 0 && terminator(lines[i-1]) == "" {
		lines[i-1] += nl
	}
}

// writeLines replaces a file with lines, each terminated by a newline.
func writeLines(path string, lines []string) error {
	return writeFile(path, strings.Join(lines, "\n")+"\n")
}

// writeFile replaces a file atomically, so an interrupted write cannot leave a
// half-written credentials file behind.
func writeFile(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cx-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Credentials must not be world-readable.
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// AWSStaticSpec describes a profile authenticated with long-lived or pasted
// keys.
type AWSStaticSpec struct {
	Name            string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	// SessionToken is optional, and when set makes the profile one that expires.
	SessionToken string
}

// WriteAWSStatic creates or updates a key-based profile. Keys go to the
// credentials file and settings to the config file, matching where the AWS CLI
// itself puts them.
func WriteAWSStatic(s AWSStaticSpec) error {
	if err := ValidateName(s.Name); err != nil {
		return err
	}
	if s.AccessKeyID == "" || s.SecretAccessKey == "" {
		return fmt.Errorf("access key id and secret access key are both required")
	}

	creds := map[string]string{
		"aws_access_key_id":     s.AccessKeyID,
		"aws_secret_access_key": s.SecretAccessKey,
	}
	if s.SessionToken != "" {
		creds["aws_session_token"] = s.SessionToken
	}
	if err := upsertINISection(awsCredentialsPath(), s.Name, creds); err != nil {
		return err
	}

	if s.Region != "" {
		return upsertINISection(awsConfigPath(), awsConfigSection(s.Name), map[string]string{
			"region": s.Region,
		})
	}
	return nil
}

// AWSSSOSpec describes a profile authenticated through IAM Identity Center.
type AWSSSOSpec struct {
	Name        string
	SessionName string
	StartURL    string
	SSORegion   string
	AccountID   string
	RoleName    string
	Region      string
}

// WriteAWSSSO creates or updates an SSO profile plus the sso-session block it
// refers to. This is the credential kind worth steering people towards: the CLI
// refreshes its tokens, so the profile does not silently go stale.
func WriteAWSSSO(s AWSSSOSpec) error {
	if err := ValidateName(s.Name); err != nil {
		return err
	}
	if err := ValidateName(s.SessionName); err != nil {
		return fmt.Errorf("session name: %w", err)
	}
	if s.StartURL == "" || s.SSORegion == "" {
		return fmt.Errorf("start URL and SSO region are both required")
	}

	if err := upsertINISection(awsConfigPath(), "sso-session "+s.SessionName, map[string]string{
		"sso_start_url":           s.StartURL,
		"sso_region":              s.SSORegion,
		"sso_registration_scopes": "sso:account:access",
	}); err != nil {
		return err
	}

	profile := map[string]string{"sso_session": s.SessionName}
	if s.AccountID != "" {
		profile["sso_account_id"] = s.AccountID
	}
	if s.RoleName != "" {
		profile["sso_role_name"] = s.RoleName
	}
	if s.Region != "" {
		profile["region"] = s.Region
	}
	return upsertINISection(awsConfigPath(), awsConfigSection(s.Name), profile)
}

// awsConfigSection returns the section header a profile uses in the config
// file, where every profile but default carries a "profile " prefix.
func awsConfigSection(name string) string {
	if name == "default" {
		return "default"
	}
	return "profile " + name
}

// GCPConfigSpec describes a gcloud configuration.
type GCPConfigSpec struct {
	Name    string
	Account string
	Project string
	Region  string
}

// WriteGCPConfig creates or updates a gcloud configuration by writing its file
// directly.
//
// gcloud stores configurations as plain INI files, and shelling out to
// `gcloud config configurations create` would cost about a second per call for
// something that is three lines of text.
func WriteGCPConfig(s GCPConfigSpec) error {
	if err := ValidateName(s.Name); err != nil {
		return err
	}
	path := filepath.Join(gcloudDir(), "configurations", "config_"+s.Name)

	core := map[string]string{}
	if s.Account != "" {
		core["account"] = s.Account
	}
	if s.Project != "" {
		core["project"] = s.Project
	}
	if len(core) > 0 {
		if err := upsertINISection(path, "core", core); err != nil {
			return err
		}
	}
	if s.Region != "" {
		return upsertINISection(path, "compute", map[string]string{"region": s.Region})
	}
	// An empty configuration still needs the file to exist.
	if len(core) == 0 {
		return upsertINISection(path, "core", map[string]string{})
	}
	return nil
}

// removeINISection deletes a whole section from an INI file, leaving the rest
// byte for byte as it was. A section that is not there is not an error: the
// caller wants it gone, and it is.
func removeINISection(path, section string) (bool, error) {
	lines, err := readLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	// Every occurrence goes: leaving a duplicate behind would leave the
	// section readable, which is the opposite of what was asked.
	removed := false
	for {
		start, end := findINISection(lines, section)
		if start < 0 {
			break
		}
		// A section's span already takes the blank line separating it from
		// the next one. The last section has none, so take the one before it
		// instead, or repeated deletes would leave a growing gap at the end.
		if end == len(lines) && start > 0 && strings.TrimSpace(lines[start-1]) == "" {
			start--
		}
		lines = append(lines[:start], lines[end:]...)
		removed = true
	}
	if !removed {
		return false, nil
	}
	return true, writeFile(path, strings.Join(lines, ""))
}

// DeleteAWSProfile removes a profile from both AWS files.
//
// It deliberately does not touch any sso-session block the profile referred to:
// those are shared, and removing one would break every other profile using it.
func DeleteAWSProfile(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}

	removedCreds, err := removeINISection(awsCredentialsPath(), name)
	if err != nil {
		return err
	}
	removedConfig, err := removeINISection(awsConfigPath(), awsConfigSection(name))
	if err != nil {
		return err
	}
	if !removedCreds && !removedConfig {
		return fmt.Errorf("no AWS profile named %q", name)
	}
	return nil
}

// DeleteGCPConfig removes a gcloud configuration.
func DeleteGCPConfig(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	path := filepath.Join(gcloudDir(), "configurations", "config_"+name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no gcloud configuration named %q", name)
		}
		return err
	}
	return nil
}

// AWSProfileFields returns a profile's stored settings, merged across both
// files, for pre-filling an edit form.
func AWSProfileFields(name string) (map[string]string, error) {
	cfg, err := parseINI(awsConfigPath())
	if err != nil {
		return nil, err
	}
	creds, err := parseINI(awsCredentialsPath())
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	for k, v := range cfg[awsConfigSection(name)] {
		out[k] = v
	}
	for k, v := range creds[name] {
		out[k] = v
	}

	// An SSO profile keeps its endpoint details in a shared session block.
	if sess := out["sso_session"]; sess != "" {
		for k, v := range cfg["sso-session "+sess] {
			out[k] = v
		}
	}
	return out, nil
}

// GCPConfigFields returns a gcloud configuration's properties, flattened to the
// names the forms use.
func GCPConfigFields(name string) (map[string]string, error) {
	props, err := parseINI(filepath.Join(gcloudDir(), "configurations", "config_"+name))
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"account": props.get("core", "account"),
		"project": props.get("core", "project"),
		"region":  props.get("compute", "region"),
	}, nil
}
