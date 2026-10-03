# Publishing the repository

The code is licensed Apache-2.0 (decided 2026-10-03), but the GitHub repository is **private** and stays so until
this checklist is done. Publishing is effectively irreversible: once public, anyone can clone and keep the full
history, so everything below happens first. Items marked **owner** need you, not an agent.

## Done

- [x] **Licence in place:** `LICENSE` (Apache-2.0), `NOTICE`, licence labels on images and charts.
- [x] **Community files:** `CONTRIBUTING.md` (DCO), `SECURITY.md` (private reporting), `CODE_OF_CONDUCT.md`,
      pull-request template, `CODEOWNERS`, a DCO check in CI.
- [x] **Secret scan of the full history** (2026-10-03, all commits, `git grep` over every tree): no private keys, cloud
      or API tokens, JWTs, key or kubeconfig files, or personal paths. The only match is a deliberately truncated fake
      key header in a unit test. *Re-run immediately before publishing; also run a dedicated scanner (gitleaks or
      trufflehog) as a second opinion.*
- [x] **Dependency licence audit:** all 33 modules compiled into the binaries are Apache-2.0, MIT or BSD (no copyleft,
      none unknown). `make licenses` regenerates [third-party-licenses.md](third-party-licenses.md) and fails on a
      copyleft or unrecognised licence.
- [x] **Internal references removed** from the docs (private working-agreement wording).

## To do before flipping to public

- [ ] **Author identity (owner).** All commits carry a personal Gmail address as author and committer, and it becomes
      public with the history. Options: accept it; or, **before the repository is public and while only you have
      clones**, rewrite history to the GitHub `noreply` address (`git filter-repo --mailmap`), then force-push. Rewriting
      later is far costlier. Set `git config user.email` to the noreply address going forward either way.
- [ ] **Name and trademark check (owner).** Confirm "Sextant" is usable for this product (search the Kubernetes tooling
      space and the trademark registers) and that you control the `andean.io` domain used in the API group
      `sextant.andean.io`. Decide whether to add a `TRADEMARK.md` so forks cannot ship under the name.
- [ ] **CI has never run on a GitHub runner.** Push to a branch, get `go`, `dco` and `e2e` green (expect a fix or two
      in `e2e`), so the first public impression is a passing build.
- [ ] **GitHub settings (owner):** enable private vulnerability reporting, secret scanning with push protection and
      Dependabot; require the CI checks, the DCO check and code-owner review on `main`; add a description and topics;
      confirm Actions is allowed for forks' pull requests with read-only tokens.
- [ ] **Conduct contact.** `CODE_OF_CONDUCT.md` currently routes reports through GitHub's private reporting; publish a
      dedicated address when you have one.
- [ ] **Read the whole tree once as a stranger would:** README accuracy, the plan's commercial wording, and anything you
      would not want attributed to the project.
- [ ] **Decide the hosted-offering story** (see the plan's "Open source and the business model") enough that the README
      does not over- or under-promise.

## Flipping it

Only after the list above: *Settings → General → Danger Zone → Change visibility → Public*, then tag a pre-release
and announce deliberately.
