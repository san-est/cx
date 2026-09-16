package cloud

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// awsEnvOverrides are the environment variables that take precedence over, or
// interfere with, profile-based credential resolution. Probes clear them so the
// result reflects the profile on disk rather than whatever the parent shell
// happens to be exporting.
var awsEnvOverrides = []string{
	"AWS_PROFILE",
	"AWS_DEFAULT_PROFILE",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AWS_SECURITY_TOKEN",
	"AWS_CREDENTIAL_EXPIRATION",
}

func awsConfigPath() string {
	if p := os.Getenv("AWS_CONFIG_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aws", "config")
}

func awsCredentialsPath() string {
	if p := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aws", "credentials")
}

// LoadAWS reads the AWS profiles from disk without contacting the network.
// Health is left Unknown; call ProbeAWS to fill it in.
func LoadAWS() ([]Target, error) {
	cfg, err := parseINI(awsConfigPath())
	if err != nil {
		return nil, err
	}
	creds, err := parseINI(awsCredentialsPath())
	if err != nil {
		return nil, err
	}

	// Merge both files into one view keyed by bare profile name. ~/.aws/config
	// prefixes every non-default section with "profile "; ~/.aws/credentials
	// never does.
	merged := map[string]map[string]string{}
	add := func(name string, kv map[string]string) {
		if _, ok := merged[name]; !ok {
			merged[name] = map[string]string{}
		}
		for k, v := range kv {
			// Credentials file wins on conflict, matching the AWS CLI's own
			// precedence for credential keys.
			merged[name][k] = v
		}
	}
	for section, kv := range cfg {
		name := section
		if strings.HasPrefix(section, "profile ") {
			name = strings.TrimSpace(strings.TrimPrefix(section, "profile "))
		} else if strings.HasPrefix(section, "sso-session ") {
			// Not a profile; referenced by name from profiles that use it.
			continue
		} else if name != "default" {
			// A bare [name] section in config is not a profile per the spec.
			continue
		}
		add(name, kv)
	}
	for section, kv := range creds {
		add(section, kv)
	}

	active := os.Getenv("AWS_PROFILE")
	if active == "" {
		active = os.Getenv("AWS_DEFAULT_PROFILE")
	}
	// With no explicit selection the CLI falls back to the default profile, so
	// that is what the current shell actually targets.
	if active == "" {
		if _, ok := merged["default"]; ok {
			active = "default"
		}
	}

	out := make([]Target, 0, len(merged))
	for name, kv := range merged {
		t := Target{
			Name:   name,
			Kind:   classifyAWS(kv),
			Scope:  kv["region"],
			Active: name == active,
			Health: Unknown,
		}
		if id := kv["sso_account_id"]; id != "" {
			t.Account = id
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func classifyAWS(kv map[string]string) CredKind {
	switch {
	case kv["credential_process"] != "":
		return KindProcess
	case kv["sso_session"] != "":
		return KindSSOSession
	case kv["sso_start_url"] != "" || kv["sso_account_id"] != "":
		return KindSSO
	case kv["role_arn"] != "":
		return KindAssumeRole
	case kv["aws_access_key_id"] != "" && kv["aws_session_token"] != "":
		return KindStaticTemp
	case kv["aws_access_key_id"] != "":
		return KindStatic
	default:
		return KindNone
	}
}

type callerIdentity struct {
	Account string `json:"Account"`
	Arn     string `json:"Arn"`
}

// ProbeAWS resolves each profile's real identity by calling STS, concurrently
// and with a hard timeout. Probing serially -- as a shell loop must -- costs
// seconds per profile on a slow link, which is the main reason this tool is not
// a shell script.
//
// It shells out to the AWS CLI rather than using the SDK so that SSO token
// caches, credential_process helpers, and every other resolution mechanism the
// user has already configured work identically here and in their terminal.
func ProbeAWS(ctx context.Context, targets []Target, concurrency int) {
	if concurrency < 1 {
		concurrency = 8
	}
	if _, err := exec.LookPath("aws"); err != nil {
		for i := range targets {
			targets[i].Health = Missing
			targets[i].Detail = "aws CLI not found on PATH"
			targets[i].ProbedAt = time.Now()
		}
		return
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range targets {
		// A profile with nothing configured cannot authenticate; skip the round
		// trip rather than waiting for it to fail.
		if targets[i].Kind == KindNone {
			targets[i].Health = Missing
			targets[i].Detail = "no credentials configured"
			targets[i].ProbedAt = time.Now()
			continue
		}
		wg.Add(1)
		go func(t *Target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			probeOneAWS(ctx, t)
		}(&targets[i])
	}
	wg.Wait()
}

func probeOneAWS(ctx context.Context, t *Target) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "aws", "sts", "get-caller-identity", "--output", "json")
	cmd.Env = append(cleanEnv(awsEnvOverrides), "AWS_PROFILE="+t.Name)
	// Suppress the interactive SSO browser prompt: a dashboard refresh must
	// never hijack the user's browser. An unauthenticated SSO profile should
	// simply report as expired.
	cmd.Env = append(cmd.Env, "AWS_CLI_AUTO_PROMPT=off")
	cmd.Stdin = nil

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.ProbedAt = time.Now()
	if err := cmd.Run(); err != nil {
		t.Health = Expired
		t.Detail = summarizeAWSError(stderr.String(), ctx.Err())
		return
	}

	var id callerIdentity
	if err := json.Unmarshal([]byte(stdout.String()), &id); err != nil {
		t.Health = Expired
		t.Detail = "unreadable STS response"
		return
	}
	t.Health = Valid
	t.Account = id.Account
	t.Identity = shortARN(id.Arn)
	t.Detail = ""
}

// summarizeAWSError turns a wall of CLI stderr into one line a person can act
// on. The distinction that matters is "your token died" versus "this profile is
// misconfigured", because only the first is fixed by logging in again.
func summarizeAWSError(stderr string, ctxErr error) string {
	if ctxErr != nil {
		return "timed out"
	}
	s := stderr
	switch {
	case strings.Contains(s, "ExpiredToken"), strings.Contains(s, "security token included in the request is expired"):
		return "session token expired"
	case strings.Contains(s, "sso") && strings.Contains(s, "expired"),
		strings.Contains(s, "SSO session associated with this profile has expired"),
		strings.Contains(s, "Token has expired and refresh failed"):
		return "SSO session expired - run: aws sso login"
	case strings.Contains(s, "InvalidClientTokenId"):
		return "access key not recognized"
	case strings.Contains(s, "SignatureDoesNotMatch"):
		return "bad secret key (or empty session token)"
	case strings.Contains(s, "AccessDenied"):
		return "authenticated but denied sts:GetCallerIdentity"
	case strings.Contains(s, "Unable to locate credentials"):
		return "no credentials resolved"
	case strings.Contains(s, "Could not connect"), strings.Contains(s, "EndpointConnectionError"):
		return "network unreachable"
	}
	// Fall back to the CLI's own last line, trimmed to something printable.
	lines := strings.Split(strings.TrimSpace(s), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return "probe failed"
	}
	if len(last) > 70 {
		last = last[:70] + "..."
	}
	return last
}

// shortARN keeps the useful tail of an ARN. Full ARNs are too wide for a table
// and the leading partition/service/account segments are already shown.
func shortARN(arn string) string {
	if arn == "" {
		return ""
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return arn
	}
	return parts[5]
}

// cleanEnv returns the current environment with the named variables removed.
func cleanEnv(drop []string) []string {
	skip := make(map[string]bool, len(drop))
	for _, k := range drop {
		skip[k] = true
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq > 0 && skip[kv[:eq]] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
