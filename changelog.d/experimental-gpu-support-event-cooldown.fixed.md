The `ExperimentalGPUSupport` warning event is now throttled to one per Model per 10 minutes, the
same cooldown `VRAMPressure` has, through a shared per-reason helper
(`shouldEmitThrottledEvent`). It restates a static backend/arch support level, but it was emitted on
every reconcile, about 2,600 events per lane per day on the live fleet.
