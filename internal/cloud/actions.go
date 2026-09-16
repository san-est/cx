package cloud

// Action is an external command cx runs on the user's behalf, by suspending the
// dashboard and handing the terminal over. cx never reimplements these flows:
// they are interactive, they change over time, and the vendor CLIs already do
// them correctly.
type Action struct {
	// Label is what the menu shows.
	Label string
	// Key is the single keystroke that selects it.
	Key string
	// Cmd and Args are the command to run.
	Cmd  string
	Args []string
	// Hint explains what the flow does, for the menu.
	Hint string
}

// LoginAction returns the command that re-authenticates an AWS profile.
//
// The right command depends on how the profile gets its credentials: an SSO
// profile needs a new SSO session, while a static profile has no login flow at
// all and can only be re-entered by hand.
func LoginAction(t Target) Action {
	switch t.Kind {
	case KindSSO, KindSSOSession:
		return Action{
			Label: "aws sso login",
			Cmd:   "aws", Args: []string{"sso", "login", "--profile", t.Name},
		}
	case KindAssumeRole:
		// The role itself is assumed on demand; what expires is the source
		// profile's own credentials.
		return Action{
			Label: "aws sso login (source profile)",
			Cmd:   "aws", Args: []string{"sso", "login", "--profile", t.Name},
		}
	default:
		return Action{
			Label: "aws configure",
			Cmd:   "aws", Args: []string{"configure", "--profile", t.Name},
		}
	}
}

// LoginActionGCP returns the command that re-authenticates a gcloud
// configuration's account.
func LoginActionGCP(t Target) Action {
	if t.Account == "" {
		// Nothing to re-authenticate yet; the configuration needs setting up.
		return Action{
			Label: "gcloud init",
			Cmd:   "gcloud", Args: []string{"init"},
		}
	}
	return Action{
		Label: "gcloud auth login",
		Cmd:   "gcloud", Args: []string{"auth", "login", "--account", t.Account},
	}
}
