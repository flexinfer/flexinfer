GPU power sampling is on for cblevins-5930k and cblevins-radeonvii (`agent.gpuPower.nodes` in
`deploy/system/values-k3s.yaml`, 1 Hz). cblevins-7900xtx stays off until the PCIe/SMU soak
verdict. Rolling this restarts every agent pod and grants the agent `get`/`list` on Models in
`flexinfer-system`; turning it off is emptying the list.
