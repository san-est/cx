# Contributing to cx

Thanks for considering a contribution. `cx` handles cloud credentials, so this
repository is deliberately strict about what reaches `main`. Nothing below is
meant to discourage patches — it is meant to make them easy to accept.

**Found a security problem? Do not open a pull request.** Follow
[SECURITY.md](SECURITY.md) instead, so a fix can ship before the details are
public.

## Before you write code

For anything beyond a typo or an obvious bug fix, **open an issue first** and
get agreement on the approach. The design notes in the README and in
SECURITY.md describe constraints that are not obvious from reading the code —
several tempting simplifications are the bugs this tool was built to detect. A
short conversation up front saves a rewritten pull request.

## Requirements for every pull request

Pull requests are accepted from forks, and every one of them must satisfy all of
the following. The first three are enforced automatically and cannot be waived.

### 1. Sign off every commit (DCO)

Every commit must carry a `Signed-off-by:` trailer matching its author. This is
the [Developer Certificate of Origin](https://developercertificate.org): by
adding it you certify that you wrote the patch, or otherwise have the right to
submit it under this project's licence.

```sh
git commit -s -m "your message"          # sign off as you commit
git commit --amend --signoff             # fix the most recent commit
git rebase --signoff main                # fix an entire branch
```

To do it automatically from now on:

```sh
git config --global format.signOff true
```

### 2. Sign every commit cryptographically

`main` requires verified signatures, so commits must be signed with a GPG or SSH
key registered on your GitHub account. An unverified signature counts as no
signature.

```sh
# SSH signing — simplest if you already push over SSH
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519.pub
git config --global commit.gpgsign true
```

Then add the same key to GitHub as a **signing key** (Settings → SSH and GPG
keys). GitHub's guide covers GPG and hardware keys:
<https://docs.github.com/authentication/managing-commit-signature-verification>

### 3. Keep CI green

```sh
make lint    # gofmt + go vet + go test
make build
```

CI additionally runs the race detector, `govulncheck`, CodeQL, and a dependency
review. Run `go test -race ./...` locally if you touch concurrency.

### 4. Match the surrounding code

- `gofmt` decides formatting; there is no separate style debate
- Standard library first. A new third-party dependency needs a justification in
  the pull request — every one of them is code that runs next to credentials
- Comments explain **why**, not what. The existing comments are the model: they
  record the failure a piece of code exists to prevent
- Every behavioural change needs a test. Tests must not depend on the machine
  running them — `cx` sets environment variables that a developer's own shell
  will have inherited, so a test that reads the ambient environment will pass in
  CI and fail on a contributor's laptop
- Keep commits focused. Unrelated reformatting in a functional change makes
  review, and later bisection, much harder

## How a fork pull request is handled

Because this repository deals with credentials, workflows do not run on fork
pull requests until a maintainer approves each run. Expect your checks to sit in
`Expected — waiting for approval` at first; that is normal and not a sign that
anything is wrong.

Workflows use the `pull_request` trigger and never `pull_request_target`, so
your code runs with a read-only token and no access to repository secrets. This
protects the project from a malicious pull request, and protects you from being
blamed for one.

Review then comes from the code owner, and `main` requires that review, a green
CI run, and every conversation resolved before anything merges.

## Getting set up

```sh
git clone https://github.com/<you>/cx.git
cd cx
make build
make lint
```

You will want the shell wrapper installed to exercise switching:

```sh
make install
eval "$(cx shell-init zsh)"
```

Note that `cx` reads your real cloud configuration. Tests do not — they operate
on fixtures — and **no test should ever be written against real credentials**.

## Reporting bugs

Use the issue templates. A bug report for a credential tool is far more useful
with the shape of the configuration that triggered it: which credential kinds,
whether ADC is present, what `cx status` printed. **Redact account identifiers
and never paste secrets.**

## Licence

Contributions are licensed under the [Apache License 2.0](LICENSE), the same
terms as the project.
