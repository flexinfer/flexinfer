The flexinfer-agent DaemonSet is pinned by digest (`sha256:de13ce8f…`, master 62c87123, tag
20261101-181207) in `deploy/system/values-k3s.yaml`, like the controller and proxy. It no longer
follows Flux image automation, which rolled the agent on every GPU node after each master build;
an agent upgrade is now an explicit digest bump. Applying it restarts every agent pod once.
