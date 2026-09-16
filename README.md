# cx — cloud context

A terminal tool for seeing and switching the cloud account your shell is pointed
at: AWS profiles, gcloud configurations, and Application Default Credentials, on
one screen.

It exists because `gcloud config configurations activate` writes to a
**machine-wide file**, and because ADC is a **separate login** from your active
gcloud configuration. Between those two facts, `gcloud config list` can say
`staging` while `terraform apply` goes to production, with nothing on screen to
warn you.

## Install

```sh
make install                        # builds and installs to ~/.local/bin
echo 'eval "$(cx shell-init zsh)"' >> ~/.zshrc
```

The shell wrapper is required for switching, and adds the prompt segment. See
[Why a wrapper is needed](#why-a-wrapper-is-needed).

## Usage

```
cx                      dashboard
cx status               one-shot report; exits 2 if the shell can be misdirected
cx status --no-probe    same, no network, instant
cx use aws <profile>    point this shell at an AWS profile
cx use gcp <config>     point this shell at a gcloud configuration
cx clear [aws|gcp|all]  drop this shell's overrides
cx prompt               compact status for a shell prompt
cx shell-init [zsh]     print the shell wrapper
```

The dashboard borrows its shape from k9s: a context block and key map at the
top, then bordered tables with a count in the title and a full-width highlight
on the selected row.

```
╭─ cx · context ──────────────────────╮╭─ Keys ─────────────────────────╮╭─────────────────╮
│ AWS Profile: client-prod            ││ <↑↓>     navigate  <l>  login  ││  ▄████▄ ██   ██ │
│ GCP Config:  acme-dev               ││ <enter>  switch    <x>  clear  ││ ██       ▀█▄█▀  │
│ GCP Project: acme-dev-1234          ││ <a>      add       <r>  refresh││ ██        ███   │
│ ADC:         you@example.com        ││ <e>      edit      <q>  quit   ││ ██       ▄█▀█▄  │
│ GCP Pin:     shell-local            ││ <d>      delete                ││  ▀████▀ ██   ██ │
╰─────────────────────────────────────╯╰────────────────────────────────╯╰─────────────────╯
╭─ Warnings(1) ─────────────────────────────────────────────────────────────────╮
│ ⚠ ADC quota project (acme-dev) differs from active gcloud project (acme-prod) │
│   terraform and the client libraries follow ADC, not `gcloud config`          │
╰───────────────────────────────────────────────────────────────────────────────╯
╭─ AWS Profiles(3) ─────────────────────────────────────────────────────────────╮
│     NAME              KIND           ACCOUNT        SCOPE         STATUS      │
│ ●   default           static         111122223333   us-east-1     user/vasil  │
│ ● ▪ client-prod       sso-session    999988887777   eu-west-1     AWSAdmin    │
│ ✗   oldclient         static+token   444455556666   eu-central-1  expired     │
╰───────────────────────────────────────────────────────────────────────────────╯
```

`▪` marks what this shell currently resolves to. Colours follow tokyonight.

The layout follows the terminal as it is resized. Narrower widths shrink the
name and scope columns first, then drop the account id, then the credential
kind; the status column keeps a minimum share, since a status clipped to `val…`
tells the reader nothing. The key map is a fixed width and the wordmark takes any
slack, so it keeps the same shape at every size rather than spreading into
sparse columns. The wordmark is dropped first when space runs short, then below
about 84 columns the header panels stack, and below 44 the dashboard says the
terminal is too narrow rather than drawing a broken table. Very wide terminals
are capped so rows stay readable.

The dashboard also budgets its height: tables scroll to keep the selection
visible and report how many rows are hidden, rather than drawing past the bottom
of the terminal, which is what leaves torn frames behind after a resize.

Keys: `↑/↓` or `j/k` move, `enter` switch, `a` add, `e` edit, `d` delete,
`l` log in, `x` clear, `r` refresh, `q` quit. The add menu is navigated the same
way — arrows and `enter`, or a number key to jump.

`e` opens the selected target's settings pre-filled, with the form matching how
it authenticates — the SSO fields for an SSO profile, the key fields for a
static one. The name is not editable: renaming means deleting one section and
writing another, and a half-completed rename would strand credentials under the
old name. Delete and re-add instead.

An edit leaves the stored secret alone unless you type a new one, so changing a
region cannot silently blank a key.

`d` deletes, behind a confirmation that says what will be removed and warns when
it is the target this shell is using. A deleted AWS profile leaves any
`sso-session` block it referred to in place, since other profiles may share it.

Press `a` to add a target, then pick from the list. Creating one is a form inside cx — it writes
`~/.aws/config`, `~/.aws/credentials` (mode 600) or `~/.config/gcloud` directly:

```
╭─────────────────────────────────────────────────────────────╮
│  Add AWS profile (access keys)                              │
│                                                             │
│    Profile name      *  client-prod                         │
│    Access key ID     *  AKIAIOSFODNN7EXAMPLE                │
│  ▸ Secret access key *  •••••••••••••                       │
│    Session token        optional — temporary credentials    │
│    Default region       eu-west-1                           │
│                                                             │
│  tab/↑↓ move · enter next, or save on the last · esc cancel │
╰─────────────────────────────────────────────────────────────╯
```

Only browser sign-ins leave the app, because only the vendor CLI can complete
them: `l` on a row runs `aws sso login` or `gcloud auth login`, and `a` then `d`
runs `gcloud auth application-default login`. Those suspend the dashboard and
reload when the CLI exits.

Writes edit the target section line by line and leave the rest of the file
untouched, so indented sub-properties — which a parse-and-rewrite would silently
drop — survive.

`cx status` exits **2** when it finds state that can misdirect a command, so it
can gate a script:

```sh
cx status --no-probe >/dev/null || { echo "refusing to apply"; exit 1; }
```

## The prompt segment

`cx prompt` prints a compact summary:

```
~/work  aws:client-prod gcp:acme-dev
```

A trailing `!` means this shell's gcloud target comes from the machine-wide
file, so another terminal can change it out from under you:

```
~/work  aws:default gcp:acme-prod!
```

It reads only local files — about **5 ms**, versus roughly **800 ms** for a
single `gcloud config get-value` — so it never stalls a prompt.

### Auto-pin

Every new terminal would otherwise start unpinned, falling back to the shared
gcloud file — which means a "your target is shared" warning would be
permanently true, and therefore useless.

So `shell-init` pins each shell, at startup, to whatever configuration was
selected at that moment. Another terminal switching afterwards cannot retarget
you, and the warning goes quiet because the hazard is gone rather than merely
reported.

One consequence worth knowing: running `gcloud config configurations activate`
by hand no longer affects the shell you run it in, because that shell is pinned.
Use `cx use gcp <name>` instead, or `cx clear gcp` to follow the machine-wide
setting again. `CX_NO_AUTOPIN=1` disables it entirely.

### With starship

Starship's own `[aws]` and `[gcloud]` modules already show the current target,
and they do honour the environment variables `cx` sets, so a switch shows up
immediately. What they cannot show is whether that target is *safe*. Add `cx` as
a custom module for exactly that:

```toml
[custom.cx]
command = "cx prompt --warn"
when = "cx prompt --warn"
format = "[$symbol$output]($style) "
symbol = "⚠ "
style = "bold red"
description = "cloud context hazards"
```

Then add `${custom.cx}` to your `format` string.

`cx prompt --warn` prints nothing and exits non-zero when everything is fine, so
the module disappears entirely unless there is something to say. When there is:

```
~/work  ⚠ gcp:shared adc:acme-dev
```

- `gcp:shared` — the target comes from the machine-wide file, and more than one
  configuration exists (with only one there is nothing to confuse it with, so
  this stays quiet)
- `gcp:no-config` — `active_config` names a configuration that no longer exists
- `adc:<project>` — ADC will hit that project, not the one gcloud reports

Do **not** set `shell = ["sh", "-c"]` on the module. Starship pipes the command
to the shell on stdin, so the explicit `-c` becomes `sh -c -c` and the module
fails silently.

`shell-init` skips `RPROMPT` when starship is running, since starship rewrites
it on every render. Set `CX_NO_RPROMPT=1` to skip it in any shell.

## What it checks

**Per target**

| Column | Meaning |
|---|---|
| `▪` | this target is what the current shell resolves to |
| kind | how credentials are obtained — `sso-session`, `assume-role`, `credential_process`, `static`, `static+token` |
| status | probed against STS / gcloud, concurrently, with a timeout |

`static+token` is highlighted because a hand-pasted session token is the only
credential kind here that expires with no way to refresh itself. It causes most
"it worked yesterday" failures.

**Across targets** — hazards no single row can express:

- the gcloud target comes from machine-wide `active_config` rather than this
  shell's `CLOUDSDK_ACTIVE_CONFIG_NAME`, so another terminal can retarget you
- `active_config` names a configuration that no longer exists, which makes
  gcloud fall back to built-in defaults without saying so
- ADC's quota project differs from the active gcloud project
- ADC authenticates as a different principal than gcloud
- `CLOUDSDK_CORE_PROJECT` / `CLOUDSDK_CORE_ACCOUNT` are silently overriding the
  active configuration

## Why a wrapper is needed

A program cannot change the environment of the shell that started it. When `cx`
exits, anything it exported dies with it.

So `cx` does not try. The wrapper function hands it a scratch file, `cx` writes
the environment changes there, and the wrapper *sources* that file — which does
run in your shell, and so can change it. `cx shell-init zsh` prints it.

Without the wrapper installed, `cx` says so rather than appearing to work.

## Design notes

**Switching only ever sets environment variables.** It never runs
`gcloud config configurations activate` or `kubectl config use-context`, because
those write machine-wide state and would retarget terminals you are not looking
at. That is the bug this tool was built to detect, so it must not cause it.

**Switching AWS clears ambient credential variables.** The AWS CLI reads
`AWS_ACCESS_KEY_ID` *before* `AWS_PROFILE`. Leaving stale keys in place is how a
profile switch appears to work while commands keep hitting the old account.

**Probes shell out to `aws` and `gcloud`.** Using the SDKs directly would mean
reimplementing SSO token caches, `credential_process` helpers, and every other
resolution mechanism. Shelling out guarantees that what `cx` reports is exactly
what your terminal will do.

**Reading configuration does not.** `gcloud` is a Python program with roughly a
second of startup cost. The config files are a documented stable format, so `cx`
parses them directly, renders instantly, and fills in probe results
asynchronously.

**The INI parser is hand-written.** The AWS config format allows indented
sub-properties under a key (`s3 =` followed by an indented block). Strict INI
libraries mis-parse those as top-level keys and corrupt the section.

**Emitted shell values are single-quoted.** Names come from files on disk, and
should not be able to run anything when the wrapper sources the script.

## Roadmap

- [x] Phase 1 — read-only dashboard across AWS, GCP, and ADC
- [x] Prompt segment
- [x] Phase 2 — switching via the shell wrapper, environment variables only
- [ ] Phase 3 — Kubernetes pane, with a per-shell kubeconfig copy so
      `use-context` in one terminal cannot retarget another
- [ ] Mark targets as production in a config file; require confirmation
- [x] Add and re-authenticate targets from the dashboard
- [ ] `cx add aws <name>` for static-key profiles from the command line

## Development

```
make lint   # fmt + vet + test
make build
make install
```
