The flexinfer-scheduler Deployment is pinned by digest (`sha256:f76cc08c…`, master 4546a85d, tag
20261106-171118, the build it ran when this landed) in `deploy/system/values-k3s.yaml`, like the
controller, proxy and agent. It no longer follows Flux image automation, which rolled it ~20–30 min
after every master build; a scheduler upgrade is now an explicit digest bump. The agent and
scheduler digests join `deploy/pins/control-plane-retention-pins.yaml` so Harbor retention keeps them.
