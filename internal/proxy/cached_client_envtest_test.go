package proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Runs against a real API server when envtest assets are installed
// (KUBEBUILDER_ASSETS, as for the controllers suite); skipped otherwise.
func TestNewCachedReadClient_Envtest(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if testing.Short() || assets == "" {
		t.Skip("set KUBEBUILDER_ASSETS to run the informer cache envtest")
	}
	if _, err := os.Stat(filepath.Join(assets, "kube-apiserver")); err != nil {
		t.Skipf("envtest assets not found in %s", assets)
	}
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testEnv.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	direct, err := client.New(cfg, client.Options{Scheme: Scheme})
	require.NoError(t, err)
	const ns = "default"
	newModel := func(name string) *aiv1alpha2.Model {
		return &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       aiv1alpha2.ModelSpec{Backend: "llamacpp", Source: "HF://org/model"},
		}
	}
	m1 := newModel("m1")
	require.NoError(t, direct.Create(ctx, m1))
	m1.Status.Phase = aiv1alpha2.ModelPhaseLoading
	require.NoError(t, direct.Status().Update(ctx, m1))
	require.NoError(t, direct.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "uncached", Namespace: ns}}))

	c, err := NewCachedReadClient(ctx, cfg, direct, ns, 30*time.Second)
	require.NoError(t, err)

	t.Run("reads cached kinds after sync and follows watches", func(t *testing.T) {
		got := &aiv1alpha2.Model{}
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "m1", Namespace: ns}, got))
		assert.Equal(t, aiv1alpha2.ModelPhaseLoading, got.Status.Phase)
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "uncached", Namespace: ns}, &corev1.ConfigMap{}),
			"uncached kinds must read the API server, not fail on a missing informer")

		require.NoError(t, direct.Create(ctx, newModel("m2")))
		require.Eventually(t, func() bool {
			return c.Get(ctx, client.ObjectKey{Name: "m2", Namespace: ns}, &aiv1alpha2.Model{}) == nil
		}, 5*time.Second, 20*time.Millisecond)
	})

	t.Run("demand patch from a stale cached copy keeps newer status", func(t *testing.T) {
		stale := &aiv1alpha2.Model{}
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "m1", Namespace: ns}, stale))
		// The controller moves the Model on after the proxy's read.
		fresh := &aiv1alpha2.Model{}
		require.NoError(t, direct.Get(ctx, client.ObjectKeyFromObject(stale), fresh))
		fresh.Status.Phase = aiv1alpha2.ModelPhaseReady
		require.NoError(t, direct.Status().Update(ctx, fresh))

		require.NoError(t, patchLastActiveTime(ctx, c, stale))

		written := &aiv1alpha2.Model{}
		require.NoError(t, direct.Get(ctx, client.ObjectKeyFromObject(stale), written))
		require.NotNil(t, written.Status.LastActiveTime)
		assert.Equal(t, aiv1alpha2.ModelPhaseReady, written.Status.Phase, "the patch must not revert the controller's write")
	})

	// Without watch the informers still sync from their initial LIST and then
	// freeze, so construction must refuse rather than serve a stale cache.
	t.Run("a role without watch is refused", func(t *testing.T) {
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "proxy-no-watch", Namespace: ns},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{"ai.flexinfer", ""},
				Resources: []string{"models", "loraadapters", "services"},
				Verbs:     []string{"get", "list"},
			}},
		}
		require.NoError(t, direct.Create(ctx, role))
		require.NoError(t, direct.Create(ctx, &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "proxy-no-watch", Namespace: ns},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "proxy-no-watch"}},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "proxy-no-watch"},
		}))
		user, err := testEnv.AddUser(envtest.User{Name: "proxy-no-watch"}, cfg)
		require.NoError(t, err)
		userCfg := user.Config()
		userDirect, err := client.New(userCfg, client.Options{Scheme: Scheme})
		require.NoError(t, err)

		_, err = NewCachedReadClient(ctx, userCfg, userDirect, ns, 2*time.Second)
		require.ErrorContains(t, err, "not allowed to watch")

		// Granting watch makes the same identity cacheable.
		role.Rules[0].Verbs = append(role.Rules[0].Verbs, "watch")
		require.NoError(t, direct.Update(ctx, role))
		require.Eventually(t, func() bool {
			_, err := NewCachedReadClient(ctx, userCfg, userDirect, ns, 5*time.Second)
			return err == nil
		}, 10*time.Second, 200*time.Millisecond)
	})
}
