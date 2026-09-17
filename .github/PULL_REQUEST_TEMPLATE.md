## What this changes

<!-- What behaviour differs after this merges, and why. -->

## Why

<!-- Link the issue this implements: "Fixes #123".
     Non-trivial changes should have an agreed issue first — see CONTRIBUTING.md. -->

## How it was verified

<!-- `make lint` passing is the floor, not the answer. What did you actually
     exercise, on which shell and OS? -->

- [ ] `make lint` passes (gofmt, vet, test)
- [ ] `go test -race ./...` passes
- [ ] Tested manually against a real configuration (say which credential kinds)

## Checklist

- [ ] Every commit is signed off (`git commit -s`) — DCO check enforces this
- [ ] Every commit is signed with a key registered on GitHub — `main` requires it
- [ ] Tests cover the change, and do not read the ambient environment
- [ ] No new third-party dependency, or it is justified below
- [ ] No secret, real account identifier, or personal path appears in the diff

## Credential-safety review

<!-- Delete any line that genuinely does not apply. -->

- [ ] No value from disk reaches generated shell code unquoted
- [ ] No subprocess is invoked through a shell
- [ ] Any file holding secret material is created `0600`
- [ ] No secret is written to a log, an error message, or a process argument
- [ ] `cx` cannot now report one target while the shell resolves to another
