# Flux GitOps Operator Workflow

## Guardrails

- Always start in `platform/gitops/` and `source dev-env.sh` before cluster operations.
- Do not hot-edit live resources (`kubectl edit`, `kubectl set env`, etc.) unless handling an incident; mirror changes back into Git immediately so Flux stops fighting you.

## Common Commands

- Status: `make status`, `make quick-status`
- Troubleshoot: `make troubleshoot`
- Reconcile:
  - `flux reconcile kustomization apps -n flux-system`
  - `kubectl -n flux-system get kustomizations`
- Rollout checks:
  - `kubectl -n <ns> get pods`
  - `kubectl -n <ns> rollout status deploy/<name>`
