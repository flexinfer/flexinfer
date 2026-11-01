The node agent can sample GPU board power into `flexinfer_gpu_power_watts{gpu,node}`, read from
sysfs hwmon `power1_average` (fallback `power1_input`) at `--gpu-power-interval` (default 1 s),
never through `rocm-smi`. It is off by default and enabled per node by name
(`--gpu-power-nodes`, chart `agent.gpuPower.nodes`, no wildcard); with the default empty list the
agent DaemonSet renders exactly as before. The sampler skips runtime-suspended cards and stops
itself, latched until the agent restarts, when the health sentinel reports the node degraded, on a
device I/O error, or after 5 consecutive failed reads; `flexinfer_gpu_power_sampler_active{node}`
shows which. The agent now parses its flags once, after all of them are defined: before, any
agent argument made the first `flag.Parse()` exit with "flag provided but not defined".
