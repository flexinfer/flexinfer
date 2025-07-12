# Contributing to FlexInfer

First – **thank you** for deciding to help make heterogeneous-GPU LLM serving easier for everyone!
Whether you are fixing a typo, filing a bug, adding documentation, or implementing a new scheduler feature, we welcome and value your contribution.

> **TL;DR**
> 1. Fork → clone → create a topic branch.
> 2. Write clear, tested code.
> 3. Sign off each commit (`git commit -s …`) to agree to the DCO.
> 4. Open a pull request that follows the PR checklist below.
> 5. One maintainer reviews; a second LGTM merges after CI passes.

---

## 1 Ground rules

| ✔ Do | ✖ Don’t |
|---|---|
| Keep the discussion on GitHub (issues/PRs) so others can follow. | Ask maintainers for private reviews over email/DM. |
| Use respectful, inclusive language. Our [Code of Conduct](CODE_OF_CONDUCT.md) applies. | Use off-topic memes or harassing language. |
| Prefer small, focused PRs (≤ 400 LOC, 1–2 logical changes). | Combine refactors, new features, and drive-by fixes in one PR. |
| Write tests and docs alongside code. | Leave TODOs or failing unit tests in the commit. |

---

## 2 Development quick-start

```bash
# 0. Fork the repo and clone your fork
git clone https://github.com/<you>/flexinfer.git
cd flexinfer
git remote add upstream https://github.com/flexinfer/flexinfer.git

# 1. Create a topic branch
git checkout -b feat/my-awesome-feature

# 2. Build & run the controller locally (Kind demo cluster)
make kind-up kind-load deploy IMG=ghcr.io/<you>/flexinfer:dev

# 3. Run unit tests
go test ./...

# 4. Lint and static analysis
golangci-lint run

# 5. Commit (with DCO sign-off)
git add .
git commit -s -m "feat: add super-cool scheduler knob"

# 6. Push and open a PR
git push origin feat/my-awesome-feature
```

---

## 3 Commit message conventions (Conventional Commits v1.0)

<type>(<scope>): <subject>

<body>  # optional

<footer>  # optional, DCO sign-off is automatic with -s

* type: feat, fix, docs, chore, refactor, test, perf, ci.
* scope: folder or component (scheduler, agent, docs).
* subject: imperative present tense, ≤ 72 chars.

Example:

fix(agent): handle ROCm 6.x pci bus renumbering

---

## 4 Pull-request checklist

* Rebased on latest main, no merge commits.
* CI green – make test, golangci-lint, and make e2e-kind all pass.
* Docs updated (README, AGENTS.md, config flags, Helm values).
* One commit = one logical change (squash if necessary).
* Signed off by all authors (Signed-off-by: line present).

GitHub Actions enforce DCO and run the full test matrix (Go 1.22 × Linux/amd64 & arm64).

---

## 5 Issue triage labels

| Label | Meaning | Typical next step |
|---|---|---|
| `good first issue` | Low-complexity, well-described tasks | New contributors encouraged |
| `help wanted` | Core team lacks bandwidth | Community PRs welcome |
| `kind/bug` | Reproducible defect | Add unit/e2e test; create fix: PR |
| `kind/feature` | Bigger enhancement request | Discuss design in issue before PR |
| `kind/question` | Usage/support query | Convert to docs PR if answerable |

Please search existing issues before opening a new one to avoid duplicates.

---

## 6 Style & design guidelines

* Language: Go 1.22, modules enabled.
* Code style: go fmt + golangci-lint defaults.
* Imports: group std-lib, third-party, project-local (goimports handles this).
* K8s API: use controller-runtime abstractions; avoid raw client-go where possible.
* Logging: slog (log/slog), structured with keys ("msg", "model", "node").
* Metrics: prometheus/client_golang, prefix everything with flexinfer_.
* Tests: table-driven, use envtest for controller logic; Kind for e2e.

---

## 7 Security disclosures

If you believe you have found a potential security vulnerability, do not open a GitHub issue.
Instead, email security@flexinfer.ai (PGP key in SECURITY.md). We follow a 90-day responsible disclosure window.

---

## 8 Community & communication channels

| Channel | Purpose |
|---|---|
| GitHub Discussions | Architecture questions, roadmap talk, general chat |
| GitHub Issues | Bug reports, feature requests |
| Discord #flexinfer (Llama.cpp server) | Real-time debugging, “office hours” |
| Twitter/X @FlexInferAI | Release announcements |

---

## 9 License & Developer Certificate of Origin

* All contributions are licensed under the Apache License 2.0 (same as the project).
* By signing off your commits (-s flag) you certify that you wrote the code or have the right to submit it under this license, as described in the DCO 1.1.

---

Happy hacking – we look forward to your PR! 🚀
