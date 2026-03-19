# Rollout Checklist (Flux / Kustomize)

## Preconditions

- [ ] `source platform/gitops/dev-env.sh`
- [ ] Confirm context (`kc-k3s` or `kc-harv`)
- [ ] Working tree clean or changes committed

## Change

- [ ] Edit manifests in `platform/gitops/**`
- [ ] Commit with clear message
- [ ] Push to remote

## Reconcile

- [ ] `flux reconcile kustomization apps -n flux-system` (or relevant kustomization)
- [ ] Watch `kubectl -n flux-system get kustomizations`

## Verify

- [ ] `kubectl -n <ns> get pods -o wide`
- [ ] `kubectl -n <ns> rollout status deploy/<name>` (or sts)
- [ ] Check logs for errors

## Backout

- [ ] Revert commit and push
- [ ] Reconcile again
