package quantization

import corev1 "k8s.io/api/core/v1"

const rocmAllocatorConfig = "garbage_collection_threshold:0.7,max_split_size_mb:256"

func rocmAllocatorEnv() corev1.EnvVar {
	return corev1.EnvVar{
		Name:  "PYTORCH_ALLOC_CONF",
		Value: rocmAllocatorConfig,
	}
}
