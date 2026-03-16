package commands

import (
	"fmt"

	"github.com/spf13/cobra"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
)

var benchmarkCmd = &cobra.Command{
	Use:     "benchmark <name>",
	Aliases: []string{"bench"},
	Short:   "Trigger a benchmark run for a ModelDeployment",
	Long: `Trigger a benchmark by deleting the benchmark results ConfigMap (and any existing benchmark Job).
The controller will recreate the Job and publish fresh tokens/sec results.

Examples:
  # Trigger a benchmark for a ModelDeployment
  flexinfer benchmark qwen3-8b-fast

  # Trigger a benchmark in a specific namespace
  flexinfer benchmark -n flexinfer-system qwen3-8b-fast`,
	Args: cobra.ExactArgs(1),
	RunE: runBenchmark,
}

func runBenchmark(cmd *cobra.Command, args []string) error {
	fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: v1alpha1 ModelDeployment is deprecated. Please migrate to v1alpha2 Model. See: flexinfer migrate generate")
	out := cmd.OutOrStdout()

	if getNamespace() == "" {
		return fmt.Errorf("benchmark requires a single namespace; do not use --all-namespaces")
	}
	ns := getNamespace()
	name := args[0]

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Validate the ModelDeployment exists.
	md := &aiv1alpha1.ModelDeployment{}
	if err := k8sClient.Get(ctx(), types.NamespacedName{Name: name, Namespace: ns}, md); err != nil {
		return fmt.Errorf("failed to get ModelDeployment %s/%s: %w", ns, name, err)
	}

	jobName := fmt.Sprintf("%s-benchmark", name)
	cmName := benchmarkconfig.DeploymentResultsConfigMapName(name)

	// Best-effort deletes (ignore NotFound).
	if err := k8sClient.Delete(ctx(), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: ns}}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete benchmark Job %s/%s: %w", ns, jobName, err)
	}
	if err := k8sClient.Delete(ctx(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: ns}}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete benchmark results ConfigMap %s/%s: %w", ns, cmName, err)
	}

	_, _ = fmt.Fprintf(out, "Triggered benchmark for %s/%s (deleted %s and %s)\n", ns, name, jobName, cmName)
	return nil
}
