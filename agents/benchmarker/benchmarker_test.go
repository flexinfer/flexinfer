package benchmarker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRun(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	b := &Benchmarker{
		kubeClient: clientset,
		namespace:  "default",
	}

	model := "test-model"
	configMapName := "test-cm"

	err := b.Run(context.Background(), model, configMapName)
	require.NoError(t, err)

	cm, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), configMapName, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, model, cm.Data["model"])
	assert.Equal(t, "150.75", cm.Data["tokensPerSecond"])
	assert.NotEmpty(t, cm.Data["timestamp"])
}
