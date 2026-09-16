package cloud

import "time"

// Health is the result of probing a target's credentials.
type Health int

const (
	// Unknown means the target has not been probed yet this refresh.
	Unknown Health = iota
	// Probing means a probe is currently in flight.
	Probing
	// Valid means the credentials resolved to a real identity.
	Valid
	// Expired means credentials exist but the provider rejected them. For AWS
	// this is the common case for a hand-pasted session token; for GCP it means
	// no unexpired refresh token for the account.
	Expired
	// Missing means the target names credentials that are not present at all.
	Missing
)

func (h Health) String() string {
	switch h {
	case Probing:
		return "checking"
	case Valid:
		return "valid"
	case Expired:
		return "expired"
	case Missing:
		return "no creds"
	default:
		return "unknown"
	}
}

// CredKind describes how a target obtains credentials. This matters more than
// it looks: static long-lived keys and hand-pasted session tokens rot, while
// SSO and credential_process refresh themselves. Surfacing the distinction is
// the point of the dashboard.
type CredKind string

const (
	KindStatic     CredKind = "static"          // access key + secret, long-lived
	KindStaticTemp CredKind = "static+token"    // access key + secret + pasted session token
	KindSSO        CredKind = "sso"             // legacy inline sso_* keys
	KindSSOSession CredKind = "sso-session"     // modern sso_session reference
	KindAssumeRole CredKind = "assume-role"     // role_arn + source_profile
	KindProcess    CredKind = "process"         // credential_process
	KindUser       CredKind = "user"            // GCP: an authorized user account
	KindServiceAcc CredKind = "service-account" // GCP: a service account key
	KindNone       CredKind = "none"            // nothing configured
)

// Rots reports whether this credential kind expires without a way to refresh
// itself. These are the targets that produce the "it worked yesterday" class of
// failure, so the UI calls them out.
func (k CredKind) Rots() bool {
	return k == KindStaticTemp
}

// Target is one switchable destination: an AWS profile, a GCP configuration, or
// the ADC slot. The zero value is not useful; construct via the provider
// loaders.
type Target struct {
	// Name is the identifier the user switches to.
	Name string
	// Kind is how credentials are obtained.
	Kind CredKind
	// Account is the human-facing owner: an AWS account ID, or a GCP account
	// email. Populated by a probe where it is not known statically.
	Account string
	// Scope is the blast radius: an AWS region, or a GCP project.
	Scope string
	// Identity is the resolved principal (an AWS ARN, a GCP email).
	Identity string
	// Active reports whether this target applies to the current shell.
	Active bool
	// Health is the probe result.
	Health Health
	// Detail carries a short explanation for a non-Valid health, shown inline.
	Detail string
	// Sensitive marks a target the user has flagged as production.
	Sensitive bool
	// ProbedAt records when Health was last established.
	ProbedAt time.Time
}
