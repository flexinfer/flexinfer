The flexinfer-agent DaemonSet is pinned by digest (`sha256:ba2fe906…`, master 1d5ca03d, tag
20261104-185128, the build the agents ran when it landed) in `deploy/system/values-k3s.yaml`, like
the controller and proxy. It no longer follows Flux image automation, which rolled the agent on
every GPU node after each master build; an agent upgrade is now an explicit digest bump. Applying
it restarts every agent pod once.
