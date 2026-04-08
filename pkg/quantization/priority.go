package quantization

const (
	// PriorityClassWarmup is for tiny image warmup jobs that should never block
	// real pipeline work on constrained single-GPU nodes.
	PriorityClassWarmup = "flexinfer-modelcache-warmup"

	// PriorityClassBulk is for lower-priority bulk work such as quantization and
	// publishing. These jobs can be preempted by more critical pipeline stages.
	PriorityClassBulk = "flexinfer-modelcache-bulk"

	// PriorityClassDownload is for source materialization jobs that must finish
	// before transform stages can start.
	PriorityClassDownload = "flexinfer-modelcache-download"

	// PriorityClassTransform is for critical transform stages such as
	// abliteration and finetuning. These should win scheduling on scarce GPU nodes.
	PriorityClassTransform = "flexinfer-modelcache-transform"
)
