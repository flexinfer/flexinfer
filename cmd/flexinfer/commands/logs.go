/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package commands

import (
	"bufio"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	follow    bool
	tailLines int64
)

var logsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Stream logs from a ModelDeployment",
	Long: `Stream logs from the pods of a ModelDeployment.

Examples:
  # Show recent logs
  flexinfer logs qwen3-8b-amd

  # Follow logs in real-time
  flexinfer logs qwen3-8b-amd -f

  # Show last 100 lines
  flexinfer logs qwen3-8b-amd --tail 100`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	logsCmd.Flags().Int64Var(&tailLines, "tail", 50, "Number of lines to show from the end of logs")
}

func runLogs(cmd *cobra.Command, args []string) error {
	name := args[0]

	clientset, err := getClientset()
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	// Find pods for this ModelDeployment
	labelSelector := fmt.Sprintf("modeldeployment_cr=%s", name)
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods found for ModelDeployment %s", name)
	}

	// Find the first running pod
	var targetPod *corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase == corev1.PodRunning {
			targetPod = pod
			break
		}
	}

	if targetPod == nil {
		// Fall back to first pod if none are running
		targetPod = &pods.Items[0]
		fmt.Printf("Warning: No running pods found, using pod %s (phase: %s)\n\n", targetPod.Name, targetPod.Status.Phase)
	}

	fmt.Printf("Streaming logs from pod %s...\n\n", targetPod.Name)

	// Get logs
	req := clientset.CoreV1().Pods(namespace).GetLogs(targetPod.Name, &corev1.PodLogOptions{
		Follow:    follow,
		TailLines: &tailLines,
	})

	stream, err := req.Stream(ctx())
	if err != nil {
		return fmt.Errorf("failed to stream logs: %w", err)
	}
	defer stream.Close()

	// Read and print logs
	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading logs: %w", err)
		}
		fmt.Print(line)
	}

	return nil
}
