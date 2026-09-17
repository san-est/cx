# Security policy

`cx` reads cloud credential configuration and writes to `~/.aws/config`,
`~/.aws/credentials` and `~/.config/gcloud`. A defect here can misdirect a
command at the wrong cloud account, or expose a secret. Reports are welcome and
taken seriously.

## Reporting a vulnerability

**Do not open a public issue.**

Use GitHub's private vulnerability reporting:
[**Report a vulnerability**](https://github.com/san-est/cx/security/advisories/new).
It opens a private channel visible only to the maintainer, and can mint a CVE
once the issue is fixed.

Please include the version or commit, your OS and shell, the configuration shape
that triggers it (with secrets redacted), and what an attacker gains.

**Never paste real credentials into a report.** Use placeholder values.

### What to expect

| Stage | Target |
|---|---|
| Acknowledgement | 72 hours |
| Initial assessment | 7 days |
| Fix or mitigation for a confirmed high-severity issue | 30 days |

This is a personal project maintained in spare time, so these are honest targets
rather than a contractual SLA. If you have not heard back within a week, please
send a reminder through the same channel.

Coordinated disclosure is the expectation: please give a fix a reasonable chance
to ship before publishing. Reporters are credited in the advisory unless they
ask not to be.

## Scope

**In scope**

- Leaking secret material — into the terminal, a log, a scratch file, an
  environment variable, a process argument list, or a file with loose permissions
- Command injection through a configuration value: profile names, configuration
  names, and account identifiers all originate in files on disk and flow into
  generated shell code and into `exec` arguments
- Writes that corrupt `~/.aws/config`, `~/.aws/credentials` or the gcloud
  configuration directory, or that widen their permissions
- A state in which `cx` reports one target while the shell actually resolves to
  another — the tool exists to prevent exactly this, so a false "safe" reading
  is a security bug, not a cosmetic one
- Privilege or context escalation through the shell wrapper emitted by
  `cx shell-init`

**Out of scope**

- Vulnerabilities in `aws`, `gcloud`, or any other vendor CLI that `cx` invokes
  — report those to the respective vendor
- An attacker who already has arbitrary code execution as your user, or read
  access to your home directory. Such an attacker can read the credential files
  directly and does not need `cx`
- Credentials being visible to the user who owns them
- Missing hardening with no demonstrated impact, and automated scanner output
  submitted without a working scenario

## Design notes relevant to security

These are deliberate properties. A change that breaks one of them is a
regression worth reporting.

- **Switching only ever sets environment variables in the calling shell.** `cx`
  never runs `gcloud config configurations activate` or any other command that
  writes machine-wide state, because that would retarget terminals the user is
  not looking at.
- **Values emitted into shell code are single-quoted.** Names come from
  filenames and file contents on disk, so they are untrusted input to the
  wrapper that sources the generated script.
- **Subprocesses are executed with an argument vector, never through a shell.**
  There is no interpolation into a command string.
- **Credential files are written through a temporary file chmod'd to `0600`
  before the rename**, so a secret is never briefly world-readable.
- **Writes are line-oriented and touch only the target section**, so indented
  sub-properties that a parse-and-rewrite would drop survive intact.
- **Secrets are never echoed.** The add and edit forms mask secret fields, and
  an edit leaves a stored secret untouched unless a new value is typed.

## Supported versions

The most recent release on `main` is supported. Given the project's size,
fixes ship forward rather than being backported.
