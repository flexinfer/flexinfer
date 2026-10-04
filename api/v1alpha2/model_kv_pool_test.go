package v1alpha2

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestKVPoolStatusDeepCopy(t *testing.T) {
	tokens := int64(70004)
	status := &ModelStatus{KVPool: &KVPoolStatus{KVCacheTokens: &tokens, MaxConcurrency: "1.07", PodName: "first", ObservedAt: metav1.Now(), RecheckedReplicaSets: []string{"rs1"}}}
	copy := status.DeepCopy()
	*copy.KVPool.KVCacheTokens = 132561
	copy.KVPool.RecheckedReplicaSets[0] = "rs2"
	if *status.KVPool.KVCacheTokens != 70004 || status.KVPool.RecheckedReplicaSets[0] != "rs1" {
		t.Fatal("deep copy aliased KV pool status")
	}
}
