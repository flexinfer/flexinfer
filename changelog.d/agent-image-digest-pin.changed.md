The flexinfer-agent DaemonSet is pinned by digest (`sha256:6b832f38…`, master ac73e607, tag
20261104-190946, the build the agents ran when it landed) in `deploy/system/values-k3s.yaml`, like
the controller and proxy. It no longer follows Flux image automation, which rolled the agent on
every GPU node after each master build; an agent upgrade is now an explicit digest bump. Applying
it restarts every agent pod once.
