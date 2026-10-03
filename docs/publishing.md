# Publishing the repository

**Status: public since 2026-10-03.** The code is licensed Apache-2.0 and the repository was made public by the owner
after the checks below. This file records what was verified, and what is still open now that the history is public.

## Done before publishing

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

## Done after publishing (2026-10-03)

- [x] **Repository settings:** secret scanning with push protection, Dependabot alerts and security updates, and
      private vulnerability reporting (which `SECURITY.md` points reporters to) are enabled; description and topics set.
- [x] **CI on a GitHub runner:** the `go` and `e2e` jobs pass (the e2e job runs the kind suite with docker).

## Still open

- [ ] **Branch protection (owner).** `main` is unprotected. Require the CI checks, the DCO check and code-owner review
      (`CODEOWNERS` is in place), and disallow force-pushes.
- [ ] **Author identity.** The Gmail address on every commit is public and cannot be recalled by rewriting history
      (forks and scrapers may already have it). Use the GitHub `noreply` address from now on
      (`git config user.email`); treat the old one as exposed.
- [ ] **Name and trademark (owner).** Confirm "Sextant" is usable for this product and that you control `andean.io`
      (API group `sextant.andean.io`). Decide whether to add a `TRADEMARK.md`.
- [ ] **DCO check is untested:** it only runs on pull requests, so the first external or test PR will show whether it works.
- [ ] **Second-opinion secret scan.** Run gitleaks or trufflehog over the full history once; the in-repo scan used
      pattern matching only.
- [ ] **Conduct contact.** Publish a dedicated address in `CODE_OF_CONDUCT.md` when you have one.
- [ ] **Hosted-offering story.** Settle it enough that the README does not over- or under-promise (see the plan's
      "Open source and the business model").
