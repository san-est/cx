package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/san-est/cx/internal/cloud"
)

// prompter supplies the values a profile needs. Secret answers are kept
// separate from ordinary ones so the terminal implementation can stop echoing
// for exactly the values that matter, and no wider.
type prompter interface {
	// Line asks for a value that may be shown as it is typed.
	Line(label string) (string, error)
	// Secret asks for a value that must not be.
	Secret(label string) (string, error)
}

// ttyPrompter asks a person, on the terminal.
type ttyPrompter struct{ out io.Writer }

func (p ttyPrompter) Line(label string) (string, error) {
	fmt.Fprintf(p.out, "%s: ", label)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (p ttyPrompter) Secret(label string) (string, error) {
	fmt.Fprintf(p.out, "%s: ", label)
	b, err := term.ReadPassword(os.Stdin.Fd())
	// ReadPassword swallows the newline the user typed, so the next line of
	// output would otherwise run on from the prompt.
	fmt.Fprintln(p.out)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// pipePrompter reads answers from stdin, one per line and in the order asked,
// so the command can be driven by a script.
type pipePrompter struct {
	sc     *bufio.Scanner
	labels []string
}

func newPipePrompter(r io.Reader) *pipePrompter {
	return &pipePrompter{sc: bufio.NewScanner(r)}
}

func (p *pipePrompter) next(label string) (string, error) {
	p.labels = append(p.labels, label)
	if !p.sc.Scan() {
		if err := p.sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("stdin ended before %s; expected one value per line, in the order %s",
			label, strings.Join(p.labels, ", then "))
	}
	return strings.TrimSpace(p.sc.Text()), nil
}

func (p *pipePrompter) Line(label string) (string, error)   { return p.next(label) }
func (p *pipePrompter) Secret(label string) (string, error) { return p.next(label) }

// addFlags are the settings `cx add aws` accepts on the command line.
//
// Deliberately absent: the secret access key and the session token. A process's
// arguments are readable by every other process on the machine, and they land
// in shell history, so cx will not take a credential that way even when asked.
type addFlags struct {
	region    string
	accessKey string
	withToken bool
	force     bool
	rest      []string
}

// parseAddFlags reads the flags `cx add aws` accepts, leaving the positional
// arguments in rest.
func parseAddFlags(args []string) (addFlags, error) {
	var f addFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}

		switch {
		case a == "--region" || a == "-region":
			v, err := value()
			if err != nil {
				return f, err
			}
			f.region = v
		case a == "--access-key" || a == "-access-key":
			v, err := value()
			if err != nil {
				return f, err
			}
			f.accessKey = v
		case a == "--with-session-token" || a == "-with-session-token":
			f.withToken = true
		case a == "--force" || a == "-force":
			f.force = true
		// Named explicitly rather than falling into "unknown flag", so the
		// refusal explains itself instead of looking like a typo.
		case strings.HasPrefix(a, "--secret") || strings.HasPrefix(a, "-secret"),
			strings.HasPrefix(a, "--session-token"), strings.HasPrefix(a, "-session-token"):
			return f, fmt.Errorf("cx will not take a credential on the command line: " +
				"process arguments are readable by other processes and are kept in shell history.\n" +
				"    Leave it off and cx will ask, or pipe it in on stdin")
		case strings.HasPrefix(a, "-"):
			return f, fmt.Errorf("unknown flag %q", a)
		default:
			f.rest = append(f.rest, a)
		}
	}
	return f, nil
}

// collectAWSStatic fills in whatever the flags did not supply.
func collectAWSStatic(p prompter, f addFlags, name string) (cloud.AWSStaticSpec, error) {
	spec := cloud.AWSStaticSpec{Name: name, Region: f.region, AccessKeyID: f.accessKey}

	var err error
	if spec.AccessKeyID == "" {
		if spec.AccessKeyID, err = p.Line("Access key ID"); err != nil {
			return spec, err
		}
	}
	if spec.SecretAccessKey, err = p.Secret("Secret access key"); err != nil {
		return spec, err
	}
	if f.withToken {
		if spec.SessionToken, err = p.Secret("Session token"); err != nil {
			return spec, err
		}
	}
	if spec.Region == "" {
		if spec.Region, err = p.Line("Default region (optional)"); err != nil {
			return spec, err
		}
	}
	return spec, nil
}

// runAddAWS creates a key-based AWS profile without opening the dashboard.
func runAddAWS(args []string) int {
	f, err := parseAddFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cx:", err)
		return 1
	}
	if len(f.rest) != 1 {
		fmt.Fprintln(os.Stderr, "cx: usage: cx add aws <name> [--region <region>] "+
			"[--access-key <id>] [--with-session-token] [--force]")
		return 1
	}
	name := f.rest[0]
	if err := cloud.ValidateName(name); err != nil {
		fmt.Fprintln(os.Stderr, "cx:", err)
		return 1
	}

	// Overwriting a profile replaces credentials that may be the only copy, so
	// it has to be asked for rather than assumed.
	if !f.force {
		existing, err := cloud.LoadAWS()
		if err == nil && hasTarget(existing, name) {
			fmt.Fprintf(os.Stderr, "cx: AWS profile %q already exists; pass --force to replace its credentials\n", name)
			return 1
		}
	}

	var p prompter
	if term.IsTerminal(os.Stdin.Fd()) {
		p = ttyPrompter{out: os.Stderr}
	} else {
		// One value per line, in the order the prompts would have come.
		p = newPipePrompter(os.Stdin)
	}

	spec, err := collectAWSStatic(p, f, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cx:", err)
		return 1
	}
	if spec.AccessKeyID == "" || spec.SecretAccessKey == "" {
		fmt.Fprintln(os.Stderr, "cx: an access key id and a secret access key are both required")
		return 1
	}

	if err := cloud.WriteAWSStatic(spec); err != nil {
		fmt.Fprintln(os.Stderr, "cx:", err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "cx: wrote AWS profile %s\n", name)
	if spec.SessionToken != "" {
		// The one credential kind here that expires with nothing able to
		// refresh it, and the cause of most "it worked yesterday" reports.
		fmt.Fprintln(os.Stderr, "    it carries a pasted session token, so it will expire")
	}
	fmt.Fprintf(os.Stderr, "    point this shell at it with: cx use aws %s\n", name)
	return 0
}

// runAdd dispatches the add subcommands.
func runAdd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "cx: usage: cx add aws <name> [flags]")
		return 1
	}
	switch args[0] {
	case "aws":
		return runAddAWS(args[1:])
	default:
		// Only static-key AWS profiles are scriptable: the others are browser
		// sign-ins that only the vendor CLI can complete, and the dashboard
		// runs those already.
		fmt.Fprintf(os.Stderr, "cx: cannot add %q from the command line (only aws); press a in the dashboard\n", args[0])
		return 1
	}
}
