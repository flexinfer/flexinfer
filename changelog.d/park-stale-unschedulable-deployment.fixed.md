When a Model no longer matches any GPU node (`Schedulable=False` / `NoMatchingNodes`), the
controller now scales an existing Deployment to zero if that Deployment was rendered for a
node selector the Model no longer declares, and records a `DeploymentParked` event. The
reconcile returned before `ensureDeployment` in that state, so the old Deployment kept running:
its pod stayed bound to the previous node, held that node's device-plugin GPU slots, and was
recreated with the stale template after every node reboot. A Deployment whose selector still
matches is left alone, so a node that is merely NotReady does not turn a warm pod into a cold
start.
