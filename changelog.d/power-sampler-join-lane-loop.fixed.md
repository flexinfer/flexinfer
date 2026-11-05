The GPU power sampler now waits for its lane-refresh loop before `Run` returns, and a cancelled
loop no longer lists Active Models on a tick that raced the cancel. Without the wait, one more
listing could land after the sampler stopped, so `TestLaneEnergy_RefreshLoopEndsWithRun` failed
about one run in twelve under `-race` and turned master's `race_test` red (pipeline 37269).
